package bench

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"dnsbench/internal/model"
)

const labelChars = "abcdefghijklmnopqrstuvwxyz0123456789"

func (e *Engine) runRounds(ctx context.Context, active []model.Server) error {
	e.warnSkippedCategories(ctx)
	watch := &connWatch{engine: e}
	seq := uint64(0)
	for w := 0; w < e.cfg.WarmupRounds; w++ {
		if _, _, err := e.runRound(ctx, active, 0, true, seq); err != nil {
			return err
		}
		seq++
	}
	for r := 1; r <= e.cfg.Rounds; r++ {
		total, allTimedOut, err := e.runRound(ctx, active, r, false, seq)
		seq++
		if err != nil {
			return err
		}
		if e.cfg.ConnectivityWatch {
			if err := watch.observe(ctx, total, allTimedOut, active); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) warnSkippedCategories(ctx context.Context) {
	for _, cat := range e.cfg.Categories {
		switch cat {
		case model.CatCached:
			if len(e.cfg.CachedDomains) == 0 {
				e.emit(ctx, model.Event{Type: model.EvWarn, Msg: "cached category skipped: no cached domains are configured"})
			}
		case model.CatUncached:
			if e.cfg.UncachedZone == "" {
				e.emit(ctx, model.Event{Type: model.EvWarn, Msg: "uncached category skipped: no uncached zone is configured"})
			}
		case model.CatTLD:
			if e.cfg.TLDZone == "" {
				e.emit(ctx, model.Event{Type: model.EvWarn, Msg: "tld category skipped: no TLD zone is configured"})
			}
		}
	}
}

func (e *Engine) runRound(ctx context.Context, active []model.Server, round int, warmup bool, seq uint64) (int, bool, error) {
	if err := e.gate.wait(ctx); err != nil {
		return 0, false, err
	}
	order := e.roundOrder(len(active), seq)
	var wg sync.WaitGroup
	var mu sync.Mutex
	total, timeouts := 0, 0
	for pos, idx := range order {
		if !e.acquireSlot(ctx) {
			break
		}
		wg.Add(1)
		go func(pos, idx int) {
			defer wg.Done()
			defer e.releaseSlot()
			t, to := e.runServerRound(ctx, active[idx], idx, pos, round, warmup, seq)
			mu.Lock()
			total += t
			timeouts += to
			mu.Unlock()
		}(pos, idx)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return total, false, err
	}
	e.emit(ctx, model.Event{Type: model.EvRoundDone, Round: round})
	return total, total > 0 && timeouts == total, nil
}

func (e *Engine) roundOrder(n int, seq uint64) []int {
	rng := rand.New(rand.NewPCG(uint64(e.seed), seq<<1|1))
	return rng.Perm(n)
}

func (e *Engine) runServerRound(ctx context.Context, s model.Server, stableIdx, pos, round int, warmup bool, seq uint64) (int, int) {
	rng := rand.New(rand.NewPCG(uint64(e.seed), (seq<<24)|(uint64(stableIdx)<<1)))
	cats := append([]model.Category(nil), e.cfg.Categories...)
	rng.Shuffle(len(cats), func(i, j int) { cats[i], cats[j] = cats[j], cats[i] })
	total, timeouts := 0, 0
	for _, cat := range cats {
		qname, ok := e.buildQName(cat, round, pos, rng)
		if !ok {
			continue
		}
		if !e.waitServerGap(ctx, s.ID) {
			return total, timeouts
		}
		if e.gate.wait(ctx) != nil {
			return total, timeouts
		}
		if !e.pace.wait(ctx) {
			return total, timeouts
		}
		e.emit(ctx, model.Event{Type: model.EvQueryStart, ServerID: s.ID, Round: round})
		sample := e.measure(ctx, s, cat, round, warmup, qname)
		e.markServerQueryDone(s.ID)
		if sample.Result.Err != nil && (sample.Result.Err.Kind == model.ErrCanceled || ctx.Err() != nil) {
			return total, timeouts
		}
		e.record(ctx, sample)
		total++
		if sample.Result.Err.IsTimeout() {
			timeouts++
		}
	}
	return total, timeouts
}

func (e *Engine) buildQName(cat model.Category, round, pos int, rng *rand.Rand) (string, bool) {
	switch cat {
	case model.CatCached:
		if len(e.cfg.CachedDomains) == 0 {
			return "", false
		}
		return e.cfg.CachedDomains[(round+pos)%len(e.cfg.CachedDomains)], true
	case model.CatUncached:
		if e.cfg.UncachedZone == "" {
			return "", false
		}
		return randomLabel(rng) + "." + e.cfg.UncachedZone, true
	case model.CatTLD:
		if e.cfg.TLDZone == "" {
			return "", false
		}
		return randomLabel(rng) + "." + e.cfg.TLDZone, true
	}
	return "", false
}

func randomLabel(rng *rand.Rand) string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = labelChars[rng.IntN(len(labelChars))]
	}
	return string(b)
}

func (e *Engine) measure(ctx context.Context, s model.Server, cat model.Category, round int, warmup bool, qname string) model.Sample {
	startedAt := time.Now()
	sample := model.Sample{
		ServerID: s.ID,
		Category: cat,
		Round:    round,
		Warmup:   warmup,
		QName:    qname,
		QType:    "A",
		At:       startedAt,
	}
	retries := e.cfg.Retries
	if retries < 0 {
		retries = 0
	}
	var res model.QueryResult
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			if !sleepCtx(ctx, e.cfg.RetryInterval) {
				break
			}
			if !e.pace.wait(ctx) {
				break
			}
		}
		res = e.attemptQuery(ctx, s, qname)
		e.observePace(ctx, s.ID, res)
		sample.Attempts++
		if !res.Answered() {
			sample.FailedAttempts++
		}
		if res.Err.IsTimeout() {
			sample.TimeoutCount++
		}
		if res.Answered() || (res.Err != nil && res.Err.Kind == model.ErrCanceled) {
			break
		}
	}
	sample.Elapsed = time.Since(startedAt)
	sample.Result = res
	return sample
}

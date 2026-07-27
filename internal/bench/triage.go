package bench

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"dnsbench/internal/model"
)

var errKindOrder = []model.ErrKind{
	model.ErrTimeout,
	model.ErrNetwork,
	model.ErrTLS,
	model.ErrHTTP,
	model.ErrProtocol,
}

func (e *Engine) prepare(ctx context.Context) ([]model.Server, error) {
	if !e.cfg.TriageEnabled {
		active := make([]model.Server, 0, len(e.servers))
		for _, s := range e.servers {
			e.setState(s.ID, model.StateActive)
			active = append(active, s)
		}
		return active, nil
	}
	if len(e.cfg.CachedDomains) == 0 {
		return nil, errors.New("triage requires at least one cached domain")
	}
	return e.runTriage(ctx)
}

func (e *Engine) runTriage(ctx context.Context) ([]model.Server, error) {
	results := make([]*model.TriageResult, len(e.servers))
	var wg sync.WaitGroup
	for i := range e.servers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = e.triageServer(ctx, e.servers[i])
		}(i)
	}
	wg.Wait()
	var active []model.Server
	for i, s := range e.servers {
		tr := results[i]
		e.mu.Lock()
		e.triage[s.ID] = tr
		e.states[s.ID] = tr.State
		e.mu.Unlock()
		e.emit(ctx, model.Event{Type: model.EvTriage, ServerID: s.ID, Triage: tr})
		if tr.State == model.StateActive {
			active = append(active, s)
		} else {
			e.emit(ctx, model.Event{Type: model.EvStateChange, ServerID: s.ID, State: tr.State, Msg: tr.Reason})
		}
	}
	return active, ctx.Err()
}

func (e *Engine) triageServer(ctx context.Context, s model.Server) *model.TriageResult {
	tr := &model.TriageResult{ServerID: s.ID}
	domain := e.cfg.CachedDomains[0]
	counts := make(map[model.ErrKind]int)
	msgs := make(map[model.ErrKind]string)
	attempts := e.cfg.TriageAttempts
	if attempts < 1 {
		attempts = 1
	}
	started := false
	for i := 0; i < attempts; i++ {
		var attemptStartedAt time.Time
		res, executed := e.executeAttempt(ctx, func() model.QueryResult {
			if !started {
				e.emit(ctx, model.Event{Type: model.EvQueryStart, ServerID: s.ID})
				started = true
			}
			attemptStartedAt = time.Now()
			return e.singleQuery(ctx, s, domain)
		})
		if !executed {
			break
		}
		e.observePace(ctx, s, model.CatCached, attemptStartedAt, res)
		if res.Err != nil && res.Err.Kind == model.ErrCanceled {
			break
		}
		tr.Attempts++
		if res.ValidFor(model.CatCached) {
			tr.Responses++
			if tr.BestRTT == 0 || res.RTT < tr.BestRTT {
				tr.BestRTT = res.RTT
			}
			if s.Source != model.SourceBuiltin || res.RTT <= e.cfg.TriageThreshold {
				break
			}
		} else if res.Err != nil {
			counts[res.Err.Kind]++
			if msgs[res.Err.Kind] == "" {
				msgs[res.Err.Kind] = res.Err.Msg
			}
		} else {
			counts[model.ErrProtocol]++
			if msgs[model.ErrProtocol] == "" {
				msgs[model.ErrProtocol] = "DNS response was not a valid A or CNAME answer"
			}
		}
	}
	e.classifyTriage(s, tr, counts, msgs)
	return tr
}

func (e *Engine) classifyTriage(s model.Server, tr *model.TriageResult, counts map[model.ErrKind]int, msgs map[model.ErrKind]string) {
	if tr.Attempts == 0 {
		tr.State = model.StateOffline
		tr.Reason = "triage was interrupted before any probe completed"
		return
	}
	if tr.Responses == 0 {
		tr.State = model.StateOffline
		tr.Reason = offlineReason(tr.Attempts, counts, msgs)
		return
	}
	if s.Source == model.SourceBuiltin && tr.BestRTT > e.cfg.TriageThreshold {
		if e.cfg.ForceAll {
			tr.State = model.StateActive
			tr.Reason = fmt.Sprintf("best RTT %s is above the triage threshold %s; kept active by force-all",
				tr.BestRTT, e.cfg.TriageThreshold)
			return
		}
		tr.State = model.StateBenched
		tr.Reason = fmt.Sprintf("best RTT %s is above the triage threshold %s", tr.BestRTT, e.cfg.TriageThreshold)
		return
	}
	tr.State = model.StateActive
}

func offlineReason(attempts int, counts map[model.ErrKind]int, msgs map[model.ErrKind]string) string {
	kind, n := dominantErrKind(counts)
	if n == 0 {
		return fmt.Sprintf("no response from any of %d probes", attempts)
	}
	switch kind {
	case model.ErrTimeout:
		return fmt.Sprintf("no response: %d of %d probes timed out with no reply", n, attempts)
	case model.ErrNetwork:
		if strings.Contains(msgs[kind], "connection refused") {
			return fmt.Sprintf("connection refused on %d of %d probes", n, attempts)
		}
		return fmt.Sprintf("network error on %d of %d probes: %s", n, attempts, msgs[kind])
	default:
		return fmt.Sprintf("%s error on %d of %d probes: %s", kind, n, attempts, msgs[kind])
	}
}

func dominantErrKind(counts map[model.ErrKind]int) (model.ErrKind, int) {
	var kind model.ErrKind
	best := 0
	for _, k := range errKindOrder {
		if counts[k] > best {
			best = counts[k]
			kind = k
		}
	}
	return kind, best
}

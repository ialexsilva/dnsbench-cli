package bench

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dnsbench/internal/model"
	"dnsbench/internal/transport"
)

func runEngine(t *testing.T, e *Engine, ctx context.Context) ([]model.Event, error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	var events []model.Event
	for ev := range e.Events() {
		events = append(events, ev)
	}
	return events, <-done
}

func countEvents(events []model.Event, typ model.EventType) int {
	n := 0
	for _, ev := range events {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

func TestSampleCountsAndWarmupFlags(t *testing.T) {
	f := newFakeFactory()
	cfg := testConfig()
	e := NewEngine(testServers("s1", "s2", "s3"), cfg, f.factory)
	events, err := runEngine(t, e, context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	samples, _, states, seed := e.Result()
	if seed != 1 {
		t.Fatalf("seed = %d, want 1", seed)
	}
	if len(samples) != 24 {
		t.Fatalf("len(samples) = %d, want 24", len(samples))
	}
	warmups, measured := 0, 0
	for _, s := range samples {
		if s.Warmup {
			warmups++
			if s.Round != 0 {
				t.Fatalf("warmup sample has round %d, want 0", s.Round)
			}
		} else {
			measured++
			if s.Round < 1 || s.Round > cfg.Rounds {
				t.Fatalf("measured sample has round %d, want 1..%d", s.Round, cfg.Rounds)
			}
		}
		if s.Attempts != 1 || s.QType != "A" || s.At.IsZero() {
			t.Fatalf("unexpected sample fields: %+v", s)
		}
	}
	if warmups != 6 || measured != 18 {
		t.Fatalf("warmups = %d, measured = %d, want 6 and 18", warmups, measured)
	}
	if n := countEvents(events, model.EvSample); n != 24 {
		t.Fatalf("EvSample count = %d, want 24", n)
	}
	var roundsDone []int
	for _, ev := range events {
		if ev.Type == model.EvRoundDone {
			roundsDone = append(roundsDone, ev.Round)
		}
	}
	if !slices.Equal(roundsDone, []int{0, 1, 2, 3}) {
		t.Fatalf("round-done sequence = %v, want [0 1 2 3]", roundsDone)
	}
	if countEvents(events, model.EvDone) != 1 {
		t.Fatalf("expected exactly one EvDone")
	}
	for _, id := range []string{"s1", "s2", "s3"} {
		if states[id] != model.StateActive {
			t.Fatalf("state[%s] = %s, want active", id, states[id])
		}
	}
}

func TestUncachedSkippedWithWarn(t *testing.T) {
	f := newFakeFactory()
	cfg := testConfig()
	cfg.WarmupRounds = 0
	cfg.Rounds = 2
	cfg.Categories = []model.Category{model.CatCached, model.CatUncached}
	cfg.UncachedZone = ""
	e := NewEngine(testServers("s1"), cfg, f.factory)
	events, err := runEngine(t, e, context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if n := countEvents(events, model.EvWarn); n != 1 {
		t.Fatalf("EvWarn count = %d, want 1", n)
	}
	samples, _, _, _ := e.Result()
	if len(samples) != 2 {
		t.Fatalf("len(samples) = %d, want 2", len(samples))
	}
	for _, s := range samples {
		if s.Category != model.CatCached {
			t.Fatalf("unexpected category %s", s.Category)
		}
	}
}

func TestTriageClassification(t *testing.T) {
	f := newFakeFactory()
	f.script("fast", staticResult(okResult(10*time.Millisecond)))
	f.script("slow", staticResult(okResult(200*time.Millisecond)))
	f.script("silent", staticResult(timeoutResult()))
	f.script("refused", staticResult(networkResult("dial udp 127.0.0.1:53: connect: connection refused")))
	cfg := testConfig()
	cfg.TriageEnabled = true
	cfg.TriageAttempts = 3
	cfg.TriageThreshold = 50 * time.Millisecond
	cfg.WarmupRounds = 0
	cfg.Rounds = 1
	e := NewEngine(builtinTestServers("fast", "slow", "silent", "refused"), cfg, f.factory)
	events, err := runEngine(t, e, context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	samples, triage, states, _ := e.Result()
	if n := countEvents(events, model.EvTriage); n != 4 {
		t.Fatalf("EvTriage count = %d, want 4", n)
	}
	if n := countEvents(events, model.EvStateChange); n != 3 {
		t.Fatalf("EvStateChange count = %d, want 3", n)
	}
	fast := triage["fast"]
	if fast.State != model.StateActive || fast.Attempts != 1 || fast.Responses != 1 || fast.BestRTT != 10*time.Millisecond {
		t.Fatalf("fast triage = %+v", fast)
	}
	slow := triage["slow"]
	if slow.State != model.StateBenched || slow.Attempts != 3 || slow.Responses != 3 || slow.BestRTT != 200*time.Millisecond {
		t.Fatalf("slow triage = %+v", slow)
	}
	if !strings.Contains(slow.Reason, "threshold") {
		t.Fatalf("slow reason = %q", slow.Reason)
	}
	silent := triage["silent"]
	if silent.State != model.StateOffline || silent.Attempts != 3 || silent.Responses != 0 {
		t.Fatalf("silent triage = %+v", silent)
	}
	if !strings.Contains(silent.Reason, "timed out") {
		t.Fatalf("silent reason = %q", silent.Reason)
	}
	refused := triage["refused"]
	if refused.State != model.StateOffline || !strings.Contains(refused.Reason, "connection refused") {
		t.Fatalf("refused triage = %+v", refused)
	}
	if states["fast"] != model.StateActive || states["slow"] != model.StateBenched ||
		states["silent"] != model.StateOffline || states["refused"] != model.StateOffline {
		t.Fatalf("states = %v", states)
	}
	if len(samples) != 2 {
		t.Fatalf("len(samples) = %d, want 2", len(samples))
	}
	for _, s := range samples {
		if s.ServerID != "fast" {
			t.Fatalf("sample from %s, want only fast", s.ServerID)
		}
	}
}

func TestTriageForceAllKeepsSlowActive(t *testing.T) {
	f := newFakeFactory()
	f.script("fast", staticResult(okResult(10*time.Millisecond)))
	f.script("slow", staticResult(okResult(200*time.Millisecond)))
	f.script("silent", staticResult(timeoutResult()))
	cfg := testConfig()
	cfg.TriageEnabled = true
	cfg.TriageAttempts = 3
	cfg.TriageThreshold = 50 * time.Millisecond
	cfg.ForceAll = true
	cfg.WarmupRounds = 0
	cfg.Rounds = 1
	e := NewEngine(builtinTestServers("fast", "slow", "silent"), cfg, f.factory)
	if _, err := runEngine(t, e, context.Background()); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	samples, triage, states, _ := e.Result()
	if triage["slow"].State != model.StateActive || states["slow"] != model.StateActive {
		t.Fatalf("slow state = %s, want active", triage["slow"].State)
	}
	if triage["silent"].State != model.StateOffline {
		t.Fatalf("silent state = %s, want offline", triage["silent"].State)
	}
	seen := make(map[string]bool)
	for _, s := range samples {
		seen[s.ServerID] = true
	}
	if !seen["fast"] || !seen["slow"] || seen["silent"] {
		t.Fatalf("sampled servers = %v", seen)
	}
}

func TestTriageThresholdAppliesOnlyToBuiltins(t *testing.T) {
	f := newFakeFactory()
	for id, rtt := range map[string]time.Duration{
		"builtin-at-limit":   200 * time.Millisecond,
		"builtin-over-limit": 201 * time.Millisecond,
		"custom-slow":        250 * time.Millisecond,
	} {
		f.script(id, staticResult(okResult(rtt)))
	}
	f.script("custom-recovers", func(_ context.Context, n int, _ transport.Question) model.QueryResult {
		if n == 0 {
			return timeoutResult()
		}
		return okResult(300 * time.Millisecond)
	})
	f.script("custom-offline", staticResult(timeoutResult()))
	servers := builtinTestServers("builtin-at-limit", "builtin-over-limit")
	servers = append(servers, testServers("custom-slow", "custom-recovers", "custom-offline")...)
	cfg := testConfig()
	cfg.TriageEnabled = true
	cfg.TriageAttempts = 3
	cfg.TriageThreshold = 200 * time.Millisecond
	cfg.WarmupRounds = 0
	cfg.Rounds = 1
	cfg.Categories = []model.Category{model.CatCached}
	e := NewEngine(servers, cfg, f.factory)
	if _, err := runEngine(t, e, context.Background()); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	samples, triage, states, _ := e.Result()
	if tr := triage["builtin-at-limit"]; tr.State != model.StateActive || tr.Attempts != 1 {
		t.Errorf("builtin at limit = %+v, want active after one attempt", tr)
	}
	if tr := triage["builtin-over-limit"]; tr.State != model.StateBenched || tr.Attempts != cfg.TriageAttempts {
		t.Errorf("builtin over limit = %+v, want benched after %d attempts", tr, cfg.TriageAttempts)
	}
	if tr := triage["custom-slow"]; tr.State != model.StateActive || tr.Attempts != 1 {
		t.Errorf("custom slow = %+v, want active after one valid answer", tr)
	}
	if tr := triage["custom-recovers"]; tr.State != model.StateActive || tr.Attempts != 2 || tr.Responses != 1 {
		t.Errorf("custom recovered = %+v, want active after its first valid answer", tr)
	}
	if tr := triage["custom-offline"]; tr.State != model.StateOffline || tr.Attempts != cfg.TriageAttempts || tr.Responses != 0 {
		t.Errorf("custom offline = %+v, want offline after every attempt failed", tr)
	}
	if states["custom-slow"] != model.StateActive || states["custom-recovers"] != model.StateActive {
		t.Errorf("custom states = slow:%s recovered:%s, want active", states["custom-slow"], states["custom-recovers"])
	}
	seen := map[string]bool{}
	for _, sample := range samples {
		seen[sample.ServerID] = true
	}
	if !seen["builtin-at-limit"] || !seen["custom-slow"] || !seen["custom-recovers"] ||
		seen["builtin-over-limit"] || seen["custom-offline"] {
		t.Fatalf("sampled resolvers = %v", seen)
	}
}

func TestTriageRejectsSemanticallyInvalidDNSResponses(t *testing.T) {
	f := newFakeFactory()
	f.script("invalid", staticResult(model.QueryResult{Rcode: 2}))
	cfg := testConfig()
	cfg.TriageAttempts = 3
	e := NewEngine(testServers("invalid"), cfg, f.factory)
	tr := e.triageServer(context.Background(), testServers("invalid")[0])
	if tr.Attempts != 3 || tr.Responses != 0 || tr.State != model.StateOffline {
		t.Fatalf("triage = %+v, want 3 attempts, no valid responses and offline", tr)
	}
	if !strings.Contains(tr.Reason, "protocol error") {
		t.Fatalf("reason = %q, want semantic DNS failure detail", tr.Reason)
	}
}

func TestDeterministicQueryPlanWithFixedSeed(t *testing.T) {
	runOnce := func() []string {
		f := newFakeFactory()
		cfg := testConfig()
		cfg.Seed = 42
		cfg.Concurrency = 1
		cfg.Rounds = 3
		cfg.WarmupRounds = 1
		cfg.Categories = []model.Category{model.CatCached, model.CatUncached, model.CatTLD}
		cfg.UncachedZone = "bench.example"
		e := NewEngine(testServers("s1", "s2", "s3", "s4"), cfg, f.factory)
		events, err := runEngine(t, e, context.Background())
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		var seq []string
		for _, ev := range events {
			if ev.Type != model.EvSample {
				continue
			}
			s := ev.Sample
			seq = append(seq, fmt.Sprintf("%d|%t|%s|%s|%s", s.Round, s.Warmup, s.ServerID, s.Category, s.QName))
		}
		slices.Sort(seq)
		return seq
	}
	first := runOnce()
	second := runOnce()
	if len(first) != 48 {
		t.Fatalf("len(sequence) = %d, want 48", len(first))
	}
	if !slices.Equal(first, second) {
		t.Fatalf("two runs with the same seed diverged:\n%v\n%v", first, second)
	}
}

func TestGeneratedSeedIsExposed(t *testing.T) {
	f := newFakeFactory()
	cfg := testConfig()
	cfg.Seed = 0
	cfg.Rounds = 1
	cfg.WarmupRounds = 0
	cfg.Categories = []model.Category{model.CatCached}
	e := NewEngine(testServers("s1"), cfg, f.factory)
	if _, err := runEngine(t, e, context.Background()); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	_, _, _, seed := e.Result()
	if seed == 0 {
		t.Fatal("expected a generated non-zero seed")
	}
}

func TestCancellationStopsQuickly(t *testing.T) {
	f := newFakeFactory()
	blocking := func(ctx context.Context, n int, q transport.Question) model.QueryResult {
		<-ctx.Done()
		return model.QueryResult{Err: &model.QueryError{Kind: model.ErrCanceled, Msg: ctx.Err().Error()}}
	}
	for _, id := range []string{"s1", "s2", "s3"} {
		f.script(id, blocking)
	}
	cfg := testConfig()
	e := NewEngine(testServers("s1", "s2", "s3"), cfg, f.factory)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := runEngine(t, e, ctx)
	elapsed := time.Since(start)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Run took %s after cancellation, want under 2s", elapsed)
	}
	samples, _, _, _ := e.Result()
	if len(samples) != 0 {
		t.Fatalf("len(samples) = %d, want 0", len(samples))
	}
}

func TestPauseThenResumeContinues(t *testing.T) {
	f := newFakeFactory()
	cfg := testConfig()
	cfg.WarmupRounds = 0
	cfg.Rounds = 2
	e := NewEngine(testServers("s1", "s2"), cfg, f.factory)
	e.Pause()
	done := make(chan error, 1)
	go func() { done <- e.Run(context.Background()) }()
	select {
	case ev, ok := <-e.Events():
		if !ok {
			t.Fatal("events channel closed while paused")
		}
		t.Fatalf("unexpected event while paused: %s", ev.Type)
	case <-time.After(150 * time.Millisecond):
	}
	e.Resume()
	sampleCount := 0
	for ev := range e.Events() {
		if ev.Type == model.EvSample {
			sampleCount++
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if sampleCount != 8 {
		t.Fatalf("samples after resume = %d, want 8", sampleCount)
	}
}

func TestConnectivityLostAndRestored(t *testing.T) {
	f := newFakeFactory()
	var down atomic.Bool
	down.Store(true)
	respond := func(ctx context.Context, n int, q transport.Question) model.QueryResult {
		if down.Load() {
			return timeoutResult()
		}
		return okResult(5 * time.Millisecond)
	}
	f.script("s1", respond)
	f.script("s2", respond)
	cfg := testConfig()
	cfg.WarmupRounds = 0
	cfg.Rounds = 6
	cfg.Categories = []model.Category{model.CatCached}
	cfg.ConnectivityWatch = true
	e := NewEngine(testServers("s1", "s2"), cfg, f.factory)
	e.canaryInterval = 2 * time.Millisecond
	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- e.Run(context.Background()) }()
	var events []model.Event
	for ev := range e.Events() {
		if ev.Type == model.EvConnLost {
			down.Store(false)
		}
		events = append(events, ev)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("run took %s, want under 8s", elapsed)
	}
	lostIdx, restoredIdx := -1, -1
	for i, ev := range events {
		switch ev.Type {
		case model.EvConnLost:
			if lostIdx != -1 {
				t.Fatal("EvConnLost emitted more than once")
			}
			lostIdx = i
		case model.EvConnRestored:
			if restoredIdx != -1 {
				t.Fatal("EvConnRestored emitted more than once")
			}
			restoredIdx = i
		}
	}
	if lostIdx == -1 || restoredIdx == -1 || lostIdx > restoredIdx {
		t.Fatalf("lost index = %d, restored index = %d", lostIdx, restoredIdx)
	}
	samples, _, _, _ := e.Result()
	if len(samples) != 12 {
		t.Fatalf("len(samples) = %d, want 12", len(samples))
	}
	answered := 0
	for _, s := range samples {
		if s.Result.Answered() {
			answered++
		}
	}
	if answered != 8 {
		t.Fatalf("answered samples = %d, want 8", answered)
	}
}

func TestPersistentSessionCreatesOneQuerierPerServer(t *testing.T) {
	f := newFakeFactory()
	cfg := testConfig()
	cfg.Session = model.SessionPersistent
	e := NewEngine(testServers("s1", "s2", "s3"), cfg, f.factory)
	if _, err := runEngine(t, e, context.Background()); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	for _, id := range []string{"s1", "s2", "s3"} {
		created, closed := f.counts(id)
		if created != 1 || closed != 1 {
			t.Fatalf("server %s: created = %d, closed = %d, want 1 and 1", id, created, closed)
		}
	}
}

func TestConcurrencyCapsActualQueryAttempts(t *testing.T) {
	f := newFakeFactory()
	var inFlight atomic.Int32
	var peak atomic.Int32
	track := func(_ context.Context, _ int, _ transport.Question) model.QueryResult {
		current := inFlight.Add(1)
		for {
			seen := peak.Load()
			if current <= seen || peak.CompareAndSwap(seen, current) {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
		inFlight.Add(-1)
		return okResult(time.Millisecond)
	}
	ids := []string{"s1", "s2", "s3", "s4", "s5", "s6"}
	for _, id := range ids {
		f.script(id, track)
	}
	cfg := testConfig()
	cfg.TriageEnabled = false
	cfg.WarmupRounds = 0
	cfg.Rounds = 1
	cfg.Categories = []model.Category{model.CatCached}
	cfg.Concurrency = 2
	cfg.PaceInterval = 0
	cfg.PerServerGap = 0
	e := NewEngine(testServers(ids...), cfg, f.factory)
	if _, err := runEngine(t, e, context.Background()); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got := peak.Load(); got != int32(cfg.Concurrency) {
		t.Fatalf("peak in-flight queries = %d, want %d", got, cfg.Concurrency)
	}
}

func TestConcurrencySlotIsReleasedDuringPerServerGap(t *testing.T) {
	f := newFakeFactory()
	var mu sync.Mutex
	var order []string
	for _, id := range []string{"s1", "s2"} {
		id := id
		f.script(id, func(_ context.Context, _ int, _ transport.Question) model.QueryResult {
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			return okResult(time.Millisecond)
		})
	}
	cfg := testConfig()
	cfg.TriageEnabled = false
	cfg.WarmupRounds = 0
	cfg.Rounds = 1
	cfg.Categories = []model.Category{model.CatCached, model.CatTLD}
	cfg.Concurrency = 1
	cfg.PaceInterval = 0
	cfg.PerServerGap = 50 * time.Millisecond
	e := NewEngine(testServers("s1", "s2"), cfg, f.factory)
	if _, err := runEngine(t, e, context.Background()); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 4 {
		t.Fatalf("query order = %v, want four attempts", order)
	}
	if order[0] == order[1] {
		t.Fatalf("query order = %v; concurrency slot stayed with one server during its gap", order)
	}
}

func TestColdSessionCreatesOneQuerierPerQuery(t *testing.T) {
	f := newFakeFactory()
	cfg := testConfig()
	cfg.Session = model.SessionCold
	e := NewEngine(testServers("s1", "s2"), cfg, f.factory)
	if _, err := runEngine(t, e, context.Background()); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	perServer := (cfg.Rounds + cfg.WarmupRounds) * len(cfg.Categories)
	for _, id := range []string{"s1", "s2"} {
		created, closed := f.counts(id)
		if created != perServer || closed != perServer {
			t.Fatalf("server %s: created = %d, closed = %d, want %d each", id, created, closed, perServer)
		}
	}
}

func TestMeasureRecordsEndToEndRetryCost(t *testing.T) {
	f := newFakeFactory()
	f.script("s1", func(_ context.Context, n int, _ transport.Question) model.QueryResult {
		if n == 0 {
			return timeoutResult()
		}
		return okResult(time.Millisecond)
	})
	cfg := testConfig()
	cfg.TriageEnabled = false
	cfg.WarmupRounds = 0
	cfg.Rounds = 1
	cfg.Categories = []model.Category{model.CatCached}
	cfg.Retries = 1
	cfg.RetryInterval = 10 * time.Millisecond
	cfg.PerServerGap = 0
	e := NewEngine(testServers("s1"), cfg, f.factory)
	if _, err := runEngine(t, e, context.Background()); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	samples, _, _, _ := e.Result()
	if len(samples) != 1 {
		t.Fatalf("samples = %d, want 1", len(samples))
	}
	sample := samples[0]
	if sample.Attempts != 2 || sample.FailedAttempts != 1 || sample.TimeoutCount != 1 {
		t.Fatalf("retry fields = attempts %d, failures %d, timeouts %d; want 2, 1, 1",
			sample.Attempts, sample.FailedAttempts, sample.TimeoutCount)
	}
	if sample.Elapsed < cfg.RetryInterval {
		t.Errorf("Elapsed = %s, want at least retry interval %s", sample.Elapsed, cfg.RetryInterval)
	}
}

func TestCancellationDuringRetryAdmissionDoesNotRecordAnAnsweredSample(t *testing.T) {
	f := newFakeFactory()
	firstFailure := make(chan struct{})
	var e *Engine
	f.script("s1", func(_ context.Context, n int, _ transport.Question) model.QueryResult {
		if n == 0 {
			e.Pause()
			close(firstFailure)
		}
		return networkResult("temporary failure")
	})
	cfg := testConfig()
	cfg.TriageEnabled = false
	cfg.WarmupRounds = 0
	cfg.Rounds = 1
	cfg.Categories = []model.Category{model.CatCached}
	cfg.Retries = 1
	cfg.RetryInterval = 0
	cfg.PaceInterval = 0
	cfg.PerServerGap = 0
	e = NewEngine(testServers("s1"), cfg, f.factory)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-firstFailure
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if _, err := runEngine(t, e, ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	samples, _, _, _ := e.Result()
	if len(samples) != 0 {
		t.Fatalf("samples = %+v, want no fabricated partial sample", samples)
	}
}

func TestPerServerGapAppliesAcrossRoundBoundaries(t *testing.T) {
	f := newFakeFactory()
	var mu sync.Mutex
	var starts []time.Time
	f.script("s1", func(_ context.Context, _ int, _ transport.Question) model.QueryResult {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		return okResult(time.Millisecond)
	})
	cfg := testConfig()
	cfg.TriageEnabled = false
	cfg.WarmupRounds = 0
	cfg.Rounds = 3
	cfg.Categories = []model.Category{model.CatCached}
	cfg.PerServerGap = 20 * time.Millisecond
	e := NewEngine(testServers("s1"), cfg, f.factory)
	if _, err := runEngine(t, e, context.Background()); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(starts) != cfg.Rounds {
		t.Fatalf("query starts = %d, want %d", len(starts), cfg.Rounds)
	}
	for i := 1; i < len(starts); i++ {
		if gap := starts[i].Sub(starts[i-1]); gap < cfg.PerServerGap-2*time.Millisecond {
			t.Errorf("gap %d = %s, want at least approximately %s", i, gap, cfg.PerServerGap)
		}
	}
}

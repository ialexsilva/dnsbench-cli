package bench

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"dnsbench/internal/model"
	"dnsbench/internal/transport"
)

func TestPacerSpacesSequentialWaits(t *testing.T) {
	p := newPacer(20*time.Millisecond, true, 1, 0)
	start := time.Now()
	for i := 0; i < 5; i++ {
		if !p.wait(context.Background()) {
			t.Fatal("wait returned false")
		}
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Fatalf("5 paced waits took %s, want at least 80ms", elapsed)
	}
}

func TestNilPacerDoesNotBlock(t *testing.T) {
	var p *pacer
	start := time.Now()
	for i := 0; i < 100; i++ {
		if !p.wait(context.Background()) {
			t.Fatal("wait returned false")
		}
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("100 nil-pacer waits took %s, want nearly instant", elapsed)
	}
}

func TestPacerStopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if newPacer(time.Hour, true, 1, 0).wait(ctx) {
		t.Error("live pacer wait = true on canceled context")
	}
	var p *pacer
	if p.wait(ctx) {
		t.Error("nil pacer wait = true on canceled context")
	}
}

func TestFixedPacerKeepsTheExplicitInterval(t *testing.T) {
	p := newPacer(20*time.Millisecond, false, 1, 0)
	p.next = time.Now().Add(time.Hour)
	before := p.next
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p.wait(ctx) {
		t.Fatal("fixed pacer wait returned true on canceled context")
	}
	if step := p.next.Sub(before); step != 20*time.Millisecond {
		t.Fatalf("fixed pacer reservation step = %s, want exactly 20ms without jitter", step)
	}
	for i := 0; i < 100; i++ {
		if adj, changed := observeTestPacer(p, "s1", i%2 == 0); changed {
			t.Fatalf("fixed pacer adapted after an observation: %+v", adj)
		}
	}
	if p.interval != 20*time.Millisecond {
		t.Fatalf("fixed interval = %s, want 20ms", p.interval)
	}
}

func observeTestPacer(p *pacer, serverID string, timedOut bool) (paceAdjust, bool) {
	now := time.Now()
	result := okResult(time.Millisecond)
	if timedOut {
		result = timeoutResult()
	}
	return p.observeAttemptAt(
		model.Server{ID: serverID, Name: serverID},
		"",
		now,
		now,
		result,
	)
}

func markAnswered(p *pacer, ids ...string) {
	for _, id := range ids {
		observeTestPacer(p, id, false)
	}
}

func TestPacerBacksOffOnCrossServerTimeouts(t *testing.T) {
	p := newPacer(20*time.Millisecond, true, 1, 0)
	markAnswered(p, "s1", "s2", "s3")
	if _, changed := observeTestPacer(p, "s1", true); changed {
		t.Fatal("backed off after one server timed out")
	}
	if _, changed := observeTestPacer(p, "s2", true); changed {
		t.Fatal("backed off after two servers timed out")
	}
	adj, changed := observeTestPacer(p, "s3", true)
	if !changed || adj.recovering {
		t.Fatalf("adjust = %+v, changed = %t, want a backoff on the third distinct server", adj, changed)
	}
	if adj.from != 20*time.Millisecond || adj.to != 40*time.Millisecond {
		t.Fatalf("backoff %s -> %s, want 20ms -> 40ms", adj.from, adj.to)
	}
	if adj.servers != 3 || adj.timeouts != 3 {
		t.Fatalf("adjust = %+v, want 3 servers and 3 timeouts", adj)
	}
}

func TestPacerCountsIndependentFailureDomainsInsteadOfEndpoints(t *testing.T) {
	tests := []struct {
		name    string
		servers []model.Server
	}{
		{
			name: "same operator",
			servers: []model.Server{
				{ID: "provider-udp", Operator: "Example DNS", Address: "192.0.2.1", Protocol: model.ProtoUDP},
				{ID: "provider-dot", Operator: " example   dns ", Address: "192.0.2.1", Protocol: model.ProtoDoT},
				{ID: "provider-doh", Operator: "EXAMPLE DNS", Address: "192.0.2.2", Protocol: model.ProtoDoH},
				{ID: "provider-doq", Operator: "Example DNS", Address: "192.0.2.2", Protocol: model.ProtoDoQ},
			},
		},
		{
			name: "same address without operator metadata",
			servers: []model.Server{
				{ID: "address-udp", Address: "192.0.2.10", Protocol: model.ProtoUDP},
				{ID: "address-dot", Address: "192.0.2.10", Protocol: model.ProtoDoT},
				{ID: "address-doh", Address: "192.0.2.10", Protocol: model.ProtoDoH},
				{ID: "address-doq", Address: "192.0.2.10", Protocol: model.ProtoDoQ},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPacer(20*time.Millisecond, true, 1, 3*time.Second)
			base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
			for _, server := range tt.servers {
				p.observeAttemptAt(server, model.CatCached, base, base, okResult(time.Millisecond))
			}
			for i, server := range tt.servers {
				startedAt := base.Add(time.Second + time.Duration(i)*100*time.Millisecond)
				observedAt := startedAt.Add(3 * time.Second)
				if adj, changed := p.observeAttemptAt(
					server, model.CatCached, startedAt, observedAt, timeoutResult(),
				); changed {
					t.Fatalf("backed off for correlated endpoints in one failure domain: %+v", adj)
				}
			}
			if p.interval != 20*time.Millisecond {
				t.Fatalf("interval = %s, want unchanged 20ms", p.interval)
			}
		})
	}
}

func TestPacerBacksOffOnThreeRecentlyHealthyFailureDomains(t *testing.T) {
	p := newPacer(20*time.Millisecond, true, 1, 3*time.Second)
	base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	servers := []model.Server{
		{ID: "alpha-udp", Operator: "Alpha DNS", Address: "192.0.2.1", Protocol: model.ProtoUDP},
		{ID: "beta-doh", Operator: "Beta DNS", Address: "192.0.2.2", Protocol: model.ProtoDoH},
		{ID: "gamma-dot", Operator: "Gamma DNS", Address: "192.0.2.3", Protocol: model.ProtoDoT},
	}
	for _, server := range servers {
		p.observeAttemptAt(server, model.CatCached, base, base, okResult(time.Millisecond))
	}
	categories := []model.Category{model.CatCached, model.CatTLD, model.CatTLD}
	var adj paceAdjust
	var changed bool
	for i, server := range servers {
		startedAt := base.Add(time.Second + time.Duration(i)*500*time.Millisecond)
		observedAt := startedAt.Add(3 * time.Second)
		adj, changed = p.observeAttemptAt(server, categories[i], startedAt, observedAt, timeoutResult())
	}
	if !changed {
		t.Fatal("three independent, recently healthy failure domains did not trigger a backoff")
	}
	if adj.timeouts != 3 || adj.servers != 3 || len(adj.failureDomains) != 3 {
		t.Fatalf("adjustment evidence = %+v, want 3 timeouts, servers and failure domains", adj)
	}
	if got, want := adj.failureDomains, []string{"Alpha DNS", "Beta DNS", "Gamma DNS"}; !slices.Equal(got, want) {
		t.Fatalf("failure domains = %v, want %v", got, want)
	}
	if got, want := adj.categories, []model.Category{model.CatCached, model.CatTLD}; !slices.Equal(got, want) {
		t.Fatalf("categories = %v, want %v", got, want)
	}
	if got, want := adj.protocols, []model.Protocol{model.ProtoDoH, model.ProtoDoT, model.ProtoUDP}; !slices.Equal(got, want) {
		t.Fatalf("protocols = %v, want %v", got, want)
	}
	if span := adj.evidenceEndedAt.Sub(adj.evidenceStartedAt); span != time.Second {
		t.Fatalf("launch evidence span = %s, want 1s", span)
	}
}

func TestPacerIgnoresServersWithoutARecentAnswer(t *testing.T) {
	p := newPacer(20*time.Millisecond, true, 1, 3*time.Second)
	base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	servers := []model.Server{
		{ID: "alpha", Operator: "Alpha DNS"},
		{ID: "beta", Operator: "Beta DNS"},
		{ID: "gamma", Operator: "Gamma DNS"},
	}
	for _, server := range servers {
		p.observeAttemptAt(server, model.CatCached, base, base, okResult(time.Millisecond))
	}
	for i, server := range servers {
		startedAt := base.Add(recentAnswerWindow + time.Second + time.Duration(i)*100*time.Millisecond)
		if adj, changed := p.observeAttemptAt(
			server, model.CatCached, startedAt, startedAt.Add(3*time.Second), timeoutResult(),
		); changed {
			t.Fatalf("backed off using stale health evidence: %+v", adj)
		}
	}
	if p.interval != 20*time.Millisecond {
		t.Fatalf("interval = %s, want unchanged 20ms", p.interval)
	}
}

func TestPacerCorrelatesTimeoutsByAttemptStartTime(t *testing.T) {
	p := newPacer(20*time.Millisecond, true, 1, 10*time.Second)
	base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	servers := []model.Server{
		{ID: "alpha", Operator: "Alpha DNS"},
		{ID: "beta", Operator: "Beta DNS"},
		{ID: "gamma", Operator: "Gamma DNS"},
	}
	for _, server := range servers {
		p.observeAttemptAt(server, model.CatCached, base.Add(-time.Second), base, okResult(time.Millisecond))
	}
	started := []time.Time{
		base.Add(time.Second),
		base.Add(1500 * time.Millisecond),
		base.Add(2 * time.Second),
	}
	observed := []time.Time{
		base.Add(4 * time.Second),
		base.Add(7 * time.Second),
		base.Add(10 * time.Second),
	}
	var adj paceAdjust
	var changed bool
	for i, server := range servers {
		adj, changed = p.observeAttemptAt(
			server, model.CatCached, started[i], observed[i], timeoutResult(),
		)
	}
	if !changed {
		t.Fatal("launch-correlated timeouts did not trigger when their completions were far apart")
	}
	if adj.evidenceEndedAt.Sub(adj.evidenceStartedAt) != time.Second {
		t.Fatalf("adjustment used completion times instead of the 1s launch window: %+v", adj)
	}
}

func TestPacerDoesNotCorrelateOnlyCompletionTimes(t *testing.T) {
	p := newPacer(20*time.Millisecond, true, 1, 10*time.Second)
	base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	servers := []model.Server{
		{ID: "alpha", Operator: "Alpha DNS"},
		{ID: "beta", Operator: "Beta DNS"},
		{ID: "gamma", Operator: "Gamma DNS"},
	}
	for _, server := range servers {
		p.observeAttemptAt(server, model.CatCached, base.Add(-time.Second), base, okResult(time.Millisecond))
	}
	for i, server := range servers {
		startedAt := base.Add(time.Second + time.Duration(i)*3*time.Second)
		observedAt := base.Add(10*time.Second + time.Duration(i)*10*time.Millisecond)
		if adj, changed := p.observeAttemptAt(
			server, model.CatCached, startedAt, observedAt, timeoutResult(),
		); changed {
			t.Fatalf("backed off for completion-correlated but launch-distant timeouts: %+v", adj)
		}
	}
	if p.interval != 20*time.Millisecond {
		t.Fatalf("interval = %s, want unchanged 20ms", p.interval)
	}
}

func TestPacerIgnoresSingleServerTimeouts(t *testing.T) {
	p := newPacer(20*time.Millisecond, true, 1, 0)
	markAnswered(p, "s1")
	for i := 0; i < 10; i++ {
		if _, changed := observeTestPacer(p, "s1", true); changed {
			t.Fatal("backed off for timeouts concentrated on a single server")
		}
	}
	if p.interval != 20*time.Millisecond {
		t.Fatalf("interval = %s, want unchanged 20ms", p.interval)
	}
}

func TestPacerIgnoresNeverAnsweredServers(t *testing.T) {
	p := newPacer(20*time.Millisecond, true, 1, 0)
	for _, id := range []string{"dead1", "dead2", "dead3", "dead4"} {
		if _, changed := observeTestPacer(p, id, true); changed {
			t.Fatal("backed off for servers that never answered")
		}
	}
	if p.interval != 20*time.Millisecond {
		t.Fatalf("interval = %s, want unchanged 20ms", p.interval)
	}
}

func TestPacerCooldownAndCeiling(t *testing.T) {
	p := newPacer(20*time.Millisecond, true, 1, 0)
	markAnswered(p, "s1", "s2", "s3")
	trigger := func() bool {
		var changed bool
		for _, id := range []string{"s1", "s2", "s3"} {
			_, c := observeTestPacer(p, id, true)
			changed = changed || c
		}
		return changed
	}
	if !trigger() {
		t.Fatal("first trigger did not back off")
	}
	if trigger() {
		t.Fatal("backed off again during the cooldown")
	}
	for i := 0; i < 10; i++ {
		p.mu.Lock()
		p.suppressUntil = time.Now().Add(-time.Second)
		p.mu.Unlock()
		trigger()
	}
	if p.interval != 80*time.Millisecond {
		t.Fatalf("interval = %s, want the 80ms ceiling", p.interval)
	}
}

func TestPacerNeverRunsFasterThanConfigured(t *testing.T) {
	p := newPacer(20*time.Millisecond, true, 1, 0)
	for i := 0; i < 20*p.cleanTarget; i++ {
		if adj, changed := observeTestPacer(p, "s1", false); changed {
			t.Fatalf("clean traffic at the configured pace adjusted it: %+v", adj)
		}
	}
	if p.interval != 20*time.Millisecond {
		t.Fatalf("interval = %s, want configured floor 20ms", p.interval)
	}
}

func TestPacerRecoversGraduallyToConfiguredInterval(t *testing.T) {
	p := newPacer(20*time.Millisecond, true, 1, 0)
	p.interval = 80 * time.Millisecond
	for _, want := range []time.Duration{
		70 * time.Millisecond,
		60 * time.Millisecond,
		50 * time.Millisecond,
		40 * time.Millisecond,
		30 * time.Millisecond,
		20 * time.Millisecond,
	} {
		for i := 1; i < p.cleanTarget; i++ {
			if adj, changed := observeTestPacer(p, "s1", false); changed {
				t.Fatalf("recovered before %d clean answers: %+v", p.cleanTarget, adj)
			}
		}
		adj, changed := observeTestPacer(p, "s1", false)
		if !changed || !adj.recovering || adj.to != want || adj.to >= adj.from {
			t.Fatalf("adjust = %+v, changed = %t, want gradual recovery to %s", adj, changed, want)
		}
	}
	for i := 0; i < 2*p.cleanTarget; i++ {
		if adj, changed := observeTestPacer(p, "s1", false); changed {
			t.Fatalf("recovered below the configured pace: %+v", adj)
		}
	}
	if p.interval != 20*time.Millisecond {
		t.Fatalf("interval = %s, want configured floor 20ms", p.interval)
	}
}

func TestPacerRecoveryWaitsForCooldown(t *testing.T) {
	p := newPacer(20*time.Millisecond, true, 1, 0)
	p.interval = 40 * time.Millisecond
	p.suppressUntil = time.Now().Add(time.Hour)
	for i := 0; i < 2*p.cleanTarget; i++ {
		if adj, changed := observeTestPacer(p, "s1", false); changed {
			t.Fatalf("recovered during cooldown: %+v", adj)
		}
	}
	if p.interval != 40*time.Millisecond {
		t.Fatalf("interval = %s during cooldown, want 40ms", p.interval)
	}
	p.suppressUntil = time.Now().Add(-time.Second)
	for i := 0; i < p.cleanTarget; i++ {
		observeTestPacer(p, "s1", false)
	}
	if p.interval != 30*time.Millisecond {
		t.Fatalf("interval = %s after cooldown, want first recovery step 30ms", p.interval)
	}
}

func TestPacerBackoffReturnsToNextSafetyLevel(t *testing.T) {
	p := newPacer(20*time.Millisecond, true, 1, 0)
	markAnswered(p, "s1", "s2", "s3")
	trigger := func() paceAdjust {
		var got paceAdjust
		for _, id := range []string{"s1", "s2", "s3"} {
			if adj, changed := observeTestPacer(p, id, true); changed {
				got = adj
			}
		}
		return got
	}
	p.interval = 30 * time.Millisecond
	if adj := trigger(); adj.from != 30*time.Millisecond || adj.to != 40*time.Millisecond {
		t.Fatalf("partial-recovery backoff = %+v, want 30ms -> 40ms", adj)
	}
	p.suppressUntil = time.Now().Add(-time.Second)
	p.interval = 70 * time.Millisecond
	if adj := trigger(); adj.from != 70*time.Millisecond || adj.to != 80*time.Millisecond {
		t.Fatalf("partial-recovery backoff = %+v, want 70ms -> 80ms", adj)
	}
}

func TestEngineEmitsPaceAdjustOnCongestion(t *testing.T) {
	f := newFakeFactory()
	flaky := func(_ context.Context, n int, _ transport.Question) model.QueryResult {
		if n == 0 {
			return okResult(time.Millisecond)
		}
		return timeoutResult()
	}
	for _, id := range []string{"s1", "s2", "s3"} {
		f.script(id, flaky)
	}
	f.script("s4", staticResult(okResult(time.Millisecond)))
	cfg := testConfig()
	cfg.WarmupRounds = 0
	cfg.Rounds = 4
	cfg.Categories = []model.Category{model.CatCached}
	cfg.PaceInterval = time.Millisecond
	cfg.PaceAdaptive = true
	cfg.PerServerGap = 0
	servers := testServers("s1", "s2", "s3", "s4")
	for i := range servers {
		servers[i].Operator = "operator-" + servers[i].ID
		servers[i].Address = ""
	}
	e := NewEngine(servers, cfg, f.factory)
	events, err := runEngine(t, e, context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if n := countEvents(events, model.EvPaceAdjust); n != 1 {
		t.Fatalf("EvPaceAdjust count = %d, want 1 (one backoff, then cooldown)", n)
	}
	if e.pace.interval != 2*time.Millisecond {
		t.Fatalf("interval = %s, want 2ms after one backoff", e.pace.interval)
	}
	adjustments := e.PaceAdjustments()
	if len(adjustments) != 1 {
		t.Fatalf("recorded pace adjustments = %d, want 1", len(adjustments))
	}
	if got := adjustments[0]; got.Reason != model.PaceSharedTimeoutBurst ||
		len(got.FailureDomains) != 3 ||
		!slices.Equal(got.Categories, []model.Category{model.CatCached}) ||
		!slices.Equal(got.Protocols, []model.Protocol{model.ProtoUDP}) {
		t.Fatalf("recorded pace evidence = %+v", got)
	}
	for _, ev := range events {
		if ev.Type != model.EvPaceAdjust {
			continue
		}
		if !strings.Contains(ev.Msg, "possible shared-path congestion") ||
			!strings.Contains(ev.Msg, "3 resolver groups timed out in 2s") {
			t.Fatalf("pace event message = %q", ev.Msg)
		}
		if ev.Pace == nil || ev.Pace.Reason != model.PaceSharedTimeoutBurst {
			t.Fatalf("pace event lacks structured evidence: %+v", ev)
		}
	}
}

func TestEngineRecordsPaceRecoveryWithoutEmittingAWarning(t *testing.T) {
	server := model.Server{
		ID:       "alpha",
		Operator: "Alpha DNS",
		Address:  "192.0.2.1",
		Protocol: model.ProtoUDP,
	}
	cfg := testConfig()
	cfg.PaceInterval = 20 * time.Millisecond
	cfg.PaceAdaptive = true
	e := NewEngine([]model.Server{server}, cfg, nil)
	e.pace.interval = 40 * time.Millisecond
	e.pace.suppressUntil = time.Now().Add(-time.Second)
	for range e.pace.cleanTarget {
		e.observePace(context.Background(), server, model.CatCached, time.Now(), okResult(time.Millisecond))
	}
	adjustments := e.PaceAdjustments()
	if len(adjustments) != 1 {
		t.Fatalf("recorded pace adjustments = %d, want one recovery", len(adjustments))
	}
	got := adjustments[0]
	if got.Reason != model.PaceCleanAnswerRecovery ||
		got.FromInterval != 40*time.Millisecond ||
		got.ToInterval != 30*time.Millisecond ||
		got.CleanAnswers != e.pace.cleanTarget {
		t.Fatalf("recorded recovery = %+v", got)
	}
	select {
	case ev := <-e.Events():
		t.Fatalf("recovery emitted a live warning event: %+v", ev)
	default:
	}
}

func TestEngineSpacesQueryLaunchesAcrossServersAndTriage(t *testing.T) {
	f := newFakeFactory()
	var mu sync.Mutex
	var starts []time.Time
	record := func(_ context.Context, _ int, _ transport.Question) model.QueryResult {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		return okResult(time.Millisecond)
	}
	ids := []string{"s1", "s2", "s3"}
	for _, id := range ids {
		f.script(id, record)
	}
	cfg := testConfig()
	cfg.WarmupRounds = 0
	cfg.Rounds = 2
	cfg.Categories = []model.Category{model.CatCached}
	cfg.PaceInterval = 50 * time.Millisecond
	cfg.PerServerGap = 0
	cfg.TriageEnabled = true
	cfg.TriageAttempts = 1
	cfg.TriageThreshold = 50 * time.Millisecond
	e := NewEngine(testServers(ids...), cfg, f.factory)
	if _, err := runEngine(t, e, context.Background()); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 9 {
		t.Fatalf("query launches = %d, want 9 (3 triage + 6 bench)", len(starts))
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	for i := 1; i < len(starts); i++ {
		if gap := starts[i].Sub(starts[i-1]); gap < cfg.PaceInterval-20*time.Millisecond {
			t.Errorf("launch gap %d = %s, want at least approximately %s", i, gap, cfg.PaceInterval)
		}
	}
}

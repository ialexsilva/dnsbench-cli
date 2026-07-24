package bench

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"dnsbench/internal/model"
	"dnsbench/internal/transport"
)

func TestPacerSpacesSequentialWaits(t *testing.T) {
	p := newPacer(20*time.Millisecond, 1, 0)
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
	if newPacer(time.Hour, 1, 0).wait(ctx) {
		t.Error("live pacer wait = true on canceled context")
	}
	var p *pacer
	if p.wait(ctx) {
		t.Error("nil pacer wait = true on canceled context")
	}
}

func markAnswered(p *pacer, ids ...string) {
	for _, id := range ids {
		p.observe(id, false)
	}
}

func TestPacerBacksOffOnCrossServerTimeouts(t *testing.T) {
	p := newPacer(20*time.Millisecond, 1, 0)
	markAnswered(p, "s1", "s2", "s3")
	if _, changed := p.observe("s1", true); changed {
		t.Fatal("backed off after one server timed out")
	}
	if _, changed := p.observe("s2", true); changed {
		t.Fatal("backed off after two servers timed out")
	}
	adj, changed := p.observe("s3", true)
	if !changed || adj.faster {
		t.Fatalf("adjust = %+v, changed = %t, want a backoff on the third distinct server", adj, changed)
	}
	if adj.from != 20*time.Millisecond || adj.to != 40*time.Millisecond {
		t.Fatalf("backoff %s -> %s, want 20ms -> 40ms", adj.from, adj.to)
	}
	if adj.servers != 3 || adj.timeouts != 3 {
		t.Fatalf("adjust = %+v, want 3 servers and 3 timeouts", adj)
	}
}

func TestPacerIgnoresSingleServerTimeouts(t *testing.T) {
	p := newPacer(20*time.Millisecond, 1, 0)
	markAnswered(p, "s1")
	for i := 0; i < 10; i++ {
		if _, changed := p.observe("s1", true); changed {
			t.Fatal("backed off for timeouts concentrated on a single server")
		}
	}
	if p.interval != 20*time.Millisecond {
		t.Fatalf("interval = %s, want unchanged 20ms", p.interval)
	}
}

func TestPacerIgnoresNeverAnsweredServers(t *testing.T) {
	p := newPacer(20*time.Millisecond, 1, 0)
	for _, id := range []string{"dead1", "dead2", "dead3", "dead4"} {
		if _, changed := p.observe(id, true); changed {
			t.Fatal("backed off for servers that never answered")
		}
	}
	if p.interval != 20*time.Millisecond {
		t.Fatalf("interval = %s, want unchanged 20ms", p.interval)
	}
}

func TestPacerCooldownAndCeiling(t *testing.T) {
	p := newPacer(20*time.Millisecond, 1, 0)
	markAnswered(p, "s1", "s2", "s3")
	trigger := func() bool {
		var changed bool
		for _, id := range []string{"s1", "s2", "s3"} {
			_, c := p.observe(id, true)
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
	if p.interval != 160*time.Millisecond {
		t.Fatalf("interval = %s, want the 160ms ceiling", p.interval)
	}
}

func TestPacerSpeedsUpAfterCleanTrafficDownToFloor(t *testing.T) {
	p := newPacer(20*time.Millisecond, 1, 0)
	sawFaster := false
	for i := 0; i < 20*p.cleanTarget; i++ {
		adj, changed := p.observe("s1", false)
		if changed {
			if !adj.faster || adj.to >= adj.from {
				t.Fatalf("adjust = %+v, want a speed-up", adj)
			}
			sawFaster = true
		}
	}
	if !sawFaster {
		t.Fatal("clean traffic never sped the pacer up")
	}
	if p.interval != 5*time.Millisecond {
		t.Fatalf("interval = %s, want the 5ms floor", p.interval)
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
	cfg.PerServerGap = 0
	e := NewEngine(testServers("s1", "s2", "s3", "s4"), cfg, f.factory)
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
	for _, ev := range events {
		if ev.Type == model.EvPaceAdjust && !strings.Contains(ev.Msg, "local congestion") {
			t.Fatalf("pace event message = %q", ev.Msg)
		}
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

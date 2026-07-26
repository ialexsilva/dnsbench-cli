package ui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"dnsbench/internal/model"
)

func liveServers() []model.Server {
	return []model.Server{
		{ID: "a", Name: "Alpha DNS", Address: "127.0.0.1", Protocol: model.ProtoUDP, Source: model.SourceBuiltin},
		{ID: "b", Name: "Beta DNS", Address: "127.0.0.2", Protocol: model.ProtoUDP, Source: model.SourceBuiltin},
	}
}

func sampleEvent(id string, cat model.Category, ms float64, lost bool) model.Event {
	res := model.QueryResult{
		RTT:     time.Duration(ms * float64(time.Millisecond)),
		Answers: []model.RR{{Type: "A", Data: "192.0.2.1"}},
	}
	if cat == model.CatTLD {
		res.Rcode = 3
		res.Answers = nil
	}
	if lost {
		res.Err = &model.QueryError{Kind: model.ErrTimeout, Msg: "timeout"}
	}
	return model.Event{Type: model.EvSample, ServerID: id, Sample: &model.Sample{
		ServerID: id,
		Category: cat,
		Result:   res,
	}}
}

func invalidSampleEvent(id string, cat model.Category, ms float64) model.Event {
	return model.Event{Type: model.EvSample, ServerID: id, Sample: &model.Sample{
		ServerID: id,
		Category: cat,
		Result: model.QueryResult{
			RTT:   time.Duration(ms * float64(time.Millisecond)),
			Rcode: 2,
		},
	}}
}

func runLive(t *testing.T, l *Live, events []model.Event) string {
	t.Helper()
	ch := make(chan model.Event)
	done := make(chan struct{})
	go func() {
		l.Run(ch)
		close(done)
	}()
	for _, ev := range events {
		select {
		case ch <- ev:
		case <-time.After(5 * time.Second):
			t.Fatal("live consumer stalled")
		}
	}
	close(ch)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("live did not finish after channel close")
	}
	return ""
}

func TestLiveNonTTY(t *testing.T) {
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.Rounds = 20
	var buf bytes.Buffer
	l := NewLive(liveServers(), cfg, &buf, false, "median")
	events := []model.Event{
		sampleEvent("a", model.CatCached, 8, false),
		sampleEvent("b", model.CatCached, 15, false),
		{Type: model.EvRoundDone, Round: 2},
		{Type: model.EvRoundDone, Round: 10},
		{Type: model.EvWarn, Msg: "clock skew detected"},
		{Type: model.EvConnLost},
		{Type: model.EvConnRestored},
		{Type: model.EvStateChange, ServerID: "b", State: model.StateOffline, Msg: "no route to host"},
		{Type: model.EvRoundDone, Round: 20},
		{Type: model.EvDone},
	}
	runLive(t, l, events)
	out := buf.String()
	for _, want := range []string{
		"dnsbench — benchmarking 2 resolvers",
		"progress  10%",
		"progress  50%",
		"progress 100%",
		"round 20/20",
		"warning: clock skew detected",
		"connectivity lost",
		"connectivity restored",
		"Beta DNS: unreachable (no route to host)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("non-TTY output missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("non-TTY output contains ANSI escapes\n%q", out)
	}
}

func TestLiveNonTTYProgressStep(t *testing.T) {
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.Rounds = 100
	var buf bytes.Buffer
	l := NewLive(liveServers(), cfg, &buf, false, "median")
	var events []model.Event
	for r := 1; r <= 100; r++ {
		events = append(events, model.Event{Type: model.EvRoundDone, Round: r})
	}
	runLive(t, l, events)
	out := buf.String()
	if got := strings.Count(out, "progress"); got != 10 {
		t.Errorf("expected 10 progress lines, got %d\n%s", got, out)
	}
}

func TestLiveTTYClosedImmediately(t *testing.T) {
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	var buf bytes.Buffer
	l := NewLive(liveServers(), cfg, &buf, true, "median")
	runLive(t, l, nil)
	out := buf.String()
	if !strings.Contains(out, "\x1b[?1049h") || !strings.Contains(out, "\x1b[?1049l") {
		t.Errorf("TTY output missing alternate-screen lifecycle\n%q", out)
	}
	if !strings.Contains(out, "dnsbench — evaluating 2 resolvers") {
		t.Errorf("TTY output missing header\n%q", out)
	}
}

func TestLiveTTYFrameFitsViewport(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	servers := make([]model.Server, 30)
	for i := range servers {
		servers[i] = model.Server{
			ID:       fmt.Sprintf("server-%02d", i+1),
			Name:     fmt.Sprintf("Server %02d", i+1),
			Protocol: model.ProtoUDP,
		}
	}
	var buf bytes.Buffer
	l := NewLive(servers, cfg, &buf, true, "name")
	l.width = 120
	l.height = 10
	frame := l.renderFrame()
	if lines := strings.Count(frame, "\n"); lines > 9 {
		t.Fatalf("frame has %d lines, want at most 9:\n%s", lines, frame)
	}
	if !strings.Contains(frame, "showing top 2/30 active") {
		t.Fatalf("frame missing truncation status:\n%s", frame)
	}
	if strings.Contains(frame, "Server 03") {
		t.Fatalf("frame rendered rows outside the viewport:\n%s", frame)
	}
}

func TestLiveModelUpdatesStateWithoutWritingDirectly(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.Rounds = 10
	var buf bytes.Buffer
	l := NewLive(liveServers(), cfg, &buf, true, "median")

	updated, cmd := l.Update(tea.WindowSizeMsg{Width: 90, Height: 9})
	if updated != l || cmd != nil {
		t.Fatalf("window update returned model=%T cmd=%v", updated, cmd)
	}
	if l.width != 90 || l.height != 9 {
		t.Fatalf("window size = %dx%d, want 90x9", l.width, l.height)
	}

	updated, cmd = l.Update(benchmarkEventMsg{event: sampleEvent("a", model.CatCached, 7, false)})
	if updated != l || cmd != nil {
		t.Fatalf("sample update returned model=%T cmd=%v", updated, cmd)
	}
	if buf.Len() != 0 {
		t.Fatalf("Update wrote directly to output instead of letting Bubble Tea render: %q", buf.String())
	}
	if frame := l.View(); !strings.Contains(frame, "7.0 ms") {
		t.Fatalf("updated view missing sample:\n%s", frame)
	}

	_, cmd = l.Update(benchmarkEventMsg{event: model.Event{Type: model.EvRoundDone, Round: 5}})
	if cmd == nil {
		t.Fatal("round update did not request a viewport refresh")
	}
}

func TestLiveProgressShowsTriageWarmupAndBenchmarkPhases(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.Rounds = 250
	cfg.WarmupRounds = 2
	l := NewLive(liveServers(), cfg, &bytes.Buffer{}, true, "median")
	l.width = 80

	first := l.progressLine()
	if !strings.Contains(first, "triaging resolvers…") {
		t.Fatalf("initial progress is missing the triage phase: %q", first)
	}
	if strings.Contains(first, "/") {
		t.Fatalf("triage progress contains a transient counter: %q", first)
	}
	if strings.Contains(first, "0%") || strings.Contains(first, "round 0/") {
		t.Fatalf("initial progress should be indeterminate instead of showing zero: %q", first)
	}

	updated, cmd := l.Update(livePrepareTickMsg{})
	if updated != l || cmd == nil {
		t.Fatalf("preparation tick returned model=%T cmd=%v", updated, cmd)
	}
	if second := l.progressLine(); second == first {
		t.Fatalf("triage bar did not move after a tick: %q", second)
	}

	l.handle(model.Event{Type: model.EvQueryStart, ServerID: "a", Round: 0})
	if triage := l.progressLine(); !strings.Contains(triage, "triaging resolvers…") {
		t.Fatalf("triage query changed the phase prematurely: %q", triage)
	}
	for _, server := range liveServers() {
		l.handle(model.Event{Type: model.EvTriage, Triage: &model.TriageResult{
			ServerID: server.ID,
			State:    model.StateActive,
		}})
	}
	if warmup := l.progressLine(); !strings.Contains(warmup, "warming up resolvers…") {
		t.Fatalf("completed triage did not enter warmup: %q", warmup)
	} else if strings.Contains(warmup, "/") {
		t.Fatalf("warmup progress contains a transient counter: %q", warmup)
	}

	l.handle(model.Event{Type: model.EvQueryStart, ServerID: "a", Round: 0})
	if warmup := l.progressLine(); !strings.Contains(warmup, "warming up resolvers…") {
		t.Fatalf("first warmup round is not visible: %q", warmup)
	}
	l.handle(model.Event{Type: model.EvRoundDone, Round: 0})
	l.handle(model.Event{Type: model.EvQueryStart, ServerID: "a", Round: 0})
	if warmup := l.progressLine(); !strings.Contains(warmup, "warming up resolvers…") {
		t.Fatalf("second warmup round is not visible: %q", warmup)
	}

	l.handle(model.Event{Type: model.EvQueryStart, ServerID: "a", Round: 1})
	running := l.progressLine()
	if strings.Contains(running, "warming up") || strings.Contains(running, "triaging") {
		t.Fatalf("progress remained in preparation after measurement started: %q", running)
	}
	if !strings.Contains(running, "0%") || !strings.Contains(running, "round 1/250") {
		t.Fatalf("first measured query progress = %q, want 0%% and round 1/250", running)
	}

	firstSample := sampleEvent("a", model.CatCached, 8, false)
	firstSample.Sample.Round = 1
	l.handle(firstSample)
	if inProgress := l.progressLine(); !strings.Contains(inProgress, "1%") {
		t.Fatalf("first measured sample progress = %q, want 1%%", inProgress)
	}

	l.handle(model.Event{Type: model.EvRoundDone, Round: 1})
	completed := l.progressLine()
	if !strings.Contains(completed, "  1%") || !strings.Contains(completed, "round 1/250") {
		t.Fatalf("first completed round progress = %q, want 1%% and round 1/250", completed)
	}
}

func TestLiveQuickProgressStartsAtZeroAndAdvancesWithinFirstRound(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.TriageEnabled = false
	cfg.WarmupRounds = 0
	l := NewLive(liveServers(), cfg, &bytes.Buffer{}, true, "median")
	l.width = 80

	l.handle(model.Event{Type: model.EvQueryStart, ServerID: "a", Round: 1})
	started := l.progressLine()
	if !strings.Contains(started, "0%") || !strings.Contains(started, "round 1/50") {
		t.Fatalf("first quick round progress = %q, want 0%% and round 1/50", started)
	}
	if strings.ContainsAny(started, "░▒▓") || strings.Contains(started, "benchmarking ·") {
		t.Fatalf("first quick round still uses the indeterminate benchmark bar: %q", started)
	}

	firstSample := sampleEvent("a", model.CatCached, 8, false)
	firstSample.Sample.Round = 1
	l.handle(firstSample)
	if inProgress := l.progressLine(); !strings.Contains(inProgress, "1%") {
		t.Fatalf("first quick sample progress = %q, want 1%% before round completion", inProgress)
	}

	l.handle(model.Event{Type: model.EvRoundDone, Round: 1})
	completed := l.progressLine()
	if !strings.Contains(completed, "  2%") {
		t.Fatalf("first completed quick round progress = %q, want 2%%", completed)
	}
}

func TestLiveProgressDoesNotReach100BeforeFinalRound(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.Rounds = 250
	l := NewLive(liveServers(), cfg, &bytes.Buffer{}, true, "median")
	l.width = 80

	l.handle(model.Event{Type: model.EvRoundDone, Round: 249})
	penultimate := l.progressLine()
	if !strings.Contains(penultimate, "99%") || strings.Contains(penultimate, "100%") {
		t.Fatalf("penultimate progress = %q, want 99%% and not 100%%", penultimate)
	}

	l.handle(model.Event{Type: model.EvRoundDone, Round: 250})
	if complete := l.progressLine(); !strings.Contains(complete, "100%") {
		t.Fatalf("completed progress = %q, want 100%%", complete)
	}
}

func TestLiveProgressUpdatesElapsedOnClockAndETAOnCompletedRounds(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.TriageEnabled = false
	cfg.WarmupRounds = 0
	cfg.Rounds = 20
	l := NewLive(liveServers(), cfg, &bytes.Buffer{}, true, "median")
	l.width = 120
	base := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	l.benchmarkStart = base

	completeRound := func(round int, elapsed time.Duration) {
		l.round = round
		l.roundStarted = round
		l.noteRoundCompleted(round, base.Add(elapsed))
	}
	completeRound(1, 7*time.Second)
	completeRound(2, 15*time.Second)
	completeRound(3, 24*time.Second)
	if progress := l.progressLine(); !strings.Contains(progress, "ETA ~") {
		t.Fatalf("initial ETA should use the tilde placeholder: %q", progress)
	}

	completeRound(4, 34*time.Second)
	_, _ = l.Update(livePrepareTickMsg{at: base.Add(34 * time.Second)})
	progress := l.progressLine()
	if !strings.Contains(progress, "elapsed 0:34") || !strings.Contains(progress, "ETA ~2:15") {
		t.Fatalf("stable progress line = %q, want elapsed 0:34 and rounded ETA 2:15", progress)
	}
	if got, want := l.etaEstimate, 135*time.Second; got != want {
		t.Fatalf("ETA = %s, want %s", got, want)
	}

	l.handle(sampleEvent("a", model.CatCached, 8, false))
	if afterSample := l.progressLine(); afterSample != progress {
		t.Fatalf("query callback changed timing text:\nbefore: %q\nafter:  %q", progress, afterSample)
	}

	_, cmd := l.Update(livePrepareTickMsg{at: base.Add(35 * time.Second)})
	if cmd == nil {
		t.Fatal("clock tick did not schedule the next one-second update")
	}
	afterTick := l.progressLine()
	if !strings.Contains(afterTick, "elapsed 0:35") || !strings.Contains(afterTick, "ETA ~2:15") {
		t.Fatalf("clock tick changed ETA unexpectedly: %q", afterTick)
	}
}

func TestOscillatingBarBounces(t *testing.T) {
	disableColors(t)

	if got, want := oscillatingBar(10, 0), "▏░▒▓█····▕"; got != want {
		t.Fatalf("initial oscillating bar = %q, want %q", got, want)
	}
	if got, want := oscillatingBar(10, 4), "▏····░▒▓█▕"; got != want {
		t.Fatalf("far edge oscillating bar = %q, want %q", got, want)
	}
	if got, want := oscillatingBar(10, 6), "▏··█▓▒░··▕"; got != want {
		t.Fatalf("returning oscillating bar = %q, want %q", got, want)
	}
}

func TestLiveLossAndInvalidAreLabeledSeparately(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.Categories = []model.Category{model.CatCached}
	l := NewLive(liveServers(), cfg, &bytes.Buffer{}, true, "median")
	l.width = 120
	l.height = 12
	l.handle(sampleEvent("a", model.CatCached, 8, false))
	l.handle(sampleEvent("a", model.CatCached, 9, true))
	l.handle(invalidSampleEvent("a", model.CatCached, 10))

	frame := l.renderFrame()
	for _, want := range []string{
		"loss = unanswered queries",
		"inv = unusable answers",
		"loss 33.3% (1/3)",
		"inv 1",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame missing %q:\n%s", want, frame)
		}
	}
}

func TestLiveHighlightsServerUnderTest(t *testing.T) {
	enableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.Categories = []model.Category{model.CatCached}
	l := NewLive(liveServers(), cfg, &bytes.Buffer{}, true, "name")
	l.width = 120
	l.height = 12

	l.handle(model.Event{Type: model.EvQueryStart, ServerID: "a"})
	frame := l.renderFrame()
	if !strings.Contains(frame, Cyan("Alpha DNS")[:5]) {
		t.Fatalf("server under test is not highlighted:\n%q", frame)
	}
	aCyan := strings.Contains(frame, "\x1b[36mAlpha DNS")
	bCyan := strings.Contains(frame, "\x1b[36mBeta DNS")
	if !aCyan || bCyan {
		t.Fatalf("only the in-flight server should be cyan (a=%v b=%v):\n%q", aCyan, bCyan, frame)
	}

	l.handle(sampleEvent("a", model.CatCached, 8, false))
	if l.inflight["a"] {
		t.Fatal("sample did not clear the in-flight highlight")
	}
	if strings.Contains(l.renderFrame(), "\x1b[36mAlpha DNS") {
		t.Fatalf("highlight persisted after the query finished")
	}
	if legend := l.legendLine(); !strings.Contains(legend, Cyan("name")+" = querying") {
		t.Fatalf("legend does not explain the cyan activity state: %q", legend)
	}
}

func TestLivePaceNoticeWrapsWithIcon(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.Categories = []model.Category{model.CatCached}
	l := NewLive(liveServers(), cfg, &bytes.Buffer{}, true, "median")
	l.width = 40
	l.height = 16
	msg := "local congestion — pacing eased 5ms → 10ms (3 timeouts on 3 servers)"
	l.handle(model.Event{Type: model.EvPaceAdjust, Msg: msg})

	frame := l.renderFrame()
	if !strings.Contains(frame, "⚠") {
		t.Fatalf("pace notice missing icon:\n%s", frame)
	}
	notice := l.noticeLines()
	if len(notice) < 2 || len(notice) > maxNoticeLines {
		t.Fatalf("expected the long notice to wrap to 2 lines, got %d:\n%v", len(notice), notice)
	}
	for _, ln := range notice {
		if visibleWidth(ln) > l.width {
			t.Fatalf("wrapped notice line exceeds width %d: %q (%d)", l.width, ln, visibleWidth(ln))
		}
	}
}

func TestLiveSharedP90ScaleMarksOutlier(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	servers := make([]model.Server, 5)
	for i := range servers {
		servers[i] = model.Server{
			ID:   fmt.Sprintf("s%d", i),
			Name: fmt.Sprintf("Server %d", i),
		}
	}
	l := NewLive(servers, cfg, &bytes.Buffer{}, true, "median")
	l.width = 120
	l.height = 30
	value := 10.0
	for _, server := range servers {
		for _, category := range cfg.Categories {
			l.handle(sampleEvent(server.ID, category, value, false))
			value += 10
		}
	}

	active, _ := l.partition()
	if got := l.p90Scale(active, "median"); got != 90 {
		t.Fatalf("P90 scale = %.1f, want 90.0", got)
	}
	frame := l.renderFrame()
	if !strings.Contains(frame, "scale P90 90.0 ms") {
		t.Fatalf("frame missing shared scale:\n%s", frame)
	}
	if !strings.Contains(frame, "›") {
		t.Fatalf("frame missing above-P90 marker:\n%s", frame)
	}
}

func TestLiveLatencyMetricMatchesSort(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.Categories = []model.Category{model.CatCached}

	for sortKey, wantValue := range map[string]string{"mean": "34.0 ms", "p95": "100.0 ms"} {
		t.Run(sortKey, func(t *testing.T) {
			l := NewLive(liveServers(), cfg, &bytes.Buffer{}, true, sortKey)
			for _, ms := range []float64{1, 1, 100} {
				l.handle(sampleEvent("a", model.CatCached, ms, false))
			}
			for _, ms := range []float64{10, 10, 10} {
				l.handle(sampleEvent("b", model.CatCached, ms, false))
			}

			active, _ := l.partition()
			if got := active[0].server.ID; got != "b" {
				t.Fatalf("first server sorted by %s = %s, want b", sortKey, got)
			}
			if got := l.latencyMetric(); got != sortKey {
				t.Fatalf("latency metric = %q, want %q", got, sortKey)
			}
			frame := l.renderFrame()
			if !strings.Contains(frame, sortKey+" · lower is better") {
				t.Fatalf("frame does not describe %s metric:\n%s", sortKey, frame)
			}
			if !strings.Contains(frame, wantValue) {
				t.Fatalf("frame does not show %s value %q:\n%s", sortKey, wantValue, frame)
			}
		})
	}
}

func TestLiveNonLatencySortDescriptions(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	for sortKey, want := range map[string]string{
		"loss":  "order loss · bars median · lower is better",
		"name":  "order name · bars median · lower is better",
		"cost":  "order median · final ranking uses latency cost",
		"score": "order median · final ranking uses latency cost",
	} {
		t.Run(sortKey, func(t *testing.T) {
			l := NewLive(liveServers(), cfg, &bytes.Buffer{}, true, sortKey)
			l.width = 120
			l.handle(sampleEvent("a", model.CatCached, 8, false))
			frame := l.renderFrame()
			if !strings.Contains(frame, want) {
				t.Fatalf("frame does not explain %s live order:\n%s", sortKey, frame)
			}
		})
	}
}

func TestLiveResponsiveWidthsDoNotWrap(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.Categories = model.AllCategories()
	servers := liveServers()

	for _, width := range []int{50, 60, 80, 120, 160} {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			l := NewLive(servers, cfg, &bytes.Buffer{}, true, "median")
			l.width = width
			l.height = 12
			for _, server := range servers {
				for i, category := range cfg.Categories {
					l.handle(sampleEvent(server.ID, category, float64(10+i*15), false))
				}
			}

			frame := l.renderFrame()
			if lines := strings.Count(frame, "\n"); lines > l.height-1 {
				t.Fatalf("frame has %d lines, want at most %d:\n%s", lines, l.height-1, frame)
			}
			for _, line := range strings.Split(strings.TrimSuffix(frame, "\n"), "\n") {
				if got := visibleWidth(line); got > width {
					t.Fatalf("line width = %d, want <= %d:\n%s", got, width, frame)
				}
			}
			layout := l.makeRowLayout(serversAsLive(l, servers))
			if width < liveNarrowWidth && layout.showBars {
				t.Fatalf("width %d unexpectedly enabled bars", width)
			}
			if width >= liveNarrowWidth && !layout.showBars {
				t.Fatalf("width %d unexpectedly disabled bars", width)
			}
		})
	}
}

func TestLiveServerRowsUseCategoryMarkersWithoutColor(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.Categories = model.AllCategories()
	l := NewLive(liveServers(), cfg, &bytes.Buffer{}, true, "median")
	l.width = 100
	for i, category := range cfg.Categories {
		l.handle(sampleEvent("a", category, float64(10+i*5), false))
	}
	layout := l.makeRowLayout([]*liveServer{l.byID["a"]})
	lines := strings.Join(l.serverLines(1, l.byID["a"], "median", 15, layout), "\n")
	if strings.Contains(lines, "cached") || strings.Contains(lines, "recursive/TLD") {
		t.Fatalf("server rows repeated category labels already present in legend:\n%s", lines)
	}
	for _, marker := range []string{"C ", "U ", "T "} {
		if !strings.Contains(lines, marker) {
			t.Fatalf("server rows are missing the %q category marker:\n%s", marker, lines)
		}
	}
	if !layout.categoryMarkers {
		t.Fatalf("no-color layout did not reserve category markers: %+v", layout)
	}
	if strings.Contains(lines, "█") || !strings.Contains(lines, "▄") || !strings.Contains(lines, "▀") {
		t.Fatalf("server rows must use touching lower/upper half-height bars:\n%s", lines)
	}
}

func TestLiveLatencyTrackUsesCompactHalfBlocks(t *testing.T) {
	disableColors(t)
	l := &Live{}

	if got, want := l.latencyTrack(5, 10, 8, "▄", func(s string) string { return s }, true), "▄▄▄▄────"; got != want {
		t.Fatalf("latencyTrack() = %q, want %q", got, want)
	}
	if got, want := l.latencyTrack(11, 10, 8, "▀", func(s string) string { return s }, true), "▀▀▀▀▀▀▀›"; got != want {
		t.Fatalf("latencyTrack() above scale = %q, want %q", got, want)
	}
}

func TestLiveSmallViewportKeepsCategoryBlockComplete(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.Categories = model.AllCategories()
	l := NewLive(liveServers(), cfg, &bytes.Buffer{}, true, "median")
	l.width = 100
	l.height = 8
	for i, category := range cfg.Categories {
		l.handle(sampleEvent("a", category, float64(10+i*5), false))
	}

	frame := l.renderFrame()
	if lines := strings.Count(frame, "\n"); lines != 7 {
		t.Fatalf("frame has %d lines, want 7:\n%s", lines, frame)
	}
	for _, label := range []string{"cached", "uncached", "recursive/TLD"} {
		if !strings.Contains(frame, label) {
			t.Fatalf("small frame omitted category %q:\n%s", label, frame)
		}
	}
	if strings.Contains(frame, "Beta DNS") {
		t.Fatalf("small frame rendered a partial second block:\n%s", frame)
	}
}

func serversAsLive(l *Live, servers []model.Server) []*liveServer {
	out := make([]*liveServer, 0, len(servers))
	for _, server := range servers {
		out = append(out, l.byID[server.ID])
	}
	return out
}

func TestLiveTTYFrame(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	cfg.Rounds = 10
	var buf bytes.Buffer
	l := NewLive(liveServers(), cfg, &buf, true, "median")
	events := []model.Event{
		sampleEvent("a", model.CatCached, 8, false),
		sampleEvent("a", model.CatTLD, 14, false),
		sampleEvent("b", model.CatCached, 20, true),
		{Type: model.EvTriage, Triage: &model.TriageResult{ServerID: "b", State: model.StateBenched, Reason: "too slow in triage"}},
		{Type: model.EvConnLost, Msg: "default route gone"},
		{Type: model.EvRoundDone, Round: 5},
	}
	runLive(t, l, events)
	out := buf.String()
	for _, want := range []string{
		"Alpha DNS",
		"cached", "recursive/TLD",
		"Beta DNS — sidelined: too slow in triage",
		"connectivity lost — benchmark paused (default route gone)",
		"8.0 ms",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("TTY frame missing %q\n%s", want, out)
		}
	}
}

func TestLiveSortByName(t *testing.T) {
	disableColors(t)
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	var buf bytes.Buffer
	l := NewLive(liveServers(), cfg, &buf, true, "name")
	events := []model.Event{
		sampleEvent("b", model.CatCached, 5, false),
		sampleEvent("a", model.CatCached, 9, false),
	}
	runLive(t, l, events)
	out := buf.String()
	frame := out[strings.LastIndex(out, "dnsbench"):]
	ai := strings.Index(frame, "Alpha DNS")
	bi := strings.Index(frame, "Beta DNS")
	if ai < 0 || bi < 0 || ai > bi {
		t.Errorf("name sort wrong: alpha=%d beta=%d\n%s", ai, bi, frame)
	}
}

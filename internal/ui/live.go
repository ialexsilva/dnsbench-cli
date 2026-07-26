package ui

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"dnsbench/internal/model"
)

const (
	livePreferredName  = 24
	liveMinNameWidth   = 8
	liveMinBarWidth    = 8
	liveMsWidth        = 9
	liveProgressWidth  = 28
	liveFPS            = 12
	liveFallbackWidth  = 120
	liveFallbackHeight = 24
	liveMinHeight      = 8
	liveNarrowWidth    = 72
	liveTinyWidth      = 56
	liveDetailedWidth  = 96
	liveFullLegend     = 116
)

type liveAgg struct {
	sorted  []float64
	sum     float64
	lost    int
	invalid int
}

func (a *liveAgg) add(ms float64) {
	i := sort.SearchFloat64s(a.sorted, ms)
	a.sorted = append(a.sorted, 0)
	copy(a.sorted[i+1:], a.sorted[i:])
	a.sorted[i] = ms
	a.sum += ms
}

func (a *liveAgg) median() float64 {
	n := len(a.sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return a.sorted[n/2]
	}
	return (a.sorted[n/2-1] + a.sorted[n/2]) / 2
}

func (a *liveAgg) mean() float64 {
	if len(a.sorted) == 0 {
		return 0
	}
	return a.sum / float64(len(a.sorted))
}

func (a *liveAgg) p95() float64 {
	n := len(a.sorted)
	if n == 0 {
		return 0
	}
	i := int(math.Ceil(0.95*float64(n))) - 1
	if i < 0 {
		i = 0
	}
	if i >= n {
		i = n - 1
	}
	return a.sorted[i]
}

func (a *liveAgg) value(metric string) (float64, bool) {
	if a == nil || len(a.sorted) == 0 {
		return 0, false
	}
	switch metric {
	case "mean":
		return a.mean(), true
	case "p95":
		return a.p95(), true
	default:
		return a.median(), true
	}
}

type liveServer struct {
	server model.Server
	state  model.ServerState
	reason string
	cats   map[model.Category]*liveAgg
}

type benchmarkEventMsg struct {
	event model.Event
}

type eventsClosedMsg struct{}
type livePrepareTickMsg struct{}

func (ls *liveServer) totalSamples() int {
	total, _, _ := ls.counts()
	return total
}

func (ls *liveServer) lossPct() float64 {
	total, lost, _ := ls.counts()
	if total == 0 {
		return 0
	}
	return float64(lost) / float64(total) * 100
}

func (ls *liveServer) counts() (total, lost, invalid int) {
	for _, a := range ls.cats {
		total += len(a.sorted) + a.lost + a.invalid
		lost += a.lost
		invalid += a.invalid
	}
	return total, lost, invalid
}

type Live struct {
	servers      []model.Server
	cfg          model.BenchConfig
	out          io.Writer
	isTTY        bool
	sortKey      string
	categories   []model.Category
	byID         map[string]*liveServer
	inflight     map[string]bool
	start        time.Time
	round        int
	roundStarted int
	prepareFrame int
	lastPctTen   int
	noticeText   string
	noticeColor  func(string) string
	width        int
	height       int
}

const maxNoticeLines = 2

func NewLive(servers []model.Server, cfg model.BenchConfig, out io.Writer, isTTY bool, sortKey string) *Live {
	cats := cfg.Categories
	if len(cats) == 0 {
		cats = model.AllCategories()
	}
	byID := make(map[string]*liveServer, len(servers))
	for _, s := range servers {
		ls := &liveServer{server: s, state: model.StateActive, cats: map[model.Category]*liveAgg{}}
		for _, c := range cats {
			ls.cats[c] = &liveAgg{}
		}
		byID[s.ID] = ls
	}
	return &Live{
		servers:    servers,
		cfg:        cfg,
		out:        out,
		isTTY:      isTTY,
		sortKey:    normalizeLiveSortKey(sortKey),
		categories: cats,
		byID:       byID,
		inflight:   make(map[string]bool, len(servers)),
		start:      time.Now(),
		width:      liveFallbackWidth,
		height:     liveFallbackHeight,
	}
}

func (l *Live) Run(events <-chan model.Event) {
	l.start = time.Now()
	if !l.isTTY {
		fmt.Fprintf(l.out, "dnsbench — benchmarking %d servers\n", len(l.servers))
		for ev := range events {
			l.handle(ev)
		}
		return
	}

	program := tea.NewProgram(
		l,
		tea.WithAltScreen(),
		tea.WithOutput(l.out),
		tea.WithInput(nil),
		tea.WithFPS(liveFPS),
		tea.WithoutBracketedPaste(),
		tea.WithoutSignalHandler(),
	)
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		for ev := range events {
			program.Send(benchmarkEventMsg{event: ev})
		}
		program.Send(eventsClosedMsg{})
	}()

	if _, err := program.Run(); err != nil {
		fmt.Fprintf(l.out, "warning: live UI stopped: %v\n", err)
	}
	// If terminal setup or rendering fails, keep draining events so the
	// benchmark engine can still complete and produce its final report.
	<-pumpDone
}

func (l *Live) Init() tea.Cmd {
	return l.prepareTick()
}

func (l *Live) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			l.width = msg.Width
		}
		if msg.Height > 0 {
			l.height = msg.Height
		}
	case benchmarkEventMsg:
		l.handle(msg.event)
		if msg.event.Type == model.EvRoundDone {
			// SIGWINCH is unavailable on Windows, so also poll the viewport
			// between rounds. Other platforms still receive resize messages.
			return l, tea.WindowSize()
		}
	case eventsClosedMsg:
		return l, tea.Quit
	case livePrepareTickMsg:
		if l.roundStarted > 0 {
			return l, nil
		}
		l.prepareFrame++
		return l, l.prepareTick()
	}
	return l, nil
}

func (l *Live) View() string {
	return l.renderFrame()
}

func (l *Live) prepareTick() tea.Cmd {
	return tea.Tick(time.Second/liveFPS, func(time.Time) tea.Msg {
		return livePrepareTickMsg{}
	})
}

func (l *Live) handle(ev model.Event) {
	switch ev.Type {
	case model.EvQueryStart:
		l.noteRoundStarted(ev.Round)
		if ev.ServerID != "" {
			l.inflight[ev.ServerID] = true
		}
	case model.EvSample:
		if ev.Sample != nil {
			l.noteRoundStarted(ev.Sample.Round)
			delete(l.inflight, ev.Sample.ServerID)
		}
		l.recordSample(ev.Sample)
	case model.EvRoundDone:
		l.round = ev.Round
		l.noteRoundStarted(ev.Round)
		if !l.isTTY {
			l.printProgress()
		}
	case model.EvTriage:
		if ev.Triage != nil {
			delete(l.inflight, ev.Triage.ServerID)
			l.setState(ev.Triage.ServerID, ev.Triage.State, ev.Triage.Reason, false)
		}
	case model.EvStateChange:
		delete(l.inflight, ev.ServerID)
		l.setState(ev.ServerID, ev.State, ev.Msg, true)
	case model.EvWarn:
		l.notice("warning: "+ev.Msg, Yellow)
	case model.EvPaceAdjust:
		l.notice("⚠ "+ev.Msg, Yellow)
	case model.EvConnLost:
		l.notice(connLine("connectivity lost — benchmark paused", ev.Msg), Red)
	case model.EvConnRestored:
		l.notice(connLine("connectivity restored — resuming benchmark", ev.Msg), Green)
	}
}

func (l *Live) noteRoundStarted(round int) {
	if round > l.roundStarted {
		l.roundStarted = round
	}
}

func connLine(base, msg string) string {
	if msg == "" {
		return base
	}
	return base + " (" + msg + ")"
}

func (l *Live) recordSample(s *model.Sample) {
	if s == nil || s.Warmup {
		return
	}
	ls := l.byID[s.ServerID]
	if ls == nil {
		return
	}
	agg := ls.cats[s.Category]
	if agg == nil {
		agg = &liveAgg{}
		ls.cats[s.Category] = agg
	}
	if s.Result.ValidFor(s.Category) {
		elapsed := s.Elapsed
		if elapsed <= 0 {
			elapsed = s.Result.RTT
		}
		agg.add(float64(elapsed) / float64(time.Millisecond))
	} else if !s.Result.Answered() {
		agg.lost++
	} else {
		agg.invalid++
	}
}

func (l *Live) setState(id string, state model.ServerState, reason string, announceActive bool) {
	ls := l.byID[id]
	if ls == nil || state == "" {
		return
	}
	ls.state = state
	ls.reason = reason
	if l.isTTY {
		return
	}
	if state == model.StateActive && !announceActive {
		return
	}
	line := ls.server.DisplayName() + ": " + state.Label()
	if reason != "" {
		line += " (" + reason + ")"
	}
	fmt.Fprintln(l.out, line)
}

func (l *Live) printProgress() {
	rounds := l.cfg.Rounds
	if rounds <= 0 {
		return
	}
	pct := l.round * 100 / rounds
	if pct > 100 {
		pct = 100
	}
	if pct/10 <= l.lastPctTen {
		return
	}
	l.lastPctTen = pct / 10
	fmt.Fprintf(l.out, "progress %3d%%  round %d/%d  elapsed %s\n",
		pct, l.round, rounds, formatElapsed(time.Since(l.start)))
}

func (l *Live) notice(plain string, color func(string) string) {
	if !l.isTTY {
		fmt.Fprintln(l.out, plain)
		return
	}
	l.noticeText = plain
	l.noticeColor = color
}

func (l *Live) noticeLines() []string {
	if l.noticeText == "" {
		return nil
	}
	width := max(1, l.width-2)
	wrapped := wrapVisible(l.noticeText, width)
	if len(wrapped) > maxNoticeLines {
		last := truncateVisible(wrapped[maxNoticeLines-1]+" …", width)
		wrapped = append(wrapped[:maxNoticeLines-1], last)
	}
	color := l.noticeColor
	if color == nil {
		color = func(s string) string { return s }
	}
	lines := make([]string, len(wrapped))
	for i, w := range wrapped {
		lines[i] = "  " + color(w)
	}
	return lines
}

func (l *Live) renderFrame() string {
	active, inactive := l.partition()

	height := max(l.height, liveMinHeight)
	lineBudget := height - 1
	showMetric := true
	showLegend := true
	noticeLines := l.noticeLines()
	headerLines := 4 + len(noticeLines)
	blockHeight := max(1, len(l.categories))
	if len(active) > 0 && lineBudget-headerLines-1 < blockHeight {
		showMetric = false
		headerLines--
	}
	if len(active) > 0 && lineBudget-headerLines-1 < blockHeight {
		showLegend = false
		headerLines--
	}
	bodySlots := lineBudget - headerLines - 1 // Always reserve a status footer.
	if bodySlots < 0 {
		bodySlots = 0
	}

	activeShown := min(len(active), bodySlots/blockHeight)
	visibleActive := active[:activeShown]
	remaining := bodySlots - activeShown*blockHeight
	inactiveShown := min(len(inactive), remaining)

	metric := l.latencyMetric()
	scale := l.p90Scale(visibleActive, metric)
	layout := l.makeRowLayout(visibleActive)

	var b strings.Builder
	l.writeFrameLine(&b, Bold(fmt.Sprintf("dnsbench — benchmarking %d servers", len(l.servers))))
	l.writeFrameLine(&b, l.progressLine())
	if showMetric {
		l.writeFrameLine(&b, l.metricLine(metric, scale))
	}
	if showLegend {
		l.writeFrameLine(&b, l.legendLine())
	}
	for _, ln := range noticeLines {
		l.writeFrameLine(&b, ln)
	}

	for i, ls := range visibleActive {
		for _, line := range l.serverLines(i+1, ls, metric, scale, layout) {
			l.writeFrameLine(&b, line)
		}
	}
	for _, ls := range inactive[:inactiveShown] {
		line := ls.server.DisplayName() + " — " + ls.state.Label()
		if ls.reason != "" {
			line += ": " + ls.reason
		}
		l.writeFrameLine(&b, "  "+Gray(line))
	}

	l.writeFrameLine(&b, l.statusLine(activeShown, len(active), len(inactive)))
	return b.String()
}

func (l *Live) progressLine() string {
	rounds := l.cfg.Rounds
	if l.roundStarted <= 0 {
		return l.preparingLine()
	}

	pct := 0.0
	if rounds > 0 {
		pct = float64(l.round) * 100 / float64(rounds)
		if pct > 100 {
			pct = 100
		}
	}
	elapsed := formatElapsed(time.Since(l.start))
	pctText := "0%"
	if pct > 0 {
		displayPct := max(1, int(math.Floor(pct)))
		if l.round >= rounds {
			displayPct = 100
		}
		pctText = fmt.Sprintf("%d%%", displayPct)
	}
	displayRound := max(l.round, l.roundStarted)
	suffix := fmt.Sprintf("%4s  round %d/%d  elapsed %s", pctText, displayRound, rounds, elapsed)
	barWidth := min(liveProgressWidth, l.width-visibleWidth(suffix)-4)
	if barWidth < 4 {
		return "  " + suffix
	}
	barValue := pct
	if barValue <= 0 {
		barValue = 0.01
	}
	bar := liveProgressBar(barValue, 100, barWidth)
	return "  " + bar + "  " + suffix
}

func (l *Live) preparingLine() string {
	const suffix = "preparing benchmark…"
	barWidth := min(liveProgressWidth, l.width-visibleWidth(suffix)-4)
	if barWidth < 6 {
		return "  " + suffix
	}
	return "  " + oscillatingBar(barWidth, l.prepareFrame) + "  " + suffix
}

func oscillatingBar(width, frame int) string {
	const (
		rightComet = "░▒▓█"
		leftComet  = "█▓▒░"
	)
	if width < 6 {
		return ""
	}
	innerWidth := width - 2
	cometWidth := len([]rune(rightComet))
	lastStart := innerWidth - cometWidth
	start := 0
	comet := rightComet
	if lastStart > 0 {
		cycle := lastStart * 2
		phase := frame % cycle
		start = phase
		if phase > lastStart {
			start = cycle - phase
			comet = leftComet
		}
	}
	before := strings.Repeat("·", start)
	after := strings.Repeat("·", innerWidth-start-cometWidth)
	head, tail := "█", "▓▒░"
	if comet == rightComet {
		head, tail = "█", "░▒▓"
		return Gray("▏"+before) + Cyan(tail) + White(head) + Gray(after+"▕")
	}
	return Gray("▏"+before) + White(head) + Cyan(tail) + Gray(after+"▕")
}

func liveProgressBar(value, maximum float64, width int) string {
	if width < 3 {
		return Green(Bar(value, maximum, width))
	}
	innerWidth := width - 2
	fill := strings.TrimRight(Bar(value, maximum, innerWidth), " ")
	empty := innerWidth - len([]rune(fill))
	return Gray("▏") + Green(fill) + Gray(strings.Repeat("·", empty)+"▕")
}

func (l *Live) legendLine() string {
	parts := make([]string, 0, len(l.categories))
	for _, c := range l.categories {
		parts = append(parts, CategoryColor(c)("■")+" "+c.Label())
	}
	switch {
	case l.width >= liveFullLegend:
		parts = append(parts, "loss = unanswered queries", "inv = unusable answers", "› = above P90")
	case l.width >= liveTinyWidth:
		parts = append(parts, "loss = unanswered", "inv = invalid", "› above P90")
	default:
		parts = append(parts, "L=loss", "I=invalid")
	}
	return "  " + strings.Join(parts, "   ")
}

func (l *Live) metricLine(metric string, scale float64) string {
	scaleText := "waiting for samples"
	if scale > 0 {
		scaleText = "P90 " + FormatMs(scale)
	}
	switch l.sortKey {
	case "loss":
		return fmt.Sprintf("  live order: loss · bars: %s latency · shared scale: %s", metric, scaleText)
	case "name":
		return fmt.Sprintf("  live order: name · bars: %s latency · shared scale: %s", metric, scaleText)
	case "cost":
		return fmt.Sprintf("  live order: median latency · final latency cost after benchmark · shared scale: %s", scaleText)
	default:
		return fmt.Sprintf("  live order and bars: %s latency · shared scale: %s · lower is better", metric, scaleText)
	}
}

func (l *Live) statusLine(shown, active, inactive int) string {
	if l.width >= 80 {
		return Gray(fmt.Sprintf(
			"  showing top %d/%d active · %d sidelined · final report includes all",
			shown, active, inactive,
		))
	}
	return Gray(fmt.Sprintf("  shown %d/%d active · %d sidelined", shown, active, inactive))
}

func (l *Live) writeFrameLine(b *strings.Builder, line string) {
	b.WriteString(truncateVisible(line, max(1, l.width)))
	b.WriteByte('\n')
}

type liveRowLayout struct {
	rankDigits       int
	nameWidth        int
	barWidth         int
	reliabilityWidth int
	showBars         bool
	detailed         bool
	tiny             bool
}

func (l *Live) makeRowLayout(visible []*liveServer) liveRowLayout {
	layout := liveRowLayout{
		rankDigits: max(2, len(fmt.Sprint(max(1, len(l.servers))))),
		showBars:   l.width >= liveNarrowWidth,
		detailed:   l.width >= liveDetailedWidth,
		tiny:       l.width < liveTinyWidth,
	}

	setReliabilityWidth := func() {
		layout.reliabilityWidth = visibleWidth("loss")
		if layout.tiny {
			layout.reliabilityWidth = visibleWidth("L")
		}
		for _, ls := range visible {
			layout.reliabilityWidth = max(
				layout.reliabilityWidth,
				visibleWidth(l.reliabilityCell(ls, layout.detailed, layout.tiny)),
			)
		}
	}
	setReliabilityWidth()

	fixed := func(withBar bool) int {
		n := layout.rankDigits + 2 + 2 + liveMsWidth + 2 + layout.reliabilityWidth
		if withBar {
			n++ // Space between the track and latency value.
		}
		return n
	}

	if layout.showBars {
		available := l.width - fixed(true)
		layout.nameWidth = min(livePreferredName, available-liveMinBarWidth)
		if layout.nameWidth < liveMinNameWidth && layout.detailed {
			layout.detailed = false
			setReliabilityWidth()
			available = l.width - fixed(true)
			layout.nameWidth = min(livePreferredName, available-liveMinBarWidth)
		}
		if layout.nameWidth >= liveMinNameWidth {
			layout.barWidth = available - layout.nameWidth
		} else {
			layout.showBars = false
		}
	}

	if !layout.showBars {
		available := l.width - fixed(false)
		layout.nameWidth = min(livePreferredName, available)
		layout.nameWidth = max(1, layout.nameWidth)
		layout.barWidth = 0
	}
	return layout
}

func (l *Live) reliabilityCell(ls *liveServer, detailed, tiny bool) string {
	total, lost, invalid := ls.counts()
	label := "loss"
	if tiny {
		label = "L"
	}

	base := label + " —"
	if total > 0 {
		base = label + " " + FormatPct(float64(lost)/float64(total)*100)
		if detailed {
			base += fmt.Sprintf(" (%d/%d)", lost, total)
		}
	}
	switch {
	case total == 0:
		base = Gray(base)
	case lost > 0:
		base = Red(base)
	default:
		base = Green(base)
	}
	if invalid > 0 {
		if tiny {
			base += Yellow(fmt.Sprintf(" I%d", invalid))
		} else {
			base += Yellow(fmt.Sprintf(" inv %d", invalid))
		}
	}
	return base
}

func (l *Live) serverLines(rank int, ls *liveServer, metric string, scale float64, layout liveRowLayout) []string {
	lines := make([]string, 0, max(1, len(l.categories)))
	reliability := l.reliabilityCell(ls, layout.detailed, layout.tiny)
	rankCell := fmt.Sprintf("%*d  ", layout.rankDigits, rank)
	blankRank := strings.Repeat(" ", layout.rankDigits+2)

	testing := l.inflight[ls.server.ID]
	for i, c := range l.categories {
		prefix := blankRank
		name := strings.Repeat(" ", layout.nameWidth)
		if i == 0 {
			prefix = rankCell
			name = TruncatePad(ls.server.DisplayName(), layout.nameWidth)
			if testing {
				name = Cyan(name)
			}
		}
		value, valid := ls.cats[c].value(metric)
		valueCell := "-"
		if valid {
			valueCell = FormatMs(value)
		}

		var b strings.Builder
		b.WriteString(prefix)
		b.WriteString(name)
		b.WriteString("  ")
		if layout.showBars {
			glyph := "▄"
			if i%2 == 1 {
				glyph = "▀"
			}
			b.WriteString(l.latencyTrack(value, scale, layout.barWidth, glyph, CategoryColor(c), valid))
			b.WriteByte(' ')
		}
		b.WriteString(padLeft(valueCell, liveMsWidth))
		b.WriteString("  ")
		if i == 0 {
			b.WriteString(padRight(reliability, layout.reliabilityWidth))
		} else {
			b.WriteString(strings.Repeat(" ", layout.reliabilityWidth))
		}
		lines = append(lines, b.String())
	}
	if len(lines) == 0 {
		lines = append(lines, rankCell+TruncatePad(ls.server.DisplayName(), layout.nameWidth))
	}
	return lines
}

func (l *Live) latencyTrack(value, scale float64, width int, glyph string, color func(string) string, valid bool) string {
	if width <= 0 {
		return ""
	}
	if glyph == "" {
		glyph = "▄"
	}
	if !valid || scale <= 0 {
		return Gray(strings.Repeat("─", width))
	}
	if value > scale {
		if width == 1 {
			return color("›")
		}
		return color(strings.Repeat(glyph, width-1) + "›")
	}

	filled := int(math.Ceil(value / scale * float64(width)))
	filled = min(width, max(0, filled))
	return color(strings.Repeat(glyph, filled)) + Gray(strings.Repeat("─", width-filled))
}

func (l *Live) p90Scale(visible []*liveServer, metric string) float64 {
	var values []float64
	for _, ls := range visible {
		for _, c := range l.categories {
			if value, ok := ls.cats[c].value(metric); ok {
				values = append(values, value)
			}
		}
	}
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	index := int(math.Ceil(0.90*float64(len(values)))) - 1
	index = max(0, min(index, len(values)-1))
	return values[index]
}

func (l *Live) partition() (active, inactive []*liveServer) {
	for _, s := range l.servers {
		ls := l.byID[s.ID]
		if ls == nil {
			continue
		}
		if ls.state == model.StateActive {
			active = append(active, ls)
		} else {
			inactive = append(inactive, ls)
		}
	}
	sort.SliceStable(active, func(i, j int) bool { return l.less(active[i], active[j]) })
	return active, inactive
}

func (l *Live) less(a, b *liveServer) bool {
	an := strings.ToLower(a.server.DisplayName())
	bn := strings.ToLower(b.server.DisplayName())
	if l.sortKey == "name" {
		return an < bn
	}
	va, vb := l.sortValue(a), l.sortValue(b)
	if va != vb {
		return va < vb
	}
	return an < bn
}

func normalizeLiveSortKey(k string) string {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "cost", "score":
		return "cost"
	}
	return normalizeSortKey(k)
}

func (l *Live) latencyMetric() string {
	switch l.sortKey {
	case "mean", "p95":
		return l.sortKey
	default:
		return "median"
	}
}

func (l *Live) sortValue(ls *liveServer) float64 {
	if l.sortKey == "loss" {
		if ls.totalSamples() == 0 {
			return math.Inf(1)
		}
		return ls.lossPct()
	}
	sum, n := 0.0, 0
	for _, c := range l.categories {
		a := ls.cats[c]
		value, ok := a.value(l.latencyMetric())
		if !ok {
			continue
		}
		sum += value
		n++
	}
	if n == 0 {
		return math.Inf(1)
	}
	return sum / float64(n)
}

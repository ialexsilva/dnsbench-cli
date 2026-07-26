package report

import (
	"fmt"
	"html"
	"io"
	"sort"
	"strings"
	"time"

	"dnsbench/internal/model"
)

const htmlStyle = `
:root {
  --paper: #f9f9f7; --surface: #fcfcfb; --ink: #0b0b0b; --ink-2: #52514e;
  --muted: #898781; --hairline: #e1e0d9; --accent: #2a78d6;
  --track: #cde2fb; --row-hl: #f1f6fd;
  --good: #0ca30c; --good-text: #006300; --warn: #fab219; --bad: #d03b3b;
  --sans: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  --mono: ui-monospace, "SF Mono", "Cascadia Code", Menlo, Consolas, monospace;
}
@media (prefers-color-scheme: dark) {
  :root {
    --paper: #0d0d0d; --surface: #1a1a19; --ink: #ffffff; --ink-2: #c3c2b7;
    --muted: #898781; --hairline: #2c2c2a; --accent: #3987e5;
    --track: #104281; --row-hl: #14202e;
    --good-text: #0ca30c;
  }
}
* { box-sizing: border-box; }
html { background: var(--paper); }
body {
  margin: 0 auto; padding: 3rem 1.5rem 4rem; max-width: 66rem;
  color: var(--ink); font: 15px/1.6 var(--sans);
}
.eyebrow {
  font: 600 .72rem/1 var(--mono); letter-spacing: .18em; text-transform: uppercase;
  color: var(--muted); margin: 0 0 .6rem;
}
.spec { font: .8rem/1.7 var(--mono); color: var(--ink-2); margin: 0; }
.spec b { font-weight: 600; color: var(--ink); }
.verdict { margin: 2.75rem 0 0; }
.vlabel { font: .9rem/1 var(--sans); color: var(--ink-2); margin: 0 0 .5rem; }
.vname { font: 650 2.1rem/1.15 var(--sans); letter-spacing: -.015em; margin: 0; }
.vendpoint { font: 400 1rem/1 var(--mono); color: var(--muted); margin-left: .6rem; white-space: nowrap; }
.vfigures { font: .92rem/1.6 var(--mono); color: var(--ink-2); margin: .6rem 0 0; }
.vfigures b { color: var(--ink); font-weight: 600; }
.vyou {
  margin: 1.25rem 0 0; padding: .6rem .9rem; border-left: 3px solid var(--accent);
  background: var(--row-hl); font-size: .95rem;
}
h2 {
  display: flex; align-items: center; gap: .9rem; margin: 3rem 0 1rem;
  font: 600 .72rem/1 var(--mono); letter-spacing: .18em; text-transform: uppercase;
  color: var(--muted);
}
h2::after { content: ""; flex: 1; height: 1px; background: var(--hairline); }
p { margin: .4rem 0; color: var(--ink-2); }
.tablewrap { overflow-x: auto; }
table { border-collapse: collapse; width: 100%; font-size: .88rem; }
th {
  font: 600 .66rem/1.2 var(--mono); letter-spacing: .1em; text-transform: uppercase;
  color: var(--muted); text-align: left; padding: .45rem .65rem;
  border-bottom: 1px solid var(--hairline);
}
td { padding: .42rem .65rem; border-bottom: 1px solid var(--hairline); white-space: nowrap; }
td.num, th.num { text-align: right; font-family: var(--mono); font-size: .84rem; font-variant-numeric: tabular-nums; }
tbody tr:hover { background: var(--row-hl); }
tr.current > td { background: var(--row-hl); }
tr.current > td:first-child { box-shadow: inset 3px 0 0 var(--accent); }
.chip {
  display: inline-block; margin-left: .55rem; padding: .1rem .45rem; border-radius: 4px;
  font: 600 .62rem/1.3 var(--mono); letter-spacing: .08em; text-transform: uppercase;
  color: var(--accent); border: 1px solid var(--accent);
}
.meter { width: 172px; }
.track {
  display: block; width: 160px; height: 8px; border-radius: 4px;
  background: var(--track); overflow: hidden;
}
.fill { display: block; height: 100%; background: var(--accent); border-radius: 0 4px 4px 0; }
.dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: .45rem; vertical-align: baseline; }
.dot.good { background: var(--good); }
.dot.warn { background: var(--warn); }
.dot.bad { background: var(--bad); }
.dot.off { background: var(--muted); }
.good-num { color: var(--good-text); }
.bad-num { color: var(--bad); }
.tablenote { font: .78rem/1.6 var(--mono); color: var(--muted); margin: .5rem 0 0; }
.sidelined { color: var(--muted); font-size: .88rem; }
ul.sidelined { margin: .4rem 0; padding-left: 1.2rem; }
figure { margin: 0; overflow-x: auto; }
figure svg { max-width: 100%; height: auto; border-radius: 6px; }
.only-dark { display: none; }
@media (prefers-color-scheme: dark) {
  .only-dark { display: block; }
  .only-light { display: none; }
}
details > summary {
  cursor: pointer; list-style: none; display: flex; align-items: center; gap: .9rem;
  margin: 3rem 0 1rem; font: 600 .72rem/1 var(--mono); letter-spacing: .18em;
  text-transform: uppercase; color: var(--muted);
}
details > summary::before { content: "▸"; color: var(--accent); }
details[open] > summary::before { content: "▾"; }
details > summary::after { content: ""; flex: 1; height: 1px; background: var(--hairline); }
details > summary::-webkit-details-marker { display: none; }
:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
footer {
  margin-top: 4rem; padding-top: .9rem; border-top: 1px solid var(--hairline);
  font: .76rem/1.7 var(--mono); color: var(--muted);
}
`

func ExportHTML(w io.Writer, res *model.RunResult, mode model.RankMode) error {
	sel := mode
	if len(res.Scores[sel]) == 0 {
		sel = selectedMode(res)
	}
	var b strings.Builder
	writeHTMLHead(&b, res)
	writeHTMLHeader(&b, res, sel)
	writeHTMLVerdict(&b, res, sel)
	writeHTMLRanking(&b, res, sel)
	writeHTMLChart(&b, res, sel)
	writeHTMLScores(&b, res, sel)
	writeHTMLCharacteristics(&b, res)
	writeHTMLMetrics(&b, res)
	writeHTMLLines(&b, "Current DNS configuration", systemServerLines(res))
	writeHTMLLines(&b, "Run coverage", serversTestedLines(res))
	writeHTMLLines(&b, "Statistical comparisons", comparisonLines(res))
	fmt.Fprintf(&b, "<footer>generated by %s %s · seed %d · re-run with --seed %d to reproduce the schedule · results are specific to this network, ISP, location and time of day</footer>\n",
		esc(model.AppName), esc(model.AppVersion), res.Config.Seed, res.Config.Seed)
	b.WriteString("</body>\n</html>\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func esc(s string) string { return html.EscapeString(s) }

func activeCount(res *model.RunResult) int {
	n := 0
	for i := range res.Servers {
		if stateOf(res, res.Servers[i].ID) == model.StateActive {
			n++
		}
	}
	return n
}

func currentIDSet(res *model.RunResult) map[string]bool {
	current := make(map[string]bool, len(res.SystemIDs))
	for _, id := range res.SystemIDs {
		current[id] = true
	}
	return current
}

func writeHTMLHead(b *strings.Builder, res *model.RunResult) {
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	fmt.Fprintf(b, "<title>%s report — %s</title>\n", esc(model.AppName), res.Info.StartedAt.Format("2006-01-02"))
	b.WriteString("<style>" + htmlStyle + "</style>\n</head>\n<body>\n")
}

func writeHTMLHeader(b *strings.Builder, res *model.RunResult, sel model.RankMode) {
	b.WriteString("<header>\n")
	fmt.Fprintf(b, "<p class=\"eyebrow\">%s · measurement report</p>\n", esc(model.AppName))
	specA := []string{
		res.Info.StartedAt.Format("2006-01-02 15:04 MST"),
		res.Info.Duration.Round(time.Second).String(),
		res.Info.OS + "/" + res.Info.Arch,
	}
	if len(res.Info.Interfaces) > 0 {
		specA = append(specA, strings.Join(res.Info.Interfaces, ", "))
	}
	fmt.Fprintf(b, "<p class=\"spec\">%s</p>\n", esc(strings.Join(specA, " · ")))
	cfg := res.Config
	fmt.Fprintf(b, "<p class=\"spec\">%d resolvers · %s mode · %d rounds · %s · timeout %s · %s session · ranked by <b>%s</b></p>\n",
		len(res.Servers), esc(string(cfg.Mode)), cfg.Rounds, esc(categoryList(cfg.Categories)), cfg.Timeout, esc(string(cfg.Session)), esc(sel.Label()))
	b.WriteString("</header>\n")
}

func writeHTMLVerdict(b *strings.Builder, res *model.RunResult, sel model.RankMode) {
	ranked := rankedScores(res, sel)
	if len(ranked) == 0 {
		return
	}
	winner := ranked[0]
	ws := res.ServerByID(winner.ServerID)
	if ws == nil {
		return
	}
	b.WriteString("<section class=\"verdict\">\n")
	fmt.Fprintf(b, "<p class=\"vlabel\">Top-ranked resolver on this network — %s ranking</p>\n", esc(sel.Label()))
	fmt.Fprintf(b, "<div class=\"vname\">%s<span class=\"vendpoint\">%s</span></div>\n", esc(ws.DisplayName()), esc(ws.Endpoint()))

	figures := []string{fmt.Sprintf("latency cost <b>%.1f ms</b>", winner.TotalMs)}
	if st := res.Stats[winner.ServerID]; st != nil {
		cats := res.Config.Categories
		if len(cats) == 0 {
			cats = statsCategories(res)
		}
		for _, c := range cats {
			if d := st.PerCategory[c]; d != nil && d.Valid > 0 {
				figures = append(figures, fmt.Sprintf("%s median %.1f ms", esc(c.Label()), d.MedianMs))
			}
		}
		figures = append(figures, fmt.Sprintf("loss %.1f%%", aggregateLoss(st)))
	}
	if tieA, tieB, tied := TopTwoTied(res, sel); tied {
		other := tieA
		if other == winner.ServerID {
			other = tieB
		}
		figures = append(figures, fmt.Sprintf("≈ statistically tied with %s", esc(displayNameByID(res, other))))
	}
	fmt.Fprintf(b, "<p class=\"vfigures\">%s</p>\n", strings.Join(figures, " · "))

	if line := currentDNSLine(res, ranked); line != "" {
		fmt.Fprintf(b, "<p class=\"vyou\">%s</p>\n", line)
	}
	b.WriteString("</section>\n")
}

func currentDNSLine(res *model.RunResult, ranked []model.Score) string {
	current := currentIDSet(res)
	if len(current) == 0 {
		return ""
	}
	for _, sc := range ranked {
		if !current[sc.ServerID] {
			continue
		}
		name := esc(displayNameByID(res, sc.ServerID))
		if sc.Rank == 1 {
			return fmt.Sprintf("Your current DNS — <b>%s</b> — is already the top-ranked measured choice on this network.", name)
		}
		line := fmt.Sprintf("Your current DNS — <b>%s</b> — ranked #%d of %d with latency cost %.1f ms", name, sc.Rank, len(ranked), sc.TotalMs)
		if ranked[0].TotalMs > 0 {
			ratio := sc.TotalMs / ranked[0].TotalMs
			if ratio >= 1.05 {
				line += fmt.Sprintf(" (%.1f× the winner)", ratio)
			}
		}
		return line + "."
	}
	return "Your current DNS was not ranked in this run — see run coverage below."
}

func writeHTMLRanking(b *strings.Builder, res *model.RunResult, sel model.RankMode) {
	ranked := rankedScores(res, sel)
	cats := res.Config.Categories
	if len(cats) == 0 {
		cats = statsCategories(res)
	}
	current := currentIDSet(res)
	tieA, tieB, tied := TopTwoTied(res, sel)

	fmt.Fprintf(b, "<section>\n<h2>Ranking — %s</h2>\n", esc(sel.Label()))
	if len(ranked) == 0 {
		b.WriteString("<p class=\"sidelined\">No ranked servers.</p>\n</section>\n")
		return
	}
	maxScore := 0.0
	for _, sc := range ranked {
		if sc.TotalMs > maxScore {
			maxScore = sc.TotalMs
		}
	}
	b.WriteString("<div class=\"tablewrap\"><table>\n<thead><tr><th class=\"num\">#</th><th>resolver</th>")
	for _, c := range cats {
		fmt.Fprintf(b, "<th class=\"num\">%s med</th>", esc(c.Label()))
	}
	b.WriteString("<th class=\"num\">loss</th><th class=\"num\">latency cost<br><small>ms, lower is better</small></th><th>relative cost</th></tr></thead>\n<tbody>\n")
	for _, sc := range ranked {
		st := res.Stats[sc.ServerID]
		rowClass := ""
		if current[sc.ServerID] {
			rowClass = " class=\"current\""
		}
		fmt.Fprintf(b, "<tr%s><td class=\"num\">%d</td><td>%s", rowClass, sc.Rank, esc(displayNameByID(res, sc.ServerID)))
		if current[sc.ServerID] {
			b.WriteString("<span class=\"chip\">current dns</span>")
		}
		if tied && (sc.ServerID == tieA || sc.ServerID == tieB) {
			b.WriteString("<span class=\"chip\" title=\"statistically tied at the top\">≈ tied</span>")
		}
		b.WriteString("</td>")
		for _, c := range cats {
			cell := "–"
			if st != nil {
				if d := st.PerCategory[c]; d != nil && d.Valid > 0 {
					cell = fmt.Sprintf("%.1f", d.MedianMs)
				}
			}
			fmt.Fprintf(b, "<td class=\"num\">%s</td>", cell)
		}
		loss := aggregateLoss(st)
		lossClass := "good-num"
		if loss > 0 {
			lossClass = "bad-num"
		}
		fmt.Fprintf(b, "<td class=\"num %s\">%.1f%%</td>", lossClass, loss)
		fmt.Fprintf(b, "<td class=\"num\"><b>%.1f</b></td>", sc.TotalMs)
		pct := 100.0
		if maxScore > 0 {
			pct = sc.TotalMs / maxScore * 100
		}
		fmt.Fprintf(b, "<td class=\"meter\"><span class=\"track\"><span class=\"fill\" style=\"width:%.0f%%\"></span></span></td></tr>\n", pct)
	}
	b.WriteString("</tbody></table></div>\n")
	b.WriteString("<p class=\"tablenote\">shorter cost bar is better · category medians in ms</p>\n")
	writeHTMLSidelined(b, res)
	b.WriteString("</section>\n")
}

func writeHTMLSidelined(b *strings.Builder, res *model.RunResult) {
	var lines []string
	for i := range res.Servers {
		s := &res.Servers[i]
		state := stateOf(res, s.ID)
		if state == "" || state == model.StateActive {
			continue
		}
		reason := ""
		if tr := res.Triage[s.ID]; tr != nil && tr.Reason != "" {
			reason = ": " + tr.Reason
		}
		lines = append(lines, fmt.Sprintf("%s (%s) — %s%s", esc(s.DisplayName()), esc(s.Endpoint()), esc(state.Label()), esc(reason)))
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "<p class=\"sidelined\">Not ranked (%d):</p>\n<ul class=\"sidelined\">\n", len(lines))
	for _, l := range lines {
		fmt.Fprintf(b, "<li>%s</li>\n", l)
	}
	b.WriteString("</ul>\n")
}

func writeHTMLChart(b *strings.Builder, res *model.RunResult, sel model.RankMode) {
	heading := "Median latency by category"
	if activeCount(res) > svgMaxRows {
		heading = fmt.Sprintf("Median latency by category — top %d", svgMaxRows)
	}
	fmt.Fprintf(b, "<section>\n<h2>%s</h2>\n", esc(heading))
	b.WriteString("<figure class=\"only-light\">\n")
	writeSVG(b, res, sel, svgLight)
	b.WriteString("</figure>\n<figure class=\"only-dark\">\n")
	writeSVG(b, res, sel, svgDark)
	b.WriteString("</figure>\n</section>\n")
}

func writeHTMLScores(b *strings.Builder, res *model.RunResult, sel model.RankMode) {
	ranked := rankedScores(res, sel)
	if len(ranked) == 0 {
		return
	}
	keys := presentHTMLPenaltyKeys(ranked)
	fmt.Fprintf(b, "<section>\n<h2>Latency cost breakdown — %s</h2>\n", esc(sel.Label()))
	b.WriteString("<div class=\"tablewrap\"><table>\n<thead><tr><th class=\"num\">#</th><th>resolver</th><th class=\"num\">base</th>")
	for _, k := range keys {
		fmt.Fprintf(b, "<th class=\"num\">+%s</th>", esc(k))
	}
	b.WriteString("<th class=\"num\">total</th></tr></thead>\n<tbody>\n")
	for _, sc := range ranked {
		fmt.Fprintf(b, "<tr><td class=\"num\">%d</td><td>%s</td><td class=\"num\">%.1f</td>",
			sc.Rank, esc(displayNameByID(res, sc.ServerID)), sc.BaseMs)
		for _, k := range keys {
			if v := sc.Penalties[k]; v > 0 {
				fmt.Fprintf(b, "<td class=\"num\">%.1f</td>", v)
			} else {
				b.WriteString("<td class=\"num\"></td>")
			}
		}
		fmt.Fprintf(b, "<td class=\"num\"><b>%.1f</b></td></tr>\n", sc.TotalMs)
	}
	b.WriteString("</tbody></table></div>\n")
	if wts, ok := res.Weights[sel]; ok {
		fmt.Fprintf(b, "<p class=\"tablenote\">%s</p>\n", esc(strings.TrimSuffix(weightsLine(wts), "\n")))
	}
	b.WriteString("</section>\n")
}

func presentHTMLPenaltyKeys(scores []model.Score) []string {
	present := map[string]bool{}
	for _, sc := range scores {
		for k, v := range sc.Penalties {
			if v > 0 {
				present[k] = true
			}
		}
	}
	var keys []string
	for _, k := range []string{"loss", "servfail", "invalid-response", "retry", "jitter", "nxdomain-interception", "no-dnssec"} {
		if present[k] {
			keys = append(keys, k)
			delete(present, k)
		}
	}
	var extra []string
	for k := range present {
		extra = append(extra, k)
	}
	sort.Strings(extra)
	return append(keys, extra...)
}

func statusDotCell(v model.Verdict, goodWhenYes bool, yesLabel, noLabel string) string {
	label := yesLabel
	class := "good"
	switch v {
	case model.VerdictYes:
		if !goodWhenYes {
			class = "bad"
		}
	case model.VerdictNo:
		label = noLabel
		class = "bad"
		if !goodWhenYes {
			class = "good"
		}
	case model.VerdictPartial:
		label, class = "partial", "warn"
	default:
		label, class = "unknown", "off"
	}
	return fmt.Sprintf("<span class=\"dot %s\"></span>%s", class, esc(label))
}

func writeHTMLCharacteristics(b *strings.Builder, res *model.RunResult) {
	b.WriteString("<section>\n<h2>Server characteristics</h2>\n")
	b.WriteString("<div class=\"tablewrap\"><table>\n<thead><tr><th>resolver</th><th>protocol</th><th>status</th><th>dnssec validation</th><th>nxdomain honesty</th></tr></thead>\n<tbody>\n")
	for i := range res.Servers {
		s := &res.Servers[i]
		state := stateOf(res, s.ID)
		stateCell := "<span class=\"dot off\"></span>unknown"
		switch state {
		case model.StateActive:
			stateCell = "<span class=\"dot good\"></span>" + esc(state.Label())
		case model.StateBenched:
			stateCell = "<span class=\"dot warn\"></span>sidelined"
		case model.StateOffline, model.StateError:
			stateCell = "<span class=\"dot bad\"></span>" + esc(state.Label())
		}
		p := res.Probes[s.ID]
		dnssec, nx := "<span class=\"dot off\"></span>not probed", "<span class=\"dot off\"></span>not probed"
		if p != nil {
			dnssec = statusDotCell(p.DNSSEC.Validating, true, "validating", "not validating")
			nx = statusDotCell(p.NXInterception, false, "intercepts", "honest")
		}
		fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			esc(s.DisplayName()), esc(s.Protocol.Label()), stateCell, dnssec, nx)
	}
	b.WriteString("</tbody></table></div>\n</section>\n")
}

func writeHTMLMetrics(b *strings.Builder, res *model.RunResult) {
	cats := res.Config.Categories
	if len(cats) == 0 {
		cats = statsCategories(res)
	}
	for _, cat := range cats {
		type row struct {
			name string
			d    *model.Distribution
		}
		var rows []row
		for id, st := range res.Stats {
			if st == nil {
				continue
			}
			if d := st.PerCategory[cat]; d != nil && d.Count > 0 {
				rows = append(rows, row{displayNameByID(res, id), d})
			}
		}
		if len(rows) == 0 {
			continue
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].d.MedianMs != rows[j].d.MedianMs {
				return rows[i].d.MedianMs < rows[j].d.MedianMs
			}
			return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name)
		})
		fmt.Fprintf(b, "<details>\n<summary>Detailed metrics — %s category</summary>\n", esc(cat.Label()))
		b.WriteString("<div class=\"tablewrap\"><table>\n<thead><tr><th>resolver</th>")
		for _, h := range []string{"count", "answered", "valid", "timeouts", "errors", "loss", "retry", "min", "median", "mean", "p90", "p95", "p99", "max", "stddev", "jitter"} {
			fmt.Fprintf(b, "<th class=\"num\">%s</th>", h)
		}
		b.WriteString("</tr></thead>\n<tbody>\n")
		for _, r := range rows {
			d := r.d
			fmt.Fprintf(b, "<tr><td>%s</td>", esc(r.name))
			fmt.Fprintf(b, "<td class=\"num\">%d</td><td class=\"num\">%d</td><td class=\"num\">%d</td><td class=\"num\">%d</td><td class=\"num\">%d</td>",
				d.Count, d.Answered, d.Valid, d.Timeouts, d.Errors)
			fmt.Fprintf(b, "<td class=\"num\">%.1f%%</td><td class=\"num\">%.1f%%</td>", d.LossPct, d.RetryPct)
			for _, v := range []float64{d.MinMs, d.MedianMs, d.MeanMs, d.P90Ms, d.P95Ms, d.P99Ms, d.MaxMs, d.StdDevMs, d.JitterMs} {
				fmt.Fprintf(b, "<td class=\"num\">%.1f</td>", v)
			}
			b.WriteString("</tr>\n")
		}
		b.WriteString("</tbody></table></div>\n</details>\n")
	}
}

func writeHTMLLines(b *strings.Builder, title string, lines []string) {
	fmt.Fprintf(b, "<section>\n<h2>%s</h2>\n", esc(title))
	for _, l := range lines {
		fmt.Fprintf(b, "<p>%s</p>\n", esc(l))
	}
	b.WriteString("</section>\n")
}

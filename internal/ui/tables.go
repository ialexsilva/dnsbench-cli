package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"dnsbench/internal/model"
)

func renderTable(headers []string, rightAlign []bool, rows [][]string) string {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = visibleWidth(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(widths) && visibleWidth(c) > widths[i] {
				widths[i] = visibleWidth(c)
			}
		}
	}
	alignCell := func(i int, c string) string {
		if rightAlign != nil && i < len(rightAlign) && rightAlign[i] {
			return padLeft(c, widths[i])
		}
		return padRight(c, widths[i])
	}
	var b strings.Builder
	writeRow := func(cells []string) {
		parts := make([]string, len(cells))
		for i, c := range cells {
			parts[i] = alignCell(i, c)
		}
		b.WriteString(strings.TrimRight(strings.Join(parts, "  "), " ") + "\n")
	}
	headerCells := make([]string, len(headers))
	for i, h := range headers {
		headerCells[i] = Bold(h)
	}
	writeRow(headerCells)
	seps := make([]string, len(headers))
	for i, w := range widths {
		seps[i] = strings.Repeat("─", w)
	}
	b.WriteString(strings.Join(seps, "  ") + "\n")
	for _, r := range rows {
		writeRow(r)
	}
	return b.String()
}

func RenderTable(headers []string, rightAlign []bool, rows [][]string) string {
	return renderTable(headers, rightAlign, rows)
}

func RenderServerCharacteristics(res *model.RunResult) string {
	states := make(map[string]model.ServerState, len(res.Servers))
	for _, server := range res.Servers {
		if stats := res.Stats[server.ID]; stats != nil {
			states[server.ID] = stats.State
		}
		if states[server.ID] == "" {
			if triage := res.Triage[server.ID]; triage != nil {
				states[server.ID] = triage.State
			}
		}
	}
	current := systemIDSet(res)
	servers := append([]model.Server(nil), res.Servers...)
	sort.SliceStable(servers, func(i, j int) bool {
		si, sj := statePriority(states[servers[i].ID]), statePriority(states[servers[j].ID])
		if si != sj {
			return si < sj
		}
		ci, cj := current[servers[i].ID], current[servers[j].ID]
		if ci != cj {
			return ci
		}
		return strings.ToLower(servers[i].DisplayName()) < strings.ToLower(servers[j].DisplayName())
	})

	headers := []string{" ", "resolver", "proto", "status", "dnssec", "nxdomain"}
	rows := make([][]string, 0, len(servers))
	for _, s := range servers {
		marker := ""
		if current[s.ID] {
			marker = Cyan("●")
		}
		p := res.Probes[s.ID]
		rows = append(rows, []string{
			marker,
			s.DisplayName(),
			s.Protocol.Label(),
			statusCell(states, s.ID),
			dnssecCell(p),
			nxCell(p),
		})
	}
	legend := ""
	if len(current) > 0 {
		legend = Cyan("●") + " current DNS resolver\n"
	}
	return legend + renderTable(headers, nil, rows)
}

func RenderServersTable(servers []model.Server, probes map[string]*model.ProbeResult, states map[string]model.ServerState) string {
	headers := []string{"endpoint", "name", "operator", "protocol", "source", "scope", "status", "dnssec", "nxdomain"}
	rows := make([][]string, 0, len(servers))
	for _, s := range servers {
		p := probes[s.ID]
		rows = append(rows, []string{
			s.Endpoint(),
			s.DisplayName(),
			dashIfEmpty(s.Operator),
			s.Protocol.Label(),
			s.Source.Label(),
			scopeCell(s),
			statusCell(states, s.ID),
			dnssecCell(p),
			nxCell(p),
		})
	}
	return renderTable(headers, nil, rows)
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func scopeCell(s model.Server) string {
	if s.Protocol.UsesURL() && s.Address == "" {
		return "remote"
	}
	return model.ScopeOfString(s.Address).Label()
}

func statePriority(state model.ServerState) int {
	switch state {
	case model.StateActive:
		return 0
	case model.StateBenched:
		return 1
	case model.StateOffline:
		return 2
	case model.StateError:
		return 3
	}
	return 4
}

func statusCell(states map[string]model.ServerState, id string) string {
	st, ok := states[id]
	if !ok {
		return "-"
	}
	label := st.Label()
	switch st {
	case model.StateActive:
		return Green(label)
	case model.StateBenched:
		return Yellow("sidelined")
	case model.StateOffline, model.StateError:
		return Red(label)
	}
	return label
}

func coloredVerdict(v model.Verdict) string {
	switch v {
	case model.VerdictYes:
		return Green(v.Label())
	case model.VerdictNo:
		return Red(v.Label())
	case model.VerdictPartial:
		return Yellow(v.Label())
	}
	return v.Label()
}

func dnssecCell(p *model.ProbeResult) string {
	if p == nil {
		return "?"
	}
	return coloredVerdict(p.DNSSEC.Validating)
}

func nxCell(p *model.ProbeResult) string {
	if p == nil {
		return "?"
	}
	switch p.NXInterception {
	case model.VerdictYes:
		return Red("intercepts")
	case model.VerdictNo:
		return Green("ok")
	case model.VerdictPartial:
		return Yellow("partial")
	}
	return "?"
}

func RenderMetricsTable(res *model.RunResult, category model.Category, sortKey string) string {
	key := normalizeSortKey(sortKey)
	type metricsRow struct {
		name string
		d    *model.Distribution
	}
	var rows []metricsRow
	for id, st := range res.Stats {
		if st == nil {
			continue
		}
		d := st.PerCategory[category]
		if d == nil || d.Count == 0 {
			continue
		}
		rows = append(rows, metricsRow{serverName(res, id), d})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		an, bn := strings.ToLower(rows[i].name), strings.ToLower(rows[j].name)
		if key == "name" {
			return an < bn
		}
		av, bv := metricValue(rows[i].d, key), metricValue(rows[j].d, key)
		if av != bv {
			return av < bv
		}
		return an < bn
	})
	headers := []string{
		"server", "count", "ans", "valid", "tmo", "err", "sf", "trunc", "inv", "loss", "retry%",
		"min", "max", "mean", "median", "stddev", "var", "p50", "p90", "p95", "p99",
		"ci95lo", "ci95hi", "jitter", "sf%", "inv%", "trunc%",
	}
	rightAlign := make([]bool, len(headers))
	for i := 1; i < len(rightAlign); i++ {
		rightAlign[i] = true
	}
	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		d := r.d
		cells = append(cells, []string{
			r.name,
			strconv.Itoa(d.Count), strconv.Itoa(d.Answered), strconv.Itoa(d.Valid), strconv.Itoa(d.Timeouts),
			strconv.Itoa(d.Errors), strconv.Itoa(d.Servfails), strconv.Itoa(d.TruncatedN),
			strconv.Itoa(d.Invalid), FormatPct(d.LossPct), FormatPct(d.RetryPct),
			FormatMs(d.MinMs), FormatMs(d.MaxMs), FormatMs(d.MeanMs), FormatMs(d.MedianMs),
			FormatMs(d.StdDevMs), fmt.Sprintf("%.1f", d.VarianceMs2),
			FormatMs(d.P50Ms), FormatMs(d.P90Ms), FormatMs(d.P95Ms), FormatMs(d.P99Ms),
			FormatMs(d.CI95LowMs), FormatMs(d.CI95HighMs), FormatMs(d.JitterMs),
			FormatPct(d.ServfailPct), FormatPct(d.InvalidPct), FormatPct(d.TruncatedPct),
		})
	}
	return renderTable(headers, rightAlign, cells)
}

func metricValue(d *model.Distribution, key string) float64 {
	switch key {
	case "mean":
		return d.MeanMs
	case "p95":
		return d.P95Ms
	case "loss":
		return d.LossPct
	}
	return d.MedianMs
}

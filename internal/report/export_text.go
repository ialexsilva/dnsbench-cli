package report

import (
	"fmt"
	"io"
	"strings"
	"time"

	"dnsbench/internal/model"
)

func ExportText(w io.Writer, res *model.RunResult) error {
	var b strings.Builder
	title := fmt.Sprintf("%s report", model.AppName)
	b.WriteString(title + "\n" + strings.Repeat("=", len(title)) + "\n\n")
	fmt.Fprintf(&b, "Date: %s\n", res.Info.StartedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(&b, "Duration: %s\n", res.Info.Duration.Round(time.Millisecond))
	fmt.Fprintf(&b, "Platform: %s/%s\n", res.Info.OS, res.Info.Arch)
	if len(res.Info.Interfaces) > 0 {
		fmt.Fprintf(&b, "Interfaces: %s\n", strings.Join(res.Info.Interfaces, ", "))
	}
	fmt.Fprintf(&b, "App version: %s\n\n", res.Info.AppVersion)

	cfg := res.Config
	fmt.Fprintf(&b, "Configuration: mode %s, %d rounds, categories %s, timeout %s, session %s, seed %d\n",
		cfg.Mode, cfg.Rounds, categoryList(cfg.Categories), cfg.Timeout, cfg.Session, cfg.Seed)
	sel := selectedMode(res)
	fmt.Fprintf(&b, "Ranking mode: %s\n", sel.Label())
	b.WriteString("Latency cost: weighted latency plus penalties, in ms — lower is better\n")
	if wts, ok := res.Weights[sel]; ok {
		b.WriteString(weightsLine(wts))
	}
	b.WriteString("\n")

	b.WriteString(summaryTable(res, sel))
	b.WriteString("\n")
	b.WriteString(BuildConclusions(res))
	_, err := io.WriteString(w, b.String())
	return err
}

func categoryList(cats []model.Category) string {
	if len(cats) == 0 {
		return "none"
	}
	labels := make([]string, 0, len(cats))
	for _, c := range cats {
		labels = append(labels, c.Label())
	}
	return strings.Join(labels, ", ")
}

func weightsLine(wts model.Weights) string {
	var parts []string
	for _, cat := range model.AllCategories() {
		if v, ok := wts.Category[cat]; ok {
			parts = append(parts, fmt.Sprintf("%s=%.2f", cat.Label(), v))
		}
	}
	catPart := "none"
	if len(parts) > 0 {
		catPart = strings.Join(parts, ", ")
	}
	metric := wts.LatencyMetric
	if metric == "" {
		metric = "-"
	}
	return fmt.Sprintf("Weights: categories %s; latency metric %s; penalties in ms: per loss pct %.1f, per SERVFAIL pct %.1f, per invalid-response pct %.1f, per retry pct %.1f, NX interception %.1f, no DNSSEC %.1f; jitter weight %.2f\n",
		catPart, metric,
		wts.PenaltyPerLossPctMs, wts.PenaltyPerServfailPctMs,
		wts.PenaltyPerInvalidPctMs, wts.PenaltyPerRetryPctMs, wts.PenaltyNXInterceptionMs,
		wts.PenaltyNoDNSSECMs, wts.JitterWeight)
}

func summaryTable(res *model.RunResult, sel model.RankMode) string {
	cats := res.Config.Categories
	if len(cats) == 0 {
		cats = statsCategories(res)
	}
	scoreByID := make(map[string]float64)
	for _, sc := range res.Scores[sel] {
		scoreByID[sc.ServerID] = sc.TotalMs
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-24s %-18s", "Server", "State")
	for _, cat := range cats {
		fmt.Fprintf(&b, " %-20s", cat.Label()+" med ms")
	}
	fmt.Fprintf(&b, " %-8s %-15s\n", "Loss %", "Latency cost ms")
	width := 24 + 1 + 18 + len(cats)*21 + 1 + 8 + 1 + 15
	b.WriteString(strings.Repeat("-", width) + "\n")
	for i := range res.Servers {
		s := &res.Servers[i]
		state := stateOf(res, s.ID)
		stateLabel := "unknown"
		if state != "" {
			stateLabel = state.Label()
		}
		fmt.Fprintf(&b, "%-24s %-18s", s.DisplayName(), stateLabel)
		st := res.Stats[s.ID]
		for _, cat := range cats {
			cell := "-"
			if st != nil {
				if d := st.PerCategory[cat]; d != nil {
					cell = fmt.Sprintf("%.1f", d.MedianMs)
				}
			}
			fmt.Fprintf(&b, " %-20s", cell)
		}
		loss := "-"
		if st != nil {
			loss = fmt.Sprintf("%.1f", aggregateLoss(st))
		}
		score := "-"
		if v, ok := scoreByID[s.ID]; ok {
			score = fmt.Sprintf("%.1f", v)
		}
		fmt.Fprintf(&b, " %-8s %-15s\n", loss, score)
	}
	return b.String()
}

func statsCategories(res *model.RunResult) []model.Category {
	var cats []model.Category
	for _, cat := range model.AllCategories() {
		for _, st := range res.Stats {
			if st != nil && st.PerCategory[cat] != nil {
				cats = append(cats, cat)
				break
			}
		}
	}
	return cats
}

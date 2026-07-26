package ui

import (
	"fmt"
	"sort"
	"strings"

	"dnsbench/internal/model"
)

const (
	rankNameWidth = 28
	rankBarWidth  = 24
	rankTopN      = 10
)

func RenderRunSummary(res *model.RunResult) string {
	parts := []string{fmt.Sprintf("%d resolvers", len(res.Servers))}
	if res.Config.Mode != "" {
		parts = append(parts, string(res.Config.Mode)+" mode")
	}
	if cats := categoryLabels(res); cats != "" {
		parts = append(parts, cats)
	}
	if len(res.Info.Interfaces) > 0 {
		parts = append(parts, strings.Join(res.Info.Interfaces, ", "))
	}
	if res.Info.Duration > 0 {
		parts = append(parts, formatElapsed(res.Info.Duration))
	}
	return Bold("dnsbench") + Gray(" · "+strings.Join(parts, " · ")) + "\n"
}

func categoryLabels(res *model.RunResult) string {
	labels := make([]string, 0, len(res.Config.Categories))
	for _, c := range res.Config.Categories {
		labels = append(labels, c.Label())
	}
	return strings.Join(labels, " + ")
}

func RenderRankingList(res *model.RunResult, mode model.RankMode, tieA, tieB string) string {
	var b strings.Builder
	scores := sortedScores(res, mode)
	system := systemIDSet(res)
	shown := min(len(scores), rankTopN)
	hidden := scores[shown:]
	var hiddenSystem []model.Score
	for _, sc := range hidden {
		if system[sc.ServerID] {
			hiddenSystem = append(hiddenSystem, sc)
		}
	}
	visible := append(append([]model.Score{}, scores[:shown]...), hiddenSystem...)

	b.WriteString(Bold("Ranked by "+mode.Label()) + Gray(" — latency cost in ms, lower is better") + "\n")
	writeRankingLegend(&b, visible, system, tieA)
	if len(scores) == 0 {
		b.WriteString(Gray("  no resolvers completed the benchmark") + "\n")
		b.WriteString(renderUnrankedSummary(res, 0))
		return b.String()
	}
	b.WriteString(rankingHeader() + "\n")
	best := scores[0].TotalMs
	worst := 0.0
	for _, sc := range scores {
		if sc.TotalMs > worst {
			worst = sc.TotalMs
		}
	}
	cats := displayCategories(res)
	for _, sc := range scores[:shown] {
		b.WriteString(rankingRow(res, sc, best, worst, cats, system, tieA, tieB) + "\n")
	}
	if len(hiddenSystem) > 0 {
		b.WriteString(Gray("   ⋮") + "\n")
		for _, sc := range hiddenSystem {
			b.WriteString(rankingRow(res, sc, best, worst, cats, system, tieA, tieB) + "\n")
		}
	}
	if rest := len(hidden) - len(hiddenSystem); rest > 0 {
		b.WriteString(Gray(fmt.Sprintf("   … %d more ranked — full list in the report", rest)) + "\n")
	}
	b.WriteString(renderUnrankedSummary(res, len(scores)))
	return b.String()
}

func writeRankingLegend(b *strings.Builder, scores []model.Score, system map[string]bool, tieA string) {
	hasCurrent := false
	hasPenalty := false
	for _, sc := range scores {
		if system[sc.ServerID] {
			hasCurrent = true
		}
		if penaltyTotal(sc) > 0 {
			hasPenalty = true
		}
	}
	var parts []string
	if hasCurrent {
		parts = append(parts, Cyan("● current DNS"))
	}
	if tieA != "" {
		parts = append(parts, Dim("≈ statistically tied"))
	}
	if hasPenalty {
		parts = append(parts, Dim("* cost includes penalties — breakdown in the report"))
	}
	if len(parts) > 0 {
		b.WriteString("  " + strings.Join(parts, "   ") + "\n")
	}
}

func rankingRow(res *model.RunResult, sc model.Score, best, worst float64, cats []model.Category, system map[string]bool, tieA, tieB string) string {
	nameCell := TruncatePad(serverName(res, sc.ServerID), rankNameWidth)
	rankCell := padLeft(fmt.Sprintf("%d", sc.Rank), 4)
	currentCell := " "
	switch {
	case system[sc.ServerID]:
		nameCell = Cyan(nameCell)
		currentCell = Cyan("●")
	case sc.Rank == 1:
		nameCell = Bold(nameCell)
	}
	if sc.Rank == 1 {
		rankCell = Bold(rankCell)
	}
	bar := scoreColor(sc.TotalMs, best)(Bar(sc.TotalMs, worst, rankBarWidth))
	scoreCell := Bold(padLeft(FormatMs(sc.TotalMs), 10))
	penCell := " "
	if penaltyTotal(sc) > 0 {
		penCell = Dim("*")
	}
	lossCell := Gray(padLeft("-", 6))
	if st := res.Stats[sc.ServerID]; st != nil {
		loss := aggregateLossPct(st, cats)
		cell := padLeft(FormatPct(loss), 6)
		if loss > 0 {
			lossCell = Red(cell)
		} else {
			lossCell = Green(cell)
		}
	}
	marker := ""
	if sc.ServerID == tieA || sc.ServerID == tieB {
		marker = Dim("  ≈ tied")
	}
	return fmt.Sprintf("%s %s  %s  %s%s%s %s%s", currentCell, rankCell, nameCell, bar, scoreCell, penCell, lossCell, marker)
}

func rankingHeader() string {
	return Bold(fmt.Sprintf(
		"  %s  %s  %s%s %s",
		padLeft("#", 4),
		padRight("resolver", rankNameWidth),
		strings.Repeat(" ", rankBarWidth),
		padLeft("cost", 10),
		padLeft("loss", 7),
	))
}

func penaltyTotal(sc model.Score) float64 {
	total := 0.0
	for _, v := range sc.Penalties {
		if v > 0 {
			total += v
		}
	}
	return total
}

func renderUnrankedSummary(res *model.RunResult, ranked int) string {
	counts := map[model.ServerState]int{}
	for _, server := range res.Servers {
		state := model.ServerState("")
		if stats := res.Stats[server.ID]; stats != nil {
			state = stats.State
		}
		if state == "" {
			if triage := res.Triage[server.ID]; triage != nil {
				state = triage.State
			}
		}
		counts[state]++
	}
	parts := []string{}
	if n := counts[model.StateBenched]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d sidelined", n))
	}
	if n := counts[model.StateOffline]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d unreachable", n))
	}
	if n := counts[model.StateError]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d with errors", n))
	}
	if len(parts) == 0 {
		return ""
	}
	return Gray("   "+strings.Join(parts, " · ")+" — reasons in the report") + "\n"
}

func RenderReportFooter(exportPaths []string, opening bool) string {
	htmlPath := ""
	var others []string
	for _, p := range exportPaths {
		if htmlPath == "" && strings.HasSuffix(strings.ToLower(p), ".html") {
			htmlPath = p
		} else {
			others = append(others, p)
		}
	}
	var b strings.Builder
	if htmlPath != "" {
		line := "full report → " + htmlPath
		if opening {
			line += " · opening in your browser"
		}
		b.WriteString(Gray(line) + "\n")
	} else {
		b.WriteString(Gray("full report: add --open (or --export html) · terminal tables: --details") + "\n")
	}
	if len(others) > 0 {
		b.WriteString(Gray("exports → "+strings.Join(others, " · ")) + "\n")
	}
	return b.String()
}

func sortedScores(res *model.RunResult, mode model.RankMode) []model.Score {
	scores := make([]model.Score, len(res.Scores[mode]))
	copy(scores, res.Scores[mode])
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].TotalMs != scores[j].TotalMs {
			return scores[i].TotalMs < scores[j].TotalMs
		}
		return scores[i].ServerID < scores[j].ServerID
	})
	return scores
}

func displayCategories(res *model.RunResult) []model.Category {
	cats := res.Config.Categories
	if len(cats) == 0 {
		cats = model.AllCategories()
	}
	return cats
}

func systemIDSet(res *model.RunResult) map[string]bool {
	set := make(map[string]bool, len(res.SystemIDs))
	for _, id := range res.SystemIDs {
		set[id] = true
	}
	return set
}

func serverName(res *model.RunResult, id string) string {
	if srv := res.ServerByID(id); srv != nil {
		return srv.DisplayName()
	}
	return id
}

func scoreColor(total, best float64) func(string) string {
	if best <= 0 {
		return Green
	}
	ratio := total / best
	switch {
	case ratio <= 1.25:
		return Green
	case ratio <= 2:
		return Yellow
	}
	return Red
}

func aggregateLossPct(st *model.ServerStats, cats []model.Category) float64 {
	total, lost := 0, 0
	for _, c := range cats {
		if d := st.PerCategory[c]; d != nil {
			total += d.Count
			lost += d.Count - d.Answered
		}
	}
	if total == 0 {
		return 0
	}
	return float64(lost) / float64(total) * 100
}

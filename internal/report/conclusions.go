package report

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"dnsbench/internal/model"
)

const localForwarderSentence = "This system DNS endpoint was detected successfully. It is a local/private forwarder, so only the upstream behind this endpoint is unknown; other detected DNS endpoints are listed separately."

func BuildConclusions(res *model.RunResult) string {
	var b strings.Builder
	writeSection(&b, "Current DNS configuration", systemServerLines(res))
	writeSection(&b, "Run coverage", serversTestedLines(res))
	writeSection(&b, "Statistical comparisons", comparisonLines(res))
	return b.String()
}

func writeSection(b *strings.Builder, title string, lines []string) {
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(strings.Repeat("-", len(title)))
	b.WriteString("\n")
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func systemServerLines(res *model.RunResult) []string {
	selectedByKey := make(map[string]*model.Server, len(res.SystemIDs))
	for _, id := range res.SystemIDs {
		if selected := res.ServerByID(id); selected != nil {
			selectedByKey[selected.Key()] = selected
		}
	}
	if len(res.SystemServers) > 0 {
		var lines []string
		localForwarder := false
		for _, system := range res.SystemServers {
			line := "Detected resolver " + system.Endpoint()
			if system.Interface != "" {
				line += " on " + system.Interface
			}
			if system.SystemRole != "" {
				line += " (" + system.SystemRole + ")"
			}
			if selected := selectedByKey[system.Key()]; selected != nil {
				line += "; included in this run as " + selected.DisplayName()
			} else {
				line += "; not included in this run"
			}
			lines = append(lines, line+".")
			localForwarder = localForwarder || model.ScopeOfString(system.Address).IsLocal()
		}
		if localForwarder {
			lines = append(lines, localForwarderSentence)
		}
		return lines
	}
	if len(res.SystemIDs) == 0 {
		return []string{"No system DNS resolver was detected."}
	}

	var lines []string
	for _, id := range res.SystemIDs {
		s := res.ServerByID(id)
		if s == nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("The current system DNS server is %s (%s).", s.DisplayName(), s.Endpoint()))
		if model.ScopeOfString(s.Address).IsLocal() {
			lines = append(lines, localForwarderSentence)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "No system DNS server was detected.")
	}
	return lines
}

func serversTestedLines(res *model.RunResult) []string {
	counts := map[model.ServerState]int{}
	for i := range res.Servers {
		st := stateOf(res, res.Servers[i].ID)
		if st != "" {
			counts[st]++
		}
	}
	line := fmt.Sprintf("Resolvers selected: %d total (%d active, %d sidelined, %d unreachable",
		len(res.Servers), counts[model.StateActive], counts[model.StateBenched], counts[model.StateOffline])
	if counts[model.StateError] > 0 {
		line += fmt.Sprintf(", %d with errors", counts[model.StateError])
	}
	line += ")."
	lines := []string{line}
	for i := range res.Servers {
		s := &res.Servers[i]
		if stateOf(res, s.ID) != model.StateOffline {
			continue
		}
		reason := "no reason recorded"
		if tr, ok := res.Triage[s.ID]; ok && tr != nil && tr.Reason != "" {
			reason = tr.Reason
		}
		lines = append(lines, fmt.Sprintf("Unreachable: %s (%s): %s.", s.DisplayName(), s.Endpoint(), reason))
	}
	return lines
}

func comparisonLines(res *model.RunResult) []string {
	var lines []string
	mode := selectedMode(res)
	if aID, bID, tied := TopTwoTied(res, mode); tied {
		lines = append(lines, fmt.Sprintf("%s and %s are effectively tied at the top of the %s ranking; the measured difference does not support ordering them.",
			displayNameByID(res, aID), displayNameByID(res, bID), mode.Label()))
	}
	for _, c := range res.Comparisons {
		lines = append(lines, comparisonSentence(res, c))
	}
	if len(lines) == 0 {
		lines = append(lines, "No pairwise statistical comparisons were produced.")
	}
	return lines
}

func comparisonSentence(res *model.RunResult, c model.Comparison) string {
	a := displayNameByID(res, c.ServerA)
	b := displayNameByID(res, c.ServerB)
	if c.RankingMode != "" {
		if c.BootstrapSamples == 0 && c.Level == model.SigInconclusive {
			return fmt.Sprintf(
				"The aggregate %s comparison between %s and %s was inconclusive because there were not enough complete paired rounds.",
				c.RankingMode.Label(), a, b)
		}
		magnitude := math.Abs(c.DeltaScoreMs)
		var base string
		if magnitude < 0.001 {
			base = fmt.Sprintf(
				"%s and %s had the same aggregate %s latency cost (95%% bootstrap interval %.1f to %.1f ms)",
				a, b, c.RankingMode.Label(), c.CI95LowMs, c.CI95HighMs)
		} else {
			faster, slower := a, b
			if c.DeltaScoreMs > 0 {
				faster, slower = b, a
			}
			base = fmt.Sprintf(
				"%s had a %.1f ms lower aggregate %s latency cost than %s (95%% bootstrap interval %.1f to %.1f ms)",
				faster, magnitude, c.RankingMode.Label(), slower, c.CI95LowMs, c.CI95HighMs)
		}
		switch c.Level {
		case model.SigSignificant:
			return base + ", and the difference was statistically significant."
		case model.SigLikely:
			return base + ", and the difference was likely real, though not conclusive."
		case model.SigNegligible:
			return base + ", but the difference is too small to matter in practice."
		default:
			return base + ", but the difference was inconclusive."
		}
	}
	mag := math.Abs(c.DeltaMeanMs)
	var base string
	if mag < 0.001 {
		base = fmt.Sprintf("In the %s category, servers %s and %s showed essentially the same average latency", c.Category.Label(), a, b)
	} else {
		faster, slower := a, b
		if c.DeltaMeanMs > 0 {
			faster, slower = b, a
		}
		base = fmt.Sprintf("In the %s category, server %s showed lower average latency than server %s (%.1f ms difference)", c.Category.Label(), faster, slower, mag)
	}
	switch c.Level {
	case model.SigSignificant:
		return base + ", and the difference was statistically significant."
	case model.SigLikely:
		return base + ", and the difference was likely real, though not conclusive."
	case model.SigNegligible:
		return base + ", but the difference is too small to matter in practice."
	default:
		return base + ", but the difference was not statistically significant."
	}
}

func TopTwoTied(res *model.RunResult, mode model.RankMode) (string, string, bool) {
	ranked := rankedScores(res, mode)
	if len(ranked) < 2 {
		return "", "", false
	}
	a, b := ranked[0].ServerID, ranked[1].ServerID
	found := false
	for _, c := range res.Comparisons {
		if c.RankingMode != "" && c.RankingMode != mode {
			continue
		}
		if (c.ServerA == a && c.ServerB == b) || (c.ServerA == b && c.ServerB == a) {
			if c.Level != model.SigInconclusive && c.Level != model.SigNegligible {
				return "", "", false
			}
			found = true
		}
	}
	return a, b, found
}

func selectedMode(res *model.RunResult) model.RankMode {
	switch res.SelectedRanking {
	case model.RankLatency, model.RankBrowsing, model.RankReliability:
		if len(res.Scores[res.SelectedRanking]) > 0 {
			return res.SelectedRanking
		}
	}
	for _, m := range []model.RankMode{model.RankBrowsing, model.RankLatency, model.RankReliability} {
		if len(res.Scores[m]) > 0 {
			return m
		}
	}
	return model.RankLatency
}

func rankedScores(res *model.RunResult, mode model.RankMode) []model.Score {
	src := res.Scores[mode]
	if len(src) == 0 {
		return nil
	}
	out := make([]model.Score, len(src))
	copy(out, src)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Rank < out[j].Rank })
	return out
}

func stateOf(res *model.RunResult, id string) model.ServerState {
	if st, ok := res.Stats[id]; ok && st != nil && st.State != "" {
		return st.State
	}
	if tr, ok := res.Triage[id]; ok && tr != nil {
		return tr.State
	}
	return ""
}

func displayNameByID(res *model.RunResult, id string) string {
	if s := res.ServerByID(id); s != nil {
		return s.DisplayName()
	}
	return id
}

func aggregateLoss(st *model.ServerStats) float64 {
	if st == nil {
		return 0
	}
	total := 0
	weighted := 0.0
	for _, d := range st.PerCategory {
		if d == nil || d.Count == 0 {
			continue
		}
		total += d.Count
		weighted += d.LossPct * float64(d.Count)
	}
	if total == 0 {
		return 0
	}
	return weighted / float64(total)
}

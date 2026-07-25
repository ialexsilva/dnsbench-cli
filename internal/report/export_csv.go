package report

import (
	"encoding/csv"
	"io"
	"strconv"

	"dnsbench/internal/model"
)

var csvColumns = []string{
	"server_id", "name", "operator", "protocol", "endpoint", "source", "state", "category",
	"queries", "answered", "valid", "attempts", "attempt_failures", "timeout_attempts", "retried", "timeouts", "errors",
	"loss_pct", "retry_pct", "attempt_failure_pct",
	"min_ms", "max_ms", "mean_ms", "median_ms", "stddev_ms", "variance_ms2",
	"p50_ms", "p90_ms", "p95_ms", "p99_ms", "ci95_low_ms", "ci95_high_ms",
	"jitter_ms", "servfail_pct", "invalid_pct", "truncated_pct",
	"ranking_mode", "rank", "cost_base_ms", "cost_penalty_ms", "latency_cost_ms",
}

func ExportCSV(w io.Writer, res *model.RunResult) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvColumns); err != nil {
		return err
	}
	mode := selectedMode(res)
	modeCell := ""
	scores := make(map[string]model.Score, len(res.Scores[mode]))
	for _, sc := range res.Scores[mode] {
		scores[sc.ServerID] = sc
		modeCell = string(mode)
	}
	for i := range res.Servers {
		s := &res.Servers[i]
		st, ok := res.Stats[s.ID]
		if !ok || st == nil {
			continue
		}
		for _, cat := range model.AllCategories() {
			d := st.PerCategory[cat]
			if d == nil {
				continue
			}
			row := []string{
				s.ID, s.Name, s.Operator, string(s.Protocol), s.Endpoint(), string(s.Source),
				string(st.State), string(cat),
				strconv.Itoa(d.Count), strconv.Itoa(d.Answered), strconv.Itoa(d.Valid),
				strconv.Itoa(d.Attempts), strconv.Itoa(d.AttemptFailures), strconv.Itoa(d.TimeoutAttempts), strconv.Itoa(d.Retried),
				strconv.Itoa(d.Timeouts), strconv.Itoa(d.Errors),
				csvFloat(d.LossPct), csvFloat(d.RetryPct), csvFloat(d.AttemptFailurePct),
				csvFloat(d.MinMs), csvFloat(d.MaxMs), csvFloat(d.MeanMs), csvFloat(d.MedianMs),
				csvFloat(d.StdDevMs), csvFloat(d.VarianceMs2),
				csvFloat(d.P50Ms), csvFloat(d.P90Ms), csvFloat(d.P95Ms), csvFloat(d.P99Ms),
				csvFloat(d.CI95LowMs), csvFloat(d.CI95HighMs),
				csvFloat(d.JitterMs), csvFloat(d.ServfailPct), csvFloat(d.InvalidPct), csvFloat(d.TruncatedPct),
			}
			row = append(row, csvRanking(scores, modeCell, s.ID)...)
			if err := cw.Write(row); err != nil {
				return err
			}
		}
	}
	cw.Flush()
	return cw.Error()
}

// csvRanking returns the ranking cells for a server. Servers that never earned
// a rank — unreachable, sidelined, or missing a category — get empty cells
// rather than a zero that a spreadsheet would happily average.
func csvRanking(scores map[string]model.Score, mode, serverID string) []string {
	sc, ok := scores[serverID]
	if !ok {
		return []string{mode, "", "", "", ""}
	}
	penalty := 0.0
	for _, v := range sc.Penalties {
		penalty += v
	}
	return []string{
		mode, strconv.Itoa(sc.Rank),
		csvFloat(sc.BaseMs), csvFloat(penalty), csvFloat(sc.TotalMs),
	}
}

func csvFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 3, 64)
}

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
}

func ExportCSV(w io.Writer, res *model.RunResult) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvColumns); err != nil {
		return err
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
			if err := cw.Write(row); err != nil {
				return err
			}
		}
	}
	cw.Flush()
	return cw.Error()
}

func csvFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 3, 64)
}

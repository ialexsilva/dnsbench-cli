package report

import (
	"encoding/json"
	"io"

	"dnsbench/internal/model"
)

func ExportJSON(w io.Writer, res *model.RunResult, includeRaw bool) error {
	out := res
	if !includeRaw {
		out = withoutRawSamples(res)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func withoutRawSamples(res *model.RunResult) *model.RunResult {
	cp := *res
	cp.Samples = nil
	if res.Stats == nil {
		return &cp
	}
	stats := make(map[string]*model.ServerStats, len(res.Stats))
	for id, st := range res.Stats {
		if st == nil {
			stats[id] = nil
			continue
		}
		sc := *st
		if st.PerCategory != nil {
			pc := make(map[model.Category]*model.Distribution, len(st.PerCategory))
			for cat, d := range st.PerCategory {
				if d == nil {
					pc[cat] = nil
					continue
				}
				dc := *d
				dc.SamplesMs = nil
				pc[cat] = &dc
			}
			sc.PerCategory = pc
		}
		stats[id] = &sc
	}
	cp.Stats = stats
	return &cp
}

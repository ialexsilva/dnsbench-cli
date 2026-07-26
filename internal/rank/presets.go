package rank

import "dnsbench/internal/model"

func Presets() map[model.RankMode]model.Weights {
	return map[model.RankMode]model.Weights{
		model.RankLatency: {
			Category: map[model.Category]float64{
				model.CatCached:   1.0 / 3.0,
				model.CatUncached: 1.0 / 3.0,
				model.CatTLD:      1.0 / 3.0,
			},
			LatencyMetric:           "median",
			PenaltyPerLossPctMs:     5,
			PenaltyPerServfailPctMs: 5,
			PenaltyPerInvalidPctMs:  5,
			PenaltyPerRetryPctMs:    2,
			PenaltyNXInterceptionMs: 5,
			PenaltyNoDNSSECMs:       5,
			JitterWeight:            0.25,
		},
		model.RankBrowsing: {
			Category: map[model.Category]float64{
				model.CatCached:   0.30,
				model.CatUncached: 0.45,
				model.CatTLD:      0.25,
			},
			LatencyMetric:           "median",
			PenaltyPerLossPctMs:     10,
			PenaltyPerServfailPctMs: 10,
			PenaltyPerInvalidPctMs:  10,
			PenaltyPerRetryPctMs:    5,
			PenaltyNXInterceptionMs: 15,
			PenaltyNoDNSSECMs:       10,
			JitterWeight:            0.5,
		},
		model.RankReliability: {
			Category: map[model.Category]float64{
				model.CatCached:   1.0 / 3.0,
				model.CatUncached: 1.0 / 3.0,
				model.CatTLD:      1.0 / 3.0,
			},
			LatencyMetric:           "p95",
			PenaltyPerLossPctMs:     25,
			PenaltyPerServfailPctMs: 25,
			PenaltyPerInvalidPctMs:  25,
			PenaltyPerRetryPctMs:    10,
			PenaltyNXInterceptionMs: 25,
			PenaltyNoDNSSECMs:       20,
			JitterWeight:            1.0,
		},
	}
}

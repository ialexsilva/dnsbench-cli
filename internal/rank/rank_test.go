package rank

import (
	"math"
	"testing"

	"dnsbench/internal/model"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func activeStats(id string, cats map[model.Category]*model.Distribution) *model.ServerStats {
	for _, distribution := range cats {
		if distribution.Valid == 0 && distribution.Answered > 0 {
			distribution.Valid = distribution.Answered
		}
	}
	return &model.ServerStats{ServerID: id, State: model.StateActive, PerCategory: cats}
}

func singleCatWeights(metric string) model.Weights {
	return model.Weights{
		Category:      map[model.Category]float64{model.CatCached: 1},
		LatencyMetric: metric,
	}
}

func cleanProbe(id string) *model.ProbeResult {
	return &model.ProbeResult{
		ServerID:       id,
		Reachable:      true,
		NXInterception: model.VerdictNo,
		DNSSEC:         model.DNSSECInfo{Validating: model.VerdictYes},
	}
}

func findScore(t *testing.T, scores []model.Score, id string) model.Score {
	t.Helper()
	for _, s := range scores {
		if s.ServerID == id {
			return s
		}
	}
	t.Fatalf("score for %q not found", id)
	return model.Score{}
}

func TestOrderingByTotal(t *testing.T) {
	stats := map[string]*model.ServerStats{
		"slow": activeStats("slow", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, MedianMs: 20},
		}),
		"fast": activeStats("fast", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, MedianMs: 10},
		}),
	}
	scores := ScoreServers(stats, nil, []model.Category{model.CatCached}, singleCatWeights("median"), model.RankLatency)
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}
	if scores[0].ServerID != "fast" || scores[0].Rank != 1 {
		t.Errorf("expected fast at rank 1, got %q rank %d", scores[0].ServerID, scores[0].Rank)
	}
	if scores[1].ServerID != "slow" || scores[1].Rank != 2 {
		t.Errorf("expected slow at rank 2, got %q rank %d", scores[1].ServerID, scores[1].Rank)
	}
	if !almostEqual(scores[0].TotalMs, 10) || !almostEqual(scores[1].TotalMs, 20) {
		t.Errorf("unexpected totals: %v %v", scores[0].TotalMs, scores[1].TotalMs)
	}
}

func TestRenormalizationWhenUncachedAbsent(t *testing.T) {
	w := model.Weights{
		Category: map[model.Category]float64{
			model.CatCached:   0.30,
			model.CatUncached: 0.45,
			model.CatTLD:      0.25,
		},
		LatencyMetric: "median",
	}
	stats := map[string]*model.ServerStats{
		"s1": activeStats("s1", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, MedianMs: 10},
			model.CatTLD:    {Count: 10, Answered: 10, MedianMs: 20},
		}),
	}
	scores := ScoreServers(stats, nil, []model.Category{model.CatCached, model.CatTLD}, w, model.RankBrowsing)
	if len(scores) != 1 {
		t.Fatalf("expected 1 score, got %d", len(scores))
	}
	expected := (0.30*10 + 0.25*20) / (0.30 + 0.25)
	if !almostEqual(scores[0].BaseMs, expected) {
		t.Errorf("expected base %v, got %v", expected, scores[0].BaseMs)
	}
	if !almostEqual(scores[0].TotalMs, expected) {
		t.Errorf("expected total %v, got %v", expected, scores[0].TotalMs)
	}
}

func TestLossPenaltyExact(t *testing.T) {
	w := singleCatWeights("median")
	w.PenaltyPerLossPctMs = 5
	stats := map[string]*model.ServerStats{
		"s1": activeStats("s1", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 100, Answered: 90, MedianMs: 10},
		}),
	}
	scores := ScoreServers(stats, nil, []model.Category{model.CatCached}, w, model.RankLatency)
	s := findScore(t, scores, "s1")
	if !almostEqual(s.Penalties["loss"], 50) {
		t.Errorf("expected loss penalty 50, got %v", s.Penalties["loss"])
	}
	if !almostEqual(s.TotalMs, 60) {
		t.Errorf("expected total 60, got %v", s.TotalMs)
	}
}

func TestServfailPenaltyExact(t *testing.T) {
	w := singleCatWeights("median")
	w.PenaltyPerServfailPctMs = 10
	stats := map[string]*model.ServerStats{
		"s1": activeStats("s1", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 100, Answered: 100, Servfails: 4, MedianMs: 10},
		}),
	}
	scores := ScoreServers(stats, nil, []model.Category{model.CatCached}, w, model.RankLatency)
	s := findScore(t, scores, "s1")
	if !almostEqual(s.Penalties["servfail"], 40) {
		t.Errorf("expected servfail penalty 40, got %v", s.Penalties["servfail"])
	}
	if !almostEqual(s.TotalMs, 50) {
		t.Errorf("expected total 50, got %v", s.TotalMs)
	}
}

func TestInvalidAndRetryPenaltiesExact(t *testing.T) {
	w := singleCatWeights("median")
	w.PenaltyPerInvalidPctMs = 4
	w.PenaltyPerRetryPctMs = 2
	stats := map[string]*model.ServerStats{
		"s1": activeStats("s1", map[model.Category]*model.Distribution{
			model.CatCached: {
				Count:    100,
				Answered: 100,
				Valid:    90,
				Invalid:  10,
				Retried:  20,
				MedianMs: 10,
			},
		}),
	}
	s := findScore(t, ScoreServers(
		stats, nil, []model.Category{model.CatCached}, w, model.RankLatency,
	), "s1")
	if !almostEqual(s.Penalties[penaltyInvalid], 40) {
		t.Errorf("expected invalid-response penalty 40, got %v", s.Penalties[penaltyInvalid])
	}
	if !almostEqual(s.Penalties[penaltyRetry], 40) {
		t.Errorf("expected retry penalty 40, got %v", s.Penalties[penaltyRetry])
	}
	if !almostEqual(s.TotalMs, 90) {
		t.Errorf("expected total 90, got %v", s.TotalMs)
	}
}

func TestAllConfiguredCategoriesRequireValidResolution(t *testing.T) {
	stats := map[string]*model.ServerStats{
		"complete": activeStats("complete", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, Valid: 10, MedianMs: 10},
			model.CatTLD:    {Count: 10, Answered: 10, Valid: 10, MedianMs: 20},
		}),
		"missing": activeStats("missing", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, Valid: 10, MedianMs: 1},
		}),
		"invalid": {
			ServerID: "invalid",
			State:    model.StateActive,
			PerCategory: map[model.Category]*model.Distribution{
				model.CatCached: {Count: 10, Answered: 10, Valid: 10, MedianMs: 1},
				model.CatTLD:    {Count: 10, Answered: 10, Valid: 0},
			},
		},
	}
	scores := ScoreServers(
		stats,
		nil,
		[]model.Category{model.CatCached, model.CatTLD},
		model.Weights{
			Category: map[model.Category]float64{
				model.CatCached: 0.5,
				model.CatTLD:    0.5,
			},
			LatencyMetric: "median",
		},
		model.RankLatency,
	)
	if len(scores) != 1 || scores[0].ServerID != "complete" {
		t.Fatalf("scores = %+v, want only the complete server", scores)
	}
	if !almostEqual(scores[0].BaseMs, 15) {
		t.Errorf("base = %v, want 15", scores[0].BaseMs)
	}
}

func TestJitterPenaltyExact(t *testing.T) {
	w := model.Weights{
		Category: map[model.Category]float64{
			model.CatCached: 0.5,
			model.CatTLD:    0.5,
		},
		LatencyMetric: "median",
		JitterWeight:  0.5,
	}
	stats := map[string]*model.ServerStats{
		"s1": activeStats("s1", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, MedianMs: 10, JitterMs: 2},
			model.CatTLD:    {Count: 10, Answered: 10, MedianMs: 20, JitterMs: 4},
		}),
	}
	scores := ScoreServers(stats, nil, []model.Category{model.CatCached, model.CatTLD}, w, model.RankLatency)
	s := findScore(t, scores, "s1")
	if !almostEqual(s.Penalties["jitter"], 1.5) {
		t.Errorf("expected jitter penalty 1.5, got %v", s.Penalties["jitter"])
	}
	if !almostEqual(s.TotalMs, 16.5) {
		t.Errorf("expected total 16.5, got %v", s.TotalMs)
	}
}

func TestNXInterceptionPenaltyExact(t *testing.T) {
	w := singleCatWeights("median")
	w.PenaltyNXInterceptionMs = 15
	stats := map[string]*model.ServerStats{
		"s1": activeStats("s1", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, MedianMs: 10},
		}),
	}
	probe := cleanProbe("s1")
	probe.NXInterception = model.VerdictYes
	probes := map[string]*model.ProbeResult{"s1": probe}
	scores := ScoreServers(stats, probes, []model.Category{model.CatCached}, w, model.RankBrowsing)
	s := findScore(t, scores, "s1")
	if !almostEqual(s.Penalties["nxdomain-interception"], 15) {
		t.Errorf("expected nxdomain-interception penalty 15, got %v", s.Penalties["nxdomain-interception"])
	}
	if !almostEqual(s.TotalMs, 25) {
		t.Errorf("expected total 25, got %v", s.TotalMs)
	}
}

func TestNoDNSSECPenaltyExact(t *testing.T) {
	w := singleCatWeights("median")
	w.PenaltyNoDNSSECMs = 10
	stats := map[string]*model.ServerStats{
		"s1": activeStats("s1", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, MedianMs: 10},
		}),
	}
	for _, verdict := range []model.Verdict{model.VerdictNo, model.VerdictUnknown, model.VerdictPartial} {
		probe := cleanProbe("s1")
		probe.DNSSEC.Validating = verdict
		probes := map[string]*model.ProbeResult{"s1": probe}
		scores := ScoreServers(stats, probes, []model.Category{model.CatCached}, w, model.RankBrowsing)
		s := findScore(t, scores, "s1")
		if !almostEqual(s.Penalties["no-dnssec"], 10) {
			t.Errorf("verdict %q: expected no-dnssec penalty 10, got %v", verdict, s.Penalties["no-dnssec"])
		}
		if !almostEqual(s.TotalMs, 20) {
			t.Errorf("verdict %q: expected total 20, got %v", verdict, s.TotalMs)
		}
	}
}

func TestZeroPenaltiesStayOutOfMap(t *testing.T) {
	w := singleCatWeights("median")
	w.PenaltyPerLossPctMs = 5
	w.PenaltyPerServfailPctMs = 5
	w.JitterWeight = 0.25
	stats := map[string]*model.ServerStats{
		"s1": activeStats("s1", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, MedianMs: 10},
		}),
	}
	probes := map[string]*model.ProbeResult{"s1": cleanProbe("s1")}
	scores := ScoreServers(stats, probes, []model.Category{model.CatCached}, w, model.RankLatency)
	s := findScore(t, scores, "s1")
	if len(s.Penalties) != 0 {
		t.Errorf("expected empty penalties map, got %v", s.Penalties)
	}
}

func TestTiesShareRank(t *testing.T) {
	stats := map[string]*model.ServerStats{
		"a": activeStats("a", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, MedianMs: 10},
		}),
		"b": activeStats("b", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, MedianMs: 10.005},
		}),
		"c": activeStats("c", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, MedianMs: 20},
		}),
	}
	scores := ScoreServers(stats, nil, []model.Category{model.CatCached}, singleCatWeights("median"), model.RankLatency)
	if len(scores) != 3 {
		t.Fatalf("expected 3 scores, got %d", len(scores))
	}
	if scores[0].ServerID != "a" || scores[0].Rank != 1 {
		t.Errorf("expected a at rank 1, got %q rank %d", scores[0].ServerID, scores[0].Rank)
	}
	if scores[1].ServerID != "b" || scores[1].Rank != 1 {
		t.Errorf("expected b tied at rank 1, got %q rank %d", scores[1].ServerID, scores[1].Rank)
	}
	if scores[2].ServerID != "c" || scores[2].Rank != 3 {
		t.Errorf("expected c at rank 3, got %q rank %d", scores[2].ServerID, scores[2].Rank)
	}
}

func TestExcludesInactiveAndUnanswered(t *testing.T) {
	stats := map[string]*model.ServerStats{
		"active-ok": activeStats("active-ok", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, MedianMs: 10},
		}),
		"benched": {
			ServerID: "benched",
			State:    model.StateBenched,
			PerCategory: map[model.Category]*model.Distribution{
				model.CatCached: {Count: 10, Answered: 10, MedianMs: 5},
			},
		},
		"offline": {
			ServerID: "offline",
			State:    model.StateOffline,
			PerCategory: map[model.Category]*model.Distribution{
				model.CatCached: {Count: 10, Answered: 10, MedianMs: 5},
			},
		},
		"no-answers": activeStats("no-answers", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 0},
		}),
	}
	scores := ScoreServers(stats, nil, []model.Category{model.CatCached}, singleCatWeights("median"), model.RankLatency)
	if len(scores) != 1 {
		t.Fatalf("expected 1 score, got %d", len(scores))
	}
	if scores[0].ServerID != "active-ok" {
		t.Errorf("expected active-ok, got %q", scores[0].ServerID)
	}
}

func TestP95MetricChangesOutcome(t *testing.T) {
	stats := map[string]*model.ServerStats{
		"fast-median": activeStats("fast-median", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, MedianMs: 10, P95Ms: 50},
		}),
		"fast-p95": activeStats("fast-p95", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, MedianMs: 20, P95Ms: 30},
		}),
	}
	byMedian := ScoreServers(stats, nil, []model.Category{model.CatCached}, singleCatWeights("median"), model.RankLatency)
	if byMedian[0].ServerID != "fast-median" {
		t.Errorf("median metric: expected fast-median first, got %q", byMedian[0].ServerID)
	}
	byP95 := ScoreServers(stats, nil, []model.Category{model.CatCached}, singleCatWeights("p95"), model.RankReliability)
	if byP95[0].ServerID != "fast-p95" {
		t.Errorf("p95 metric: expected fast-p95 first, got %q", byP95[0].ServerID)
	}
	if !almostEqual(byP95[0].TotalMs, 30) || !almostEqual(byP95[1].TotalMs, 50) {
		t.Errorf("p95 metric: unexpected totals %v %v", byP95[0].TotalMs, byP95[1].TotalMs)
	}
}

func TestMeanMetric(t *testing.T) {
	stats := map[string]*model.ServerStats{
		"s1": activeStats("s1", map[model.Category]*model.Distribution{
			model.CatCached: {Count: 10, Answered: 10, MeanMs: 12, MedianMs: 10, P95Ms: 50},
		}),
	}
	scores := ScoreServers(stats, nil, []model.Category{model.CatCached}, singleCatWeights("mean"), model.RankLatency)
	s := findScore(t, scores, "s1")
	if !almostEqual(s.BaseMs, 12) {
		t.Errorf("expected base 12, got %v", s.BaseMs)
	}
}

func TestPresets(t *testing.T) {
	presets := Presets()
	if len(presets) != 3 {
		t.Fatalf("expected 3 presets, got %d", len(presets))
	}
	latency := presets[model.RankLatency]
	if latency.LatencyMetric != "median" || !almostEqual(latency.JitterWeight, 0.25) ||
		!almostEqual(latency.Category[model.CatUncached], 1.0/3.0) ||
		!almostEqual(latency.PenaltyPerLossPctMs, 5) {
		t.Errorf("unexpected latency preset: %+v", latency)
	}
	browsing := presets[model.RankBrowsing]
	if browsing.LatencyMetric != "median" ||
		!almostEqual(browsing.Category[model.CatCached], 0.30) ||
		!almostEqual(browsing.Category[model.CatUncached], 0.45) ||
		!almostEqual(browsing.Category[model.CatTLD], 0.25) ||
		!almostEqual(browsing.PenaltyNXInterceptionMs, 15) ||
		!almostEqual(browsing.JitterWeight, 0.5) {
		t.Errorf("unexpected browsing preset: %+v", browsing)
	}
	reliability := presets[model.RankReliability]
	if reliability.LatencyMetric != "p95" ||
		!almostEqual(reliability.PenaltyPerLossPctMs, 25) ||
		!almostEqual(reliability.PenaltyNoDNSSECMs, 20) ||
		!almostEqual(reliability.JitterWeight, 1.0) {
		t.Errorf("unexpected reliability preset: %+v", reliability)
	}
}

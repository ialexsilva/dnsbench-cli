package rank

import (
	"reflect"
	"testing"
	"time"

	"github.com/miekg/dns"

	"dnsbench/internal/model"
)

func scoreSample(id string, round int, category model.Category, elapsedMs float64) model.Sample {
	result := model.QueryResult{
		RTT:     time.Duration(elapsedMs * float64(time.Millisecond)),
		Rcode:   dns.RcodeSuccess,
		Answers: []model.RR{{Type: "A", Data: "192.0.2.1"}},
	}
	if category == model.CatTLD {
		result.Rcode = dns.RcodeNameError
		result.Answers = nil
	}
	return model.Sample{
		ServerID: id,
		Category: category,
		Round:    round,
		QType:    "A",
		Attempts: 1,
		Elapsed:  time.Duration(elapsedMs * float64(time.Millisecond)),
		At:       time.Unix(int64(round), 0),
		Result:   result,
	}
}

func pairedScoreSamples(rounds int, deltaMs float64) []model.Sample {
	var samples []model.Sample
	for round := 1; round <= rounds; round++ {
		noise := float64(round%3) - 1
		samples = append(samples,
			scoreSample("a", round, model.CatCached, 10+noise),
			scoreSample("a", round, model.CatTLD, 20+noise),
			scoreSample("b", round, model.CatCached, 10+deltaMs+noise),
			scoreSample("b", round, model.CatTLD, 20+deltaMs+noise),
		)
	}
	return samples
}

func TestCompareScoresUsesAggregatePairedBootstrap(t *testing.T) {
	categories := []model.Category{model.CatCached, model.CatTLD}
	weights := model.Weights{
		Category: map[model.Category]float64{
			model.CatCached: 0.5,
			model.CatTLD:    0.5,
		},
		LatencyMetric: "median",
	}
	cmp := CompareScores(
		"a", "b", pairedScoreSamples(20, 20), categories, nil, weights,
		model.RankLatency, 42, DefaultScoreCompareConfig(),
	)
	if cmp.RankingMode != model.RankLatency {
		t.Fatalf("RankingMode = %q, want latency", cmp.RankingMode)
	}
	if cmp.Level != model.SigSignificant {
		t.Fatalf("Level = %q, want significant (%+v)", cmp.Level, cmp)
	}
	if cmp.BootstrapSamples != 1000 {
		t.Fatalf("BootstrapSamples = %d, want 1000", cmp.BootstrapSamples)
	}
	if !almostEqual(cmp.DeltaScoreMs, -20) {
		t.Errorf("DeltaScoreMs = %v, want -20", cmp.DeltaScoreMs)
	}
	if cmp.CI95HighMs >= 0 {
		t.Errorf("CI = [%v, %v], want entirely below zero", cmp.CI95LowMs, cmp.CI95HighMs)
	}
}

func TestCompareScoresMarksSmallDifferenceNegligible(t *testing.T) {
	categories := []model.Category{model.CatCached, model.CatTLD}
	weights := model.Weights{
		Category: map[model.Category]float64{
			model.CatCached: 0.5,
			model.CatTLD:    0.5,
		},
		LatencyMetric: "median",
	}
	cmp := CompareScores(
		"a", "b", pairedScoreSamples(20, 1), categories, nil, weights,
		model.RankLatency, 42, DefaultScoreCompareConfig(),
	)
	if cmp.Level != model.SigNegligible {
		t.Fatalf("Level = %q, want negligible (%+v)", cmp.Level, cmp)
	}
}

func TestCompareScoresIsDeterministicForSeed(t *testing.T) {
	categories := []model.Category{model.CatCached, model.CatTLD}
	weights := model.Weights{
		Category: map[model.Category]float64{
			model.CatCached: 0.5,
			model.CatTLD:    0.5,
		},
		LatencyMetric: "median",
	}
	cfg := DefaultScoreCompareConfig()
	first := CompareScores("a", "b", pairedScoreSamples(20, 5), categories, nil, weights, model.RankLatency, 99, cfg)
	second := CompareScores("a", "b", pairedScoreSamples(20, 5), categories, nil, weights, model.RankLatency, 99, cfg)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("comparisons differ:\nfirst:  %+v\nsecond: %+v", first, second)
	}
}

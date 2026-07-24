package rank

import (
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"dnsbench/internal/model"
	"dnsbench/internal/stats"
)

type ScoreCompareConfig struct {
	Iterations     int
	MinRounds      int
	MinRelevantMs  float64
	MinRelevantPct float64
}

func DefaultScoreCompareConfig() ScoreCompareConfig {
	return ScoreCompareConfig{
		Iterations:     1000,
		MinRounds:      5,
		MinRelevantMs:  3,
		MinRelevantPct: 10,
	}
}

// CompareScores uses a paired bootstrap over benchmark rounds. Resampling a
// whole round preserves the categories and the two resolvers' shared network
// conditions, then recomputes the same aggregate score shown in the ranking.
func CompareScores(
	idA, idB string,
	samples []model.Sample,
	categories []model.Category,
	probes map[string]*model.ProbeResult,
	weights model.Weights,
	mode model.RankMode,
	seed int64,
	cfg ScoreCompareConfig,
) model.Comparison {
	cmp := model.Comparison{
		ServerA:     idA,
		ServerB:     idB,
		RankingMode: mode,
		PValue:      1,
	}
	rounds := pairedRounds(samples, idA, idB, categories)
	actualA, actualB, ok := scorePair(idA, idB, pairSamples(samples, idA, idB), categories, probes, weights, mode)
	if !ok {
		cmp.Level = model.SigInconclusive
		cmp.Summary = fmt.Sprintf("%s and %s could not be compared because one aggregate score was incomplete.", idA, idB)
		return cmp
	}
	cmp.DeltaScoreMs = actualA - actualB
	if cfg.MinRounds < 1 {
		cfg.MinRounds = 5
	}
	if len(rounds) < cfg.MinRounds {
		cmp.Level = model.SigInconclusive
		cmp.Summary = fmt.Sprintf(
			"Not enough paired rounds to compare the aggregate %s scores of %s and %s (%d available; at least %d are required).",
			mode.Label(), idA, idB, len(rounds), cfg.MinRounds)
		return cmp
	}
	if cfg.Iterations < 1 {
		cfg.Iterations = 1000
	}
	rng := comparisonRNG(seed, idA, idB, mode)
	deltas := make([]float64, 0, cfg.Iterations)
	maxDraws := cfg.Iterations * 3
	for draw := 0; draw < maxDraws && len(deltas) < cfg.Iterations; draw++ {
		resampled := make([]pairedRound, len(rounds))
		for i := range resampled {
			resampled[i] = rounds[rng.IntN(len(rounds))]
		}
		a, b, scored := scorePair(idA, idB, flattenRounds(resampled), categories, probes, weights, mode)
		if scored {
			deltas = append(deltas, a-b)
		}
	}
	cmp.BootstrapSamples = len(deltas)
	if len(deltas) < cfg.MinRounds {
		cmp.Level = model.SigInconclusive
		cmp.Summary = fmt.Sprintf("The paired bootstrap for %s and %s did not produce enough complete aggregate scores.", idA, idB)
		return cmp
	}
	sort.Float64s(deltas)
	cmp.CI95LowMs = scorePercentile(deltas, 0.025)
	cmp.CI95HighMs = scorePercentile(deltas, 0.975)
	cmp.PValue = bootstrapPValue(deltas)

	mdr := cfg.MinRelevantMs
	if mdr <= 0 {
		mdr = 3
	}
	if pct := cfg.MinRelevantPct / 100 * math.Min(actualA, actualB); pct > mdr {
		mdr = pct
	}
	switch {
	case math.Abs(cmp.DeltaScoreMs) < mdr:
		cmp.Level = model.SigNegligible
	case cmp.CI95LowMs > 0 || cmp.CI95HighMs < 0:
		cmp.Level = model.SigSignificant
	case cmp.PValue < 0.20:
		cmp.Level = model.SigLikely
	default:
		cmp.Level = model.SigInconclusive
	}
	cmp.Summary = scoreCompareSummary(idA, idB, cmp)
	return cmp
}

func pairSamples(samples []model.Sample, idA, idB string) []model.Sample {
	out := make([]model.Sample, 0, len(samples))
	for _, sample := range samples {
		if !sample.Warmup && (sample.ServerID == idA || sample.ServerID == idB) {
			out = append(out, sample)
		}
	}
	return out
}

type pairedRound struct {
	a []model.Sample
	b []model.Sample
}

func pairedRounds(samples []model.Sample, idA, idB string, categories []model.Category) []pairedRound {
	type sides struct {
		a []model.Sample
		b []model.Sample
	}
	byRound := make(map[int]*sides)
	for _, sample := range samples {
		if sample.Warmup || sample.Round <= 0 || (sample.ServerID != idA && sample.ServerID != idB) {
			continue
		}
		side := byRound[sample.Round]
		if side == nil {
			side = &sides{}
			byRound[sample.Round] = side
		}
		if sample.ServerID == idA {
			side.a = append(side.a, sample)
		} else {
			side.b = append(side.b, sample)
		}
	}
	keys := make([]int, 0, len(byRound))
	for round, side := range byRound {
		if hasCategories(side.a, categories) && hasCategories(side.b, categories) {
			keys = append(keys, round)
		}
	}
	sort.Ints(keys)
	out := make([]pairedRound, 0, len(keys))
	for _, round := range keys {
		out = append(out, pairedRound{a: byRound[round].a, b: byRound[round].b})
	}
	return out
}

func hasCategories(samples []model.Sample, categories []model.Category) bool {
	found := make(map[model.Category]bool, len(categories))
	for _, sample := range samples {
		found[sample.Category] = true
	}
	for _, category := range categories {
		if !found[category] {
			return false
		}
	}
	return len(categories) > 0
}

func flattenRounds(rounds []pairedRound) []model.Sample {
	count := 0
	for _, round := range rounds {
		count += len(round.a) + len(round.b)
	}
	out := make([]model.Sample, 0, count)
	at := time.Unix(0, 0)
	seq := time.Duration(0)
	for roundIndex, round := range rounds {
		for _, side := range [][]model.Sample{round.a, round.b} {
			for _, sample := range side {
				sample.Round = roundIndex + 1
				sample.At = at.Add(seq)
				seq++
				out = append(out, sample)
			}
		}
	}
	return out
}

func scorePair(
	idA, idB string,
	samples []model.Sample,
	categories []model.Category,
	probes map[string]*model.ProbeResult,
	weights model.Weights,
	mode model.RankMode,
) (float64, float64, bool) {
	grouped := map[string]map[model.Category][]model.Sample{
		idA: {},
		idB: {},
	}
	for _, sample := range samples {
		if _, ok := grouped[sample.ServerID]; ok {
			grouped[sample.ServerID][sample.Category] = append(grouped[sample.ServerID][sample.Category], sample)
		}
	}
	serverStats := make(map[string]*model.ServerStats, 2)
	for _, id := range []string{idA, idB} {
		st := &model.ServerStats{
			ServerID:    id,
			State:       model.StateActive,
			PerCategory: make(map[model.Category]*model.Distribution, len(categories)),
		}
		for _, category := range categories {
			st.PerCategory[category] = stats.Compute(grouped[id][category])
		}
		serverStats[id] = st
	}
	scores := ScoreServers(serverStats, probes, categories, weights, mode)
	var scoreA, scoreB float64
	var foundA, foundB bool
	for _, score := range scores {
		switch score.ServerID {
		case idA:
			scoreA, foundA = score.TotalMs, true
		case idB:
			scoreB, foundB = score.TotalMs, true
		}
	}
	return scoreA, scoreB, foundA && foundB
}

func comparisonRNG(seed int64, idA, idB string, mode model.RankMode) *rand.Rand {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(idA))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(idB))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(mode))
	mixed := uint64(seed) ^ hash.Sum64()
	return rand.New(rand.NewPCG(mixed, mixed^0x9e3779b97f4a7c15))
}

func scorePercentile(sorted []float64, p float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	position := p * float64(len(sorted)-1)
	low := int(math.Floor(position))
	high := int(math.Ceil(position))
	if low == high {
		return sorted[low]
	}
	return sorted[low] + (position-float64(low))*(sorted[high]-sorted[low])
}

func bootstrapPValue(sortedDeltas []float64) float64 {
	nonPositive, nonNegative := 0, 0
	for _, delta := range sortedDeltas {
		if delta <= 0 {
			nonPositive++
		}
		if delta >= 0 {
			nonNegative++
		}
	}
	n := float64(len(sortedDeltas) + 1)
	p := 2 * math.Min(float64(nonPositive+1)/n, float64(nonNegative+1)/n)
	return math.Min(p, 1)
}

func scoreCompareSummary(idA, idB string, cmp model.Comparison) string {
	difference := math.Abs(cmp.DeltaScoreMs)
	faster, slower := idA, idB
	if cmp.DeltaScoreMs > 0 {
		faster, slower = idB, idA
	}
	switch cmp.Level {
	case model.SigNegligible:
		return fmt.Sprintf("%s and %s differed by only %.1f ms in aggregate score; the difference is practically irrelevant.", idA, idB, difference)
	case model.SigSignificant:
		return fmt.Sprintf("%s had a lower aggregate score than %s by %.1f ms; the paired bootstrap difference is statistically significant.", faster, slower, difference)
	case model.SigLikely:
		return fmt.Sprintf("%s had a lower aggregate score than %s by %.1f ms; the paired bootstrap suggests a likely difference.", faster, slower, difference)
	default:
		return fmt.Sprintf("%s had a lower aggregate score than %s by %.1f ms, but the paired bootstrap was inconclusive.", faster, slower, difference)
	}
}

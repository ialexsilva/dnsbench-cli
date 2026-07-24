package stats

import (
	"fmt"
	"math"

	"dnsbench/internal/model"
)

const minCompareSamples = 5

type CompareConfig struct {
	MinRelevantMs  float64
	MinRelevantPct float64
}

func DefaultCompareConfig() CompareConfig {
	return CompareConfig{MinRelevantMs: 3, MinRelevantPct: 10}
}

func Compare(idA, idB string, cat model.Category, a, b []float64, cfg CompareConfig) model.Comparison {
	cmp := model.Comparison{ServerA: idA, ServerB: idB, Category: cat, PValue: 1}
	meanA := mean(a)
	meanB := mean(b)
	cmp.DeltaMeanMs = meanA - meanB
	if len(a) < minCompareSamples || len(b) < minCompareSamples {
		cmp.Level = model.SigInconclusive
		cmp.Summary = fmt.Sprintf(
			"Not enough samples to compare %s and %s for %s queries (%d vs %d answered samples; at least %d per server are required).",
			idA, idB, cat.Label(), len(a), len(b), minCompareSamples)
		return cmp
	}
	varA := sampleVariance(a, meanA)
	varB := sampleVariance(b, meanB)
	seSquared := varA/float64(len(a)) + varB/float64(len(b))
	if seSquared > 0 {
		t := cmp.DeltaMeanMs / math.Sqrt(seSquared)
		cmp.PValue = twoTailedPValue(t, welchDF(varA, varB, len(a), len(b)))
	} else if cmp.DeltaMeanMs != 0 {
		cmp.PValue = 0
	}
	mdr := cfg.MinRelevantMs
	if pctFloor := cfg.MinRelevantPct / 100 * math.Min(meanA, meanB); pctFloor > mdr {
		mdr = pctFloor
	}
	switch {
	case math.Abs(cmp.DeltaMeanMs) < mdr:
		cmp.Level = model.SigNegligible
	case cmp.PValue < 0.05:
		cmp.Level = model.SigSignificant
	case cmp.PValue < 0.20:
		cmp.Level = model.SigLikely
	default:
		cmp.Level = model.SigInconclusive
	}
	cmp.Summary = compareSummary(idA, idB, cat, cmp)
	return cmp
}

func welchDF(varA, varB float64, nA, nB int) float64 {
	fa := varA / float64(nA)
	fb := varB / float64(nB)
	denom := fa*fa/float64(nA-1) + fb*fb/float64(nB-1)
	if denom == 0 {
		return float64(nA + nB - 2)
	}
	return (fa + fb) * (fa + fb) / denom
}

func compareSummary(idA, idB string, cat model.Category, cmp model.Comparison) string {
	absDelta := math.Abs(cmp.DeltaMeanMs)
	faster, slower := idA, idB
	if cmp.DeltaMeanMs > 0 {
		faster, slower = idB, idA
	}
	switch cmp.Level {
	case model.SigNegligible:
		if cmp.DeltaMeanMs == 0 {
			return fmt.Sprintf("%s and %s showed practically identical average latency for %s queries.",
				idA, idB, cat.Label())
		}
		return fmt.Sprintf("%s and %s differed by only %.1f ms on average for %s queries; the difference is practically irrelevant.",
			idA, idB, absDelta, cat.Label())
	case model.SigSignificant:
		return fmt.Sprintf("%s showed lower average latency than %s by %.1f ms for %s queries; the difference is statistically significant.",
			faster, slower, absDelta, cat.Label())
	case model.SigLikely:
		return fmt.Sprintf("%s showed lower average latency than %s by %.1f ms for %s queries; the difference is likely real but not conclusive.",
			faster, slower, absDelta, cat.Label())
	}
	return fmt.Sprintf("%s showed lower average latency than %s by %.1f ms for %s queries, but the difference was not statistically significant.",
		faster, slower, absDelta, cat.Label())
}

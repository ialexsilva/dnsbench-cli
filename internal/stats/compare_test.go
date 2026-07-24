package stats

import (
	"math"
	"strings"
	"testing"

	"dnsbench/internal/model"
)

func TestDefaultCompareConfig(t *testing.T) {
	cfg := DefaultCompareConfig()
	if cfg.MinRelevantMs != 3 || cfg.MinRelevantPct != 10 {
		t.Errorf("DefaultCompareConfig() = %+v, want MinRelevantMs 3 and MinRelevantPct 10", cfg)
	}
}

func TestCompareDistinctSetsSignificant(t *testing.T) {
	a := make([]float64, 30)
	b := make([]float64, 30)
	for i := range a {
		spread := float64(i%5) * 0.5
		a[i] = 10 + spread
		b[i] = 30 + spread
	}
	cmp := Compare("fastdns", "slowdns", model.CatCached, a, b, DefaultCompareConfig())
	if cmp.Level != model.SigSignificant {
		t.Errorf("Level = %s, want %s", cmp.Level, model.SigSignificant)
	}
	if cmp.PValue >= 0.05 {
		t.Errorf("PValue = %v, want below 0.05", cmp.PValue)
	}
	if math.Abs(cmp.DeltaMeanMs+20) > 1e-9 {
		t.Errorf("DeltaMeanMs = %v, want -20", cmp.DeltaMeanMs)
	}
	if !strings.Contains(cmp.Summary, "fastdns") || !strings.Contains(cmp.Summary, "slowdns") {
		t.Errorf("Summary %q must name both servers", cmp.Summary)
	}
	if !strings.Contains(cmp.Summary, "20.0 ms") {
		t.Errorf("Summary %q must contain the delta with one decimal", cmp.Summary)
	}
}

func TestCompareIdenticalSetsNegligible(t *testing.T) {
	values := []float64{10, 11, 12, 13, 14, 15, 16, 17, 18, 19}
	a := append([]float64(nil), values...)
	b := append([]float64(nil), values...)
	cmp := Compare("alpha", "beta", model.CatCached, a, b, DefaultCompareConfig())
	if cmp.Level != model.SigNegligible {
		t.Errorf("Level = %s, want %s", cmp.Level, model.SigNegligible)
	}
	if cmp.DeltaMeanMs != 0 {
		t.Errorf("DeltaMeanMs = %v, want 0", cmp.DeltaMeanMs)
	}
	if cmp.PValue != 1 {
		t.Errorf("PValue = %v, want 1", cmp.PValue)
	}
}

func TestCompareTooFewSamplesInconclusive(t *testing.T) {
	a := []float64{10, 11, 12}
	b := []float64{50, 51, 52}
	cmp := Compare("alpha", "beta", model.CatUncached, a, b, DefaultCompareConfig())
	if cmp.Level != model.SigInconclusive {
		t.Errorf("Level = %s, want %s", cmp.Level, model.SigInconclusive)
	}
	if !strings.Contains(cmp.Summary, "Not enough samples") {
		t.Errorf("Summary %q must explain insufficient samples", cmp.Summary)
	}
	if !strings.Contains(cmp.Summary, "alpha") || !strings.Contains(cmp.Summary, "beta") {
		t.Errorf("Summary %q must name both servers", cmp.Summary)
	}
}

func TestCompareSmallDeltaOnLargeMeansNegligible(t *testing.T) {
	a := make([]float64, 10)
	b := make([]float64, 10)
	for i := range a {
		a[i] = 100 + float64(i)*0.1
		b[i] = 101 + float64(i)*0.1
	}
	cmp := Compare("alpha", "beta", model.CatTLD, a, b, DefaultCompareConfig())
	if cmp.Level != model.SigNegligible {
		t.Errorf("Level = %s, want %s", cmp.Level, model.SigNegligible)
	}
	if math.Abs(cmp.DeltaMeanMs+1) > 1e-9 {
		t.Errorf("DeltaMeanMs = %v, want -1", cmp.DeltaMeanMs)
	}
}

package stats

import (
	"math"
	"testing"
)

func TestStudentTQuantile(t *testing.T) {
	cases := []struct {
		df   float64
		want float64
	}{
		{4, 2.776},
		{9, 2.262},
		{29, 2.045},
		{1000, 1.962},
	}
	for _, c := range cases {
		got := studentTQuantile(0.975, c.df)
		if math.Abs(got-c.want) > 0.01 {
			t.Errorf("studentTQuantile(0.975, %v) = %v, want %v within 0.01", c.df, got, c.want)
		}
	}
}

func TestTwoTailedPValue(t *testing.T) {
	cases := []struct {
		t    float64
		df   float64
		want float64
	}{
		{2.776, 4, 0.050},
		{2.086, 20, 0.050},
		{1.0, 10, 0.341},
	}
	for _, c := range cases {
		got := twoTailedPValue(c.t, c.df)
		if math.Abs(got-c.want) > 0.005 {
			t.Errorf("twoTailedPValue(%v, %v) = %v, want %v within 0.005", c.t, c.df, got, c.want)
		}
	}
}

func TestStudentTQuantileSymmetry(t *testing.T) {
	upper := studentTQuantile(0.975, 10)
	lower := studentTQuantile(0.025, 10)
	if math.Abs(upper+lower) > 1e-9 {
		t.Errorf("quantiles not symmetric: %v and %v", upper, lower)
	}
	if got := studentTQuantile(0.5, 10); math.Abs(got) > 1e-9 {
		t.Errorf("studentTQuantile(0.5, 10) = %v, want 0", got)
	}
}

func TestStudentTCDFAtZero(t *testing.T) {
	if got := studentTCDF(0, 7); math.Abs(got-0.5) > 1e-12 {
		t.Errorf("studentTCDF(0, 7) = %v, want 0.5", got)
	}
}

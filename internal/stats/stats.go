package stats

import (
	"math"
	"sort"
	"time"

	"github.com/miekg/dns"

	"dnsbench/internal/model"
)

func Compute(samples []model.Sample) *model.Distribution {
	d := &model.Distribution{}
	answered := make([]model.Sample, 0, len(samples))
	for _, s := range samples {
		if s.Warmup {
			continue
		}
		d.Count++
		r := s.Result
		attempts := s.Attempts
		if attempts < 1 {
			attempts = 1
		}
		d.Attempts += attempts
		failedAttempts := s.FailedAttempts
		if s.Attempts == 0 && !r.Answered() {
			failedAttempts = 1
		}
		d.AttemptFailures += failedAttempts
		d.TimeoutAttempts += s.TimeoutCount
		if s.Attempts == 0 && s.TimeoutCount == 0 && r.Err.IsTimeout() {
			d.TimeoutAttempts++
		}
		if attempts > 1 {
			d.Retried++
		}
		if !r.Answered() {
			if r.Err.IsTimeout() {
				d.Timeouts++
			} else {
				d.Errors++
			}
			continue
		}
		d.Answered++
		switch r.Rcode {
		case dns.RcodeServerFailure:
			d.Servfails++
		}
		if r.ValidFor(s.Category) {
			d.Valid++
			answered = append(answered, s)
		} else if r.Rcode != dns.RcodeServerFailure {
			d.Invalid++
		}
		if r.Truncated {
			d.TruncatedN++
		}
	}
	if d.Count > 0 {
		total := float64(d.Count)
		d.LossPct = float64(d.Count-d.Answered) / total * 100
		d.RetryPct = float64(d.Retried) / total * 100
		d.ServfailPct = float64(d.Servfails) / total * 100
		d.InvalidPct = float64(d.Invalid) / total * 100
		d.TruncatedPct = float64(d.TruncatedN) / total * 100
	}
	if d.Attempts > 0 {
		d.AttemptFailurePct = float64(d.AttemptFailures) / float64(d.Attempts) * 100
	}
	sort.SliceStable(answered, func(i, j int) bool {
		return answered[i].At.Before(answered[j].At)
	})
	chrono := make([]float64, len(answered))
	for i, s := range answered {
		chrono[i] = durationMs(sampleElapsed(s))
	}
	d.SamplesMs = chrono
	n := len(chrono)
	if n == 0 {
		return d
	}
	sorted := append([]float64(nil), chrono...)
	sort.Float64s(sorted)
	d.MinMs = sorted[0]
	d.MaxMs = sorted[n-1]
	d.MeanMs = mean(chrono)
	d.MedianMs = percentile(sorted, 0.50)
	d.P50Ms = d.MedianMs
	d.P90Ms = percentile(sorted, 0.90)
	d.P95Ms = percentile(sorted, 0.95)
	d.P99Ms = percentile(sorted, 0.99)
	if n < 2 {
		d.CI95LowMs = d.MeanMs
		d.CI95HighMs = d.MeanMs
		return d
	}
	d.VarianceMs2 = sampleVariance(chrono, d.MeanMs)
	d.StdDevMs = math.Sqrt(d.VarianceMs2)
	var absDiffSum float64
	for i := 1; i < n; i++ {
		absDiffSum += math.Abs(chrono[i] - chrono[i-1])
	}
	d.JitterMs = absDiffSum / float64(n-1)
	tCrit := studentTQuantile(0.975, float64(n-1))
	margin := tCrit * d.StdDevMs / math.Sqrt(float64(n))
	d.CI95LowMs = d.MeanMs - margin
	d.CI95HighMs = d.MeanMs + margin
	return d
}

func sampleElapsed(sample model.Sample) time.Duration {
	if sample.Elapsed > 0 {
		return sample.Elapsed
	}
	return sample.Result.RTT
}

func ComputePhases(samples []model.Sample) *model.PhaseAverages {
	pa := &model.PhaseAverages{}
	var connectSum, tlsSum, httpSum, querySum float64
	var connectN, tlsN, httpN, queryN int
	var coldSum, steadySum float64
	var coldN, steadyN int
	for _, s := range samples {
		if !s.Result.ValidFor(s.Category) {
			continue
		}
		ph := s.Result.Phases
		hasConnectionSetup := ph.Connect > 0 || ph.TLSHandshake > 0 || ph.HTTPSetup > 0
		if !s.Result.Reused && hasConnectionSetup {
			coldSum += durationMs(sampleElapsed(s))
			coldN++
		} else if !s.Warmup {
			steadySum += durationMs(sampleElapsed(s))
			steadyN++
		}
		if s.Warmup {
			continue
		}
		if s.Result.Reused {
			pa.ReusedCount++
		} else {
			pa.ColdCount++
		}
		if ph.Connect > 0 {
			connectSum += durationMs(ph.Connect)
			connectN++
		}
		if ph.TLSHandshake > 0 {
			tlsSum += durationMs(ph.TLSHandshake)
			tlsN++
		}
		if ph.HTTPSetup > 0 {
			httpSum += durationMs(ph.HTTPSetup)
			httpN++
		}
		if ph.Query > 0 {
			querySum += durationMs(ph.Query)
			queryN++
		}
	}
	if connectN > 0 {
		pa.ConnectMs = connectSum / float64(connectN)
	}
	if tlsN > 0 {
		pa.TLSMs = tlsSum / float64(tlsN)
	}
	if httpN > 0 {
		pa.HTTPMs = httpSum / float64(httpN)
	}
	if queryN > 0 {
		pa.QueryMs = querySum / float64(queryN)
	}
	if coldN > 0 {
		pa.ColdStartMs = coldSum / float64(coldN)
	}
	if steadyN > 0 {
		pa.SteadyStateMs = steadySum / float64(steadyN)
	}
	return pa
}

func durationMs(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func sampleVariance(values []float64, m float64) float64 {
	n := len(values)
	if n < 2 {
		return 0
	}
	var sum float64
	for _, v := range values {
		diff := v - m
		sum += diff * diff
	}
	return sum / float64(n-1)
}

func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	pos := p * float64(n-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo] + frac*(sorted[hi]-sorted[lo])
}

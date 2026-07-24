package stats

import (
	"math"
	"testing"
	"time"

	"github.com/miekg/dns"

	"dnsbench/internal/model"
)

func sampleAt(step int, rttMs float64) model.Sample {
	base := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	return model.Sample{
		ServerID: "srv",
		Category: model.CatCached,
		At:       base.Add(time.Duration(step) * time.Second),
		Result: model.QueryResult{
			RTT:     time.Duration(rttMs * float64(time.Millisecond)),
			Answers: []model.RR{{Type: "A", Data: "192.0.2.1"}},
		},
	}
}

func assertClose(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v within %v", name, got, want, tol)
	}
}

func TestComputeReferenceSet(t *testing.T) {
	values := []float64{10, 12, 14, 16, 18}
	samples := make([]model.Sample, 0, len(values))
	for i, v := range values {
		samples = append(samples, sampleAt(i, v))
	}
	d := Compute(samples)
	if d.Count != 5 || d.Answered != 5 || d.Valid != 5 {
		t.Fatalf("Count = %d, Answered = %d, Valid = %d, want 5, 5 and 5", d.Count, d.Answered, d.Valid)
	}
	assertClose(t, "MeanMs", d.MeanMs, 14, 1e-9)
	assertClose(t, "MedianMs", d.MedianMs, 14, 1e-9)
	assertClose(t, "P50Ms", d.P50Ms, 14, 1e-9)
	assertClose(t, "StdDevMs", d.StdDevMs, 3.1623, 1e-4)
	assertClose(t, "VarianceMs2", d.VarianceMs2, 10, 1e-9)
	assertClose(t, "P95Ms", d.P95Ms, 17.6, 1e-9)
	assertClose(t, "JitterMs", d.JitterMs, 2, 1e-9)
	assertClose(t, "MinMs", d.MinMs, 10, 1e-9)
	assertClose(t, "MaxMs", d.MaxMs, 18, 1e-9)
	assertClose(t, "CI95LowMs", d.CI95LowMs, 14-3.925, 0.01)
	assertClose(t, "CI95HighMs", d.CI95HighMs, 14+3.925, 0.01)
	assertClose(t, "LossPct", d.LossPct, 0, 1e-9)
}

func TestComputeChronologicalOrder(t *testing.T) {
	first := sampleAt(0, 10)
	second := sampleAt(1, 18)
	third := sampleAt(2, 12)
	d := Compute([]model.Sample{third, first, second})
	want := []float64{10, 18, 12}
	if len(d.SamplesMs) != len(want) {
		t.Fatalf("SamplesMs length = %d, want %d", len(d.SamplesMs), len(want))
	}
	for i, w := range want {
		assertClose(t, "SamplesMs", d.SamplesMs[i], w, 1e-9)
	}
	assertClose(t, "JitterMs", d.JitterMs, 7, 1e-9)
}

func TestComputeSingleSample(t *testing.T) {
	d := Compute([]model.Sample{sampleAt(0, 42)})
	assertClose(t, "MeanMs", d.MeanMs, 42, 1e-9)
	assertClose(t, "MedianMs", d.MedianMs, 42, 1e-9)
	assertClose(t, "P50Ms", d.P50Ms, 42, 1e-9)
	assertClose(t, "P90Ms", d.P90Ms, 42, 1e-9)
	assertClose(t, "P95Ms", d.P95Ms, 42, 1e-9)
	assertClose(t, "P99Ms", d.P99Ms, 42, 1e-9)
	assertClose(t, "StdDevMs", d.StdDevMs, 0, 1e-9)
	assertClose(t, "VarianceMs2", d.VarianceMs2, 0, 1e-9)
	assertClose(t, "JitterMs", d.JitterMs, 0, 1e-9)
	assertClose(t, "CI95LowMs", d.CI95LowMs, 42, 1e-9)
	assertClose(t, "CI95HighMs", d.CI95HighMs, 42, 1e-9)
}

func TestComputeTwoSamples(t *testing.T) {
	d := Compute([]model.Sample{sampleAt(0, 10), sampleAt(1, 20)})
	assertClose(t, "MedianMs", d.MedianMs, 15, 1e-9)
	assertClose(t, "P50Ms", d.P50Ms, 15, 1e-9)
	assertClose(t, "P90Ms", d.P90Ms, 19, 1e-9)
	assertClose(t, "P95Ms", d.P95Ms, 19.5, 1e-9)
	assertClose(t, "P99Ms", d.P99Ms, 19.9, 1e-9)
	assertClose(t, "MinMs", d.MinMs, 10, 1e-9)
	assertClose(t, "MaxMs", d.MaxMs, 20, 1e-9)
}

func TestComputeErrorCounts(t *testing.T) {
	var samples []model.Sample
	for i := 0; i < 5; i++ {
		samples = append(samples, sampleAt(i, 10))
	}
	servfail := sampleAt(5, 20)
	servfail.Result.Rcode = 2
	refused := sampleAt(6, 20)
	refused.Result.Rcode = 5
	truncated := sampleAt(7, 20)
	truncated.Result.Truncated = true
	timedOut := sampleAt(8, 0)
	timedOut.Result.Err = &model.QueryError{Kind: model.ErrTimeout, Msg: "deadline exceeded"}
	netFail := sampleAt(9, 0)
	netFail.Result.Err = &model.QueryError{Kind: model.ErrNetwork, Msg: "connection refused"}
	samples = append(samples, servfail, refused, truncated, timedOut, netFail)
	d := Compute(samples)
	if d.Count != 10 {
		t.Errorf("Count = %d, want 10", d.Count)
	}
	if d.Answered != 8 {
		t.Errorf("Answered = %d, want 8", d.Answered)
	}
	if d.Timeouts != 1 {
		t.Errorf("Timeouts = %d, want 1", d.Timeouts)
	}
	if d.Errors != 1 {
		t.Errorf("Errors = %d, want 1", d.Errors)
	}
	if d.Servfails != 1 {
		t.Errorf("Servfails = %d, want 1", d.Servfails)
	}
	if d.Invalid != 1 {
		t.Errorf("Invalid = %d, want 1", d.Invalid)
	}
	if d.TruncatedN != 1 {
		t.Errorf("TruncatedN = %d, want 1", d.TruncatedN)
	}
	assertClose(t, "LossPct", d.LossPct, 20, 1e-9)
	assertClose(t, "ServfailPct", d.ServfailPct, 10, 1e-9)
	assertClose(t, "InvalidPct", d.InvalidPct, 10, 1e-9)
	assertClose(t, "TruncatedPct", d.TruncatedPct, 10, 1e-9)
	if d.Valid != 6 {
		t.Errorf("Valid = %d, want 6", d.Valid)
	}
	if len(d.SamplesMs) != 6 {
		t.Errorf("SamplesMs length = %d, want 6", len(d.SamplesMs))
	}
}

func TestComputeRequiresCategorySpecificDNSValidity(t *testing.T) {
	cachedOK := sampleAt(0, 10)
	cachedEmpty := sampleAt(1, 1)
	cachedEmpty.Result.Answers = nil
	cachedCNAME := sampleAt(2, 12)
	cachedCNAME.Result.Answers = []model.RR{{Type: "CNAME", Data: "target.example."}}

	cached := Compute([]model.Sample{cachedOK, cachedEmpty, cachedCNAME})
	if cached.Answered != 3 || cached.Valid != 2 || cached.Invalid != 1 {
		t.Fatalf("cached counts = answered %d, valid %d, invalid %d; want 3, 2, 1",
			cached.Answered, cached.Valid, cached.Invalid)
	}
	if len(cached.SamplesMs) != 2 {
		t.Fatalf("cached latency samples = %d, want 2", len(cached.SamplesMs))
	}

	tldNX := sampleAt(3, 15)
	tldNX.Category = model.CatTLD
	tldNX.Result.Rcode = dns.RcodeNameError
	tldNX.Result.Answers = nil
	tldNoError := sampleAt(4, 2)
	tldNoError.Category = model.CatTLD
	tld := Compute([]model.Sample{tldNX, tldNoError})
	if tld.Answered != 2 || tld.Valid != 1 || tld.Invalid != 1 {
		t.Fatalf("TLD counts = answered %d, valid %d, invalid %d; want 2, 1, 1",
			tld.Answered, tld.Valid, tld.Invalid)
	}
	assertClose(t, "TLD MedianMs", tld.MedianMs, 15, 1e-9)
}

func TestComputeUsesEndToEndElapsedAndTracksRetries(t *testing.T) {
	sample := sampleAt(0, 5)
	sample.Attempts = 2
	sample.FailedAttempts = 1
	sample.TimeoutCount = 1
	sample.Elapsed = 35 * time.Millisecond
	d := Compute([]model.Sample{sample})
	if d.Attempts != 2 || d.AttemptFailures != 1 || d.TimeoutAttempts != 1 || d.Retried != 1 {
		t.Fatalf("retry counts = attempts %d, failures %d, timeout attempts %d, retried %d; want 2, 1, 1, 1",
			d.Attempts, d.AttemptFailures, d.TimeoutAttempts, d.Retried)
	}
	assertClose(t, "RetryPct", d.RetryPct, 100, 1e-9)
	assertClose(t, "AttemptFailurePct", d.AttemptFailurePct, 50, 1e-9)
	assertClose(t, "MedianMs", d.MedianMs, 35, 1e-9)
}

func TestComputeSkipsWarmup(t *testing.T) {
	warm := sampleAt(0, 900)
	warm.Warmup = true
	d := Compute([]model.Sample{warm, sampleAt(1, 10), sampleAt(2, 12)})
	if d.Count != 2 {
		t.Errorf("Count = %d, want 2", d.Count)
	}
	assertClose(t, "MeanMs", d.MeanMs, 11, 1e-9)
	assertClose(t, "MaxMs", d.MaxMs, 12, 1e-9)
}

func TestComputeEmpty(t *testing.T) {
	d := Compute(nil)
	if d.Count != 0 || d.Answered != 0 {
		t.Errorf("Count = %d, Answered = %d, want 0 and 0", d.Count, d.Answered)
	}
	assertClose(t, "LossPct", d.LossPct, 0, 1e-9)
	assertClose(t, "MeanMs", d.MeanMs, 0, 1e-9)
}

func phaseSample(step int, reused bool, connectMs, tlsMs, httpMs, queryMs float64) model.Sample {
	s := sampleAt(step, connectMs+tlsMs+httpMs+queryMs)
	s.Result.Reused = reused
	s.Result.Phases = model.QueryPhases{
		Connect:      time.Duration(connectMs * float64(time.Millisecond)),
		TLSHandshake: time.Duration(tlsMs * float64(time.Millisecond)),
		HTTPSetup:    time.Duration(httpMs * float64(time.Millisecond)),
		Query:        time.Duration(queryMs * float64(time.Millisecond)),
	}
	return s
}

func TestComputePhases(t *testing.T) {
	samples := []model.Sample{
		phaseSample(0, false, 4, 8, 0, 2),
		phaseSample(1, false, 6, 12, 0, 2),
		phaseSample(2, true, 0, 0, 0, 1),
		phaseSample(3, true, 0, 0, 0, 1),
		phaseSample(4, true, 0, 0, 0, 1),
	}
	warm := phaseSample(5, false, 100, 100, 100, 100)
	warm.Warmup = true
	failed := phaseSample(6, false, 50, 50, 50, 50)
	failed.Result.Err = &model.QueryError{Kind: model.ErrNetwork, Msg: "connection reset"}
	samples = append(samples, warm, failed)
	pa := ComputePhases(samples)
	if pa.ColdCount != 2 {
		t.Errorf("ColdCount = %d, want 2", pa.ColdCount)
	}
	if pa.ReusedCount != 3 {
		t.Errorf("ReusedCount = %d, want 3", pa.ReusedCount)
	}
	assertClose(t, "ConnectMs", pa.ConnectMs, 5, 1e-9)
	assertClose(t, "TLSMs", pa.TLSMs, 10, 1e-9)
	assertClose(t, "HTTPMs", pa.HTTPMs, 0, 1e-9)
	assertClose(t, "QueryMs", pa.QueryMs, 1.4, 1e-9)
	assertClose(t, "ColdStartMs", pa.ColdStartMs, 434.0/3.0, 1e-9)
	assertClose(t, "SteadyStateMs", pa.SteadyStateMs, 1, 1e-9)
}

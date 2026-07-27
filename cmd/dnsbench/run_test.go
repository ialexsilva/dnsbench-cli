package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dnsbench/internal/model"
)

func TestRunCommandModernDefaults(t *testing.T) {
	cmd := newRunCmd()
	if got := cmd.Flags().Lookup("session").DefValue; got != string(model.SessionPersistent) {
		t.Fatalf("--session default = %q, want %q", got, model.SessionPersistent)
	}
	if got := cmd.Flags().Lookup("ranking").DefValue; got != string(model.RankLatency) {
		t.Fatalf("--ranking default = %q, want %q", got, model.RankLatency)
	}
	if got := cmd.Flags().Lookup("triage-threshold").DefValue; got != "200ms" {
		t.Fatalf("--triage-threshold default = %q, want 200ms", got)
	}
	if got := cmd.Flags().Lookup("concurrency").Usage; !strings.Contains(got, "hard limit") || !strings.Contains(got, "does not control QPS") {
		t.Fatalf("--concurrency help does not separate in-flight queries from QPS: %q", got)
	}
	if got := cmd.Flags().Lookup("pace").Usage; !strings.Contains(got, "fixed minimum interval") || !strings.Contains(got, "when omitted") {
		t.Fatalf("--pace help does not distinguish fixed and adaptive behavior: %q", got)
	}
}

func TestBuildBenchConfigMakesExplicitPaceFixed(t *testing.T) {
	base := model.DefaultBenchConfig(model.ModeStandard)
	flags := runFlags{
		pace:         30 * time.Millisecond,
		paceExplicit: true,
	}
	cfg, _ := buildBenchConfig(&flags, model.ModeStandard, model.SessionPersistent)
	if cfg.PaceInterval != 30*time.Millisecond || cfg.PaceAdaptive {
		t.Fatalf("explicit pacing = %s adaptive=%t, want fixed 30ms", cfg.PaceInterval, cfg.PaceAdaptive)
	}

	flags.pace = base.PaceInterval
	flags.paceExplicit = false
	cfg, _ = buildBenchConfig(&flags, model.ModeStandard, model.SessionPersistent)
	if cfg.PaceInterval != base.PaceInterval || !cfg.PaceAdaptive {
		t.Fatalf("default pacing = %s adaptive=%t, want adaptive %s", cfg.PaceInterval, cfg.PaceAdaptive, base.PaceInterval)
	}
}

func TestProbeCommandHasNoPacingFlag(t *testing.T) {
	cmd := newProbeCmd()
	if got := cmd.Flags().Lookup("pace"); got != nil {
		t.Fatalf("probe unexpectedly exposes --pace: %+v", got)
	}
}

func TestMatchSystemServerIDsByEndpoint(t *testing.T) {
	selected := []model.Server{
		{ID: "builtin-quad9", Address: "9.9.9.9", Port: 53, Protocol: model.ProtoUDP, Source: model.SourceBuiltin},
		{ID: "builtin-cloudflare", Address: "1.1.1.1", Port: 53, Protocol: model.ProtoUDP, Source: model.SourceBuiltin},
		{ID: "quad9-dot", Address: "9.9.9.9", Port: 853, Protocol: model.ProtoDoT, Source: model.SourceBuiltin},
	}
	system := []model.Server{
		{ID: "system-en1-quad9", Address: "9.9.9.9", Port: 53, Protocol: model.ProtoUDP, Source: model.SourceSystem},
		{ID: "system-en1-local", Address: "fe80::1%en1", Port: 53, Protocol: model.ProtoUDP, Source: model.SourceSystem},
	}
	got := matchSystemServerIDs(selected, system)
	if len(got) != 1 || got[0] != "builtin-quad9" {
		t.Fatalf("matched system IDs = %v, want [builtin-quad9]", got)
	}
}

func TestPrintForwarderNotesClarifiesSuccessfulDetection(t *testing.T) {
	var out bytes.Buffer
	printForwarderNotes(&out, []model.Server{
		{Address: "fe80::1%en1", Interface: "en1"},
		{Address: "9.9.9.9", Interface: "en1"},
	})
	got := out.String()
	if !strings.Contains(got, "detected successfully") {
		t.Fatalf("forwarder note does not confirm successful detection:\n%s", got)
	}
	if !strings.Contains(got, "only the upstream behind this endpoint is unknown") {
		t.Fatalf("forwarder note does not scope the unknown upstream:\n%s", got)
	}
	if strings.Contains(got, "9.9.9.9") {
		t.Fatalf("public resolver unexpectedly received a local-forwarder note:\n%s", got)
	}
}

func TestLoadWeightsMergesPartialModeOverPreset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weights.json")
	if err := os.WriteFile(path, []byte(`{"latency":{"latency_metric":"mean"}}`), 0o600); err != nil {
		t.Fatalf("write weights: %v", err)
	}
	weights, err := loadWeights(path)
	if err != nil {
		t.Fatalf("loadWeights: %v", err)
	}
	got := weights[model.RankLatency]
	if got.LatencyMetric != "mean" {
		t.Fatalf("LatencyMetric = %q, want mean", got.LatencyMetric)
	}
	if got.Category[model.CatCached] == 0 ||
		got.PenaltyPerInvalidPctMs == 0 ||
		got.PenaltyPerRetryPctMs == 0 {
		t.Fatalf("partial custom mode discarded preset fields: %+v", got)
	}
}

func TestBuildComparisonsUsesSelectedAggregateScore(t *testing.T) {
	var samples []model.Sample
	for round := 1; round <= 8; round++ {
		for _, item := range []struct {
			id string
			ms time.Duration
		}{
			{id: "a", ms: 10 * time.Millisecond},
			{id: "b", ms: 20 * time.Millisecond},
		} {
			samples = append(samples, model.Sample{
				ServerID: item.id,
				Category: model.CatCached,
				Round:    round,
				QType:    "A",
				Attempts: 1,
				Elapsed:  item.ms,
				At:       time.Unix(int64(round), 0),
				Result: model.QueryResult{
					RTT:     item.ms,
					Answers: []model.RR{{Type: "A", Data: "192.0.2.1"}},
				},
			})
		}
	}
	weights := model.Weights{
		Category:      map[model.Category]float64{model.CatCached: 1},
		LatencyMetric: "median",
	}
	res := &model.RunResult{
		Config: model.BenchConfig{
			Categories: []model.Category{model.CatCached},
			Seed:       7,
		},
		SelectedRanking: model.RankLatency,
		Weights:         map[model.RankMode]model.Weights{model.RankLatency: weights},
		SystemIDs:       []string{"b"},
		Scores: map[model.RankMode][]model.Score{
			model.RankLatency: {
				{ServerID: "a", Mode: model.RankLatency, Rank: 1, TotalMs: 10},
				{ServerID: "b", Mode: model.RankLatency, Rank: 2, TotalMs: 20},
			},
		},
		Samples: samples,
	}
	comparisons := buildComparisons(res, model.RankLatency)
	if len(comparisons) != 1 {
		t.Fatalf("comparisons = %d, want 1", len(comparisons))
	}
	cmp := comparisons[0]
	if cmp.RankingMode != model.RankLatency || cmp.BootstrapSamples != 1000 {
		t.Fatalf("comparison did not use aggregate bootstrap: %+v", cmp)
	}
}

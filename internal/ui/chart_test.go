package ui

import (
	"strings"
	"testing"

	"dnsbench/internal/model"
)

func dist(median, mean, p95 float64, count, answered int) *model.Distribution {
	loss := 0.0
	if count > 0 {
		loss = float64(count-answered) / float64(count) * 100
	}
	return &model.Distribution{
		Count:    count,
		Answered: answered,
		Valid:    answered,
		LossPct:  loss,
		MinMs:    median / 2,
		MaxMs:    p95 * 2,
		MeanMs:   mean,
		MedianMs: median,
		P50Ms:    median,
		P90Ms:    p95 * 0.9,
		P95Ms:    p95,
		P99Ms:    p95 * 1.2,
		JitterMs: 1.5,
	}
}

func chartFixture() *model.RunResult {
	cfg := model.DefaultBenchConfig(model.ModeQuick)
	servers := []model.Server{
		{ID: "cf", Name: "Cloudflare", Address: "1.1.1.1", Protocol: model.ProtoUDP, Source: model.SourceBuiltin, Enabled: true},
		{ID: "goog", Name: "Google Public DNS", Address: "8.8.8.8", Protocol: model.ProtoUDP, Source: model.SourceSystem, Enabled: true},
		{ID: "quad9", Name: "Quad9 Secure", Address: "9.9.9.9", Protocol: model.ProtoDoT, TLSHostname: "dns.quad9.net", Source: model.SourceBuiltin, Enabled: true},
		{ID: "slow", Name: "Slow DNS", Address: "10.0.0.53", Protocol: model.ProtoUDP, Source: model.SourceUser, Enabled: true},
		{ID: "dead", Name: "Dead DNS", Address: "10.0.0.99", Protocol: model.ProtoUDP, Source: model.SourceUser, Enabled: true},
	}
	stats := map[string]*model.ServerStats{
		"cf": {ServerID: "cf", State: model.StateActive, PerCategory: map[model.Category]*model.Distribution{
			model.CatCached: dist(8, 8.5, 12, 100, 100),
			model.CatTLD:    dist(15, 15.5, 20, 100, 100),
		}},
		"goog": {ServerID: "goog", State: model.StateActive, PerCategory: map[model.Category]*model.Distribution{
			model.CatCached: dist(12, 12.5, 18, 100, 98),
			model.CatTLD:    dist(22, 22.5, 30, 100, 99),
		}},
		"quad9": {ServerID: "quad9", State: model.StateActive, PerCategory: map[model.Category]*model.Distribution{
			model.CatCached: dist(9, 9.5, 13, 100, 100),
			model.CatTLD:    dist(17, 17.5, 21, 100, 100),
		}},
		"slow": {ServerID: "slow", State: model.StateBenched, PerCategory: map[model.Category]*model.Distribution{}},
		"dead": {ServerID: "dead", State: model.StateOffline, PerCategory: map[model.Category]*model.Distribution{}},
	}
	latency := []model.Score{
		{ServerID: "cf", Mode: model.RankLatency, Rank: 1, BaseMs: 10.1, TotalMs: 10.1},
		{ServerID: "quad9", Mode: model.RankLatency, Rank: 2, BaseMs: 11.4, TotalMs: 11.4},
		{ServerID: "goog", Mode: model.RankLatency, Rank: 3, BaseMs: 15, Penalties: map[string]float64{"loss": 3.0}, TotalMs: 18},
	}
	reliability := []model.Score{
		{ServerID: "quad9", Mode: model.RankReliability, Rank: 1, BaseMs: 11.4, TotalMs: 11.4},
		{ServerID: "cf", Mode: model.RankReliability, Rank: 2, BaseMs: 10.1, TotalMs: 12.0},
		{ServerID: "goog", Mode: model.RankReliability, Rank: 3, BaseMs: 15, Penalties: map[string]float64{"loss": 9.0}, TotalMs: 24},
	}
	return &model.RunResult{
		Config: cfg,
		Weights: map[model.RankMode]model.Weights{
			model.RankLatency: {
				Category:            map[model.Category]float64{model.CatCached: 0.7, model.CatTLD: 0.3},
				LatencyMetric:       "median",
				PenaltyPerLossPctMs: 2,
				PenaltyNoDNSSECMs:   10,
			},
		},
		Servers: servers,
		SystemServers: []model.Server{
			{ID: "system-en0-router", Address: "192.168.1.1", Port: 53, Protocol: model.ProtoUDP, Source: model.SourceSystem, Interface: "en0"},
			{ID: "system-en0-google", Address: "8.8.8.8", Port: 53, Protocol: model.ProtoUDP, Source: model.SourceSystem, Interface: "en0"},
		},
		SystemIDs: []string{"goog"},
		Triage: map[string]*model.TriageResult{
			"slow": {ServerID: "slow", State: model.StateBenched, Reason: "median above triage threshold"},
			"dead": {ServerID: "dead", State: model.StateOffline, Reason: "no response to triage queries"},
		},
		Stats: stats,
		Scores: map[model.RankMode][]model.Score{
			model.RankLatency:     latency,
			model.RankReliability: reliability,
		},
		Comparisons: []model.Comparison{
			{ServerA: "cf", ServerB: "goog", Category: model.CatCached, DeltaMeanMs: -4, PValue: 0.001, Level: model.SigSignificant},
		},
	}
}

func TestRenderRankingList(t *testing.T) {
	disableColors(t)
	out := RenderRankingList(chartFixture(), model.RankLatency, "", "")
	for _, want := range []string{
		"Ranked by overall latency",
		"latency cost in ms, lower is better",
		"Cloudflare", "Quad9 Secure", "Google Public DNS",
		"10.1 ms", "18.0 ms",
		"● current DNS",
		"* cost includes penalties",
		"1 sidelined", "1 unreachable",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("ranking list missing %q\n%s", want, out)
		}
	}
	if !strings.Contains(out, "18.0 ms*") {
		t.Errorf("penalized resolver should carry the penalty marker\n%s", out)
	}
	if strings.Contains(out, "10.1 ms*") {
		t.Errorf("unpenalized resolver must not carry the penalty marker\n%s", out)
	}
	if strings.Contains(out, "← current DNS") {
		t.Errorf("ranking rows should no longer append the trailing current-DNS marker\n%s", out)
	}
	if !strings.Contains(out, "█") {
		t.Errorf("ranking list has no bars\n%s", out)
	}
}

func TestRenderRankingListColorsCurrentDNSName(t *testing.T) {
	SetColorEnabled(true)
	t.Cleanup(func() { SetColorEnabled(false) })
	out := RenderRankingList(chartFixture(), model.RankLatency, "", "")
	if !strings.Contains(out, "\x1b[36mGoogle Public DNS") {
		t.Errorf("current DNS name is not colored cyan\n%q", out)
	}
	if !strings.Contains(out, Cyan("● current DNS")) {
		t.Errorf("missing cyan current-DNS legend\n%q", out)
	}
}

func TestRenderRankingListWinnerFirst(t *testing.T) {
	disableColors(t)
	out := RenderRankingList(chartFixture(), model.RankLatency, "", "")
	cf := strings.Index(out, "Cloudflare")
	q9 := strings.Index(out, "Quad9 Secure")
	gg := strings.Index(out, "Google Public DNS")
	if !(cf < q9 && q9 < gg) {
		t.Errorf("ranking order wrong: cf=%d quad9=%d goog=%d\n%s", cf, q9, gg, out)
	}
}

func TestRenderRankingListTieMarker(t *testing.T) {
	disableColors(t)
	out := RenderRankingList(chartFixture(), model.RankLatency, "cf", "quad9")
	if !strings.Contains(out, "≈ tied") {
		t.Errorf("ranking list missing tie marker\n%s", out)
	}
}

func TestRenderRunSummary(t *testing.T) {
	disableColors(t)
	out := RenderRunSummary(chartFixture())
	for _, want := range []string{"dnsbench", "5 resolvers", "quick mode"} {
		if !strings.Contains(out, want) {
			t.Errorf("run summary missing %q\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"Fastest", "Most stable", "recommend"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("run summary contains editorial verdict %q\n%s", unwanted, out)
		}
	}
}

func TestRenderReportFooterPointsToHTML(t *testing.T) {
	disableColors(t)
	out := RenderReportFooter([]string{"/tmp/dnsbench-20260101.html", "/tmp/dnsbench-20260101.json"}, true)
	if !strings.Contains(out, "full report → /tmp/dnsbench-20260101.html") {
		t.Errorf("footer missing html path\n%s", out)
	}
	if !strings.Contains(out, "opening in your browser") {
		t.Errorf("footer missing open note\n%s", out)
	}
	if !strings.Contains(out, "/tmp/dnsbench-20260101.json") {
		t.Errorf("footer missing other export\n%s", out)
	}
}

func TestRenderReportFooterNoHTML(t *testing.T) {
	disableColors(t)
	out := RenderReportFooter(nil, false)
	if !strings.Contains(out, "--open") || !strings.Contains(out, "--details") {
		t.Errorf("footer missing hints\n%s", out)
	}
}

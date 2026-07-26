package report

import (
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"dnsbench/internal/model"
)

func dist(median, mean, loss, servfail float64, samples []float64) *model.Distribution {
	return &model.Distribution{
		Count:       100,
		Answered:    100 - int(loss),
		Valid:       100 - int(loss) - int(servfail),
		Timeouts:    int(loss),
		LossPct:     loss,
		MinMs:       median * 0.7,
		MaxMs:       median * 2.5,
		MeanMs:      mean,
		MedianMs:    median,
		StdDevMs:    1.2,
		VarianceMs2: 1.44,
		P50Ms:       median,
		P90Ms:       median * 1.4,
		P95Ms:       median * 1.6,
		P99Ms:       median * 2.1,
		CI95LowMs:   mean - 0.5,
		CI95HighMs:  mean + 0.5,
		JitterMs:    0.8,
		ServfailPct: servfail,
		SamplesMs:   samples,
	}
}

func testResult() *model.RunResult {
	started := time.Date(2026, 7, 22, 21, 30, 0, 0, time.UTC)
	servers := []model.Server{
		{ID: "system-1", Name: "Router", Address: "192.168.1.1", Protocol: model.ProtoUDP, Source: model.SourceSystem, SystemRole: "primary", Enabled: true},
		{ID: "steady", Name: "SteadyDNS", Operator: "Steady Inc", Address: "9.9.9.9", Protocol: model.ProtoUDP, Source: model.SourceBuiltin, Enabled: true},
		{ID: "speedy", Name: "SpeedyDNS", Operator: "Speedy Inc", Address: "1.2.3.4", Protocol: model.ProtoUDP, Source: model.SourceBuiltin, Enabled: true},
		{ID: "ghost", Name: "GhostDNS", Address: "203.0.113.9", Protocol: model.ProtoUDP, Source: model.SourceUser, Enabled: true},
	}
	stats := map[string]*model.ServerStats{
		"system-1": {
			ServerID: "system-1",
			State:    model.StateActive,
			PerCategory: map[model.Category]*model.Distribution{
				model.CatCached: dist(12.0, 12.5, 0.5, 0.0, nil),
				model.CatTLD:    dist(30.0, 31.0, 0.5, 0.0, nil),
			},
		},
		"steady": {
			ServerID: "steady",
			State:    model.StateActive,
			PerCategory: map[model.Category]*model.Distribution{
				model.CatCached: dist(8.0, 8.4, 0.0, 0.0, []float64{7.9, 8.0, 8.1}),
				model.CatTLD:    dist(20.0, 20.5, 0.0, 0.0, nil),
			},
		},
		"speedy": {
			ServerID: "speedy",
			State:    model.StateActive,
			PerCategory: map[model.Category]*model.Distribution{
				model.CatCached: dist(6.0, 6.9, 4.0, 2.5, nil),
				model.CatTLD:    dist(15.0, 15.8, 2.0, 0.0, nil),
			},
		},
	}
	probes := map[string]*model.ProbeResult{
		"system-1": {
			ServerID:       "system-1",
			Reachable:      true,
			NXInterception: model.VerdictNo,
			DNSSEC:         model.DNSSECInfo{Validating: model.VerdictNo},
		},
		"steady": {
			ServerID:       "steady",
			Reachable:      true,
			NXInterception: model.VerdictNo,
			DNSSEC:         model.DNSSECInfo{Validating: model.VerdictYes},
		},
		"speedy": {
			ServerID:       "speedy",
			Reachable:      true,
			NXInterception: model.VerdictYes,
			DNSSEC:         model.DNSSECInfo{Validating: model.VerdictYes},
			NXChecks: []model.NXCheck{
				{Label: "random subdomain", QName: "x.example.invalid", Behavior: model.NXInterceptedIP, Detail: "returned 203.0.113.80"},
			},
		},
	}
	triage := map[string]*model.TriageResult{
		"system-1": {ServerID: "system-1", Attempts: 10, Responses: 10, State: model.StateActive},
		"steady":   {ServerID: "steady", Attempts: 10, Responses: 10, State: model.StateActive},
		"speedy":   {ServerID: "speedy", Attempts: 10, Responses: 9, State: model.StateActive},
		"ghost":    {ServerID: "ghost", Attempts: 10, Responses: 0, State: model.StateOffline, Reason: "no response to any triage probe"},
	}
	scores := map[model.RankMode][]model.Score{
		model.RankLatency: {
			{ServerID: "speedy", Mode: model.RankLatency, Rank: 1, BaseMs: 7.0, TotalMs: 7.0},
			{ServerID: "steady", Mode: model.RankLatency, Rank: 2, BaseMs: 8.2, TotalMs: 8.2},
			{ServerID: "system-1", Mode: model.RankLatency, Rank: 3, BaseMs: 13.0, TotalMs: 13.0},
		},
		model.RankBrowsing: {
			{ServerID: "steady", Mode: model.RankBrowsing, Rank: 1, BaseMs: 8.2, TotalMs: 8.2},
			{ServerID: "speedy", Mode: model.RankBrowsing, Rank: 2, BaseMs: 7.0, TotalMs: 27.0, Penalties: map[string]float64{"loss": 20.0}},
			{ServerID: "system-1", Mode: model.RankBrowsing, Rank: 3, BaseMs: 13.0, TotalMs: 18.0, Penalties: map[string]float64{"no_dnssec": 5.0}},
		},
		model.RankReliability: {
			{ServerID: "steady", Mode: model.RankReliability, Rank: 1, BaseMs: 8.2, TotalMs: 8.2},
			{ServerID: "system-1", Mode: model.RankReliability, Rank: 2, BaseMs: 13.0, TotalMs: 20.0},
			{ServerID: "speedy", Mode: model.RankReliability, Rank: 3, BaseMs: 7.0, TotalMs: 47.0},
		},
	}
	comparisons := []model.Comparison{
		{ServerA: "speedy", ServerB: "steady", Category: model.CatCached, DeltaMeanMs: -1.5, PValue: 0.41, Level: model.SigInconclusive},
		{ServerA: "steady", ServerB: "system-1", Category: model.CatCached, DeltaMeanMs: -4.1, PValue: 0.001, Level: model.SigSignificant},
	}
	weights := map[model.RankMode]model.Weights{}
	for _, m := range model.AllRankModes() {
		weights[m] = model.Weights{
			Category:                map[model.Category]float64{model.CatCached: 0.6, model.CatTLD: 0.4},
			LatencyMetric:           "p90",
			PenaltyPerLossPctMs:     5.0,
			PenaltyPerServfailPctMs: 3.0,
			PenaltyNXInterceptionMs: 10.0,
			PenaltyNoDNSSECMs:       5.0,
			JitterWeight:            0.2,
		}
	}
	cfg := model.DefaultBenchConfig(model.ModeStandard)
	cfg.Seed = 42
	samples := []model.Sample{
		{
			ServerID: "steady",
			Category: model.CatCached,
			Round:    1,
			QName:    "google.com.",
			QType:    "A",
			At:       started,
			Result:   model.QueryResult{RTT: 8 * time.Millisecond},
		},
	}
	return &model.RunResult{
		Info: model.RunInfo{
			AppVersion: model.AppVersion,
			OS:         "darwin",
			Arch:       "arm64",
			StartedAt:  started,
			Duration:   90 * time.Second,
			Interfaces: []string{"en0"},
		},
		Config:          cfg,
		SelectedRanking: model.RankBrowsing,
		Weights:         weights,
		Servers:         servers,
		SystemServers: []model.Server{
			{ID: "detected-router", Name: "System Router", Address: "192.168.1.1", Port: 53, Protocol: model.ProtoUDP, Source: model.SourceSystem, Interface: "en0", SystemRole: "primary"},
		},
		SystemIDs:   []string{"system-1"},
		Probes:      probes,
		Triage:      triage,
		Stats:       stats,
		Scores:      scores,
		Comparisons: comparisons,
		Samples:     samples,
	}
}

func TestBuildConclusionsLocalForwarderSentence(t *testing.T) {
	out := BuildConclusions(testResult())
	want := "This system DNS endpoint was detected successfully. It is a local/private forwarder, so only the upstream behind this endpoint is unknown; other detected DNS endpoints are listed separately."
	if !strings.Contains(out, want) {
		t.Fatalf("conclusions missing exact local forwarder sentence:\n%s", out)
	}
}

func TestBuildConclusionsTopTwoTie(t *testing.T) {
	out := BuildConclusions(testResult())
	if !strings.Contains(out, "effectively tied") {
		t.Fatalf("conclusions missing tie statement:\n%s", out)
	}
	if !strings.Contains(out, "SpeedyDNS") || !strings.Contains(out, "SteadyDNS") {
		t.Fatalf("tie statement should name both top servers:\n%s", out)
	}
}

func TestBuildConclusionsAggregateBootstrapComparison(t *testing.T) {
	res := testResult()
	res.Comparisons = []model.Comparison{{
		ServerA:          "steady",
		ServerB:          "speedy",
		RankingMode:      model.RankBrowsing,
		DeltaScoreMs:     -4,
		CI95LowMs:        -7,
		CI95HighMs:       -1,
		PValue:           0.01,
		BootstrapSamples: 1000,
		Level:            model.SigSignificant,
	}}
	out := BuildConclusions(res)
	for _, want := range []string{"aggregate everyday browsing latency cost", "95% bootstrap interval -7.0 to -1.0 ms", "statistically significant"} {
		if !strings.Contains(out, want) {
			t.Fatalf("conclusions missing %q:\n%s", want, out)
		}
	}
}

func TestBuildConclusionsOmitsEditorialSections(t *testing.T) {
	out := BuildConclusions(testResult())
	for _, want := range []string{"Current DNS configuration", "Run coverage", "Statistical comparisons"} {
		if !strings.Contains(out, want) {
			t.Fatalf("factual report missing section %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{
		"Highlights", "Issues found", "Caveats", "Final recommendation",
		"Fastest server:", "Most stable server:", "Best encrypted option:",
		"Results are valid for the network and time of the test.",
		"Congestion can change the ranking.",
		"Lower DNS latency does not guarantee faster full page loads.",
	} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("factual report still contains editorial section/verdict %q:\n%s", unwanted, out)
		}
	}
}

func TestBuildConclusionsCoverageAndComparisons(t *testing.T) {
	out := BuildConclusions(testResult())
	if !strings.Contains(out, "GhostDNS") || !strings.Contains(out, "no response to any triage probe") {
		t.Fatalf("conclusions missing unreachable server with reason:\n%s", out)
	}
	if !strings.Contains(out, "1 unreachable") {
		t.Fatalf("conclusions missing state counts:\n%s", out)
	}
	if !strings.Contains(out, "statistically significant") {
		t.Fatalf("conclusions missing significant comparison:\n%s", out)
	}
}

func TestExportJSONStripsRawWithoutMutating(t *testing.T) {
	res := testResult()
	var buf bytes.Buffer
	if err := ExportJSON(&buf, res, false); err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, `"samples_ms"`) {
		t.Fatalf("stripped JSON must not contain samples_ms key")
	}
	if strings.Contains(out, `"samples"`) {
		t.Fatalf("stripped JSON must not contain samples key")
	}
	if res.Samples == nil || len(res.Samples) != 1 {
		t.Fatalf("original Samples was mutated")
	}
	if res.Stats["steady"].PerCategory[model.CatCached].SamplesMs == nil {
		t.Fatalf("original SamplesMs was mutated")
	}
	buf.Reset()
	if err := ExportJSON(&buf, res, true); err != nil {
		t.Fatalf("ExportJSON raw: %v", err)
	}
	raw := buf.String()
	if !strings.Contains(raw, `"samples_ms"`) || !strings.Contains(raw, `"samples"`) {
		t.Fatalf("raw JSON should contain samples and samples_ms keys")
	}
}

func TestExportCSV(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportCSV(&buf, testResult()); err != nil {
		t.Fatalf("ExportCSV: %v", err)
	}
	records, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(records) != 7 {
		t.Fatalf("expected header plus 6 rows, got %d records", len(records))
	}
	if len(records[0]) != 41 {
		t.Fatalf("expected 41 columns, got %d", len(records[0]))
	}
	if records[0][0] != "server_id" || records[0][35] != "truncated_pct" {
		t.Fatalf("pre-existing columns must keep their position: %v", records[0])
	}
	if got := records[0][36:]; !reflect.DeepEqual(got, []string{"ranking_mode", "rank", "cost_base_ms", "cost_penalty_ms", "latency_cost_ms"}) {
		t.Fatalf("unexpected ranking columns: %v", got)
	}
	seen := map[string]bool{}
	ranking := map[string][]string{}
	for _, row := range records[1:] {
		if len(row) != 41 {
			t.Fatalf("row has %d columns, expected 41: %v", len(row), row)
		}
		seen[row[0]+"|"+row[7]] = true
		ranking[row[0]] = row[36:]
		if !strings.Contains(row[23], ".") {
			t.Fatalf("median_ms should use dot decimal: %q", row[23])
		}
	}
	for _, key := range []string{"system-1|cached", "system-1|tld", "steady|cached", "steady|tld", "speedy|cached", "speedy|tld"} {
		if !seen[key] {
			t.Fatalf("missing row for %s; got %v", key, seen)
		}
	}
	// testResult selects the browsing ranking, where speedy carries a 20 ms
	// loss penalty and system-1 a 5 ms no-DNSSEC one.
	for id, want := range map[string][]string{
		"steady":   {"browsing", "1", "8.200", "0.000", "8.200"},
		"speedy":   {"browsing", "2", "7.000", "20.000", "27.000"},
		"system-1": {"browsing", "3", "13.000", "5.000", "18.000"},
	} {
		if got := ranking[id]; !reflect.DeepEqual(got, want) {
			t.Fatalf("ranking cells for %s: got %v, want %v", id, got, want)
		}
	}
}

func TestExportCSVLeavesUnrankedServersEmpty(t *testing.T) {
	res := testResult()
	// GhostDNS is unreachable: it has measurements but never earned a rank.
	res.Stats["ghost"] = &model.ServerStats{
		ServerID:    "ghost",
		State:       model.StateOffline,
		PerCategory: map[model.Category]*model.Distribution{model.CatCached: dist(0, 0, 100, 0, nil)},
	}
	var buf bytes.Buffer
	if err := ExportCSV(&buf, res); err != nil {
		t.Fatalf("ExportCSV: %v", err)
	}
	records, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	for _, row := range records[1:] {
		if row[0] != "ghost" {
			continue
		}
		if got := row[36:]; !reflect.DeepEqual(got, []string{"browsing", "", "", "", ""}) {
			t.Fatalf("unranked server must have empty ranking cells, got %v", got)
		}
		return
	}
	t.Fatal("no row emitted for the unranked server")
}

func TestChartSVGIsValidXML(t *testing.T) {
	for _, theme := range []svgTheme{svgLight, svgDark} {
		var b strings.Builder
		writeSVG(&b, testResult(), model.RankBrowsing, theme)
		var doc struct {
			XMLName xml.Name
		}
		if err := xml.Unmarshal([]byte(b.String()), &doc); err != nil {
			t.Fatalf("svg is not valid XML: %v\n%s", err, b.String())
		}
		if doc.XMLName.Local != "svg" {
			t.Fatalf("root element is %q, expected svg", doc.XMLName.Local)
		}
		out := b.String()
		if !strings.Contains(out, "SteadyDNS") || !strings.Contains(out, "loss 3.0%") {
			t.Fatalf("svg missing server label or loss indicator:\n%s", out)
		}
		if !strings.Contains(out, theme.surface) {
			t.Fatalf("svg missing theme surface %s", theme.surface)
		}
	}
}

func TestExportHTML(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportHTML(&buf, testResult(), model.RankBrowsing); err != nil {
		t.Fatalf("ExportHTML: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"<!DOCTYPE html>",
		"Top-ranked resolver on this network",
		"SteadyDNS",
		"current dns",
		"Your current DNS",
		"Ranking — everyday browsing",
		"latency cost <b>",
		"latency cost<br><small>ms, lower is better</small>",
		"Latency cost breakdown",
		"Server characteristics",
		"Detailed metrics — cached category",
		"Current DNS configuration",
		"Run coverage",
		"Statistical comparisons",
		"GhostDNS",
		"<svg",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("html report missing %q", want)
		}
	}
	if strings.Contains(out, "src=\"http") || strings.Contains(out, "href=\"http") {
		t.Fatalf("html report references external resources")
	}
}

func TestExportText(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportText(&buf, testResult()); err != nil {
		t.Fatalf("ExportText: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "2026-07-22") {
		t.Fatalf("text report missing date:\n%s", out)
	}
	if !strings.Contains(out, "Weights: categories") {
		t.Fatalf("text report missing weights:\n%s", out)
	}
	if !strings.Contains(out, "darwin/arm64") {
		t.Fatalf("text report missing platform")
	}
	if !strings.Contains(out, "seed 42") {
		t.Fatalf("text report missing seed")
	}
	if !strings.Contains(out, "This system DNS endpoint was detected successfully.") {
		t.Fatalf("text report missing conclusions")
	}
	if !strings.Contains(out, "GhostDNS") || !strings.Contains(out, "unreachable") {
		t.Fatalf("text report table missing unreachable server")
	}
	if !strings.Contains(out, "Latency cost ms") {
		t.Fatalf("text report missing latency-cost column:\n%s", out)
	}
	if !strings.Contains(out, "weighted latency plus penalties, in ms — lower is better") {
		t.Fatalf("text report never states that a lower latency cost is better:\n%s", out)
	}
}

func TestExportTextUsesSelectedRanking(t *testing.T) {
	res := testResult()
	res.SelectedRanking = model.RankLatency
	var buf bytes.Buffer
	if err := ExportText(&buf, res); err != nil {
		t.Fatalf("ExportText: %v", err)
	}
	if !strings.Contains(buf.String(), "Ranking mode: overall latency") {
		t.Fatalf("text report ignored selected ranking:\n%s", buf.String())
	}
}

func TestWriteExports(t *testing.T) {
	dir := t.TempDir()
	res := testResult()
	paths, err := WriteExports(dir, "bench", res, []string{"json", "csv", "txt", "html"}, false, model.RankBrowsing)
	if err != nil {
		t.Fatalf("WriteExports: %v", err)
	}
	if len(paths) != 4 {
		t.Fatalf("expected 4 paths, got %d", len(paths))
	}
	wantNames := []string{
		"bench-20260722-213000.json",
		"bench-20260722-213000.csv",
		"bench-20260722-213000.txt",
		"bench-20260722-213000.html",
	}
	for i, p := range paths {
		if filepath.Base(p) != wantNames[i] {
			t.Fatalf("path %d = %q, want base %q", i, p, wantNames[i])
		}
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if info.Size() == 0 {
			t.Fatalf("file %s is empty", p)
		}
	}
	if _, err := WriteExports(dir, "bench", res, []string{"bmp"}, false, model.RankBrowsing); err == nil {
		t.Fatalf("expected error for unsupported format")
	}
}

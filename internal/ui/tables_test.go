package ui

import (
	"strings"
	"testing"

	"dnsbench/internal/model"
)

func TestRenderServersTable(t *testing.T) {
	disableColors(t)
	servers := []model.Server{
		{ID: "cf", Name: "Cloudflare", Operator: "Cloudflare Inc", Address: "1.1.1.1", Protocol: model.ProtoUDP, Source: model.SourceBuiltin},
		{ID: "router", Name: "Home Router", Address: "192.168.1.1", Protocol: model.ProtoUDP, Source: model.SourceSystem},
		{ID: "doh", Name: "Mullvad DoH", DoHURL: "https://dns.mullvad.net/dns-query", Protocol: model.ProtoDoH, Source: model.SourceUser},
	}
	probes := map[string]*model.ProbeResult{
		"cf": {
			ServerID:       "cf",
			Reachable:      true,
			DNSSEC:         model.DNSSECInfo{Validating: model.VerdictYes},
			NXInterception: model.VerdictNo,
			Rebind:         model.RebindInfo{Overall: model.VerdictYes},
		},
		"router": {
			ServerID:       "router",
			Reachable:      true,
			DNSSEC:         model.DNSSECInfo{Validating: model.VerdictNo},
			NXInterception: model.VerdictYes,
			Rebind:         model.RebindInfo{Overall: model.VerdictNo},
		},
	}
	states := map[string]model.ServerState{
		"cf":     model.StateActive,
		"router": model.StateActive,
		"doh":    model.StateOffline,
	}
	out := RenderServersTable(servers, probes, states)
	for _, want := range []string{
		"endpoint", "name", "operator", "protocol", "source", "scope", "status", "dnssec", "nxdomain", "rebind",
		"1.1.1.1:53", "192.168.1.1:53", "https://dns.mullvad.net/dns-query",
		"Cloudflare Inc",
		"UDP/53", "DoH",
		"built-in", "system", "user",
		"public", "private (RFC 1918)", "remote",
		"active", "unreachable",
		"ok", "intercepts",
		"yes", "no",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("servers table missing %q\n%s", want, out)
		}
	}
}

func TestRenderServerCharacteristicsCompact(t *testing.T) {
	disableColors(t)
	out := RenderServerCharacteristics(chartFixture())
	for _, want := range []string{
		"● current DNS resolver",
		"resolver", "proto", "status", "dnssec", "nxdomain", "rebind",
		"Google Public DNS", "Cloudflare", "Quad9 Secure",
		"active", "sidelined", "unreachable",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compact characteristics missing %q\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"endpoint", "operator", "source", "scope", "8.8.8.8:53"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("compact characteristics contains redundant column/value %q\n%s", unwanted, out)
		}
	}
	google := strings.Index(out, "Google Public DNS")
	cloudflare := strings.Index(out, "Cloudflare")
	if google < 0 || cloudflare < 0 || google > cloudflare {
		t.Errorf("current DNS resolver was not prioritized:\n%s", out)
	}
}

func TestRenderMetricsTableSortKeys(t *testing.T) {
	disableColors(t)
	res := chartFixture()
	out := RenderMetricsTable(res, model.CatCached, "median")
	cf := strings.Index(out, "Cloudflare")
	q9 := strings.Index(out, "Quad9 Secure")
	gg := strings.Index(out, "Google Public DNS")
	if !(cf < q9 && q9 < gg) {
		t.Errorf("median sort wrong: cf=%d quad9=%d goog=%d\n%s", cf, q9, gg, out)
	}
	out = RenderMetricsTable(res, model.CatCached, "name")
	cf = strings.Index(out, "Cloudflare")
	q9 = strings.Index(out, "Quad9 Secure")
	gg = strings.Index(out, "Google Public DNS")
	if !(cf < gg && gg < q9) {
		t.Errorf("name sort wrong: cf=%d goog=%d quad9=%d\n%s", cf, gg, q9, out)
	}
	out = RenderMetricsTable(res, model.CatCached, "loss")
	q9 = strings.Index(out, "Quad9 Secure")
	gg = strings.Index(out, "Google Public DNS")
	if !(q9 < gg) {
		t.Errorf("loss sort wrong: quad9=%d goog=%d\n%s", q9, gg, out)
	}
}

func TestRenderMetricsTableCells(t *testing.T) {
	disableColors(t)
	out := RenderMetricsTable(chartFixture(), model.CatCached, "median")
	for _, want := range []string{
		"server", "count", "ans", "loss", "min", "max", "mean", "median",
		"stddev", "var", "p50", "p90", "p95", "p99", "ci95lo", "ci95hi", "jitter",
		"8.0 ms", "12.0 ms", "2.0%", "100",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics table missing %q\n%s", want, out)
		}
	}
}

func TestRenderScoreTable(t *testing.T) {
	disableColors(t)
	out := RenderScoreTable(chartFixture(), model.RankLatency)
	for _, want := range []string{
		"rank", "server", "base", "+loss", "total",
		"Cloudflare", "Quad9 Secure", "Google Public DNS",
		"10.1 ms", "3.0 ms", "18.0 ms",
		"score = median latency",
		"cached ×0.70", "recursive/TLD ×0.30",
		"loss ×2.0 ms per %",
		"no DNSSEC +10.0 ms",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("score table missing %q\n%s", want, out)
		}
	}
	cf := strings.Index(out, "Cloudflare")
	gg := strings.Index(out, "Google Public DNS")
	if !(cf < gg) {
		t.Errorf("score order wrong: cf=%d goog=%d\n%s", cf, gg, out)
	}
}

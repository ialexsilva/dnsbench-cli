package probe

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"dnsbench/internal/model"

	"github.com/miekg/dns"
)

func testConfig(uncachedZone string) Config {
	cfg := DefaultConfig()
	cfg.ReachabilityDomain = "reach.test"
	cfg.SignedDomain = "signed.test"
	cfg.BogusDomain = "bogus.test"
	cfg.UnsignedDomain = "unsigned.test"
	cfg.RebindV4 = []RebindCase{
		{Host: "127.0.0.1.rebind.test", Expected: "127.0.0.1"},
		{Host: "192.168.1.1.rebind.test", Expected: "192.168.1.1"},
	}
	cfg.RebindV6 = []RebindCase{
		{Host: "--1.rebind.test", Expected: "::1"},
		{Host: "fd00--1.rebind.test", Expected: "fd00::1"},
	}
	cfg.NXTLDZone = "com"
	cfg.UncachedZone = uncachedZone
	cfg.Timeout = 2 * time.Second
	cfg.Retries = 0
	cfg.Concurrency = 2
	return cfg
}

func mockServerEntry(id string, port int) model.Server {
	return model.Server{
		ID:       id,
		Name:     id,
		Address:  "127.0.0.1",
		Port:     port,
		Protocol: model.ProtoUDP,
		Source:   model.SourceUser,
		Enabled:  true,
	}
}

func runProbe(t *testing.T, p profile, cfg Config) *model.ProbeResult {
	t.Helper()
	port := startMockServer(t, p)
	results := Run(context.Background(), []model.Server{mockServerEntry("mock", port)}, cfg)
	pr := results["mock"]
	if pr == nil {
		t.Fatal("no probe result for mock server")
	}
	return pr
}

func assertVerdict(t *testing.T, field string, got, want model.Verdict) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ReachabilityDomain != "google.com" {
		t.Errorf("ReachabilityDomain = %q", cfg.ReachabilityDomain)
	}
	if cfg.SignedDomain != "cloudflare.com" {
		t.Errorf("SignedDomain = %q", cfg.SignedDomain)
	}
	if cfg.BogusDomain != "dnssec-failed.org" {
		t.Errorf("BogusDomain = %q", cfg.BogusDomain)
	}
	if cfg.UnsignedDomain != "github.com" {
		t.Errorf("UnsignedDomain = %q", cfg.UnsignedDomain)
	}
	if len(cfg.RebindV4) != 4 {
		t.Fatalf("len(RebindV4) = %d, want 4", len(cfg.RebindV4))
	}
	if cfg.RebindV4[0] != (RebindCase{Host: "127.0.0.1.sslip.io", Expected: "127.0.0.1"}) {
		t.Errorf("RebindV4[0] = %+v", cfg.RebindV4[0])
	}
	if cfg.RebindV4[3] != (RebindCase{Host: "192.168.1.1.sslip.io", Expected: "192.168.1.1"}) {
		t.Errorf("RebindV4[3] = %+v", cfg.RebindV4[3])
	}
	if len(cfg.RebindV6) != 2 {
		t.Fatalf("len(RebindV6) = %d, want 2", len(cfg.RebindV6))
	}
	if cfg.RebindV6[0] != (RebindCase{Host: "--1.sslip.io", Expected: "::1"}) {
		t.Errorf("RebindV6[0] = %+v", cfg.RebindV6[0])
	}
	if cfg.RebindV6[1] != (RebindCase{Host: "fd00--1.sslip.io", Expected: "fd00::1"}) {
		t.Errorf("RebindV6[1] = %+v", cfg.RebindV6[1])
	}
	if cfg.NXTLDZone != "com" {
		t.Errorf("NXTLDZone = %q", cfg.NXTLDZone)
	}
	if cfg.UncachedZone != "" {
		t.Errorf("UncachedZone = %q", cfg.UncachedZone)
	}
	if cfg.Timeout != 3*time.Second {
		t.Errorf("Timeout = %v", cfg.Timeout)
	}
	if cfg.Retries != 1 {
		t.Errorf("Retries = %d", cfg.Retries)
	}
	if cfg.Concurrency != 8 {
		t.Errorf("Concurrency = %d", cfg.Concurrency)
	}
	if cfg.Extended {
		t.Error("Extended should default to false")
	}
	if cfg.Factory != nil {
		t.Error("Factory should default to nil")
	}
}

func TestValidatingResolverProfile(t *testing.T) {
	cfg := testConfig("")
	cfg.Extended = true
	pr := runProbe(t, profileValidating, cfg)
	if !pr.Reachable {
		t.Fatalf("expected reachable, errors: %v", pr.Errors)
	}
	if pr.BaselineRcode != dns.RcodeSuccess {
		t.Errorf("BaselineRcode = %d", pr.BaselineRcode)
	}
	assertVerdict(t, "SupportsA", pr.SupportsA, model.VerdictYes)
	assertVerdict(t, "SupportsAAAA", pr.SupportsAAAA, model.VerdictYes)
	assertVerdict(t, "EDNS0", pr.EDNS0, model.VerdictYes)
	if pr.AdvertisedUDPSize != 4096 {
		t.Errorf("AdvertisedUDPSize = %d, want 4096", pr.AdvertisedUDPSize)
	}
	assertVerdict(t, "DNSSEC.ReturnsRRSIG", pr.DNSSEC.ReturnsRRSIG, model.VerdictYes)
	assertVerdict(t, "DNSSEC.ADOnSigned", pr.DNSSEC.ADOnSigned, model.VerdictYes)
	assertVerdict(t, "DNSSEC.SignedResolves", pr.DNSSEC.SignedResolves, model.VerdictYes)
	assertVerdict(t, "DNSSEC.BogusServfail", pr.DNSSEC.BogusServfail, model.VerdictYes)
	assertVerdict(t, "DNSSEC.BogusWithCDResolves", pr.DNSSEC.BogusWithCDResolves, model.VerdictYes)
	assertVerdict(t, "DNSSEC.Validating", pr.DNSSEC.Validating, model.VerdictYes)
	if len(pr.NXChecks) != 3 {
		t.Fatalf("len(NXChecks) = %d, want 3", len(pr.NXChecks))
	}
	for _, c := range pr.NXChecks {
		if c.Behavior != model.NXExpected {
			t.Errorf("check %s behavior = %q, want %q", c.Label, c.Behavior, model.NXExpected)
		}
	}
	tld := pr.NXChecks[0]
	if tld.Label != "nonexistent-tld" {
		t.Errorf("NXChecks[0].Label = %q", tld.Label)
	}
	if !strings.HasPrefix(tld.QName, "dnsbench-") || len(tld.QName) != len("dnsbench-")+12 || strings.Contains(tld.QName, ".") {
		t.Errorf("nonexistent-tld qname = %q", tld.QName)
	}
	for _, r := range tld.QName[len("dnsbench-"):] {
		if !strings.ContainsRune(labelAlphabet, r) {
			t.Errorf("unexpected rune %q in random label", r)
			break
		}
	}
	if !strings.HasSuffix(pr.NXChecks[1].QName, ".com") {
		t.Errorf("nonexistent-com qname = %q", pr.NXChecks[1].QName)
	}
	if !strings.HasPrefix(pr.NXChecks[2].QName, "www.") || !strings.HasSuffix(pr.NXChecks[2].QName, ".com") {
		t.Errorf("www-nonexistent qname = %q", pr.NXChecks[2].QName)
	}
	assertVerdict(t, "NXInterception", pr.NXInterception, model.VerdictNo)
	assertVerdict(t, "Rebind.V4", pr.Rebind.V4, model.VerdictYes)
	assertVerdict(t, "Rebind.V6", pr.Rebind.V6, model.VerdictNo)
	assertVerdict(t, "Rebind.Overall", pr.Rebind.Overall, model.VerdictPartial)
	if len(pr.Rebind.Details) != 4 {
		t.Errorf("len(Rebind.Details) = %d, want 4", len(pr.Rebind.Details))
	}
	if pr.ReverseName != "resolver.test" {
		t.Errorf("ReverseName = %q, want %q", pr.ReverseName, "resolver.test")
	}
	if pr.Extended == nil {
		t.Fatal("expected extended info")
	}
	assertVerdict(t, "Extended.DNS64", pr.Extended.DNS64, model.VerdictYes)
	assertVerdict(t, "Extended.QNAMEMinimization", pr.Extended.QNAMEMinimization, model.VerdictYes)
	assertVerdict(t, "Extended.HTTPSRecord", pr.Extended.HTTPSRecord, model.VerdictYes)
}

func TestInterceptorProfile(t *testing.T) {
	cfg := testConfig("probezone.test")
	cfg.Extended = true
	pr := runProbe(t, profileInterceptor, cfg)
	if !pr.Reachable {
		t.Fatalf("expected reachable, errors: %v", pr.Errors)
	}
	assertVerdict(t, "EDNS0", pr.EDNS0, model.VerdictNo)
	if pr.AdvertisedUDPSize != 0 {
		t.Errorf("AdvertisedUDPSize = %d, want 0", pr.AdvertisedUDPSize)
	}
	assertVerdict(t, "DNSSEC.ReturnsRRSIG", pr.DNSSEC.ReturnsRRSIG, model.VerdictNo)
	assertVerdict(t, "DNSSEC.ADOnSigned", pr.DNSSEC.ADOnSigned, model.VerdictNo)
	assertVerdict(t, "DNSSEC.SignedResolves", pr.DNSSEC.SignedResolves, model.VerdictYes)
	assertVerdict(t, "DNSSEC.BogusServfail", pr.DNSSEC.BogusServfail, model.VerdictNo)
	assertVerdict(t, "DNSSEC.BogusWithCDResolves", pr.DNSSEC.BogusWithCDResolves, model.VerdictYes)
	assertVerdict(t, "DNSSEC.Validating", pr.DNSSEC.Validating, model.VerdictNo)
	if len(pr.NXChecks) != 4 {
		t.Fatalf("len(NXChecks) = %d, want 4", len(pr.NXChecks))
	}
	byLabel := map[string]model.NXCheck{}
	for _, c := range pr.NXChecks {
		byLabel[c.Label] = c
	}
	for _, label := range []string{"nonexistent-tld", "nonexistent-com", "controlled-zone"} {
		c := byLabel[label]
		if c.Behavior != model.NXInterceptedIP {
			t.Errorf("check %s behavior = %q, want %q", label, c.Behavior, model.NXInterceptedIP)
		}
		if !strings.Contains(c.Detail, interceptIP) {
			t.Errorf("check %s detail = %q, want it to mention %s", label, c.Detail, interceptIP)
		}
	}
	www := byLabel["www-nonexistent"]
	if www.Behavior != model.NXInterceptedCN {
		t.Errorf("www-nonexistent behavior = %q, want %q", www.Behavior, model.NXInterceptedCN)
	}
	if !strings.Contains(www.Detail, "landing.intercept.test") {
		t.Errorf("www-nonexistent detail = %q", www.Detail)
	}
	if !strings.HasSuffix(byLabel["controlled-zone"].QName, ".probezone.test") {
		t.Errorf("controlled-zone qname = %q", byLabel["controlled-zone"].QName)
	}
	assertVerdict(t, "NXInterception", pr.NXInterception, model.VerdictYes)
	assertVerdict(t, "Rebind.V4", pr.Rebind.V4, model.VerdictUnknown)
	assertVerdict(t, "Rebind.V6", pr.Rebind.V6, model.VerdictUnknown)
	assertVerdict(t, "Rebind.Overall", pr.Rebind.Overall, model.VerdictUnknown)
	if pr.ReverseName != "resolver.test" {
		t.Errorf("ReverseName = %q", pr.ReverseName)
	}
	if pr.Extended == nil {
		t.Fatal("expected extended info")
	}
	assertVerdict(t, "Extended.DNS64", pr.Extended.DNS64, model.VerdictNo)
	assertVerdict(t, "Extended.QNAMEMinimization", pr.Extended.QNAMEMinimization, model.VerdictNo)
	assertVerdict(t, "Extended.HTTPSRecord", pr.Extended.HTTPSRecord, model.VerdictNo)
}

func TestRebindBlockingProfile(t *testing.T) {
	pr := runProbe(t, profileRebindBlocking, testConfig(""))
	if !pr.Reachable {
		t.Fatalf("expected reachable, errors: %v", pr.Errors)
	}
	assertVerdict(t, "DNSSEC.BogusServfail", pr.DNSSEC.BogusServfail, model.VerdictYes)
	assertVerdict(t, "DNSSEC.ReturnsRRSIG", pr.DNSSEC.ReturnsRRSIG, model.VerdictNo)
	assertVerdict(t, "DNSSEC.ADOnSigned", pr.DNSSEC.ADOnSigned, model.VerdictNo)
	assertVerdict(t, "DNSSEC.Validating", pr.DNSSEC.Validating, model.VerdictPartial)
	for _, c := range pr.NXChecks {
		if c.Behavior != model.NXNoErrorEmpty {
			t.Errorf("check %s behavior = %q, want %q", c.Label, c.Behavior, model.NXNoErrorEmpty)
		}
	}
	assertVerdict(t, "NXInterception", pr.NXInterception, model.VerdictNo)
	assertVerdict(t, "Rebind.V4", pr.Rebind.V4, model.VerdictYes)
	assertVerdict(t, "Rebind.V6", pr.Rebind.V6, model.VerdictYes)
	assertVerdict(t, "Rebind.Overall", pr.Rebind.Overall, model.VerdictYes)
	for _, d := range pr.Rebind.Details {
		if !strings.Contains(d, "blocked") {
			t.Errorf("detail %q should mention blocked", d)
		}
	}
	if pr.Extended != nil {
		t.Error("extended info should be nil when Extended is false")
	}
}

func TestRebindUnprotectedProfile(t *testing.T) {
	pr := runProbe(t, profileRebindUnprotected, testConfig(""))
	if !pr.Reachable {
		t.Fatalf("expected reachable, errors: %v", pr.Errors)
	}
	assertVerdict(t, "DNSSEC.BogusServfail", pr.DNSSEC.BogusServfail, model.VerdictUnknown)
	assertVerdict(t, "DNSSEC.BogusWithCDResolves", pr.DNSSEC.BogusWithCDResolves, model.VerdictNo)
	assertVerdict(t, "DNSSEC.Validating", pr.DNSSEC.Validating, model.VerdictUnknown)
	for _, c := range pr.NXChecks {
		if c.Behavior != model.NXExpected {
			t.Errorf("check %s behavior = %q, want %q", c.Label, c.Behavior, model.NXExpected)
		}
	}
	assertVerdict(t, "NXInterception", pr.NXInterception, model.VerdictNo)
	assertVerdict(t, "Rebind.V4", pr.Rebind.V4, model.VerdictNo)
	assertVerdict(t, "Rebind.V6", pr.Rebind.V6, model.VerdictNo)
	assertVerdict(t, "Rebind.Overall", pr.Rebind.Overall, model.VerdictNo)
	for _, d := range pr.Rebind.Details {
		if !strings.Contains(d, "no rebind protection") {
			t.Errorf("detail %q should mention missing protection", d)
		}
	}
}

func TestUnreachableServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tcp listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cfg := testConfig("")
	cfg.Timeout = time.Second
	srv := model.Server{ID: "dead", Address: "127.0.0.1", Port: port, Protocol: model.ProtoTCP, Enabled: true}
	results := Run(context.Background(), []model.Server{srv}, cfg)
	pr := results["dead"]
	if pr == nil {
		t.Fatal("no probe result for dead server")
	}
	if pr.Reachable {
		t.Error("expected unreachable")
	}
	if len(pr.Errors) == 0 {
		t.Fatal("expected errors for unreachable server")
	}
	if !strings.Contains(pr.Errors[0], "network error") {
		t.Errorf("error = %q, want it to mention the network failure kind", pr.Errors[0])
	}
	assertVerdict(t, "SupportsA", pr.SupportsA, model.VerdictUnknown)
	assertVerdict(t, "SupportsAAAA", pr.SupportsAAAA, model.VerdictUnknown)
	assertVerdict(t, "EDNS0", pr.EDNS0, model.VerdictUnknown)
	assertVerdict(t, "DNSSEC.Validating", pr.DNSSEC.Validating, model.VerdictUnknown)
	assertVerdict(t, "NXInterception", pr.NXInterception, model.VerdictUnknown)
	assertVerdict(t, "Rebind.Overall", pr.Rebind.Overall, model.VerdictUnknown)
	if len(pr.NXChecks) != 0 {
		t.Errorf("len(NXChecks) = %d, want 0", len(pr.NXChecks))
	}
	if pr.Extended != nil {
		t.Error("extended info should be nil for unreachable server")
	}
}

func TestRunMultipleServers(t *testing.T) {
	portA := startMockServer(t, profileValidating)
	portB := startMockServer(t, profileInterceptor)
	cfg := testConfig("")
	cfg.Concurrency = 8
	servers := []model.Server{
		mockServerEntry("a", portA),
		mockServerEntry("b", portB),
	}
	results := Run(context.Background(), servers, cfg)
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results["a"] == nil || results["b"] == nil {
		t.Fatal("missing results for probed servers")
	}
	assertVerdict(t, "a NXInterception", results["a"].NXInterception, model.VerdictNo)
	assertVerdict(t, "b NXInterception", results["b"].NXInterception, model.VerdictYes)
}

func TestRunCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	port := startMockServer(t, profileValidating)
	results := Run(ctx, []model.Server{mockServerEntry("mock", port)}, testConfig(""))
	pr := results["mock"]
	if pr == nil {
		t.Fatal("no probe result for mock server")
	}
	if pr.Reachable {
		t.Error("expected unreachable with canceled context")
	}
	if len(pr.Errors) == 0 {
		t.Error("expected errors with canceled context")
	}
}

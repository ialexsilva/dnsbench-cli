package serverlist

import (
	"net/netip"
	"net/url"
	"testing"

	"dnsbench/internal/model"
)

func TestBuiltinLoads(t *testing.T) {
	servers := Builtin()
	if len(servers) == 0 {
		t.Fatal("Builtin() returned an empty list")
	}
	for _, s := range servers {
		if s.Name == "" {
			t.Errorf("server %q has an empty name", s.ID)
		}
		if s.Operator == "" {
			t.Errorf("server %q has an empty operator", s.ID)
		}
		if s.Source != model.SourceBuiltin {
			t.Errorf("server %q has source %q, want %q", s.ID, s.Source, model.SourceBuiltin)
		}
		if !s.Enabled {
			t.Errorf("server %q is not enabled", s.ID)
		}
	}
}

func TestBuiltinCoversExpectedOperators(t *testing.T) {
	want := []string{
		"Google Public DNS", "Cloudflare", "Quad9", "OpenDNS", "AdGuard DNS",
		"CleanBrowsing", "Control D", "DigiCert UltraDNS",
		"Gcore Public DNS", "NextDNS", "Comodo Secure DNS",
	}
	seen := make(map[string]bool)
	for _, s := range Builtin() {
		seen[s.Operator] = true
	}
	for _, op := range want {
		if !seen[op] {
			t.Errorf("missing operator %q in built-in list", op)
		}
	}
}

func TestBuiltinExcludesRemovedOperators(t *testing.T) {
	removed := map[string]bool{
		"Alibaba Cloud Public DNS": true,
		"DNS.SB":                   true,
		"Mullvad":                  true,
	}
	for _, s := range Builtin() {
		if removed[s.Operator] {
			t.Errorf("removed operator %q is still present (server %q)", s.Operator, s.ID)
		}
	}
}

func TestBuiltinExcludesKnownUnreachableEndpoints(t *testing.T) {
	excluded := map[string]bool{
		"adguard-udp6-ad1": true,
		"adguard-udp6-ad2": true,
		"controld-dot":     true,
		"dns0-udp4-81":     true,
		"dns0-udp4-5":      true,
		"dns0-udp6-fc80":   true,
		"dns0-udp6-fc81":   true,
		"dns0-dot":         true,
		"dns0-doh":         true,
	}
	for _, s := range Builtin() {
		if excluded[s.ID] {
			t.Errorf("known unreachable endpoint %q is still present in the built-in list", s.ID)
		}
	}
}

func TestBuiltinAddressesParse(t *testing.T) {
	for _, s := range Builtin() {
		switch s.Protocol {
		case model.ProtoUDP, model.ProtoTCP, model.ProtoDoT:
			if _, err := netip.ParseAddr(s.Address); err != nil {
				t.Errorf("server %q has invalid address %q: %v", s.ID, s.Address, err)
			}
		}
		if s.Protocol == model.ProtoDoT && s.TLSHostname == "" {
			t.Errorf("DoT server %q has no TLS hostname", s.ID)
		}
	}
}

func TestBuiltinDoHURLsParse(t *testing.T) {
	for _, s := range Builtin() {
		if s.Protocol != model.ProtoDoH {
			continue
		}
		u, err := url.Parse(s.DoHURL)
		if err != nil {
			t.Errorf("server %q has invalid DoH URL %q: %v", s.ID, s.DoHURL, err)
			continue
		}
		if u.Scheme != "https" {
			t.Errorf("server %q DoH URL %q is not https", s.ID, s.DoHURL)
		}
		if u.Host == "" {
			t.Errorf("server %q DoH URL %q has no host", s.ID, s.DoHURL)
		}
	}
}

func TestBuiltinIDsUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, s := range Builtin() {
		if s.ID == "" {
			t.Error("found server with empty ID")
			continue
		}
		if seen[s.ID] {
			t.Errorf("duplicate ID %q", s.ID)
		}
		seen[s.ID] = true
	}
}

func TestBuiltinPortsConsistent(t *testing.T) {
	for _, s := range Builtin() {
		want := s.Protocol.DefaultPort()
		if got := s.EffectivePort(); got != want {
			t.Errorf("server %q has effective port %d, want %d for protocol %s", s.ID, got, want, s.Protocol)
		}
	}
}

func TestBuiltinPassesValidation(t *testing.T) {
	for _, s := range Builtin() {
		copied := s
		if err := ValidateAndPrepare(&copied); err != nil {
			t.Errorf("server %q fails validation: %v", s.ID, err)
		}
		if copied.ID != s.ID {
			t.Errorf("validation changed ID from %q to %q", s.ID, copied.ID)
		}
	}
}

func TestBuiltinReturnsCopy(t *testing.T) {
	first := Builtin()
	first[0].ID = "mutated"
	second := Builtin()
	if second[0].ID == "mutated" {
		t.Error("mutating the returned slice altered the built-in list")
	}
}

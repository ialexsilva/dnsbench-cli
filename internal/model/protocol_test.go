package model

import "testing"

func TestProtocolProperties(t *testing.T) {
	cases := []struct {
		p         Protocol
		port      int
		label     string
		encrypted bool
		usesURL   bool
		overQUIC  bool
	}{
		{ProtoUDP, 53, "UDP/53", false, false, false},
		{ProtoTCP, 53, "TCP/53", false, false, false},
		{ProtoDoT, 853, "DoT", true, false, false},
		{ProtoDoH, 443, "DoH", true, true, false},
		{ProtoDoH3, 443, "DoH/3", true, true, true},
		{ProtoDoQ, 853, "DoQ", true, false, true},
	}
	for _, tc := range cases {
		if got := tc.p.DefaultPort(); got != tc.port {
			t.Errorf("%s DefaultPort() = %d, want %d", tc.p, got, tc.port)
		}
		if got := tc.p.Label(); got != tc.label {
			t.Errorf("%s Label() = %q, want %q", tc.p, got, tc.label)
		}
		if got := tc.p.Encrypted(); got != tc.encrypted {
			t.Errorf("%s Encrypted() = %v, want %v", tc.p, got, tc.encrypted)
		}
		if got := tc.p.UsesURL(); got != tc.usesURL {
			t.Errorf("%s UsesURL() = %v, want %v", tc.p, got, tc.usesURL)
		}
		if got := tc.p.OverQUIC(); got != tc.overQUIC {
			t.Errorf("%s OverQUIC() = %v, want %v", tc.p, got, tc.overQUIC)
		}
	}
	if len(AllProtocols()) != len(cases) {
		t.Errorf("AllProtocols() has %d entries, want %d", len(AllProtocols()), len(cases))
	}
	if len(EncryptedProtocols()) != 4 {
		t.Errorf("EncryptedProtocols() = %v, want 4 entries", EncryptedProtocols())
	}
}

func TestBootstrapPinned(t *testing.T) {
	cases := []struct {
		name string
		s    Server
		want bool
	}{
		{"plaintext needs no pin", Server{Protocol: ProtoUDP, Address: "8.8.8.8"}, true},
		{"dot with address", Server{Protocol: ProtoDoT, Address: "9.9.9.9", TLSHostname: "dns.quad9.net"}, true},
		{"doq with address", Server{Protocol: ProtoDoQ, Address: "9.9.9.9", TLSHostname: "dns.quad9.net"}, true},
		{"dot without address", Server{Protocol: ProtoDoT, TLSHostname: "dns.quad9.net"}, false},
		{"doh with bootstrap", Server{Protocol: ProtoDoH, DoHURL: "https://dns.google/dns-query", Address: "8.8.8.8"}, true},
		{"doh with hostname only", Server{Protocol: ProtoDoH, DoHURL: "https://dns.google/dns-query"}, false},
		{"doh with IP literal URL", Server{Protocol: ProtoDoH, DoHURL: "https://1.1.1.1/dns-query"}, true},
		{"doh3 with IPv6 literal URL", Server{Protocol: ProtoDoH3, DoHURL: "https://[2620:fe::fe]/dns-query"}, true},
		{"doh3 with hostname only", Server{Protocol: ProtoDoH3, DoHURL: "https://dns.quad9.net/dns-query"}, false},
	}
	for _, tc := range cases {
		if got := tc.s.BootstrapPinned(); got != tc.want {
			t.Errorf("%s: BootstrapPinned() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEndpointAndDisplayNameForURLProtocols(t *testing.T) {
	s := Server{Protocol: ProtoDoH3, DoHURL: "https://dns.quad9.net/dns-query", Address: "9.9.9.9", Port: 443}
	if got := s.Endpoint(); got != "https://dns.quad9.net/dns-query" {
		t.Errorf("Endpoint() = %q, want the DoH URL", got)
	}
	if got := s.DisplayName(); got != "https://dns.quad9.net/dns-query" {
		t.Errorf("DisplayName() = %q, want the DoH URL", got)
	}
	doq := Server{Protocol: ProtoDoQ, Address: "2620:fe::fe", TLSHostname: "dns.quad9.net"}
	if got := doq.Endpoint(); got != "[2620:fe::fe]:853" {
		t.Errorf("DoQ Endpoint() = %q, want [2620:fe::fe]:853", got)
	}
}

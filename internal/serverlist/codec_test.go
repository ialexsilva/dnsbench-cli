package serverlist

import (
	"reflect"
	"strings"
	"testing"

	"dnsbench/internal/model"
)

func TestDetectFormat(t *testing.T) {
	cases := []struct {
		filename string
		want     Format
		wantErr  bool
	}{
		{"servers.json", FormatJSON, false},
		{"SERVERS.JSON", FormatJSON, false},
		{"list.csv", FormatCSV, false},
		{"list.txt", FormatText, false},
		{"list.yaml", "", true},
		{"noextension", "", true},
	}
	for _, tc := range cases {
		got, err := DetectFormat(tc.filename)
		if tc.wantErr {
			if err == nil {
				t.Errorf("DetectFormat(%q): expected error, got %q", tc.filename, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("DetectFormat(%q): unexpected error %v", tc.filename, err)
			continue
		}
		if got != tc.want {
			t.Errorf("DetectFormat(%q) = %q, want %q", tc.filename, got, tc.want)
		}
	}
}

func TestJSONRoundtrip(t *testing.T) {
	servers := []model.Server{
		{
			ID: "google-udp4-8888", Name: "Google (8.8.8.8)", Operator: "Google Public DNS",
			Address: "8.8.8.8", Port: 53, Protocol: model.ProtoUDP,
			Source: model.SourceBuiltin, Enabled: true,
		},
		{
			ID: "quad9-dot", Name: "Quad9 (DoT)", Operator: "Quad9",
			Address: "2620:fe::fe", Port: 853, TLSHostname: "dns.quad9.net",
			Protocol: model.ProtoDoT, Notes: "Blocks malicious domains",
			Source: model.SourceUser, Enabled: false,
		},
		{
			ID: "example-doh", Name: "Example DoH",
			DoHURL: "https://dns.example.com/dns-query", Protocol: model.ProtoDoH,
			Source: model.SourceUser, Enabled: true,
		},
	}
	data, err := EncodeJSON(servers)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(servers, decoded) {
		t.Errorf("JSON roundtrip mismatch:\nin:  %+v\nout: %+v", servers, decoded)
	}
}

func TestDecodeJSONInvalid(t *testing.T) {
	if _, err := DecodeJSON([]byte("{not json")); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestCSVRoundtrip(t *testing.T) {
	servers := []model.Server{
		{
			ID: "g1", Name: "Google (8.8.8.8)", Operator: "Google Public DNS",
			Address: "8.8.8.8", Port: 53, Protocol: model.ProtoUDP, Enabled: true,
		},
		{
			ID: "q-dot", Name: "Quad9, DoT", Operator: "Quad9",
			Address: "2620:fe::fe", Port: 853, TLSHostname: "dns.quad9.net",
			Protocol: model.ProtoDoT, Notes: "Blocks malicious domains", Enabled: false,
		},
		{
			ID: "e-doh", Name: "Example DoH", Operator: "Example",
			DoHURL: "https://dns.example.com/dns-query", Protocol: model.ProtoDoH, Enabled: true,
		},
	}
	data, err := EncodeCSV(servers)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "id,name,operator,protocol,address,port,tls_hostname,doh_url,notes,enabled") {
		t.Errorf("CSV output missing fixed header: %q", strings.SplitN(string(data), "\n", 2)[0])
	}
	decoded, err := DecodeCSV(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(servers, decoded) {
		t.Errorf("CSV roundtrip mismatch:\nin:  %+v\nout: %+v", servers, decoded)
	}
}

func TestDecodeCSVDefaults(t *testing.T) {
	data := "id,name,operator,protocol,address,port,tls_hostname,doh_url,notes,enabled\n" +
		"g1,Google,Google Public DNS,udp,8.8.8.8,,,,,\n"
	servers, err := DecodeCSV([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 {
		t.Fatalf("got %d servers, want 1", len(servers))
	}
	if servers[0].Port != 0 {
		t.Errorf("empty port decoded as %d, want 0", servers[0].Port)
	}
	if !servers[0].Enabled {
		t.Error("empty enabled column should default to true")
	}
}

func TestDecodeCSVErrors(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"empty input", ""},
		{"wrong header", "id,name\ng1,Google\n"},
		{"renamed column", "id,name,operator,protocol,address,port,hostname,doh_url,notes,enabled\n"},
		{"bad port", "id,name,operator,protocol,address,port,tls_hostname,doh_url,notes,enabled\ng1,G,,udp,8.8.8.8,abc,,,,true\n"},
		{"bad enabled", "id,name,operator,protocol,address,port,tls_hostname,doh_url,notes,enabled\ng1,G,,udp,8.8.8.8,53,,,,maybe\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeCSV([]byte(tc.data)); err == nil {
				t.Errorf("expected error for %q", tc.data)
			}
		})
	}
}

func TestTextRoundtrip(t *testing.T) {
	servers := []model.Server{
		{Protocol: model.ProtoUDP, Address: "8.8.8.8", Enabled: true},
		{Protocol: model.ProtoUDP, Address: "8.8.8.8", Port: 5353, Enabled: true},
		{Protocol: model.ProtoUDP, Address: "2620:fe::fe", Enabled: true},
		{Protocol: model.ProtoUDP, Address: "2001:4860:4860::8888", Port: 5353, Enabled: true},
		{Protocol: model.ProtoDoT, Address: "9.9.9.9", TLSHostname: "dns.quad9.net", Enabled: true},
		{Protocol: model.ProtoDoT, Address: "1.1.1.1", Port: 8853, TLSHostname: "one.one.one.one", Enabled: true},
		{Protocol: model.ProtoDoT, Address: "2620:fe::fe", Port: 8853, TLSHostname: "dns.quad9.net", Enabled: true},
		{Protocol: model.ProtoDoH, DoHURL: "https://dns.example.com/dns-query", Enabled: true},
		{Protocol: model.ProtoUDP, Address: "1.1.1.1", Name: "Cloudflare Primary", Operator: "Cloudflare", Enabled: true},
		{Protocol: model.ProtoDoH, DoHURL: "https://dns.google/dns-query", Name: "Google DoH", Operator: "Google Public DNS", Enabled: true},
		{Protocol: model.ProtoDoH, DoHURL: "https://dns.google/dns-query", Address: "8.8.8.8", Enabled: true},
		{Protocol: model.ProtoDoH, DoHURL: "https://dns.example.com/dns-query", Address: "192.0.2.1", Port: 8443, Enabled: true},
		{Protocol: model.ProtoDoH, DoHURL: "https://dns.example.com/dns-query", Address: "2620:fe::fe", Enabled: true},
		{Protocol: model.ProtoDoH3, DoHURL: "https://dns.quad9.net/dns-query", Address: "9.9.9.9", Enabled: true},
		{Protocol: model.ProtoDoH3, DoHURL: "https://dns.example.com/dns-query", Name: "Example DoH3", Enabled: true},
		{Protocol: model.ProtoDoQ, Address: "94.140.14.14", TLSHostname: "dns.adguard-dns.com", Enabled: true},
		{Protocol: model.ProtoDoQ, Address: "2620:fe::fe", Port: 8853, TLSHostname: "dns.quad9.net", Enabled: true},
	}
	data, err := EncodeText(servers)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeText(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(servers, decoded) {
		t.Errorf("text roundtrip mismatch:\ntext:\n%s\nin:  %+v\nout: %+v", data, servers, decoded)
	}
}

func TestDecodeTextGrammar(t *testing.T) {
	input := "# my servers\n" +
		"\n" +
		"8.8.8.8\n" +
		"1.1.1.1:5353 | Cloudflare alt port\n" +
		"2620:fe::fe\n" +
		"[2001:4860:4860::8888]:5300\n" +
		"tls://dns.quad9.net@9.9.9.9 | Quad9 DoT | Quad9\n" +
		"  # indented comment\n" +
		"https://dns.example.com/dns-query | Example DoH | Example\n"
	servers, err := DecodeText([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 6 {
		t.Fatalf("got %d servers, want 6: %+v", len(servers), servers)
	}
	if servers[0].Protocol != model.ProtoUDP || servers[0].Address != "8.8.8.8" || servers[0].Port != 0 {
		t.Errorf("bare IPv4 parsed as %+v", servers[0])
	}
	if servers[1].Address != "1.1.1.1" || servers[1].Port != 5353 || servers[1].Name != "Cloudflare alt port" {
		t.Errorf("address:port parsed as %+v", servers[1])
	}
	if servers[2].Protocol != model.ProtoUDP || servers[2].Address != "2620:fe::fe" || servers[2].Port != 0 {
		t.Errorf("bare IPv6 parsed as %+v", servers[2])
	}
	if servers[3].Address != "2001:4860:4860::8888" || servers[3].Port != 5300 {
		t.Errorf("bracketed IPv6 parsed as %+v", servers[3])
	}
	dot := servers[4]
	if dot.Protocol != model.ProtoDoT || dot.TLSHostname != "dns.quad9.net" || dot.Address != "9.9.9.9" || dot.Name != "Quad9 DoT" || dot.Operator != "Quad9" {
		t.Errorf("tls:// entry parsed as %+v", dot)
	}
	doh := servers[5]
	if doh.Protocol != model.ProtoDoH || doh.DoHURL != "https://dns.example.com/dns-query" || doh.Operator != "Example" {
		t.Errorf("DoH entry parsed as %+v", doh)
	}
	for i, s := range servers {
		if !s.Enabled {
			t.Errorf("server %d not enabled after text decode", i)
		}
	}
}

func TestDecodeTextEncryptedGrammar(t *testing.T) {
	input := "h3://dns.quad9.net/dns-query | Quad9 DoH3 | Quad9\n" +
		"https://dns.google/dns-query@8.8.8.8\n" +
		"quic://dns.adguard-dns.com@94.140.14.14 | AdGuard DoQ\n" +
		"https://user@dns.example.com/dns-query\n"
	servers, err := DecodeText([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 4 {
		t.Fatalf("got %d servers, want 4: %+v", len(servers), servers)
	}
	h3 := servers[0]
	if h3.Protocol != model.ProtoDoH3 || h3.DoHURL != "https://dns.quad9.net/dns-query" || h3.Operator != "Quad9" {
		t.Errorf("h3:// entry parsed as %+v", h3)
	}
	pinned := servers[1]
	if pinned.Protocol != model.ProtoDoH || pinned.DoHURL != "https://dns.google/dns-query" || pinned.Address != "8.8.8.8" {
		t.Errorf("pinned DoH entry parsed as %+v", pinned)
	}
	doq := servers[2]
	if doq.Protocol != model.ProtoDoQ || doq.TLSHostname != "dns.adguard-dns.com" || doq.Address != "94.140.14.14" {
		t.Errorf("quic:// entry parsed as %+v", doq)
	}
	userinfo := servers[3]
	if userinfo.DoHURL != "https://user@dns.example.com/dns-query" || userinfo.Address != "" {
		t.Errorf("URL with userinfo parsed as %+v", userinfo)
	}
}

func TestDecodeTextErrors(t *testing.T) {
	cases := []string{
		"banana",
		"8.8.8.8:99999",
		"8.8.8.8:0",
		"[2001:db8::1",
		"[2001:db8::1]x",
		"tls://@8.8.8.8",
		"tls://dns.example.com@",
		"tls://dns.example.com@not-an-ip",
		"https://",
		"h3://",
		"quic://@9.9.9.9",
		"quic://dns.example.com@",
		"quic://dns.example.com@not-an-ip",
		"https://@8.8.8.8",
	}
	for _, line := range cases {
		if _, err := DecodeText([]byte(line + "\n")); err == nil {
			t.Errorf("expected error for line %q", line)
		}
	}
}

func TestEncodeTextUnsupported(t *testing.T) {
	tcp := []model.Server{{Protocol: model.ProtoTCP, Address: "8.8.8.8", Enabled: true}}
	if _, err := EncodeText(tcp); err == nil {
		t.Error("expected error encoding a TCP server as text")
	}
	noHost := []model.Server{{Protocol: model.ProtoDoT, Address: "8.8.8.8", Enabled: true}}
	if _, err := EncodeText(noHost); err == nil {
		t.Error("expected error encoding a DoT server without TLS hostname")
	}
}

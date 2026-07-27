package serverlist

import (
	"slices"
	"testing"

	"dnsbench/internal/model"
)

func udpServer(id, addr string) model.Server {
	return model.Server{ID: id, Protocol: model.ProtoUDP, Address: addr, Enabled: true}
}

func TestMergePriorityAndDedupe(t *testing.T) {
	first := []model.Server{
		udpServer("user-a", "8.8.8.8"),
		udpServer("user-b", "1.1.1.1"),
	}
	second := []model.Server{
		udpServer("builtin-a", "8.8.8.8"),
		udpServer("builtin-c", "9.9.9.9"),
		udpServer("builtin-c-dup", "9.9.9.9"),
	}
	merged := Merge(first, second)
	if len(merged) != 3 {
		t.Fatalf("got %d servers, want 3: %+v", len(merged), merged)
	}
	if merged[0].ID != "user-a" || merged[1].ID != "user-b" || merged[2].ID != "builtin-c" {
		t.Errorf("unexpected merge order/priority: %q %q %q", merged[0].ID, merged[1].ID, merged[2].ID)
	}
}

func TestMergeDistinguishesProtocolAndPort(t *testing.T) {
	a := udpServer("a", "8.8.8.8")
	b := model.Server{ID: "b", Protocol: model.ProtoTCP, Address: "8.8.8.8"}
	c := udpServer("c", "8.8.8.8")
	c.Port = 5353
	merged := Merge([]model.Server{a, b, c})
	if len(merged) != 3 {
		t.Fatalf("got %d servers, want 3", len(merged))
	}
}

func TestMergeAssignsUniqueIDsToDistinctEndpoints(t *testing.T) {
	merged := Merge([]model.Server{
		udpServer("resolver", "1.1.1.1"),
		{ID: "resolver", Protocol: model.ProtoUDP, Address: "1.1.1.1", Port: 5353, Enabled: true},
		udpServer("resolver-2", "9.9.9.9"),
		{ID: "doh", Protocol: model.ProtoDoH, DoHURL: "https://dns.example/dns-query", Address: "192.0.2.1", Enabled: true},
		{ID: "doh", Protocol: model.ProtoDoH, DoHURL: "https://dns.example/dns-query", Address: "192.0.2.2", Enabled: true},
	})
	if len(merged) != 5 {
		t.Fatalf("got %d servers, want 5", len(merged))
	}
	got := make([]string, len(merged))
	for i, server := range merged {
		got[i] = server.ID
	}
	want := []string{"resolver", "resolver-3", "resolver-2", "doh", "doh-2"}
	if !slices.Equal(got, want) {
		t.Fatalf("IDs = %q, want %q", got, want)
	}
}

func TestFilter(t *testing.T) {
	servers := []model.Server{
		{ID: "g4", Protocol: model.ProtoUDP, Address: "8.8.8.8", Operator: "Google Public DNS", Source: model.SourceBuiltin},
		{ID: "g6", Protocol: model.ProtoUDP, Address: "2001:4860:4860::8888", Operator: "Google Public DNS", Source: model.SourceBuiltin},
		{ID: "cf-dot", Protocol: model.ProtoDoT, Address: "1.1.1.1", TLSHostname: "one.one.one.one", Operator: "Cloudflare", Source: model.SourceBuiltin},
		{ID: "mine", Protocol: model.ProtoDoH, DoHURL: "https://doh.example.com/dns-query", Operator: "Example", Source: model.SourceUser},
	}
	byProto := Filter(servers, FilterOptions{Protocols: []model.Protocol{model.ProtoDoT, model.ProtoDoH}})
	if len(byProto) != 2 || byProto[0].ID != "cf-dot" || byProto[1].ID != "mine" {
		t.Errorf("protocol filter returned %+v", byProto)
	}
	byOperator := Filter(servers, FilterOptions{Operators: []string{"google public dns"}})
	if len(byOperator) != 2 {
		t.Errorf("operator filter returned %d servers, want 2", len(byOperator))
	}
	byID := Filter(servers, FilterOptions{IDs: []string{"mine"}})
	if len(byID) != 1 || byID[0].ID != "mine" {
		t.Errorf("ID filter returned %+v", byID)
	}
	bySource := Filter(servers, FilterOptions{Sources: []model.Source{model.SourceUser}})
	if len(bySource) != 1 || bySource[0].ID != "mine" {
		t.Errorf("source filter returned %+v", bySource)
	}
	v4Only := Filter(servers, FilterOptions{IPv4Only: true})
	if len(v4Only) != 3 {
		t.Errorf("IPv4-only filter returned %d servers, want 3", len(v4Only))
	}
	for _, s := range v4Only {
		if s.ID == "g6" {
			t.Error("IPv4-only filter kept an IPv6 server")
		}
	}
	combined := Filter(servers, FilterOptions{
		Protocols: []model.Protocol{model.ProtoUDP},
		Operators: []string{"Google Public DNS"},
		IPv4Only:  true,
	})
	if len(combined) != 1 || combined[0].ID != "g4" {
		t.Errorf("combined filter returned %+v", combined)
	}
}

func TestValidateAndPrepareValid(t *testing.T) {
	cases := []model.Server{
		{Protocol: model.ProtoUDP, Address: "8.8.8.8"},
		{Protocol: model.ProtoTCP, Address: "1.1.1.1", Port: 53},
		{Protocol: model.ProtoDoT, Address: "2620:fe::fe", TLSHostname: "dns.quad9.net", Port: 853},
		{Protocol: model.ProtoDoH, DoHURL: "https://dns.example.com/dns-query"},
	}
	for _, s := range cases {
		if err := ValidateAndPrepare(&s); err != nil {
			t.Errorf("valid server %+v rejected: %v", s, err)
		}
		if s.ID == "" {
			t.Errorf("no ID generated for %+v", s)
		}
		if !s.Enabled {
			t.Errorf("server %+v not enabled after validation", s)
		}
	}
}

func TestValidateAndPrepareGeneratesSlug(t *testing.T) {
	named := model.Server{Name: "My Home Router!", Protocol: model.ProtoUDP, Address: "192.168.1.1"}
	if err := ValidateAndPrepare(&named); err != nil {
		t.Fatal(err)
	}
	if named.ID != "my-home-router-udp" {
		t.Errorf("got ID %q, want %q", named.ID, "my-home-router-udp")
	}
	unnamed := model.Server{Protocol: model.ProtoUDP, Address: "8.8.8.8"}
	if err := ValidateAndPrepare(&unnamed); err != nil {
		t.Fatal(err)
	}
	if unnamed.ID != "8-8-8-8-udp" {
		t.Errorf("got ID %q, want %q", unnamed.ID, "8-8-8-8-udp")
	}
	doh := model.Server{Protocol: model.ProtoDoH, DoHURL: "https://dns.example.com/dns-query"}
	if err := ValidateAndPrepare(&doh); err != nil {
		t.Fatal(err)
	}
	if doh.ID != "dns-example-com-dns-query-doh" {
		t.Errorf("got ID %q, want %q", doh.ID, "dns-example-com-dns-query-doh")
	}
	existing := model.Server{ID: "keep-me", Protocol: model.ProtoUDP, Address: "8.8.4.4"}
	if err := ValidateAndPrepare(&existing); err != nil {
		t.Fatal(err)
	}
	if existing.ID != "keep-me" {
		t.Errorf("existing ID overwritten: got %q", existing.ID)
	}
}

func TestValidateAndPrepareErrors(t *testing.T) {
	cases := []struct {
		name   string
		server model.Server
	}{
		{"invalid IP", model.Server{Protocol: model.ProtoUDP, Address: "not-an-ip"}},
		{"empty address", model.Server{Protocol: model.ProtoUDP}},
		{"invalid IP for dot", model.Server{Protocol: model.ProtoDoT, Address: "999.1.1.1", TLSHostname: "dns.example.com"}},
		{"http URL", model.Server{Protocol: model.ProtoDoH, DoHURL: "http://dns.example.com/dns-query"}},
		{"empty DoH URL", model.Server{Protocol: model.ProtoDoH}},
		{"hostless DoH URL", model.Server{Protocol: model.ProtoDoH, DoHURL: "https://"}},
		{"unknown protocol", model.Server{Protocol: "quic", Address: "8.8.8.8"}},
		{"port too high", model.Server{Protocol: model.ProtoUDP, Address: "8.8.8.8", Port: 70000}},
		{"negative port", model.Server{Protocol: model.ProtoUDP, Address: "8.8.8.8", Port: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.server
			if err := ValidateAndPrepare(&s); err == nil {
				t.Errorf("expected error for %+v", tc.server)
			}
		})
	}
}

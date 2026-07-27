package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"dnsbench/internal/model"
)

func TestParseProtocolsAcceptsEverySupportedProtocol(t *testing.T) {
	got, err := parseProtocols([]string{"udp", "tcp", "dot", "doh", "doh3", "doq"})
	if err != nil {
		t.Fatalf("parseProtocols: %v", err)
	}
	want := model.AllProtocols()
	if !slices.Equal(got, want) {
		t.Errorf("protocols = %v, want %v", got, want)
	}
}

func TestParseProtocolsPreservesSelectedSubset(t *testing.T) {
	got, err := parseProtocols([]string{"doq", "doh3"})
	if err != nil {
		t.Fatalf("parseProtocols: %v", err)
	}
	want := []model.Protocol{model.ProtoDoQ, model.ProtoDoH3}
	if !slices.Equal(got, want) {
		t.Errorf("protocols = %v, want %v", got, want)
	}
}

func TestParseProtocolsRejectsUnknownProtocol(t *testing.T) {
	if _, err := parseProtocols([]string{"doh4"}); err == nil {
		t.Error("expected an error for an unknown protocol")
	}
}

func TestSelectionCommandsExposeProtocolsWithoutEncryptedOnly(t *testing.T) {
	run := newRunCmd()
	if run.Flags().Lookup("protocols") == nil {
		t.Error("run command is missing --protocols")
	}
	if run.Flags().Lookup("encrypted-only") != nil {
		t.Error("run command unexpectedly exposes --encrypted-only")
	}

	probe := newProbeCmd()
	if probe.Flags().Lookup("protocols") == nil {
		t.Error("probe command is missing --protocols")
	}
	if probe.Flags().Lookup("encrypted-only") != nil {
		t.Error("probe command unexpectedly exposes --encrypted-only")
	}
}

func TestSelectionCommandsExplainExplicitSystemProtocolOverride(t *testing.T) {
	for name, cmd := range map[string]*cobra.Command{
		"run":   newRunCmd(),
		"probe": newProbeCmd(),
	} {
		flag := cmd.Flags().Lookup("system")
		if flag == nil {
			t.Errorf("%s command is missing --system", name)
			continue
		}
		if !strings.Contains(flag.Usage, "--protocols") {
			t.Errorf("%s --system help does not explain its precedence over --protocols: %q", name, flag.Usage)
		}
	}
}

func TestExplicitSystemSourceBypassesOnlyTheProtocolFilter(t *testing.T) {
	system := []model.Server{
		{
			ID:       "system-router",
			Name:     "System router",
			Address:  "192.168.1.1",
			Port:     53,
			Protocol: model.ProtoUDP,
			Source:   model.SourceSystem,
			Enabled:  true,
		},
	}
	discover := func(context.Context) ([]model.Server, []string, []string, error) {
		return system, []string{"en0"}, nil, nil
	}
	selected, err := selectServersWithDiscovery(context.Background(), selectionFlags{
		system:    true,
		builtin:   true,
		protocols: []string{"doh3", "doq"},
	}, discover)
	if err != nil {
		t.Fatalf("selectServersWithDiscovery: %v", err)
	}
	if !slices.ContainsFunc(selected.servers, func(server model.Server) bool {
		return server.ID == "system-router"
	}) {
		t.Fatal("the system DNS resolver was removed by --protocols")
	}
	if !slices.Equal(selected.systemIDs, []string{"system-router"}) {
		t.Fatalf("system IDs = %q, want [system-router]", selected.systemIDs)
	}

	encrypted := 0
	for _, server := range selected.servers {
		if server.ID == "system-router" {
			continue
		}
		if server.Protocol != model.ProtoDoH3 && server.Protocol != model.ProtoDoQ {
			t.Errorf("non-system %s endpoint survived the protocol filter: %+v", server.Protocol, server)
		}
		encrypted++
	}
	if encrypted == 0 {
		t.Fatal("no DoH/3 or DoQ resolver remained alongside the system baseline")
	}
}

func TestExplicitSystemSourceStillHonorsNoIPv6(t *testing.T) {
	system := []model.Server{
		{ID: "system-v4", Address: "192.168.1.1", Port: 53, Protocol: model.ProtoUDP, Source: model.SourceSystem, Enabled: true},
		{ID: "system-v6", Address: "2001:db8::53", Port: 53, Protocol: model.ProtoUDP, Source: model.SourceSystem, Enabled: true},
	}
	discover := func(context.Context) ([]model.Server, []string, []string, error) {
		return system, nil, nil, nil
	}
	selected, err := selectServersWithDiscovery(context.Background(), selectionFlags{
		system:    true,
		builtin:   true,
		protocols: []string{"doh3", "doq"},
		noIPv6:    true,
	}, discover)
	if err != nil {
		t.Fatalf("selectServersWithDiscovery: %v", err)
	}
	if slices.ContainsFunc(selected.servers, func(server model.Server) bool {
		return server.IsIPv6()
	}) {
		t.Fatalf("IPv6 resolver survived --no-ipv6: %+v", selected.servers)
	}
	if !slices.Contains(selected.systemIDs, "system-v4") || slices.Contains(selected.systemIDs, "system-v6") {
		t.Fatalf("system IDs = %q, want only the IPv4 system resolver", selected.systemIDs)
	}
}

func TestSelectionCommandsExposeRepeatableServerFlag(t *testing.T) {
	commands := map[string]*cobra.Command{
		"run":   newRunCmd(),
		"probe": newProbeCmd(),
	}
	for name, cmd := range commands {
		flag := cmd.Flag("server")
		if flag == nil {
			t.Errorf("%s command is missing --server", name)
			continue
		}
		if got := flag.Value.Type(); got != "stringArray" {
			t.Errorf("%s --server type = %q, want stringArray", name, got)
		}
	}

	run := newRunCmd()
	if err := run.ParseFlags([]string{
		"--server", "https://dns.example/dns-query@192.0.2.1",
		"--server", "quic://dns.example@192.0.2.1",
	}); err != nil {
		t.Fatalf("parse repeated --server flags: %v", err)
	}
	got, err := run.Flags().GetStringArray("server")
	if err != nil {
		t.Fatalf("get --server values: %v", err)
	}
	want := []string{
		"https://dns.example/dns-query@192.0.2.1",
		"quic://dns.example@192.0.2.1",
	}
	if !slices.Equal(got, want) {
		t.Errorf("--server values = %q, want %q", got, want)
	}
}

func TestParseInlineServersSupportsEncryptedEndpoints(t *testing.T) {
	servers, err := parseInlineServers([]string{
		"https://dns.example/dns-query@192.0.2.1",
		"quic://dns.example@192.0.2.1",
	})
	if err != nil {
		t.Fatalf("parseInlineServers: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("servers = %d, want 2", len(servers))
	}

	doh := servers[0]
	if doh.Protocol != model.ProtoDoH ||
		doh.DoHURL != "https://dns.example/dns-query" ||
		doh.Address != "192.0.2.1" {
		t.Errorf("DoH endpoint parsed as %+v", doh)
	}
	doq := servers[1]
	if doq.Protocol != model.ProtoDoQ ||
		doq.TLSHostname != "dns.example" ||
		doq.Address != "192.0.2.1" {
		t.Errorf("DoQ endpoint parsed as %+v", doq)
	}
	for i, server := range servers {
		if server.Source != model.SourceUser || !server.Enabled || server.ID == "" {
			t.Errorf("server %d was not prepared as a temporary user endpoint: %+v", i, server)
		}
	}
}

func TestDistinctInlineEndpointsHaveUniqueSelectedIDs(t *testing.T) {
	discover := func(context.Context) ([]model.Server, []string, []string, error) {
		return nil, nil, nil, nil
	}
	selected, err := selectServersWithDiscovery(context.Background(), selectionFlags{
		servers: []string{
			"1.1.1.1",
			"1.1.1.1:5353",
			"https://dns.example/dns-query@192.0.2.1",
			"https://dns.example/dns-query@192.0.2.2",
		},
	}, discover)
	if err != nil {
		t.Fatalf("selectServersWithDiscovery: %v", err)
	}
	if len(selected.servers) != 4 {
		t.Fatalf("selected %d servers, want 4", len(selected.servers))
	}
	ids := make(map[string]bool, len(selected.servers))
	for _, server := range selected.servers {
		if ids[server.ID] {
			t.Errorf("duplicate selected server ID %q", server.ID)
		}
		ids[server.ID] = true
	}
}

func TestParseInlineServersRejectsInvalidOrMultipleEntries(t *testing.T) {
	for _, entries := range [][]string{
		{""},
		{"not-an-endpoint"},
		{"8.8.8.8\n1.1.1.1"},
	} {
		if _, err := parseInlineServers(entries); err == nil {
			t.Errorf("parseInlineServers(%q): expected an error", entries)
		}
	}
}

func TestInlineServersDisableImplicitSourceDefaults(t *testing.T) {
	selected, err := selectServers(context.Background(), selectionFlags{
		servers: []string{
			"https://dns.example/dns-query@192.0.2.1",
			"quic://dns.example@192.0.2.1",
		},
	})
	if err != nil {
		t.Fatalf("selectServers: %v", err)
	}
	if len(selected.servers) != 2 {
		t.Fatalf("selected %d servers, want only the 2 inline endpoints", len(selected.servers))
	}
	for _, server := range selected.servers {
		if server.Source != model.SourceUser || server.Address != "192.0.2.1" {
			t.Errorf("implicit source unexpectedly entered selection: %+v", server)
		}
	}
}

func TestInlineServersCombineWithExplicitSources(t *testing.T) {
	selected, err := selectServers(context.Background(), selectionFlags{
		builtin: true,
		servers: []string{"https://dns.example/dns-query@192.0.2.1"},
	})
	if err != nil {
		t.Fatalf("selectServers: %v", err)
	}
	if len(selected.servers) <= 1 {
		t.Fatalf("selected %d servers, want inline endpoint plus built-ins", len(selected.servers))
	}
	if selected.servers[0].DoHURL != "https://dns.example/dns-query" {
		t.Fatalf("first selected server = %+v, want inline endpoint", selected.servers[0])
	}
	if !slices.ContainsFunc(selected.servers[1:], func(server model.Server) bool {
		return server.Source == model.SourceBuiltin
	}) {
		t.Error("explicit --builtin source was not combined with inline endpoint")
	}
}

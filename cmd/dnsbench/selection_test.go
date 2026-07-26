package main

import (
	"slices"
	"testing"

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

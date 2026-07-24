package model

import "testing"

func TestScopeOfString(t *testing.T) {
	tests := []struct {
		addr string
		want Scope
	}{
		{"127.0.0.1", ScopeLoopback},
		{"10.0.0.1", ScopePrivate},
		{"172.16.0.1", ScopePrivate},
		{"172.15.255.255", ScopePublic},
		{"192.168.1.1", ScopePrivate},
		{"100.64.0.1", ScopeCGNAT},
		{"8.8.8.8", ScopePublic},
		{"::1", ScopeLoopback},
		{"fe80::1", ScopeLinkLocal},
		{"fd00::1", ScopeULA},
		{"2001:4860:4860::8888", ScopePublic},
		{"not-an-address", ScopeUnknown},
		{"169.254.1.1", ScopeLinkLocal},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got := ScopeOfString(tt.addr)
			if got != tt.want {
				t.Errorf("ScopeOfString(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestScopeIsLocal(t *testing.T) {
	locals := []Scope{ScopeLoopback, ScopePrivate, ScopeLinkLocal, ScopeULA, ScopeCGNAT}
	for _, s := range locals {
		if !s.IsLocal() {
			t.Errorf("%q.IsLocal() = false, want true", s)
		}
	}
	nonLocals := []Scope{ScopePublic, ScopeUnknown}
	for _, s := range nonLocals {
		if s.IsLocal() {
			t.Errorf("%q.IsLocal() = true, want false", s)
		}
	}
}

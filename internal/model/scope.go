package model

import "net/netip"

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

func ScopeOf(a netip.Addr) Scope {
	if !a.IsValid() {
		return ScopeUnknown
	}
	a = a.Unmap()
	if a.IsLoopback() {
		return ScopeLoopback
	}
	if a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() {
		return ScopeLinkLocal
	}
	if a.Is4() {
		if cgnatPrefix.Contains(a) {
			return ScopeCGNAT
		}
		if a.IsPrivate() {
			return ScopePrivate
		}
		return ScopePublic
	}
	if a.IsPrivate() {
		return ScopeULA
	}
	return ScopePublic
}

func ScopeOfString(addr string) Scope {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return ScopeUnknown
	}
	return ScopeOf(a)
}

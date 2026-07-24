//go:build windows

package sysdns

import (
	"context"
	"fmt"
	"os/exec"
)

func discoverEntries(ctx context.Context) ([]Entry, []string, error) {
	v4Out, v4Err := exec.CommandContext(ctx, "netsh", "interface", "ipv4", "show", "dnsservers").Output()
	v6Out, v6Err := exec.CommandContext(ctx, "netsh", "interface", "ipv6", "show", "dnsservers").Output()
	if v4Err != nil && v6Err != nil {
		return nil, nil, fmt.Errorf("running netsh interface show dnsservers: %w", v4Err)
	}
	var warnings []string
	if v4Err != nil {
		warnings = append(warnings, "netsh interface ipv4 show dnsservers failed; IPv4 DNS servers may be missing.")
	}
	if v6Err != nil {
		warnings = append(warnings, "netsh interface ipv6 show dnsservers failed; IPv6 DNS servers may be missing.")
	}
	return ParseNetshDNS(string(v4Out), string(v6Out)), warnings, nil
}

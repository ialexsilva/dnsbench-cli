//go:build linux

package sysdns

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

const resolvConfPath = "/etc/resolv.conf"

func discoverEntries(ctx context.Context) ([]Entry, []string, error) {
	content, err := os.ReadFile(resolvConfPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", resolvConfPath, err)
	}
	entries := ParseResolvConf(string(content))
	if !stubOnly(entries) {
		return entries, nil, nil
	}
	out, err := exec.CommandContext(ctx, "resolvectl", "status").Output()
	if err != nil {
		return entries, []string{"The system uses a local stub resolver and resolvectl status is unavailable, so the upstream DNS servers could not be discovered."}, nil
	}
	upstream := ParseResolvectl(string(out))
	if len(upstream) == 0 {
		return entries, []string{"The system uses a local stub resolver and resolvectl status reported no upstream DNS servers."}, nil
	}
	warning := "The system uses a local stub resolver; upstream DNS servers were discovered via resolvectl status."
	return append(entries, upstream...), []string{warning}, nil
}

func stubOnly(entries []Entry) bool {
	if len(entries) == 0 {
		return false
	}
	for _, e := range entries {
		if e.Address != "127.0.0.53" && e.Address != "127.0.0.1" {
			return false
		}
	}
	return true
}

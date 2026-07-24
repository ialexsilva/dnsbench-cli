//go:build darwin

package sysdns

import (
	"context"
	"fmt"
	"os/exec"
)

func discoverEntries(ctx context.Context) ([]Entry, []string, error) {
	out, err := exec.CommandContext(ctx, "scutil", "--dns").Output()
	if err != nil {
		return nil, nil, fmt.Errorf("running scutil --dns: %w", err)
	}
	return ParseScutilDNS(string(out)), nil, nil
}

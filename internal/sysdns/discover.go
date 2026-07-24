package sysdns

import (
	"context"
	"fmt"
	"strings"

	"dnsbench/internal/model"
)

func Discover(ctx context.Context) ([]model.Server, []string, []string, error) {
	entries, warnings, err := discoverEntries(ctx)
	if err != nil {
		return nil, nil, warnings, err
	}
	servers, ifaces := toServers(entries)
	return servers, ifaces, warnings, nil
}

func toServers(entries []Entry) ([]model.Server, []string) {
	var servers []model.Server
	var ifaces []string
	seenAddr := map[string]bool{}
	seenIface := map[string]bool{}
	for _, e := range entries {
		if e.Iface != "" && !seenIface[e.Iface] {
			seenIface[e.Iface] = true
			ifaces = append(ifaces, e.Iface)
		}
		addrKey := strings.ToLower(e.Address)
		if seenAddr[addrKey] {
			continue
		}
		seenAddr[addrKey] = true
		servers = append(servers, model.Server{
			ID:         serverID(e.Iface, e.Address),
			Name:       serverName(e.Iface, e.Address),
			Address:    e.Address,
			Port:       53,
			Protocol:   model.ProtoUDP,
			Source:     model.SourceSystem,
			Interface:  e.Iface,
			SystemRole: e.Role,
			Enabled:    true,
		})
	}
	return servers, ifaces
}

func serverID(iface, address string) string {
	parts := []string{"system"}
	if iface != "" {
		parts = append(parts, iface)
	}
	parts = append(parts, address)
	return sanitizeID(strings.Join(parts, "-"))
}

func sanitizeID(s string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func serverName(iface, address string) string {
	if iface == "" {
		return "System " + address
	}
	return fmt.Sprintf("System (%s) %s", iface, address)
}

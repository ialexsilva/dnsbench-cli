package sysdns

import (
	"net/netip"
	"strings"
)

const (
	RolePrimary   = "primary"
	RoleSecondary = "secondary"
)

type Entry struct {
	Address string
	Iface   string
	Role    string
}

type entrySet struct {
	seen     map[string]bool
	perIface map[string]int
	entries  []Entry
}

func newEntrySet() *entrySet {
	return &entrySet{seen: map[string]bool{}, perIface: map[string]int{}}
}

func (s *entrySet) add(address, iface string) {
	key := iface + "|" + strings.ToLower(address)
	if s.seen[key] {
		return
	}
	s.seen[key] = true
	role := RolePrimary
	if s.perIface[iface] > 0 {
		role = RoleSecondary
	}
	s.perIface[iface]++
	s.entries = append(s.entries, Entry{Address: address, Iface: iface, Role: role})
}

func parseAddress(token string) (string, bool) {
	token = strings.Trim(token, ",;()[]")
	if i := strings.Index(token, "#"); i >= 0 {
		token = token[:i]
	}
	if token == "" {
		return "", false
	}
	if _, err := netip.ParseAddr(token); err != nil {
		return "", false
	}
	return token, true
}

type scutilBlock struct {
	nameservers []string
	iface       string
	domain      string
	options     string
	flags       string
}

func (b scutilBlock) emit(set *entrySet) {
	if strings.Contains(b.options, "mdns") || strings.EqualFold(b.domain, "local") {
		return
	}
	for _, ns := range b.nameservers {
		if addr, ok := parseAddress(ns); ok {
			set.add(addr, b.iface)
		}
	}
}

func splitKeyValue(line string) (string, string, bool) {
	i := strings.Index(line, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
}

func ifaceFromIfIndex(value string) string {
	open := strings.Index(value, "(")
	end := strings.Index(value, ")")
	if open >= 0 && end > open {
		return value[open+1 : end]
	}
	return ""
}

func ParseScutilDNS(output string) []Entry {
	set := newEntrySet()
	var block scutilBlock
	flush := func() {
		block.emit(set)
		block = scutilBlock{}
	}
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "resolver #") || strings.HasPrefix(trimmed, "DNS configuration") {
			flush()
			continue
		}
		key, value, ok := splitKeyValue(trimmed)
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(key, "nameserver["):
			block.nameservers = append(block.nameservers, value)
		case key == "if_index":
			block.iface = ifaceFromIfIndex(value)
		case key == "interface":
			block.iface = value
		case key == "domain":
			block.domain = value
		case key == "options":
			block.options = value
		case key == "flags":
			block.flags = value
		}
	}
	flush()
	return set.entries
}

func ParseResolvConf(content string) []Entry {
	set := newEntrySet()
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		if addr, ok := parseAddress(fields[1]); ok {
			set.add(addr, "")
		}
	}
	return set.entries
}

func linkName(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "Link ")
	if !ok {
		return "", false
	}
	open := strings.Index(rest, "(")
	end := strings.LastIndex(rest, ")")
	if open < 0 || end <= open {
		return "", false
	}
	num := strings.TrimSpace(rest[:open])
	if num == "" {
		return "", false
	}
	for _, r := range num {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return rest[open+1 : end], true
}

func addAddressTokens(set *entrySet, line, iface string) {
	for _, field := range strings.Fields(line) {
		if addr, ok := parseAddress(field); ok {
			set.add(addr, iface)
		}
	}
}

func lineIsOnlyAddresses(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		if _, ok := parseAddress(field); !ok {
			return false
		}
	}
	return true
}

func ParseResolvectl(output string) []Entry {
	set := newEntrySet()
	iface := ""
	inServers := false
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			inServers = false
			continue
		}
		if name, ok := linkName(trimmed); ok {
			iface = name
			inServers = false
			continue
		}
		if trimmed == "Global" {
			iface = ""
			inServers = false
			continue
		}
		if value, ok := strings.CutPrefix(trimmed, "Current DNS Server:"); ok {
			if addr, ok := parseAddress(strings.TrimSpace(value)); ok {
				set.add(addr, iface)
			}
			inServers = false
			continue
		}
		if value, ok := strings.CutPrefix(trimmed, "DNS Servers:"); ok {
			addAddressTokens(set, value, iface)
			inServers = true
			continue
		}
		if inServers && lineIsOnlyAddresses(trimmed) {
			addAddressTokens(set, trimmed, iface)
			continue
		}
		inServers = false
	}
	return set.entries
}

func quotedName(line string) (string, bool) {
	first := strings.Index(line, `"`)
	if first < 0 {
		return "", false
	}
	rest := line[first+1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

func parseNetshOutput(set *entrySet, output string) {
	iface := ""
	for _, line := range strings.Split(output, "\n") {
		if name, ok := quotedName(line); ok {
			iface = name
			continue
		}
		if iface == "" {
			continue
		}
		addAddressTokens(set, line, iface)
	}
}

func ParseNetshDNS(v4Output, v6Output string) []Entry {
	set := newEntrySet()
	parseNetshOutput(set, v4Output)
	parseNetshOutput(set, v6Output)
	return set.entries
}

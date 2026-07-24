package model

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed cached_domains.json
var cachedDomainsJSON []byte

type cachedDomainFile struct {
	Source   string   `json:"source"`
	Snapshot string   `json:"snapshot"`
	Notes    string   `json:"notes"`
	Domains  []string `json:"domains"`
}

var defaultCachedDomains = mustDecodeCachedDomains(cachedDomainsJSON)

func mustDecodeCachedDomains(data []byte) []string {
	domains, err := decodeCachedDomains(data)
	if err != nil {
		panic(fmt.Sprintf("built-in cached domain list is invalid: %v", err))
	}
	return domains
}

func decodeCachedDomains(data []byte) ([]string, error) {
	var file cachedDomainFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	if len(file.Domains) == 0 {
		return nil, fmt.Errorf("domains must not be empty")
	}

	domains := make([]string, len(file.Domains))
	seen := make(map[string]bool, len(file.Domains))
	for i, domain := range file.Domains {
		canonical := strings.ToLower(strings.TrimSpace(domain))
		if domain != canonical {
			return nil, fmt.Errorf("domain %d %q must be lowercase without surrounding whitespace", i, domain)
		}
		if !validCachedDomain(domain) {
			return nil, fmt.Errorf("domain %d %q is invalid", i, domain)
		}
		if seen[domain] {
			return nil, fmt.Errorf("domain %d %q is duplicated", i, domain)
		}
		seen[domain] = true
		domains[i] = domain
	}
	return domains, nil
}

func validCachedDomain(domain string) bool {
	if strings.ContainsAny(domain, " /:") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return len(domain) <= 253
}

func DefaultCachedDomains() []string {
	out := make([]string, len(defaultCachedDomains))
	copy(out, defaultCachedDomains)
	return out
}

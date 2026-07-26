package model

import (
	"strings"
	"testing"
)

func TestDefaultBenchConfigUsesPersistentSession(t *testing.T) {
	cfg := DefaultBenchConfig(ModeStandard)
	if cfg.Session != SessionPersistent {
		t.Fatalf("Session = %q, want %q", cfg.Session, SessionPersistent)
	}
}

func TestDefaultCachedDomainsAreCuratedTopDomains(t *testing.T) {
	domains := DefaultCachedDomains()
	if got := len(domains); got < 50 {
		t.Fatalf("len(DefaultCachedDomains()) = %d, want a representative popular-domain set", got)
	}

	seen := make(map[string]bool, len(domains))
	for _, domain := range domains {
		if domain == "" || strings.ContainsAny(domain, " /:") || !strings.Contains(domain, ".") {
			t.Errorf("invalid cached domain %q", domain)
		}
		if seen[domain] {
			t.Errorf("duplicate cached domain %q", domain)
		}
		seen[domain] = true
	}

	for _, want := range []string{
		"facebook.com", "chatgpt.com", "wikipedia.org", "amazon.com",
		"netflix.com", "github.com", "bbc.com", "linkedin.com",
		// A Brazilian brand on a generic TLD stays; only the .br TLD is excluded.
		"globo.com",
	} {
		if !seen[want] {
			t.Errorf("popular cached domain %q is missing", want)
		}
	}
	for _, excluded := range []string{
		// adult and gambling
		"bet.br", "pornhub.com", "xvideos.com", "onlyfans.com", "theporndude.com",
		// owned by a resolver dnsbench benchmarks
		"cloudflare.com", "google.com", "youtube.com", "gemini.google.com",
		"labs.google", "cisco.com", "opendns.com", "digicert.com", "comodo.com",
		"gcore.com", "nextdns.io", "adguard.com", "quad9.net", "controld.com",
		// russian and region-specific asian services
		"yandex.ru", "yandex.com", "yahoo.co.jp", "baidu.com", "naver.com",
		"bilibili.com", "rakuten.co.jp", "vk.com", "mail.ru",
	} {
		if seen[excluded] {
			t.Errorf("excluded domain %q must not be in the latency set", excluded)
		}
	}
	// The set is a global ranking, so every country-edition domain is out,
	// including European storefronts. Generic-in-practice TLDs (.ai, .io, .me,
	// .tv, .us) stay, because those are the service's global home rather than a
	// national edition.
	for domain := range seen {
		for _, suffix := range []string{".br", ".de", ".it", ".uk", ".pt", ".es", ".fr", ".ru", ".jp", ".su", ".vn", ".in"} {
			if strings.HasSuffix(domain, suffix) {
				t.Errorf("country-edition domain %q must not be in the global latency set", domain)
			}
		}
	}
}

func TestQueryResultValidForCategory(t *testing.T) {
	a := QueryResult{Rcode: 0, Answers: []RR{{Type: "a", Data: "192.0.2.1"}}}
	if !a.ValidFor(CatCached) || !a.ValidFor(CatUncached) {
		t.Fatal("A answer should be valid for cached and uncached categories")
	}
	cname := QueryResult{Rcode: 0, Answers: []RR{{Type: "cname", Data: "target.example."}}}
	if !cname.ValidFor(CatCached) {
		t.Fatal("CNAME answer should be valid for cached category")
	}
	if (QueryResult{Rcode: 0}).ValidFor(CatCached) {
		t.Fatal("empty NOERROR response should not be valid for cached category")
	}
	if !(QueryResult{Rcode: 3}).ValidFor(CatTLD) {
		t.Fatal("NXDOMAIN should be valid for TLD category")
	}
	if (QueryResult{Rcode: 2}).ValidFor(CatTLD) {
		t.Fatal("SERVFAIL should not be valid for TLD category")
	}
}

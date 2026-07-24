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
		"netflix.com", "github.com", "bbc.com", "mercadolivre.com.br",
		"gov.br", "uol.com.br", "correios.com.br", "reclameaqui.com.br",
	} {
		if !seen[want] {
			t.Errorf("popular cached domain %q is missing", want)
		}
	}
	for _, excluded := range []string{
		"bet.br", "pornhub.com", "xvideos.com", "onlyfans.com",
		"cloudflare.com", "google.com", "youtube.com", "gemini.google.com",
		"google.com.br", "labs.google", "yandex.ru", "yahoo.co.jp",
		"baidu.com", "naver.com", "bilibili.com",
	} {
		if seen[excluded] {
			t.Errorf("excluded domain %q must not be in the latency set", excluded)
		}
	}
	for domain := range seen {
		if strings.HasSuffix(domain, ".ru") || strings.HasSuffix(domain, ".jp") {
			t.Errorf("region-specific domain %q must not be in the Brazilian latency set", domain)
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

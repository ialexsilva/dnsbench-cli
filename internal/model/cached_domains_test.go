package model

import (
	"encoding/json"
	"testing"
)

func TestDecodeCachedDomainsRejectsInvalidFiles(t *testing.T) {
	tests := map[string]string{
		"malformed JSON": `{`,
		"empty list":     `{"domains":[]}`,
		"uppercase":      `{"domains":["Example.com"]}`,
		"whitespace":     `{"domains":[" example.com"]}`,
		"duplicate":      `{"domains":["example.com","example.com"]}`,
		"missing dot":    `{"domains":["localhost"]}`,
		"invalid label":  `{"domains":["-bad.example"]}`,
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeCachedDomains([]byte(input)); err == nil {
				t.Fatalf("decodeCachedDomains(%q) unexpectedly succeeded", input)
			}
		})
	}
}

func TestDefaultCachedDomainsReturnsIndependentCopy(t *testing.T) {
	first := DefaultCachedDomains()
	first[0] = "changed.example"

	second := DefaultCachedDomains()
	if second[0] == first[0] {
		t.Fatal("DefaultCachedDomains returned shared mutable storage")
	}
}

func TestEmbeddedCachedDomainMetadata(t *testing.T) {
	if _, err := decodeCachedDomains(cachedDomainsJSON); err != nil {
		t.Fatalf("embedded cached domain file is invalid: %v", err)
	}
	var file cachedDomainFile
	if err := json.Unmarshal(cachedDomainsJSON, &file); err != nil {
		t.Fatalf("decode embedded metadata: %v", err)
	}
	if file.Source == "" || file.Snapshot == "" || file.Notes == "" {
		t.Fatalf("embedded metadata is incomplete: %+v", file)
	}
}

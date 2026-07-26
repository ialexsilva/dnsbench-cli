package probe

import (
	"time"

	"dnsbench/internal/transport"
)

type Config struct {
	ReachabilityDomain string
	SignedDomain       string
	BogusDomain        string
	UnsignedDomain     string
	NXTLDZone          string
	UncachedZone       string
	Timeout            time.Duration
	Retries            int
	Concurrency        int
	Extended           bool
	Factory            transport.Factory
	OnResult           func()
}

func DefaultConfig() Config {
	return Config{
		ReachabilityDomain: "google.com",
		SignedDomain:       "cloudflare.com",
		BogusDomain:        "dnssec-failed.org",
		UnsignedDomain:     "github.com",
		NXTLDZone:          "com",
		Timeout:            3 * time.Second,
		Retries:            1,
		Concurrency:        8,
		Extended:           false,
	}
}

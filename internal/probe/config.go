package probe

import (
	"time"

	"dnsbench/internal/transport"
)

type RebindCase struct {
	Host     string
	Expected string
}

type Config struct {
	ReachabilityDomain string
	SignedDomain       string
	BogusDomain        string
	UnsignedDomain     string
	RebindV4           []RebindCase
	RebindV6           []RebindCase
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
		RebindV4: []RebindCase{
			{Host: "127.0.0.1.sslip.io", Expected: "127.0.0.1"},
			{Host: "10.10.10.10.sslip.io", Expected: "10.10.10.10"},
			{Host: "172.16.1.1.sslip.io", Expected: "172.16.1.1"},
			{Host: "192.168.1.1.sslip.io", Expected: "192.168.1.1"},
		},
		RebindV6: []RebindCase{
			{Host: "--1.sslip.io", Expected: "::1"},
			{Host: "fd00--1.sslip.io", Expected: "fd00::1"},
		},
		NXTLDZone:   "com",
		Timeout:     3 * time.Second,
		Retries:     1,
		Concurrency: 8,
		Extended:    false,
	}
}

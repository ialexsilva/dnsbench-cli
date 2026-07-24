package transport

import (
	"context"
	"crypto/tls"
	"time"

	"dnsbench/internal/model"
)

type Question struct {
	Name       string
	Qtype      uint16
	DO         bool
	CD         bool
	NoEDNS     bool
	UDPBufSize uint16
}

type Options struct {
	Timeout    time.Duration
	Persistent bool
	TLSConfig  *tls.Config
}

type Querier interface {
	Query(ctx context.Context, q Question) model.QueryResult
	Protocol() model.Protocol
	Close() error
}

type Factory func(s model.Server, o Options) (Querier, error)

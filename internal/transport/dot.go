package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"time"

	"dnsbench/internal/model"
)

func newDoTQuerier(s model.Server, o Options) *streamQuerier {
	return &streamQuerier{
		proto: model.ProtoDoT,
		opts:  o,
		dial:  dotDialFunc(s, o),
	}
}

func dotDialFunc(s model.Server, o Options) func(ctx context.Context) (net.Conn, model.QueryPhases, error) {
	endpoint := s.Endpoint()
	serverName := s.TLSHostname
	if serverName == "" {
		serverName = s.Address
	}
	return func(ctx context.Context) (net.Conn, model.QueryPhases, error) {
		var ph model.QueryPhases
		var d net.Dialer
		dialStart := time.Now()
		raw, err := d.DialContext(ctx, "tcp", endpoint)
		if err != nil {
			return nil, ph, err
		}
		ph.Connect = time.Since(dialStart)
		cfg := &tls.Config{}
		if o.TLSConfig != nil {
			cfg = o.TLSConfig.Clone()
		}
		cfg.ServerName = serverName
		tlsConn := tls.Client(raw, cfg)
		handshakeStart := time.Now()
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, ph, tlsHandshakeError(err)
		}
		ph.TLSHandshake = time.Since(handshakeStart)
		return tlsConn, ph, nil
	}
}

func tlsHandshakeError(err error) error {
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, os.ErrDeadlineExceeded) {
		return err
	}
	return &model.QueryError{Kind: model.ErrTLS, Msg: err.Error()}
}

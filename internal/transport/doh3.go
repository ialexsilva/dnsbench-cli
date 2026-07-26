package transport

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"

	"dnsbench/internal/model"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func newDoH3Querier(s model.Server, o Options) *dohQuerier {
	var tlsCfg *tls.Config
	if o.TLSConfig != nil {
		tlsCfg = o.TLSConfig.Clone()
	}
	tr := &http3.Transport{
		TLSClientConfig: tlsCfg,
		QUICConfig:      &quic.Config{},
	}
	closer := tr.Close
	if s.Address != "" {
		dialer := &quicDialer{}
		pinned := pinnedHostPort(s)
		tr.Dial = func(ctx context.Context, addr string, cfg *tls.Config, qcfg *quic.Config) (*quic.Conn, error) {
			udpAddr, err := net.ResolveUDPAddr("udp", pinned(addr))
			if err != nil {
				return nil, err
			}
			return dialer.dialTraced(ctx, udpAddr, cfg, qcfg)
		}
		closer = func() error {
			err := tr.Close()
			if cerr := dialer.close(); err == nil {
				err = cerr
			}
			return err
		}
	}
	return &dohQuerier{
		proto:  model.ProtoDoH3,
		url:    s.DoHURL,
		opts:   o,
		client: &http.Client{Transport: tr},
		closer: closer,
	}
}

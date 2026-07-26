package transport

import (
	"context"
	"crypto/tls"
	"net"
	"net/http/httptrace"
	"sync"

	"github.com/quic-go/quic-go"
)

type quicDialer struct {
	mu   sync.Mutex
	pc   net.PacketConn
	tr   *quic.Transport
	dead bool
}

func (d *quicDialer) transport() (*quic.Transport, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dead {
		return nil, net.ErrClosed
	}
	if d.tr != nil {
		return d.tr, nil
	}
	pc, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	d.pc = pc
	d.tr = &quic.Transport{Conn: pc}
	return d.tr, nil
}

func (d *quicDialer) dial(ctx context.Context, addr *net.UDPAddr, tlsCfg *tls.Config, qcfg *quic.Config) (*quic.Conn, error) {
	tr, err := d.transport()
	if err != nil {
		return nil, err
	}
	return tr.Dial(ctx, addr, tlsCfg, qcfg)
}

// A custom HTTP/3 dial bypasses quic-go's trace hooks, so pinned dials emit them here.
func (d *quicDialer) dialTraced(ctx context.Context, addr *net.UDPAddr, tlsCfg *tls.Config, qcfg *quic.Config) (*quic.Conn, error) {
	trace := httptrace.ContextClientTrace(ctx)
	if trace != nil && trace.TLSHandshakeStart != nil {
		trace.TLSHandshakeStart()
	}
	tr, err := d.transport()
	if err != nil {
		return nil, err
	}
	conn, err := tr.DialEarly(ctx, addr, tlsCfg, qcfg)
	if trace != nil && trace.TLSHandshakeDone != nil {
		var state tls.ConnectionState
		if conn != nil {
			state = conn.ConnectionState().TLS
		}
		trace.TLSHandshakeDone(state, err)
	}
	return conn, err
}

func (d *quicDialer) close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dead = true
	if d.tr == nil {
		return nil
	}
	err := d.tr.Close()
	if cerr := d.pc.Close(); err == nil {
		err = cerr
	}
	d.tr, d.pc = nil, nil
	return err
}

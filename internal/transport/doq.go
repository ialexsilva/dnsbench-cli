package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"dnsbench/internal/model"

	"github.com/miekg/dns"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

const doqALPN = "doq"

// RFC 9250 §4.3 error codes.
const (
	doqNoError          quic.ApplicationErrorCode = 0x0
	doqInternalError    quic.ApplicationErrorCode = 0x1
	doqProtocolError    quic.ApplicationErrorCode = 0x2
	doqRequestCancelled quic.ApplicationErrorCode = 0x3
)

const doqPaddingBlockSize = 128

type doqQuerier struct {
	opts   Options
	addr   *net.UDPAddr
	tlsCfg *tls.Config
	dialer *quicDialer
	mu     sync.Mutex
	conn   *quic.Conn
}

func newDoQQuerier(s model.Server, o Options) (*doqQuerier, error) {
	addr, err := net.ResolveUDPAddr("udp", s.Endpoint())
	if err != nil {
		return nil, err
	}
	tlsCfg := &tls.Config{}
	if o.TLSConfig != nil {
		tlsCfg = o.TLSConfig.Clone()
	}
	tlsCfg.NextProtos = []string{doqALPN}
	if tlsCfg.ServerName == "" {
		tlsCfg.ServerName = s.TLSHostname
		if tlsCfg.ServerName == "" {
			tlsCfg.ServerName = s.Address
		}
	}
	return &doqQuerier{opts: o, addr: addr, tlsCfg: tlsCfg, dialer: &quicDialer{}}, nil
}

func (d *doqQuerier) Protocol() model.Protocol { return model.ProtoDoQ }

func (d *doqQuerier) Close() error {
	d.mu.Lock()
	d.dropConn(doqNoError)
	d.mu.Unlock()
	return d.dialer.close()
}

// Caller must hold d.mu.
func (d *doqQuerier) dropConn(code quic.ApplicationErrorCode) {
	if d.conn != nil {
		d.conn.CloseWithError(code, "")
		d.conn = nil
	}
}

func (d *doqQuerier) Query(ctx context.Context, q Question) model.QueryResult {
	ctx, cancel := withTimeout(ctx, d.opts.Timeout)
	defer cancel()
	d.mu.Lock()
	defer d.mu.Unlock()
	start := time.Now()
	msg := buildDoQMsg(q)
	if d.opts.Persistent && d.conn != nil {
		queryStart := time.Now()
		resp, size, err := exchangeDoQ(ctx, d.conn, msg)
		ph := model.QueryPhases{Query: time.Since(queryStart)}
		if err == nil {
			return d.finish(start, ph, resp, size, true)
		}
		drop, code := doqConnectionFailure(ctx, err)
		if drop {
			d.dropConn(code)
		}
		if !drop || ctx.Err() != nil || isProtocolErr(err) {
			return errorResult(start, ph, classifyQUIC(ctx, err))
		}
	}
	var ph model.QueryPhases
	handshakeStart := time.Now()
	conn, err := d.dialer.dial(ctx, d.addr, d.tlsCfg, &quic.Config{})
	if err != nil {
		return errorResult(start, ph, classifyQUIC(ctx, err))
	}
	// QUIC reports its combined transport/TLS handshake as TLS.
	ph.TLSHandshake = time.Since(handshakeStart)
	if d.opts.Persistent {
		// A canceled stream must not discard the healthy connection.
		d.conn = conn
	}
	queryStart := time.Now()
	resp, size, err := exchangeDoQ(ctx, conn, msg)
	ph.Query = time.Since(queryStart)
	if err != nil {
		drop, code := doqConnectionFailure(ctx, err)
		if d.opts.Persistent {
			if drop {
				d.dropConn(code)
			}
		} else if drop {
			conn.CloseWithError(code, "")
		} else {
			conn.CloseWithError(doqNoError, "")
		}
		return errorResult(start, ph, classifyQUIC(ctx, err))
	}
	if !d.opts.Persistent {
		conn.CloseWithError(doqNoError, "")
	}
	return d.finish(start, ph, resp, size, false)
}

func (d *doqQuerier) finish(start time.Time, ph model.QueryPhases, resp *dns.Msg, size int, reused bool) model.QueryResult {
	res := resultFromMsg(resp, size)
	res.Phases = ph
	res.Reused = reused
	res.RTT = time.Since(start)
	return res
}

// buildDoQMsg pads EDNS-enabled queries to 128-byte blocks (RFC 9250 §5.4).
func buildDoQMsg(q Question) *dns.Msg {
	msg := buildMsg(q, true)
	opt := msg.IsEdns0()
	if opt == nil {
		return msg
	}
	wire, err := msg.Pack()
	if err != nil {
		return msg
	}
	const optionHeaderSize = 4
	paddingLen := (doqPaddingBlockSize - (len(wire)+optionHeaderSize)%doqPaddingBlockSize) % doqPaddingBlockSize
	opt.Option = append(opt.Option, &dns.EDNS0_PADDING{Padding: make([]byte, paddingLen)})
	return msg
}

// exchangeDoQ requires one FIN-terminated response per stream (RFC 9250 §4.2).
func exchangeDoQ(ctx context.Context, conn *quic.Conn, msg *dns.Msg) (*dns.Msg, int, error) {
	str, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, 0, err
	}
	stopCancel := context.AfterFunc(ctx, func() {
		str.CancelWrite(quic.StreamErrorCode(doqRequestCancelled))
		str.CancelRead(quic.StreamErrorCode(doqRequestCancelled))
	})
	defer stopCancel()
	if deadline, ok := ctx.Deadline(); ok {
		str.SetDeadline(deadline)
	}
	if err := writeFramed(str, msg); err != nil {
		cancelDoQStream(str, doqStreamFailureCode(ctx, err))
		return nil, 0, err
	}
	if err := str.Close(); err != nil {
		cancelDoQStream(str, doqStreamFailureCode(ctx, err))
		return nil, 0, err
	}
	resp, size, err := readFramed(str)
	if err != nil {
		err = doqResponseReadError(ctx, err)
		cancelDoQStream(str, doqStreamFailureCode(ctx, err))
		return nil, 0, err
	}
	if err := readDoQFIN(ctx, str); err != nil {
		cancelDoQStream(str, doqStreamFailureCode(ctx, err))
		return nil, 0, err
	}
	if resp.Id != msg.Id {
		err := idMismatchErr()
		cancelDoQStream(str, quic.StreamErrorCode(doqProtocolError))
		return nil, 0, err
	}
	return resp, size, nil
}

func cancelDoQStream(str *quic.Stream, code quic.StreamErrorCode) {
	str.CancelWrite(code)
	str.CancelRead(code)
}

func doqStreamFailureCode(ctx context.Context, err error) quic.StreamErrorCode {
	if isProtocolErr(err) {
		return quic.StreamErrorCode(doqProtocolError)
	}
	if doqRequestWasCancelled(ctx, err) {
		return quic.StreamErrorCode(doqRequestCancelled)
	}
	return quic.StreamErrorCode(doqInternalError)
}

func doqResponseReadError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return err
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return &model.QueryError{Kind: model.ErrProtocol, Msg: "server sent an incomplete DNS response"}
	}
	return err
}

func readDoQFIN(ctx context.Context, str *quic.Stream) error {
	var trailing [1]byte
	for {
		n, err := str.Read(trailing[:])
		if n > 0 {
			return &model.QueryError{Kind: model.ErrProtocol, Msg: "server sent data after the DNS response"}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err == nil {
			continue
		}
		if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
			return context.Canceled
		}
		return &model.QueryError{
			Kind: model.ErrProtocol,
			Msg:  "server did not terminate the DNS response stream with FIN: " + err.Error(),
		}
	}
}

// Request cancellation is stream-scoped; connection failures evict the cached session.
func doqConnectionFailure(ctx context.Context, err error) (drop bool, code quic.ApplicationErrorCode) {
	if isProtocolErr(err) {
		return true, doqProtocolError
	}
	if isQUICConnectionError(err) {
		return true, doqInternalError
	}
	if doqRequestWasCancelled(ctx, err) {
		return false, doqNoError
	}
	var streamErr *quic.StreamError
	if errors.As(err, &streamErr) {
		return false, doqNoError
	}
	return true, doqInternalError
}

func doqRequestWasCancelled(ctx context.Context, err error) bool {
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isDeadlineErr(err) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func isQUICConnectionError(err error) bool {
	var transportErr *quic.TransportError
	var appErr *quic.ApplicationError
	var idleErr *quic.IdleTimeoutError
	var handshakeErr *quic.HandshakeTimeoutError
	var resetErr *quic.StatelessResetError
	var versionErr *quic.VersionNegotiationError
	return errors.As(err, &transportErr) ||
		errors.As(err, &appErr) ||
		errors.As(err, &idleErr) ||
		errors.As(err, &handshakeErr) ||
		errors.As(err, &resetErr) ||
		errors.As(err, &versionErr) ||
		errors.Is(err, net.ErrClosed)
}

func classifyQUIC(ctx context.Context, err error) *model.QueryError {
	if isProtocolErr(err) {
		return classify(ctx, err)
	}
	if ctx != nil && ctx.Err() != nil {
		return classify(ctx, ctx.Err())
	}
	var transportErr *quic.TransportError
	if errors.As(err, &transportErr) && transportErr.ErrorCode.IsCryptoError() {
		return &model.QueryError{Kind: model.ErrTLS, Msg: err.Error()}
	}
	var h3Err *http3.Error
	if errors.As(err, &h3Err) {
		return &model.QueryError{Kind: model.ErrProtocol, Msg: err.Error()}
	}
	var appErr *quic.ApplicationError
	if errors.As(err, &appErr) {
		return &model.QueryError{Kind: model.ErrProtocol, Msg: err.Error()}
	}
	var streamErr *quic.StreamError
	if errors.As(err, &streamErr) {
		return &model.QueryError{Kind: model.ErrProtocol, Msg: err.Error()}
	}
	return classify(ctx, err)
}

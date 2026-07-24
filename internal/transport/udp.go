package transport

import (
	"context"
	"net"
	"sync"
	"time"

	"dnsbench/internal/model"

	"github.com/miekg/dns"
)

type udpQuerier struct {
	server model.Server
	opts   Options
	mu     sync.Mutex
	conn   net.Conn
}

func newUDPQuerier(s model.Server, o Options) *udpQuerier {
	return &udpQuerier{server: s, opts: o}
}

func (u *udpQuerier) Protocol() model.Protocol { return model.ProtoUDP }

func (u *udpQuerier) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.conn == nil {
		return nil
	}
	err := u.conn.Close()
	u.conn = nil
	return err
}

func (u *udpQuerier) Query(ctx context.Context, q Question) model.QueryResult {
	ctx, cancel := withTimeout(ctx, u.opts.Timeout)
	defer cancel()
	if u.opts.Persistent {
		return u.queryPersistent(ctx, q)
	}
	return u.querySingle(ctx, q)
}

func (u *udpQuerier) querySingle(ctx context.Context, q Question) model.QueryResult {
	start := time.Now()
	msg := buildMsg(q, false)
	endpoint := u.server.Endpoint()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", endpoint)
	if err != nil {
		return errorResult(start, model.QueryPhases{}, classify(ctx, err))
	}
	applyDeadline(ctx, conn)
	resp, size, err := exchangeUDP(conn, msg)
	conn.Close()
	if err != nil {
		return errorResult(start, model.QueryPhases{}, classify(ctx, err))
	}
	if resp.Id != msg.Id {
		return errorResult(start, model.QueryPhases{}, idMismatchErr())
	}
	if resp.Truncated {
		return u.retryOverTCP(ctx, start, msg, endpoint)
	}
	res := resultFromMsg(resp, size)
	res.RTT = time.Since(start)
	res.Phases.Query = res.RTT
	return res
}

func (u *udpQuerier) queryPersistent(ctx context.Context, q Question) model.QueryResult {
	u.mu.Lock()
	defer u.mu.Unlock()
	start := time.Now()
	msg := buildMsg(q, false)
	endpoint := u.server.Endpoint()
	reused := u.conn != nil
	if u.conn == nil {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "udp", endpoint)
		if err != nil {
			return errorResult(start, model.QueryPhases{}, classify(ctx, err))
		}
		u.conn = conn
	}
	applyDeadline(ctx, u.conn)
	resp, size, err := exchangeUDPDraining(u.conn, msg)
	if err != nil {
		if !isDeadlineErr(err) {
			u.conn.Close()
			u.conn = nil
		}
		return errorResult(start, model.QueryPhases{}, classify(ctx, err))
	}
	if resp.Truncated {
		return u.retryOverTCP(ctx, start, msg, endpoint)
	}
	res := resultFromMsg(resp, size)
	res.Reused = reused
	res.RTT = time.Since(start)
	res.Phases.Query = res.RTT
	return res
}

func (u *udpQuerier) retryOverTCP(ctx context.Context, start time.Time, msg *dns.Msg, endpoint string) model.QueryResult {
	var d net.Dialer
	dialStart := time.Now()
	conn, err := d.DialContext(ctx, "tcp", endpoint)
	connectTime := time.Since(dialStart)
	ph := model.QueryPhases{Connect: connectTime}
	if err != nil {
		res := errorResult(start, ph, classify(ctx, err))
		res.UsedTCP = true
		return res
	}
	defer conn.Close()
	applyDeadline(ctx, conn)
	resp, size, err := exchangeStream(conn, msg)
	if err != nil {
		res := errorResult(start, ph, classify(ctx, err))
		res.UsedTCP = true
		return res
	}
	if resp.Id != msg.Id {
		res := errorResult(start, ph, idMismatchErr())
		res.UsedTCP = true
		return res
	}
	res := resultFromMsg(resp, size)
	res.UsedTCP = true
	res.RTT = time.Since(start)
	res.Phases.Connect = connectTime
	res.Phases.Query = res.RTT - connectTime
	return res
}

type streamQuerier struct {
	proto model.Protocol
	opts  Options
	dial  func(ctx context.Context) (net.Conn, model.QueryPhases, error)
	mu    sync.Mutex
	conn  net.Conn
}

func newTCPQuerier(s model.Server, o Options) *streamQuerier {
	endpoint := s.Endpoint()
	return &streamQuerier{
		proto: model.ProtoTCP,
		opts:  o,
		dial: func(ctx context.Context) (net.Conn, model.QueryPhases, error) {
			var ph model.QueryPhases
			var d net.Dialer
			dialStart := time.Now()
			conn, err := d.DialContext(ctx, "tcp", endpoint)
			if err != nil {
				return nil, ph, err
			}
			ph.Connect = time.Since(dialStart)
			return conn, ph, nil
		},
	}
}

func (s *streamQuerier) Protocol() model.Protocol { return s.proto }

func (s *streamQuerier) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}

func (s *streamQuerier) Query(ctx context.Context, q Question) model.QueryResult {
	ctx, cancel := withTimeout(ctx, s.opts.Timeout)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	start := time.Now()
	msg := buildMsg(q, false)
	if s.opts.Persistent && s.conn != nil {
		applyDeadline(ctx, s.conn)
		resp, size, err := exchangeStream(s.conn, msg)
		if err == nil {
			return s.finish(start, model.QueryPhases{Query: time.Since(start)}, msg, resp, size, true)
		}
		s.conn.Close()
		s.conn = nil
		if ctx.Err() != nil || isProtocolErr(err) {
			return errorResult(start, model.QueryPhases{Query: time.Since(start)}, classify(ctx, err))
		}
	}
	conn, ph, err := s.dial(ctx)
	if err != nil {
		return errorResult(start, ph, classify(ctx, err))
	}
	applyDeadline(ctx, conn)
	queryStart := time.Now()
	resp, size, err := exchangeStream(conn, msg)
	ph.Query = time.Since(queryStart)
	if err != nil {
		conn.Close()
		return errorResult(start, ph, classify(ctx, err))
	}
	if s.opts.Persistent {
		s.conn = conn
	} else {
		conn.Close()
	}
	return s.finish(start, ph, msg, resp, size, false)
}

func (s *streamQuerier) finish(start time.Time, ph model.QueryPhases, msg *dns.Msg, resp *dns.Msg, size int, reused bool) model.QueryResult {
	if resp.Id != msg.Id {
		if s.conn != nil {
			s.conn.Close()
			s.conn = nil
		}
		return errorResult(start, ph, idMismatchErr())
	}
	res := resultFromMsg(resp, size)
	res.Phases = ph
	res.Reused = reused
	res.RTT = time.Since(start)
	return res
}

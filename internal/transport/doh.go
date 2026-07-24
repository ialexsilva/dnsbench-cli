package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync"
	"time"

	"dnsbench/internal/model"

	"github.com/miekg/dns"
)

const dohContentType = "application/dns-message"

type dohQuerier struct {
	url    string
	opts   Options
	client *http.Client
}

func newDoHQuerier(s model.Server, o Options) *dohQuerier {
	var tlsCfg *tls.Config
	if o.TLSConfig != nil {
		tlsCfg = o.TLSConfig.Clone()
	}
	tr := &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   tlsCfg,
		DisableKeepAlives: !o.Persistent,
	}
	if s.Address != "" {
		targetHost := s.Address
		portOverride := s.Port
		var d net.Dialer
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				port = strconv.Itoa(model.ProtoDoH.DefaultPort())
			}
			if portOverride != 0 {
				port = strconv.Itoa(portOverride)
			}
			return d.DialContext(ctx, network, net.JoinHostPort(targetHost, port))
		}
	}
	return &dohQuerier{
		url:    s.DoHURL,
		opts:   o,
		client: &http.Client{Transport: tr},
	}
}

func (d *dohQuerier) Protocol() model.Protocol { return model.ProtoDoH }

func (d *dohQuerier) Close() error {
	d.client.CloseIdleConnections()
	return nil
}

func (d *dohQuerier) Query(ctx context.Context, q Question) model.QueryResult {
	ctx, cancel := withTimeout(ctx, d.opts.Timeout)
	defer cancel()
	start := time.Now()
	msg := buildMsg(q, true)
	body, err := msg.Pack()
	if err != nil {
		return errorResult(start, model.QueryPhases{}, malformedQueryErr(err))
	}
	var (
		mu           sync.Mutex
		ph           model.QueryPhases
		connectStart time.Time
		tlsStart     time.Time
		gotConnAt    time.Time
		wroteAt      time.Time
		reused       bool
	)
	trace := &httptrace.ClientTrace{
		ConnectStart: func(string, string) {
			mu.Lock()
			connectStart = time.Now()
			mu.Unlock()
		},
		ConnectDone: func(string, string, error) {
			mu.Lock()
			if !connectStart.IsZero() {
				ph.Connect = time.Since(connectStart)
			}
			mu.Unlock()
		},
		TLSHandshakeStart: func() {
			mu.Lock()
			tlsStart = time.Now()
			mu.Unlock()
		},
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			mu.Lock()
			if !tlsStart.IsZero() {
				ph.TLSHandshake = time.Since(tlsStart)
			}
			mu.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			mu.Lock()
			gotConnAt = time.Now()
			reused = info.Reused
			mu.Unlock()
		},
		WroteRequest: func(httptrace.WroteRequestInfo) {
			mu.Lock()
			wroteAt = time.Now()
			if !gotConnAt.IsZero() {
				ph.HTTPSetup = wroteAt.Sub(gotConnAt)
			}
			mu.Unlock()
		},
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodPost, d.url, bytes.NewReader(body))
	if err != nil {
		return errorResult(start, model.QueryPhases{}, &model.QueryError{Kind: model.ErrHTTP, Msg: "failed to build HTTP request: " + err.Error()})
	}
	req.Header.Set("Content-Type", dohContentType)
	req.Header.Set("Accept", dohContentType)
	resp, err := d.client.Do(req)
	if err != nil {
		d.dropIdle()
		mu.Lock()
		phases := ph
		mu.Unlock()
		return errorResult(start, phases, classify(ctx, err))
	}
	data, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	mu.Lock()
	if !wroteAt.IsZero() {
		ph.Query = time.Since(wroteAt)
	}
	phases := ph
	wasReused := reused
	mu.Unlock()
	d.dropIdle()
	if readErr != nil {
		return errorResult(start, phases, classify(ctx, readErr))
	}
	if resp.StatusCode != http.StatusOK {
		return errorResult(start, phases, &model.QueryError{Kind: model.ErrHTTP, Msg: fmt.Sprintf("unexpected HTTP status %s", resp.Status)})
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, dohContentType) {
		return errorResult(start, phases, &model.QueryError{Kind: model.ErrProtocol, Msg: fmt.Sprintf("unexpected Content-Type %q", ct)})
	}
	respMsg := new(dns.Msg)
	if err := respMsg.Unpack(data); err != nil {
		return errorResult(start, phases, malformedErr(err))
	}
	res := resultFromMsg(respMsg, len(data))
	res.Phases = phases
	res.Reused = wasReused
	res.RTT = time.Since(start)
	return res
}

func (d *dohQuerier) dropIdle() {
	if !d.opts.Persistent {
		d.client.CloseIdleConnections()
	}
}

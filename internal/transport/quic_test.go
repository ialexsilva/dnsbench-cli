package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dnsbench/internal/model"

	"github.com/miekg/dns"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func doqServer(t *testing.T, o Options) Querier {
	t.Helper()
	port, pool := startDoQServer(t, testHandler(nil))
	srv := model.Server{Protocol: model.ProtoDoQ, Address: "127.0.0.1", Port: port, TLSHostname: "dnsbench.test"}
	o.TLSConfig = &tls.Config{RootCAs: pool}
	return mustNew(t, srv, o)
}

func TestDoQQueryAndPersistence(t *testing.T) {
	q := doqServer(t, Options{Timeout: 5 * time.Second, Persistent: true})
	r1 := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, r1)
	if r1.Reused {
		t.Error("first query Reused = true, want false")
	}
	if len(r1.Answers) != 1 || r1.Answers[0].Data != "192.0.2.1" {
		t.Errorf("Answers = %+v, want one A 192.0.2.1", r1.Answers)
	}
	if r1.Phases.TLSHandshake <= 0 {
		t.Errorf("Phases.TLSHandshake = %v, want > 0", r1.Phases.TLSHandshake)
	}
	if r1.Phases.Connect != 0 {
		t.Errorf("Phases.Connect = %v, want 0 for a QUIC transport", r1.Phases.Connect)
	}
	if r1.Phases.Query <= 0 {
		t.Errorf("Phases.Query = %v, want > 0", r1.Phases.Query)
	}
	r2 := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, r2)
	if !r2.Reused {
		t.Error("second query Reused = false, want true")
	}
	if r2.Phases.TLSHandshake != 0 {
		t.Errorf("reused query Phases.TLSHandshake = %v, want 0", r2.Phases.TLSHandshake)
	}
}

func TestDoQColdSessionRedials(t *testing.T) {
	q := doqServer(t, Options{Timeout: 5 * time.Second})
	for i := 0; i < 2; i++ {
		res := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
		mustAnswer(t, res)
		if res.Reused {
			t.Errorf("query %d Reused = true, want false in cold mode", i)
		}
		if res.Phases.TLSHandshake <= 0 {
			t.Errorf("query %d Phases.TLSHandshake = %v, want > 0", i, res.Phases.TLSHandshake)
		}
	}
}

func TestDoQUsesZeroMessageID(t *testing.T) {
	var seen atomic.Int32
	seen.Store(-1)
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		seen.Store(int32(req.Id))
		m := new(dns.Msg)
		m.SetReply(req)
		m.Answer = append(m.Answer, aRecord(req.Question[0].Name, "192.0.2.7", 30))
		w.WriteMsg(m)
	})
	port, pool := startDoQServer(t, handler)
	srv := model.Server{Protocol: model.ProtoDoQ, Address: "127.0.0.1", Port: port, TLSHostname: "dnsbench.test"}
	q := mustNew(t, srv, Options{Timeout: 5 * time.Second, TLSConfig: &tls.Config{RootCAs: pool}})
	mustAnswer(t, q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA}))
	if got := seen.Load(); got != 0 {
		t.Errorf("server saw message ID %d, want 0", got)
	}
}

func TestDoQNXDOMAIN(t *testing.T) {
	q := doqServer(t, Options{Timeout: 5 * time.Second, Persistent: true})
	res := q.Query(context.Background(), Question{Name: "nope.test", Qtype: dns.TypeA})
	mustAnswer(t, res)
	if res.Rcode != dns.RcodeNameError {
		t.Errorf("Rcode = %d, want %d", res.Rcode, dns.RcodeNameError)
	}
}

func TestDoQTimeout(t *testing.T) {
	cancelCause := make(chan error, 1)
	hook := func(str *quic.Stream, req, _ *dns.Msg) bool {
		if !strings.EqualFold(req.Question[0].Name, "cancel.test.") {
			return false
		}
		<-str.Context().Done()
		cancelCause <- context.Cause(str.Context())
		return true
	}
	port, pool := startDoQServerWithHook(t, testHandler(nil), hook)
	srv := model.Server{Protocol: model.ProtoDoQ, Address: "127.0.0.1", Port: port, TLSHostname: "dnsbench.test"}
	q := mustNew(t, srv, Options{
		Timeout:    100 * time.Millisecond,
		Persistent: true,
		TLSConfig:  &tls.Config{RootCAs: pool},
	})

	res := q.Query(context.Background(), Question{Name: "cancel.test", Qtype: dns.TypeA})
	if res.Err == nil {
		t.Fatal("expected a timeout, got success")
	}
	if res.Err.Kind != model.ErrTimeout {
		t.Errorf("Err.Kind = %q, want %q", res.Err.Kind, model.ErrTimeout)
	}
	next := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, next)
	if !next.Reused {
		t.Error("query after a stream timeout Reused = false, want the QUIC connection preserved")
	}
	select {
	case cause := <-cancelCause:
		streamErr, ok := cause.(*quic.StreamError)
		if !ok {
			t.Fatalf("server stream cancellation = %T %v, want *quic.StreamError", cause, cause)
		}
		if streamErr.ErrorCode != quic.StreamErrorCode(doqRequestCancelled) || !streamErr.Remote {
			t.Errorf("server stream cancellation = %+v, want remote DOQ_REQUEST_CANCELLED", streamErr)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not observe the client stream cancellation")
	}
}

func TestDoQBadCertificate(t *testing.T) {
	port, _ := startDoQServer(t, testHandler(nil))
	srv := model.Server{Protocol: model.ProtoDoQ, Address: "127.0.0.1", Port: port, TLSHostname: "dnsbench.test"}
	q := mustNew(t, srv, Options{Timeout: 3 * time.Second, TLSConfig: &tls.Config{RootCAs: x509.NewCertPool()}})
	res := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	if res.Err == nil {
		t.Fatal("expected error, got success")
	}
	if res.Err.Kind != model.ErrTLS {
		t.Errorf("Err.Kind = %q, want %q", res.Err.Kind, model.ErrTLS)
	}
}

func TestDoQEDNSOptionsReachTheServer(t *testing.T) {
	var (
		wireSize   atomic.Int32
		hasPadding atomic.Bool
	)
	base := testHandler(nil)
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		if wire, err := req.Pack(); err == nil {
			wireSize.Store(int32(len(wire)))
		}
		if opt := req.IsEdns0(); opt != nil {
			for _, option := range opt.Option {
				if option.Option() == dns.EDNS0PADDING {
					hasPadding.Store(true)
				}
			}
		}
		base.ServeDNS(w, req)
	})
	port, pool := startDoQServer(t, handler)
	srv := model.Server{Protocol: model.ProtoDoQ, Address: "127.0.0.1", Port: port, TLSHostname: "dnsbench.test"}
	q := mustNew(t, srv, Options{
		Timeout:    5 * time.Second,
		Persistent: true,
		TLSConfig:  &tls.Config{RootCAs: pool},
	})
	res := q.Query(context.Background(), Question{Name: "echo.test", Qtype: dns.TypeTXT, DO: true, CD: true, UDPBufSize: 1400})
	mustAnswer(t, res)
	if len(res.Answers) != 1 {
		t.Fatalf("len(Answers) = %d, want 1", len(res.Answers))
	}
	for _, want := range []string{"do=1", "cd=1", "edns=1", "bufsize=1400"} {
		if !strings.Contains(res.Answers[0].Data, want) {
			t.Errorf("answer data %q does not contain %q", res.Answers[0].Data, want)
		}
	}
	if !hasPadding.Load() {
		t.Error("server did not receive an EDNS Padding option")
	}
	if got := wireSize.Load(); got == 0 || got%doqPaddingBlockSize != 0 {
		t.Errorf("server received a %d-byte DNS query, want a non-zero multiple of %d", got, doqPaddingBlockSize)
	}
}

func TestDoQRejectsExtraResponseData(t *testing.T) {
	hook := func(str *quic.Stream, req, resp *dns.Msg) bool {
		if !strings.EqualFold(req.Question[0].Name, "extra.test.") {
			return false
		}
		if err := writeFramed(str, resp); err != nil {
			return true
		}
		_, _ = str.Write([]byte{0})
		_ = str.Close()
		return true
	}
	port, pool := startDoQServerWithHook(t, testHandler(nil), hook)
	srv := model.Server{Protocol: model.ProtoDoQ, Address: "127.0.0.1", Port: port, TLSHostname: "dnsbench.test"}
	q := mustNew(t, srv, Options{
		Timeout:    time.Second,
		Persistent: true,
		TLSConfig:  &tls.Config{RootCAs: pool},
	})

	res := q.Query(context.Background(), Question{Name: "extra.test", Qtype: dns.TypeA})
	if res.Err == nil || res.Err.Kind != model.ErrProtocol {
		t.Fatalf("extra response data error = %v, want protocol", res.Err)
	}
	next := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, next)
	if next.Reused {
		t.Error("query after a framing violation Reused = true, want a new QUIC connection")
	}
}

func TestDoQRejectsResponseWithoutFIN(t *testing.T) {
	hook := func(str *quic.Stream, req, resp *dns.Msg) bool {
		if !strings.EqualFold(req.Question[0].Name, "nofin.test.") {
			return false
		}
		if err := writeFramed(str, resp); err != nil {
			return true
		}
		<-str.Context().Done()
		return true
	}
	port, pool := startDoQServerWithHook(t, testHandler(nil), hook)
	srv := model.Server{Protocol: model.ProtoDoQ, Address: "127.0.0.1", Port: port, TLSHostname: "dnsbench.test"}
	q := mustNew(t, srv, Options{
		Timeout:    100 * time.Millisecond,
		Persistent: true,
		TLSConfig:  &tls.Config{RootCAs: pool},
	})

	res := q.Query(context.Background(), Question{Name: "nofin.test", Qtype: dns.TypeA})
	if res.Err == nil || res.Err.Kind != model.ErrProtocol {
		t.Fatalf("missing FIN error = %v, want protocol", res.Err)
	}
	if !strings.Contains(res.Err.Msg, "FIN") {
		t.Errorf("missing FIN error = %q, want FIN detail", res.Err.Msg)
	}
	next := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, next)
	if next.Reused {
		t.Error("query after a missing FIN Reused = true, want a new QUIC connection")
	}
}

func TestDoQErrorCodesMatchRFC9250(t *testing.T) {
	for name, test := range map[string]struct {
		got  uint64
		want uint64
	}{
		"no error":          {uint64(doqNoError), 0x0},
		"internal error":    {uint64(doqInternalError), 0x1},
		"protocol error":    {uint64(doqProtocolError), 0x2},
		"request cancelled": {uint64(doqRequestCancelled), 0x3},
	} {
		t.Run(name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("code = %#x, want %#x", test.got, test.want)
			}
		})
	}
}

func TestDoH3QueryAndPersistence(t *testing.T) {
	url, pool := startDoH3Server(t, testHandler(nil))
	srv := model.Server{Protocol: model.ProtoDoH3, DoHURL: url, Address: "127.0.0.1"}
	q := mustNew(t, srv, Options{Timeout: 5 * time.Second, Persistent: true, TLSConfig: &tls.Config{RootCAs: pool}})
	r1 := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, r1)
	if r1.Reused {
		t.Error("first query Reused = true, want false")
	}
	if len(r1.Answers) != 1 || r1.Answers[0].Data != "192.0.2.1" {
		t.Errorf("Answers = %+v, want one A 192.0.2.1", r1.Answers)
	}
	if r1.Phases.TLSHandshake <= 0 {
		t.Errorf("Phases.TLSHandshake = %v, want > 0", r1.Phases.TLSHandshake)
	}
	if r1.Phases.Connect != 0 {
		t.Errorf("Phases.Connect = %v, want 0 for a QUIC transport", r1.Phases.Connect)
	}
	if r1.ResponseSize <= 0 {
		t.Errorf("ResponseSize = %d, want > 0", r1.ResponseSize)
	}
	r2 := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, r2)
	if !r2.Reused {
		t.Error("second query Reused = false, want true")
	}
}

func TestDoH3OverridesDialOnlyForPinnedAddresses(t *testing.T) {
	for _, test := range []struct {
		name       string
		address    string
		wantCustom bool
	}{
		{name: "hostname uses context-aware default resolver"},
		{name: "pinned IP uses custom dialer", address: "192.0.2.1", wantCustom: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			q := newDoH3Querier(model.Server{
				Protocol: model.ProtoDoH3,
				DoHURL:   "https://dns.example/dns-query",
				Address:  test.address,
			}, Options{})
			tr, ok := q.client.Transport.(*http3.Transport)
			if !ok {
				t.Fatalf("transport = %T, want *http3.Transport", q.client.Transport)
			}
			if got := tr.Dial != nil; got != test.wantCustom {
				t.Errorf("custom Dial configured = %v, want %v", got, test.wantCustom)
			}
			if err := q.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
	}
}

func TestDoH3BadCertificate(t *testing.T) {
	url, _ := startDoH3Server(t, testHandler(nil))
	srv := model.Server{Protocol: model.ProtoDoH3, DoHURL: url, Address: "127.0.0.1"}
	q := mustNew(t, srv, Options{Timeout: 3 * time.Second, TLSConfig: &tls.Config{RootCAs: x509.NewCertPool()}})
	res := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	if res.Err == nil {
		t.Fatal("expected error, got success")
	}
	if res.Err.Kind != model.ErrTLS {
		t.Errorf("Err.Kind = %q, want %q", res.Err.Kind, model.ErrTLS)
	}
}

func TestClassifyHTTP3ErrorAsProtocol(t *testing.T) {
	err := &url.Error{
		Op:  "Post",
		URL: "https://dns.example/dns-query",
		Err: &http3.Error{Remote: true, ErrorCode: http3.ErrCodeGeneralProtocolError},
	}
	got := classifyQUIC(context.Background(), err)
	if got.Kind != model.ErrProtocol {
		t.Fatalf("Err.Kind = %q, want %q", got.Kind, model.ErrProtocol)
	}
	if !strings.Contains(got.Msg, "H3_GENERAL_PROTOCOL_ERROR") {
		t.Errorf("Err.Msg = %q, want the HTTP/3 error detail", got.Msg)
	}
}

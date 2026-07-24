package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dnsbench/internal/model"

	"github.com/miekg/dns"
)

func mustNew(t *testing.T, s model.Server, o Options) Querier {
	t.Helper()
	q, err := New(s, o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { q.Close() })
	return q
}

func mustAnswer(t *testing.T, res model.QueryResult) {
	t.Helper()
	if res.Err != nil {
		t.Fatalf("query error: %v", res.Err)
	}
}

func TestUDPSuccessfulAQuery(t *testing.T) {
	port := startDualServer(t, testHandler(nil))
	srv := model.Server{ID: "u", Protocol: model.ProtoUDP, Address: "127.0.0.1", Port: port}
	q := mustNew(t, srv, Options{Timeout: 3 * time.Second})
	res := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, res)
	if res.RTT <= 0 {
		t.Errorf("RTT = %v, want > 0", res.RTT)
	}
	if res.Phases.Query != res.RTT {
		t.Errorf("Phases.Query = %v, want %v", res.Phases.Query, res.RTT)
	}
	if res.Rcode != dns.RcodeSuccess {
		t.Errorf("Rcode = %d, want %d", res.Rcode, dns.RcodeSuccess)
	}
	if len(res.Answers) != 1 {
		t.Fatalf("len(Answers) = %d, want 1", len(res.Answers))
	}
	a := res.Answers[0]
	if a.Type != "A" || a.TTL != 60 || a.Data != "192.0.2.1" {
		t.Errorf("answer = %+v, want Type A TTL 60 Data 192.0.2.1", a)
	}
	if res.EDNSUDPSize != 4096 {
		t.Errorf("EDNSUDPSize = %d, want 4096", res.EDNSUDPSize)
	}
	if res.ResponseSize <= 0 {
		t.Errorf("ResponseSize = %d, want > 0", res.ResponseSize)
	}
	if res.UsedTCP {
		t.Error("UsedTCP = true, want false")
	}
	if res.Truncated {
		t.Error("Truncated = true, want false")
	}
}

func TestUDPNXDomainRcode(t *testing.T) {
	port := startDualServer(t, testHandler(nil))
	srv := model.Server{Protocol: model.ProtoUDP, Address: "127.0.0.1", Port: port}
	q := mustNew(t, srv, Options{Timeout: 3 * time.Second})
	res := q.Query(context.Background(), Question{Name: "nx.test", Qtype: dns.TypeA})
	mustAnswer(t, res)
	if res.Rcode != dns.RcodeNameError {
		t.Errorf("Rcode = %d, want %d", res.Rcode, dns.RcodeNameError)
	}
	if len(res.Answers) != 0 {
		t.Errorf("len(Answers) = %d, want 0", len(res.Answers))
	}
}

func TestUDPADFlag(t *testing.T) {
	port := startDualServer(t, testHandler(nil))
	srv := model.Server{Protocol: model.ProtoUDP, Address: "127.0.0.1", Port: port}
	q := mustNew(t, srv, Options{Timeout: 3 * time.Second})
	res := q.Query(context.Background(), Question{Name: "ad.test", Qtype: dns.TypeA})
	mustAnswer(t, res)
	if !res.AD {
		t.Error("AD = false, want true")
	}
}

func TestUDPTruncatedFallsBackToTCP(t *testing.T) {
	port := startDualServer(t, testHandler(nil))
	srv := model.Server{Protocol: model.ProtoUDP, Address: "127.0.0.1", Port: port}
	q := mustNew(t, srv, Options{Timeout: 3 * time.Second})
	res := q.Query(context.Background(), Question{Name: "big.test", Qtype: dns.TypeA})
	mustAnswer(t, res)
	if !res.UsedTCP {
		t.Error("UsedTCP = false, want true")
	}
	if res.Truncated {
		t.Error("Truncated = true, want false after TCP retry")
	}
	if len(res.Answers) != 1 || res.Answers[0].Data != "192.0.2.2" {
		t.Errorf("Answers = %+v, want one A 192.0.2.2", res.Answers)
	}
	if res.Phases.Connect <= 0 {
		t.Errorf("Phases.Connect = %v, want > 0", res.Phases.Connect)
	}
	if res.RTT < res.Phases.Connect {
		t.Errorf("RTT = %v < Connect = %v", res.RTT, res.Phases.Connect)
	}
}

func TestUDPTimeout(t *testing.T) {
	port := startDualServer(t, testHandler(nil))
	srv := model.Server{Protocol: model.ProtoUDP, Address: "127.0.0.1", Port: port}
	q := mustNew(t, srv, Options{Timeout: 100 * time.Millisecond})
	res := q.Query(context.Background(), Question{Name: "slow.test", Qtype: dns.TypeA})
	if res.Err == nil {
		t.Fatal("expected error, got success")
	}
	if res.Err.Kind != model.ErrTimeout {
		t.Errorf("Err.Kind = %q, want %q", res.Err.Kind, model.ErrTimeout)
	}
	if !res.Err.IsTimeout() {
		t.Error("IsTimeout() = false, want true")
	}
	if res.RTT <= 0 {
		t.Errorf("RTT = %v, want > 0", res.RTT)
	}
}

func TestCanceledContext(t *testing.T) {
	port := startDualServer(t, testHandler(nil))
	srv := model.Server{Protocol: model.ProtoUDP, Address: "127.0.0.1", Port: port}
	q := mustNew(t, srv, Options{Timeout: 3 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := q.Query(ctx, Question{Name: "a.test", Qtype: dns.TypeA})
	if res.Err == nil {
		t.Fatal("expected error, got success")
	}
	if res.Err.Kind != model.ErrCanceled {
		t.Errorf("Err.Kind = %q, want %q", res.Err.Kind, model.ErrCanceled)
	}
}

func TestQuestionFlags(t *testing.T) {
	port := startDualServer(t, testHandler(nil))
	srv := model.Server{Protocol: model.ProtoUDP, Address: "127.0.0.1", Port: port}
	cases := []struct {
		name         string
		q            Question
		wants        []string
		wantEDNSSize int
	}{
		{"default", Question{Name: "echo.test", Qtype: dns.TypeTXT}, []string{"do=0", "cd=0", "edns=1", "bufsize=1232"}, 4096},
		{"do", Question{Name: "echo.test", Qtype: dns.TypeTXT, DO: true}, []string{"do=1", "edns=1"}, 4096},
		{"cd", Question{Name: "echo.test", Qtype: dns.TypeTXT, CD: true}, []string{"cd=1"}, 4096},
		{"noedns", Question{Name: "echo.test", Qtype: dns.TypeTXT, NoEDNS: true}, []string{"edns=0", "bufsize=0"}, 0},
		{"bufsize", Question{Name: "echo.test", Qtype: dns.TypeTXT, UDPBufSize: 4096}, []string{"bufsize=4096"}, 4096},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := mustNew(t, srv, Options{Timeout: 3 * time.Second})
			res := q.Query(context.Background(), tc.q)
			mustAnswer(t, res)
			if len(res.Answers) != 1 {
				t.Fatalf("len(Answers) = %d, want 1", len(res.Answers))
			}
			for _, want := range tc.wants {
				if !strings.Contains(res.Answers[0].Data, want) {
					t.Errorf("answer data %q does not contain %q", res.Answers[0].Data, want)
				}
			}
			if res.EDNSUDPSize != tc.wantEDNSSize {
				t.Errorf("EDNSUDPSize = %d, want %d", res.EDNSUDPSize, tc.wantEDNSSize)
			}
		})
	}
}

func TestTCPPersistentReuse(t *testing.T) {
	port := startDualServer(t, testHandler(nil))
	srv := model.Server{Protocol: model.ProtoTCP, Address: "127.0.0.1", Port: port}
	q := mustNew(t, srv, Options{Timeout: 3 * time.Second, Persistent: true})
	r1 := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, r1)
	if r1.Reused {
		t.Error("first query Reused = true, want false")
	}
	if r1.Phases.Connect <= 0 {
		t.Errorf("first query Phases.Connect = %v, want > 0", r1.Phases.Connect)
	}
	if r1.Phases.Query <= 0 {
		t.Errorf("first query Phases.Query = %v, want > 0", r1.Phases.Query)
	}
	r2 := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, r2)
	if !r2.Reused {
		t.Error("second query Reused = false, want true")
	}
	if r2.Phases.Connect != 0 {
		t.Errorf("second query Phases.Connect = %v, want 0", r2.Phases.Connect)
	}
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r3 := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, r3)
	if r3.Reused {
		t.Error("query after Close Reused = true, want false")
	}
}

func TestTCPColdNoReuse(t *testing.T) {
	port := startDualServer(t, testHandler(nil))
	srv := model.Server{Protocol: model.ProtoTCP, Address: "127.0.0.1", Port: port}
	q := mustNew(t, srv, Options{Timeout: 3 * time.Second})
	for i := 0; i < 2; i++ {
		res := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
		mustAnswer(t, res)
		if res.Reused {
			t.Errorf("query %d Reused = true, want false", i+1)
		}
	}
}

func TestTCPPersistentRedialsWhenConnectionDies(t *testing.T) {
	var flaky atomic.Int32
	port := startDualServer(t, testHandler(&flaky))
	srv := model.Server{Protocol: model.ProtoTCP, Address: "127.0.0.1", Port: port}
	q := mustNew(t, srv, Options{Timeout: 3 * time.Second, Persistent: true})
	r1 := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, r1)
	r2 := q.Query(context.Background(), Question{Name: "flaky.test", Qtype: dns.TypeA})
	mustAnswer(t, r2)
	if r2.Reused {
		t.Error("redialed query Reused = true, want false")
	}
	if len(r2.Answers) != 1 || r2.Answers[0].Data != "192.0.2.4" {
		t.Errorf("Answers = %+v, want one A 192.0.2.4", r2.Answers)
	}
	r3 := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, r3)
	if !r3.Reused {
		t.Error("query after redial Reused = false, want true")
	}
}

func TestTCPConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	srv := model.Server{Protocol: model.ProtoTCP, Address: "127.0.0.1", Port: port}
	q := mustNew(t, srv, Options{Timeout: 2 * time.Second})
	res := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	if res.Err == nil {
		t.Fatal("expected error, got success")
	}
	if res.Err.Kind != model.ErrNetwork {
		t.Errorf("Err.Kind = %q, want %q", res.Err.Kind, model.ErrNetwork)
	}
}

func TestDoTQueryAndPersistence(t *testing.T) {
	port, pool := startDoTServer(t, testHandler(nil))
	srv := model.Server{Protocol: model.ProtoDoT, Address: "127.0.0.1", Port: port, TLSHostname: "dnsbench.test"}
	q := mustNew(t, srv, Options{Timeout: 3 * time.Second, Persistent: true, TLSConfig: &tls.Config{RootCAs: pool}})
	r1 := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, r1)
	if r1.Reused {
		t.Error("first query Reused = true, want false")
	}
	if r1.Phases.Connect <= 0 {
		t.Errorf("Phases.Connect = %v, want > 0", r1.Phases.Connect)
	}
	if r1.Phases.TLSHandshake <= 0 {
		t.Errorf("Phases.TLSHandshake = %v, want > 0", r1.Phases.TLSHandshake)
	}
	if r1.Phases.Query <= 0 {
		t.Errorf("Phases.Query = %v, want > 0", r1.Phases.Query)
	}
	if len(r1.Answers) != 1 || r1.Answers[0].Data != "192.0.2.1" {
		t.Errorf("Answers = %+v, want one A 192.0.2.1", r1.Answers)
	}
	r2 := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, r2)
	if !r2.Reused {
		t.Error("second query Reused = false, want true")
	}
	if r2.Phases.TLSHandshake != 0 {
		t.Errorf("second query Phases.TLSHandshake = %v, want 0", r2.Phases.TLSHandshake)
	}
}

func TestDoTBadCertificate(t *testing.T) {
	port, _ := startDoTServer(t, testHandler(nil))
	srv := model.Server{Protocol: model.ProtoDoT, Address: "127.0.0.1", Port: port, TLSHostname: "dnsbench.test"}
	q := mustNew(t, srv, Options{Timeout: 2 * time.Second, TLSConfig: &tls.Config{RootCAs: x509.NewCertPool()}})
	res := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	if res.Err == nil {
		t.Fatal("expected error, got success")
	}
	if res.Err.Kind != model.ErrTLS {
		t.Errorf("Err.Kind = %q, want %q", res.Err.Kind, model.ErrTLS)
	}
}

func TestDoHColdAndPersistent(t *testing.T) {
	httpSrv := startDoHServer(t)
	pool := dohPool(t, httpSrv)
	srv := model.Server{Protocol: model.ProtoDoH, DoHURL: httpSrv.URL}
	cold := mustNew(t, srv, Options{Timeout: 3 * time.Second, TLSConfig: &tls.Config{RootCAs: pool}})
	c1 := cold.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, c1)
	if c1.Reused {
		t.Error("cold first query Reused = true, want false")
	}
	if len(c1.Answers) != 1 || c1.Answers[0].Data != "192.0.2.53" {
		t.Errorf("Answers = %+v, want one A 192.0.2.53", c1.Answers)
	}
	if c1.Phases.Connect <= 0 {
		t.Errorf("Phases.Connect = %v, want > 0", c1.Phases.Connect)
	}
	if c1.Phases.TLSHandshake <= 0 {
		t.Errorf("Phases.TLSHandshake = %v, want > 0", c1.Phases.TLSHandshake)
	}
	if c1.Phases.Query <= 0 {
		t.Errorf("Phases.Query = %v, want > 0", c1.Phases.Query)
	}
	if c1.ResponseSize <= 0 {
		t.Errorf("ResponseSize = %d, want > 0", c1.ResponseSize)
	}
	if c1.RTT <= 0 {
		t.Errorf("RTT = %v, want > 0", c1.RTT)
	}
	c2 := cold.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, c2)
	if c2.Reused {
		t.Error("cold second query Reused = true, want false")
	}
	pers := mustNew(t, srv, Options{Timeout: 3 * time.Second, Persistent: true, TLSConfig: &tls.Config{RootCAs: pool}})
	p1 := pers.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, p1)
	if p1.Reused {
		t.Error("persistent first query Reused = true, want false")
	}
	p2 := pers.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, p2)
	if !p2.Reused {
		t.Error("persistent second query Reused = false, want true")
	}
	if p2.Phases.Connect != 0 {
		t.Errorf("persistent second query Phases.Connect = %v, want 0", p2.Phases.Connect)
	}
}

func TestDoHCustomDialAddress(t *testing.T) {
	httpSrv := startDoHServer(t)
	pool := dohPool(t, httpSrv)
	u, err := url.Parse(httpSrv.URL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	srv := model.Server{
		Protocol: model.ProtoDoH,
		DoHURL:   "https://example.com:" + u.Port() + "/dns-query",
		Address:  "127.0.0.1",
	}
	q := mustNew(t, srv, Options{Timeout: 3 * time.Second, TLSConfig: &tls.Config{RootCAs: pool}})
	res := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, res)
	if len(res.Answers) != 1 || res.Answers[0].Data != "192.0.2.53" {
		t.Errorf("Answers = %+v, want one A 192.0.2.53", res.Answers)
	}
}

func TestDoHErrors(t *testing.T) {
	httpSrv := startDoHServer(t)
	pool := dohPool(t, httpSrv)
	srv := model.Server{Protocol: model.ProtoDoH, DoHURL: httpSrv.URL}
	q := mustNew(t, srv, Options{Timeout: 3 * time.Second, TLSConfig: &tls.Config{RootCAs: pool}})
	res := q.Query(context.Background(), Question{Name: "status500.test", Qtype: dns.TypeA})
	if res.Err == nil || res.Err.Kind != model.ErrHTTP {
		t.Errorf("status500 Err = %v, want kind %q", res.Err, model.ErrHTTP)
	}
	res = q.Query(context.Background(), Question{Name: "badct.test", Qtype: dns.TypeA})
	if res.Err == nil || res.Err.Kind != model.ErrProtocol {
		t.Errorf("badct Err = %v, want kind %q", res.Err, model.ErrProtocol)
	}
}

func TestFactoryValidation(t *testing.T) {
	if _, err := New(model.Server{Protocol: "weird", Address: "127.0.0.1"}, Options{}); err == nil {
		t.Error("unknown protocol: expected error")
	}
	for _, p := range []model.Protocol{model.ProtoUDP, model.ProtoTCP, model.ProtoDoT} {
		if _, err := New(model.Server{Protocol: p}, Options{}); err == nil {
			t.Errorf("%s without address: expected error", p)
		}
	}
	if _, err := New(model.Server{Protocol: model.ProtoDoH}, Options{}); err == nil {
		t.Error("doh without URL: expected error")
	}
	cases := []struct {
		s    model.Server
		want model.Protocol
	}{
		{model.Server{Protocol: model.ProtoUDP, Address: "127.0.0.1"}, model.ProtoUDP},
		{model.Server{Protocol: model.ProtoTCP, Address: "127.0.0.1"}, model.ProtoTCP},
		{model.Server{Protocol: model.ProtoDoT, Address: "127.0.0.1"}, model.ProtoDoT},
		{model.Server{Protocol: model.ProtoDoH, DoHURL: "https://example.com/dns-query"}, model.ProtoDoH},
	}
	for _, tc := range cases {
		q, err := New(tc.s, Options{})
		if err != nil {
			t.Fatalf("New(%s): %v", tc.want, err)
		}
		if q.Protocol() != tc.want {
			t.Errorf("Protocol() = %q, want %q", q.Protocol(), tc.want)
		}
		q.Close()
	}
}

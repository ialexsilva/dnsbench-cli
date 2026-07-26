package transport

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func boolFlag(b bool) int {
	if b {
		return 1
	}
	return 0
}

func aRecord(name, ip string, ttl uint32) dns.RR {
	return &dns.A{
		Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl},
		A:   net.ParseIP(ip),
	}
}

func testHandler(flakyCount *atomic.Int32) dns.HandlerFunc {
	return func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		qname := req.Question[0].Name
		switch strings.ToLower(qname) {
		case "a.test.":
			m.Answer = append(m.Answer, aRecord(qname, "192.0.2.1", 60))
		case "ad.test.":
			m.AuthenticatedData = true
			m.Answer = append(m.Answer, aRecord(qname, "192.0.2.9", 60))
		case "big.test.":
			if w.RemoteAddr().Network() == "udp" {
				m.Truncated = true
			} else {
				m.Answer = append(m.Answer, aRecord(qname, "192.0.2.2", 60))
			}
		case "slow.test.":
			time.Sleep(300 * time.Millisecond)
			m.Answer = append(m.Answer, aRecord(qname, "192.0.2.3", 60))
		case "flaky.test.":
			if flakyCount != nil && flakyCount.Add(1) == 1 {
				w.Close()
				return
			}
			m.Answer = append(m.Answer, aRecord(qname, "192.0.2.4", 60))
		case "echo.test.":
			opt := req.IsEdns0()
			bufsize := 0
			doFlag := 0
			if opt != nil {
				bufsize = int(opt.UDPSize())
				doFlag = boolFlag(opt.Do())
			}
			txt := fmt.Sprintf("do=%d cd=%d edns=%d bufsize=%d",
				doFlag, boolFlag(req.CheckingDisabled), boolFlag(opt != nil), bufsize)
			m.Answer = append(m.Answer, &dns.TXT{
				Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 30},
				Txt: []string{txt},
			})
		default:
			m.Rcode = dns.RcodeNameError
		}
		if opt := req.IsEdns0(); opt != nil {
			respOpt := new(dns.OPT)
			respOpt.Hdr.Name = "."
			respOpt.Hdr.Rrtype = dns.TypeOPT
			respOpt.SetUDPSize(4096)
			if opt.Do() {
				respOpt.SetDo()
			}
			m.Extra = append(m.Extra, respOpt)
		}
		w.WriteMsg(m)
	}
}

func startDualServer(t *testing.T, h dns.Handler) int {
	t.Helper()
	for i := 0; i < 20; i++ {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("udp listen: %v", err)
		}
		port := pc.LocalAddr().(*net.UDPAddr).Port
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			pc.Close()
			continue
		}
		udpSrv := &dns.Server{PacketConn: pc, Handler: h}
		tcpSrv := &dns.Server{Listener: ln, Handler: h}
		go udpSrv.ActivateAndServe()
		go tcpSrv.ActivateAndServe()
		t.Cleanup(func() {
			udpSrv.Shutdown()
			tcpSrv.Shutdown()
		})
		return port
	}
	t.Fatal("could not bind udp and tcp listeners on the same port")
	return 0
}

func selfSignedCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "dnsbench.test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"dnsbench.test"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func startDoTServer(t *testing.T, h dns.Handler) (int, *x509.CertPool) {
	t.Helper()
	cert, pool := selfSignedCert(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatalf("tls listen: %v", err)
	}
	srv := &dns.Server{Listener: ln, Handler: h}
	go srv.ActivateAndServe()
	t.Cleanup(func() { srv.Shutdown() })
	return ln.Addr().(*net.TCPAddr).Port, pool
}

func startDoHServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Content-Type") != dohContentType {
			http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
			return
		}
		if r.Header.Get("Accept") != dohContentType {
			http.Error(w, "not acceptable", http.StatusNotAcceptable)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		req := new(dns.Msg)
		if err := req.Unpack(body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		qname := req.Question[0].Name
		switch strings.ToLower(qname) {
		case "status500.test.":
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		case "badct.test.":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("not dns"))
			return
		}
		m := new(dns.Msg)
		m.SetReply(req)
		m.Answer = append(m.Answer, aRecord(qname, "192.0.2.53", 60))
		out, err := m.Pack()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", dohContentType)
		w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type captureWriter struct {
	remote net.Addr
	msg    *dns.Msg
	closed bool
}

func (w *captureWriter) LocalAddr() net.Addr  { return w.remote }
func (w *captureWriter) RemoteAddr() net.Addr { return w.remote }
func (w *captureWriter) WriteMsg(m *dns.Msg) error {
	w.msg = m
	return nil
}
func (w *captureWriter) Write([]byte) (int, error) { return 0, errors.New("not supported") }
func (w *captureWriter) Close() error              { w.closed = true; return nil }
func (w *captureWriter) TsigStatus() error         { return nil }
func (w *captureWriter) TsigTimersOnly(bool)       {}
func (w *captureWriter) Hijack()                   {}

func handleWith(h dns.Handler, query *dns.Msg) *dns.Msg {
	// Keep shared handlers from applying UDP-only behavior such as truncation.
	w := &captureWriter{remote: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}}
	h.ServeDNS(w, query)
	if w.closed {
		return nil
	}
	return w.msg
}

func startDoQServer(t *testing.T, h dns.Handler) (int, *x509.CertPool) {
	return startDoQServerWithHook(t, h, nil)
}

type doqResponseHook func(*quic.Stream, *dns.Msg, *dns.Msg) (handled bool)

func startDoQServerWithHook(t *testing.T, h dns.Handler, hook doqResponseHook) (int, *x509.CertPool) {
	t.Helper()
	cert, pool := selfSignedCert(t)
	ln, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{doqALPN},
	}, &quic.Config{})
	if err != nil {
		t.Fatalf("quic listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept(context.Background())
			if err != nil {
				return
			}
			go serveDoQConn(conn, h, hook)
		}
	}()
	return ln.Addr().(*net.UDPAddr).Port, pool
}

func serveDoQConn(conn *quic.Conn, h dns.Handler, hook doqResponseHook) {
	for {
		str, err := conn.AcceptStream(context.Background())
		if err != nil {
			return
		}
		go func() {
			req, _, err := readFramed(str)
			if err != nil {
				str.CancelWrite(0)
				return
			}
			resp := handleWith(h, req)
			if resp == nil {
				str.CancelWrite(0)
				return
			}
			if hook != nil && hook(str, req, resp) {
				return
			}
			if err := writeFramed(str, resp); err != nil {
				str.CancelWrite(0)
				return
			}
			str.Close()
		}()
	}
}

func startDoH3Server(t *testing.T, h dns.Handler) (string, *x509.CertPool) {
	t.Helper()
	cert, pool := selfSignedCert(t)
	srv := &http3.Server{
		Handler:   http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveDoHRequest(w, r, h) }),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
	}
	ln, err := quic.ListenAddr("127.0.0.1:0", http3.ConfigureTLSConfig(srv.TLSConfig), &quic.Config{})
	if err != nil {
		t.Fatalf("quic listen: %v", err)
	}
	go srv.ServeListener(ln)
	t.Cleanup(func() {
		srv.Close()
		ln.Close()
	})
	port := ln.Addr().(*net.UDPAddr).Port
	return fmt.Sprintf("https://dnsbench.test:%d/dns-query", port), pool
}

func serveDoHRequest(w http.ResponseWriter, r *http.Request, h dns.Handler) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req := new(dns.Msg)
	if err := req.Unpack(body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	resp := handleWith(h, req)
	if resp == nil {
		http.Error(w, "dropped", http.StatusInternalServerError)
		return
	}
	out, err := resp.Pack()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", dohContentType)
	w.Write(out)
}

func dohPool(t *testing.T, srv *httptest.Server) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return pool
}

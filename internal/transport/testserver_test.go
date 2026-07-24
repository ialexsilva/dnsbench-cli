package transport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
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

func dohPool(t *testing.T, srv *httptest.Server) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return pool
}

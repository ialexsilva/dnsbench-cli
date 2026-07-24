package probe

import (
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

type profile int

const (
	profileValidating profile = iota
	profileInterceptor
	profileRebindBlocking
	profileRebindUnprotected
)

const (
	interceptIP    = "203.0.113.99"
	interceptCNAME = "landing.intercept.test."
	rebindSuffix   = ".rebind.test."
)

func aRR(name, ip string) dns.RR {
	return &dns.A{
		Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
		A:   net.ParseIP(ip),
	}
}

func aaaaRR(name, ip string) dns.RR {
	return &dns.AAAA{
		Hdr:  dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60},
		AAAA: net.ParseIP(ip),
	}
}

func cnameRR(name, target string) dns.RR {
	return &dns.CNAME{
		Hdr:    dns.RR_Header{Name: name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 60},
		Target: target,
	}
}

func ptrRR(name, target string) dns.RR {
	return &dns.PTR{
		Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: 60},
		Ptr: target,
	}
}

func txtRR(name, text string) dns.RR {
	return &dns.TXT{
		Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 60},
		Txt: []string{text},
	}
}

func rrsigRR(name string) dns.RR {
	return &dns.RRSIG{
		Hdr:         dns.RR_Header{Name: name, Rrtype: dns.TypeRRSIG, Class: dns.ClassINET, Ttl: 60},
		TypeCovered: dns.TypeA,
		Algorithm:   dns.ECDSAP256SHA256,
		Labels:      2,
		OrigTtl:     60,
		Expiration:  uint32(time.Now().Add(time.Hour).Unix()),
		Inception:   uint32(time.Now().Add(-time.Hour).Unix()),
		KeyTag:      4242,
		SignerName:  "signed.test.",
		Signature:   "MEUCIQDKo8oaAAAA",
	}
}

func httpsRR(name string) dns.RR {
	return &dns.HTTPS{SVCB: dns.SVCB{
		Hdr:      dns.RR_Header{Name: name, Rrtype: dns.TypeHTTPS, Class: dns.ClassINET, Ttl: 60},
		Priority: 1,
		Target:   ".",
	}}
}

func embeddedIP(host string) string {
	if _, err := netip.ParseAddr(host); err == nil {
		return host
	}
	v6 := strings.ReplaceAll(host, "-", ":")
	if _, err := netip.ParseAddr(v6); err == nil {
		return v6
	}
	return ""
}

func serveRebind(m *dns.Msg, q dns.Question, p profile) {
	host := strings.TrimSuffix(strings.ToLower(q.Name), rebindSuffix)
	switch p {
	case profileRebindBlocking:
		return
	case profileInterceptor:
		m.Answer = append(m.Answer, aRR(q.Name, interceptIP))
		return
	case profileValidating:
		if q.Qtype == dns.TypeA {
			return
		}
	}
	ip := embeddedIP(host)
	if ip == "" {
		m.Rcode = dns.RcodeNameError
		return
	}
	isV6 := strings.Contains(ip, ":")
	if q.Qtype == dns.TypeA && !isV6 {
		m.Answer = append(m.Answer, aRR(q.Name, ip))
	}
	if q.Qtype == dns.TypeAAAA && isV6 {
		m.Answer = append(m.Answer, aaaaRR(q.Name, ip))
	}
}

func serveUnknown(m *dns.Msg, q dns.Question, p profile) {
	switch p {
	case profileInterceptor:
		if strings.HasPrefix(strings.ToLower(q.Name), "www.") {
			m.Answer = append(m.Answer, cnameRR(q.Name, interceptCNAME))
			return
		}
		m.Answer = append(m.Answer, aRR(q.Name, interceptIP))
	case profileRebindBlocking:
	default:
		m.Rcode = dns.RcodeNameError
	}
}

func serveBogus(m *dns.Msg, q dns.Question, req *dns.Msg, p profile) {
	switch p {
	case profileValidating, profileRebindBlocking:
		if req.CheckingDisabled {
			m.Answer = append(m.Answer, aRR(q.Name, "192.0.2.66"))
			return
		}
		m.Rcode = dns.RcodeServerFailure
	case profileRebindUnprotected:
		m.Rcode = dns.RcodeNameError
	default:
		m.Answer = append(m.Answer, aRR(q.Name, "192.0.2.66"))
	}
}

func mockHandler(p profile) dns.HandlerFunc {
	return func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		q := req.Question[0]
		name := strings.ToLower(q.Name)
		switch {
		case name == "reach.test.":
			if q.Qtype == dns.TypeA {
				m.Answer = append(m.Answer, aRR(q.Name, "192.0.2.10"))
			}
			if q.Qtype == dns.TypeAAAA {
				m.Answer = append(m.Answer, aaaaRR(q.Name, "2001:db8::10"))
			}
		case name == "signed.test." && q.Qtype == dns.TypeHTTPS:
			if p == profileValidating {
				m.Answer = append(m.Answer, httpsRR(q.Name))
			}
		case name == "signed.test.":
			m.Answer = append(m.Answer, aRR(q.Name, "192.0.2.20"))
			if p == profileValidating {
				m.Answer = append(m.Answer, rrsigRR(q.Name))
				m.AuthenticatedData = true
			}
		case name == "bogus.test.":
			serveBogus(m, q, req, p)
		case strings.HasSuffix(name, rebindSuffix):
			serveRebind(m, q, p)
		case name == "1.0.0.127.in-addr.arpa." && q.Qtype == dns.TypePTR:
			m.Answer = append(m.Answer, ptrRR(q.Name, "resolver.test."))
		case name == "ipv4only.arpa." && q.Qtype == dns.TypeAAAA:
			if p == profileValidating {
				m.Answer = append(m.Answer, aaaaRR(q.Name, "64:ff9b::c000:aa"))
			}
		case name == "qnamemintest.internet.nl." && q.Qtype == dns.TypeTXT:
			if p == profileValidating {
				m.Answer = append(m.Answer, txtRR(q.Name, "HOORAY - QNAME minimisation is enabled"))
			} else {
				m.Answer = append(m.Answer, txtRR(q.Name, "NO - QNAME minimisation is NOT enabled"))
			}
		default:
			serveUnknown(m, q, p)
		}
		if p != profileInterceptor && req.IsEdns0() != nil {
			m.SetEdns0(4096, false)
		}
		w.WriteMsg(m)
	}
}

func startMockServer(t *testing.T, p profile) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("udp listen: %v", err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: mockHandler(p)}
	go srv.ActivateAndServe()
	t.Cleanup(func() { srv.Shutdown() })
	return pc.LocalAddr().(*net.UDPAddr).Port
}

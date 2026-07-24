package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"dnsbench/internal/model"

	"github.com/miekg/dns"
)

const defaultEDNSBufSize = 1232

func buildMsg(q Question, doh bool) *dns.Msg {
	m := new(dns.Msg)
	if !doh {
		m.Id = dns.Id()
	}
	m.RecursionDesired = true
	m.CheckingDisabled = q.CD
	m.Question = []dns.Question{{Name: dns.Fqdn(q.Name), Qtype: q.Qtype, Qclass: dns.ClassINET}}
	if !q.NoEDNS {
		size := uint16(defaultEDNSBufSize)
		if q.UDPBufSize > 0 {
			size = q.UDPBufSize
		}
		m.SetEdns0(size, false)
		if q.DO {
			if opt := m.IsEdns0(); opt != nil {
				opt.SetDo()
			}
		}
	}
	return m
}

func resultFromMsg(resp *dns.Msg, wireSize int) model.QueryResult {
	res := model.QueryResult{
		Rcode:        resp.Rcode,
		Truncated:    resp.Truncated,
		AD:           resp.AuthenticatedData,
		ResponseSize: wireSize,
	}
	if opt := resp.IsEdns0(); opt != nil {
		res.EDNSUDPSize = int(opt.UDPSize())
	}
	for _, rr := range resp.Answer {
		res.Answers = append(res.Answers, model.RR{
			Type: rrTypeString(rr),
			TTL:  rr.Header().Ttl,
			Data: rdataText(rr),
		})
	}
	return res
}

func rrTypeString(rr dns.RR) string {
	if s, ok := dns.TypeToString[rr.Header().Rrtype]; ok {
		return s
	}
	return dns.Type(rr.Header().Rrtype).String()
}

func rdataText(rr dns.RR) string {
	full := rr.String()
	head := rr.Header().String()
	if head != "" && strings.HasPrefix(full, head) {
		return full[len(head):]
	}
	return full
}

func exchangeUDP(c net.Conn, m *dns.Msg) (*dns.Msg, int, error) {
	out, err := m.Pack()
	if err != nil {
		return nil, 0, malformedQueryErr(err)
	}
	if _, err := c.Write(out); err != nil {
		return nil, 0, err
	}
	buf := make([]byte, dns.MaxMsgSize)
	n, err := c.Read(buf)
	if err != nil {
		return nil, 0, err
	}
	resp := new(dns.Msg)
	if err := resp.Unpack(buf[:n]); err != nil {
		return nil, 0, malformedErr(err)
	}
	return resp, n, nil
}

func exchangeUDPDraining(c net.Conn, m *dns.Msg) (*dns.Msg, int, error) {
	out, err := m.Pack()
	if err != nil {
		return nil, 0, malformedQueryErr(err)
	}
	if _, err := c.Write(out); err != nil {
		return nil, 0, err
	}
	buf := make([]byte, dns.MaxMsgSize)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return nil, 0, err
		}
		resp := new(dns.Msg)
		if err := resp.Unpack(buf[:n]); err != nil {
			continue
		}
		if resp.Id == m.Id {
			return resp, n, nil
		}
	}
}

func exchangeStream(c net.Conn, m *dns.Msg) (*dns.Msg, int, error) {
	out, err := m.Pack()
	if err != nil {
		return nil, 0, malformedQueryErr(err)
	}
	framed := make([]byte, 2+len(out))
	binary.BigEndian.PutUint16(framed, uint16(len(out)))
	copy(framed[2:], out)
	if _, err := c.Write(framed); err != nil {
		return nil, 0, err
	}
	var lenBuf [2]byte
	if _, err := io.ReadFull(c, lenBuf[:]); err != nil {
		return nil, 0, err
	}
	n := int(binary.BigEndian.Uint16(lenBuf[:]))
	if n == 0 {
		return nil, 0, &model.QueryError{Kind: model.ErrProtocol, Msg: "server sent an empty DNS message"}
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c, body); err != nil {
		return nil, 0, err
	}
	resp := new(dns.Msg)
	if err := resp.Unpack(body); err != nil {
		return nil, 0, malformedErr(err)
	}
	return resp, n, nil
}

func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d > 0 {
		return context.WithTimeout(ctx, d)
	}
	return context.WithCancel(ctx)
}

func applyDeadline(ctx context.Context, c net.Conn) {
	if d, ok := ctx.Deadline(); ok {
		c.SetDeadline(d)
		return
	}
	c.SetDeadline(time.Time{})
}

func errorResult(start time.Time, ph model.QueryPhases, qe *model.QueryError) model.QueryResult {
	return model.QueryResult{RTT: time.Since(start), Phases: ph, Err: qe}
}

func idMismatchErr() *model.QueryError {
	return &model.QueryError{Kind: model.ErrProtocol, Msg: "response ID does not match query ID"}
}

func malformedErr(err error) *model.QueryError {
	return &model.QueryError{Kind: model.ErrProtocol, Msg: "malformed DNS response: " + err.Error()}
}

func malformedQueryErr(err error) *model.QueryError {
	return &model.QueryError{Kind: model.ErrProtocol, Msg: "failed to encode DNS query: " + err.Error()}
}

func classify(ctx context.Context, err error) *model.QueryError {
	var qe *model.QueryError
	if errors.As(err, &qe) {
		return qe
	}
	ctxCanceled := ctx != nil && errors.Is(ctx.Err(), context.Canceled)
	if errors.Is(err, context.Canceled) || (ctxCanceled && isDeadlineErr(err)) {
		return &model.QueryError{Kind: model.ErrCanceled, Msg: err.Error()}
	}
	if errors.Is(err, context.DeadlineExceeded) || isDeadlineErr(err) {
		return &model.QueryError{Kind: model.ErrTimeout, Msg: err.Error()}
	}
	if isTLSErr(err) {
		return &model.QueryError{Kind: model.ErrTLS, Msg: err.Error()}
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return &model.QueryError{Kind: model.ErrTimeout, Msg: err.Error()}
	}
	return &model.QueryError{Kind: model.ErrNetwork, Msg: err.Error()}
}

func isDeadlineErr(err error) bool {
	return errors.Is(err, os.ErrDeadlineExceeded)
}

func isProtocolErr(err error) bool {
	var qe *model.QueryError
	return errors.As(err, &qe) && qe.Kind == model.ErrProtocol
}

func isTLSErr(err error) bool {
	var certVerify *tls.CertificateVerificationError
	var recordHeader tls.RecordHeaderError
	var alert tls.AlertError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certInvalid x509.CertificateInvalidError
	return errors.As(err, &certVerify) ||
		errors.As(err, &recordHeader) ||
		errors.As(err, &alert) ||
		errors.As(err, &unknownAuthority) ||
		errors.As(err, &hostname) ||
		errors.As(err, &certInvalid)
}

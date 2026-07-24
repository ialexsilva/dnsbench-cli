package probe

import (
	"context"
	"net/netip"
	"strings"

	"dnsbench/internal/model"

	"github.com/miekg/dns"
)

type rebindOutcome int

const (
	rebindBlocked rebindOutcome = iota
	rebindUnprotected
	rebindUndetermined
)

func (p *prober) checkRebind(ctx context.Context) {
	var details []string
	v4 := p.rebindFamily(ctx, p.cfg.RebindV4, dns.TypeA, "A", &details)
	v6 := p.rebindFamily(ctx, p.cfg.RebindV6, dns.TypeAAAA, "AAAA", &details)
	p.res.Rebind = model.RebindInfo{
		V4:      v4,
		V6:      v6,
		Overall: rebindOverall(v4, v6),
		Details: details,
	}
}

func (p *prober) rebindFamily(ctx context.Context, cases []RebindCase, qtype uint16, rrType string, details *[]string) model.Verdict {
	blocked := 0
	determined := 0
	for _, c := range cases {
		outcome, detail := classifyRebind(p.query(ctx, c.Host, qtype, false, false), c.Expected, rrType)
		*details = append(*details, c.Host+": "+detail)
		if outcome != rebindUndetermined {
			determined++
		}
		if outcome == rebindBlocked {
			blocked++
		}
	}
	switch {
	case determined == 0:
		return model.VerdictUnknown
	case blocked == determined:
		return model.VerdictYes
	case blocked == 0:
		return model.VerdictNo
	}
	return model.VerdictPartial
}

func classifyRebind(r model.QueryResult, expected, rrType string) (rebindOutcome, string) {
	if r.Err != nil {
		if r.Err.IsTimeout() {
			return rebindUndetermined, "timed out (undetermined)"
		}
		return rebindUndetermined, "query failed (" + string(r.Err.Kind) + ")"
	}
	switch r.Rcode {
	case dns.RcodeNameError:
		return rebindBlocked, "blocked (NXDOMAIN)"
	case dns.RcodeRefused:
		return rebindBlocked, "blocked (REFUSED)"
	}
	if answersContainIP(r, rrType, expected) {
		return rebindUnprotected, "resolved to private address " + expected + " (no rebind protection)"
	}
	if r.Rcode == dns.RcodeSuccess {
		if len(r.Answers) == 0 {
			return rebindBlocked, "blocked (empty answer)"
		}
		return rebindUndetermined, "unexpected answer: " + answerSummary(r)
	}
	return rebindUndetermined, "unexpected rcode " + rcodeName(r.Rcode)
}

func answersContainIP(r model.QueryResult, rrType, expected string) bool {
	want, err := netip.ParseAddr(expected)
	if err != nil {
		return false
	}
	for _, a := range r.Answers {
		if a.Type != rrType {
			continue
		}
		got, err := netip.ParseAddr(strings.TrimSpace(a.Data))
		if err == nil && got == want {
			return true
		}
	}
	return false
}

func rebindOverall(v4, v6 model.Verdict) model.Verdict {
	switch {
	case v4 == model.VerdictYes && v6 == model.VerdictYes:
		return model.VerdictYes
	case v4 == model.VerdictNo && v6 == model.VerdictNo:
		return model.VerdictNo
	case v4 == model.VerdictUnknown && v6 == model.VerdictUnknown:
		return model.VerdictUnknown
	}
	return model.VerdictPartial
}

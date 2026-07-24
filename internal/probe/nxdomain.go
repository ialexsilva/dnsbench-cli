package probe

import (
	"context"
	crand "crypto/rand"
	"math/rand/v2"

	"dnsbench/internal/model"

	"github.com/miekg/dns"
)

const labelAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func newLabelRand() *rand.Rand {
	var seed [32]byte
	crand.Read(seed[:])
	return rand.New(rand.NewChaCha8(seed))
}

func randomLabel(r *rand.Rand) string {
	b := make([]byte, 12)
	for i := range b {
		b[i] = labelAlphabet[r.IntN(len(labelAlphabet))]
	}
	return string(b)
}

func (p *prober) checkNXDomain(ctx context.Context) {
	r := newLabelRand()
	checks := []model.NXCheck{
		{Label: "nonexistent-tld", QName: "dnsbench-" + randomLabel(r)},
		{Label: "nonexistent-com", QName: randomLabel(r) + "." + p.cfg.NXTLDZone},
		{Label: "www-nonexistent", QName: "www." + randomLabel(r) + "." + p.cfg.NXTLDZone},
	}
	if p.cfg.UncachedZone != "" {
		checks = append(checks, model.NXCheck{
			Label: "controlled-zone",
			QName: randomLabel(r) + "." + p.cfg.UncachedZone,
		})
	}
	for i := range checks {
		res := p.query(ctx, checks[i].QName, dns.TypeA, false, false)
		checks[i].Behavior, checks[i].Detail = classifyNX(res)
	}
	p.res.NXChecks = checks
	p.res.NXInterception = nxVerdict(checks)
}

func classifyNX(r model.QueryResult) (model.NXBehavior, string) {
	if r.Err != nil {
		if r.Err.IsTimeout() {
			return model.NXTimeout, r.Err.Msg
		}
		return model.NXOther, r.Err.Error()
	}
	switch r.Rcode {
	case dns.RcodeNameError:
		return model.NXExpected, ""
	case dns.RcodeRefused:
		return model.NXBlocked, ""
	}
	hasIP := r.HasAnswerType("A") || r.HasAnswerType("AAAA")
	hasCNAME := r.HasAnswerType("CNAME")
	switch {
	case hasIP:
		return model.NXInterceptedIP, answerSummary(r)
	case hasCNAME:
		return model.NXInterceptedCN, answerSummary(r)
	case r.Rcode == dns.RcodeSuccess:
		return model.NXNoErrorEmpty, ""
	}
	return model.NXOther, "unexpected rcode " + rcodeName(r.Rcode)
}

func nxVerdict(checks []model.NXCheck) model.Verdict {
	intercepted := false
	clean := 0
	determined := 0
	for _, c := range checks {
		switch c.Behavior {
		case model.NXInterceptedIP, model.NXInterceptedCN:
			intercepted = true
			determined++
		case model.NXExpected, model.NXNoErrorEmpty:
			clean++
			determined++
		case model.NXBlocked:
			determined++
		}
	}
	switch {
	case intercepted:
		return model.VerdictYes
	case determined == 0:
		return model.VerdictUnknown
	case clean == determined:
		return model.VerdictNo
	}
	return model.VerdictUnknown
}

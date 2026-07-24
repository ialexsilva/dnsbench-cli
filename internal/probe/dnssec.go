package probe

import (
	"context"

	"dnsbench/internal/model"

	"github.com/miekg/dns"
)

func (p *prober) checkDNSSEC(ctx context.Context) {
	info := &p.res.DNSSEC
	signed := p.query(ctx, p.cfg.SignedDomain, dns.TypeA, true, false)
	if signed.Err == nil {
		info.ReturnsRRSIG = verdict(signed.HasAnswerType("RRSIG"))
		info.ADOnSigned = verdict(signed.AD)
		info.SignedResolves = verdict(signed.Rcode == dns.RcodeSuccess && len(signed.Answers) > 0)
	}
	bogus := p.query(ctx, p.cfg.BogusDomain, dns.TypeA, true, false)
	switch {
	case bogus.Err != nil:
	case bogus.Rcode == dns.RcodeServerFailure:
		info.BogusServfail = model.VerdictYes
	case bogus.Rcode == dns.RcodeSuccess && len(bogus.Answers) > 0:
		info.BogusServfail = model.VerdictNo
	}
	bogusCD := p.query(ctx, p.cfg.BogusDomain, dns.TypeA, true, true)
	if bogusCD.Err == nil {
		info.BogusWithCDResolves = verdict(bogusCD.Rcode == dns.RcodeSuccess && len(bogusCD.Answers) > 0)
	}
	switch {
	case info.BogusServfail == model.VerdictNo:
		info.Validating = model.VerdictNo
	case info.BogusServfail == model.VerdictYes &&
		(info.ReturnsRRSIG == model.VerdictYes || info.ADOnSigned == model.VerdictYes):
		info.Validating = model.VerdictYes
	case info.BogusServfail == model.VerdictYes:
		info.Validating = model.VerdictPartial
	}
}

package probe

import (
	"context"
	"strings"

	"dnsbench/internal/model"

	"github.com/miekg/dns"
)

const (
	dns64ProbeName    = "ipv4only.arpa"
	qnameMinProbeName = "qnamemintest.internet.nl"
)

func (p *prober) checkExtended(ctx context.Context) {
	info := &model.ExtendedInfo{
		DNS64:             model.VerdictUnknown,
		QNAMEMinimization: model.VerdictUnknown,
		HTTPSRecord:       model.VerdictUnknown,
	}
	p.res.Extended = info
	d := p.query(ctx, dns64ProbeName, dns.TypeAAAA, false, false)
	if d.Err == nil {
		if d.HasAnswerType("AAAA") {
			info.DNS64 = model.VerdictYes
		} else if d.Rcode == dns.RcodeSuccess && len(d.Answers) == 0 {
			info.DNS64 = model.VerdictNo
		}
	}
	q := p.query(ctx, qnameMinProbeName, dns.TypeTXT, false, false)
	if q.Err == nil {
		text := txtText(q)
		if strings.Contains(text, "HOORAY") {
			info.QNAMEMinimization = model.VerdictYes
		} else if strings.Contains(text, "NO") {
			info.QNAMEMinimization = model.VerdictNo
		}
	}
	h := p.query(ctx, p.cfg.SignedDomain, dns.TypeHTTPS, false, false)
	if h.Err == nil {
		if h.HasAnswerType("HTTPS") {
			info.HTTPSRecord = model.VerdictYes
		} else if h.Rcode == dns.RcodeSuccess {
			info.HTTPSRecord = model.VerdictNo
		}
	}
}

func txtText(r model.QueryResult) string {
	var b strings.Builder
	for _, a := range r.Answers {
		if a.Type == "TXT" {
			b.WriteString(a.Data)
			b.WriteString(" ")
		}
	}
	return b.String()
}

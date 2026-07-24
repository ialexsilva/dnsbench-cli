package probe

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"dnsbench/internal/model"
	"dnsbench/internal/transport"

	"github.com/miekg/dns"
)

func Run(ctx context.Context, servers []model.Server, cfg Config) map[string]*model.ProbeResult {
	factory := cfg.Factory
	if factory == nil {
		factory = transport.New
	}
	limit := cfg.Concurrency
	if limit < 1 {
		limit = 1
	}
	results := make(map[string]*model.ProbeResult, len(servers))
	var mu sync.Mutex
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for _, srv := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := probeServer(ctx, srv, cfg, factory)
			mu.Lock()
			results[srv.ID] = res
			mu.Unlock()
			if cfg.OnResult != nil {
				cfg.OnResult()
			}
		}()
	}
	wg.Wait()
	return results
}

func probeServer(ctx context.Context, srv model.Server, cfg Config, factory transport.Factory) *model.ProbeResult {
	res := newResult(srv.ID)
	querier, err := factory(srv, transport.Options{Timeout: cfg.Timeout})
	if err != nil {
		res.Errors = append(res.Errors, "could not set up transport: "+err.Error())
		return res
	}
	defer querier.Close()
	p := prober{server: srv, cfg: cfg, querier: querier, res: res}
	p.run(ctx)
	return res
}

func newResult(serverID string) *model.ProbeResult {
	return &model.ProbeResult{
		ServerID:     serverID,
		SupportsA:    model.VerdictUnknown,
		SupportsAAAA: model.VerdictUnknown,
		EDNS0:        model.VerdictUnknown,
		DNSSEC: model.DNSSECInfo{
			ReturnsRRSIG:        model.VerdictUnknown,
			SignedResolves:      model.VerdictUnknown,
			BogusServfail:       model.VerdictUnknown,
			BogusWithCDResolves: model.VerdictUnknown,
			ADOnSigned:          model.VerdictUnknown,
			Validating:          model.VerdictUnknown,
		},
		NXInterception: model.VerdictUnknown,
		Rebind: model.RebindInfo{
			V4:      model.VerdictUnknown,
			V6:      model.VerdictUnknown,
			Overall: model.VerdictUnknown,
		},
	}
}

type prober struct {
	server  model.Server
	cfg     Config
	querier transport.Querier
	res     *model.ProbeResult
}

func (p *prober) run(ctx context.Context) {
	baseline := p.checkReachability(ctx)
	if !p.res.Reachable {
		return
	}
	p.checkBaseline(ctx, baseline)
	if p.canceled(ctx) {
		return
	}
	p.checkDNSSEC(ctx)
	if p.canceled(ctx) {
		return
	}
	p.checkNXDomain(ctx)
	if p.canceled(ctx) {
		return
	}
	p.checkRebind(ctx)
	if p.canceled(ctx) {
		return
	}
	p.checkReverseName(ctx)
	if p.cfg.Extended && !p.canceled(ctx) {
		p.checkExtended(ctx)
	}
}

func (p *prober) canceled(ctx context.Context) bool {
	if ctx.Err() == nil {
		return false
	}
	p.res.Errors = append(p.res.Errors, "probe canceled before completion")
	return true
}

func (p *prober) query(ctx context.Context, name string, qtype uint16, do, cd bool) model.QueryResult {
	return p.querier.Query(ctx, transport.Question{Name: name, Qtype: qtype, DO: do, CD: cd})
}

func (p *prober) checkReachability(ctx context.Context) model.QueryResult {
	attempts := p.cfg.Retries + 1
	if attempts < 1 {
		attempts = 1
	}
	var last model.QueryResult
	for i := 0; i < attempts; i++ {
		last = p.query(ctx, p.cfg.ReachabilityDomain, dns.TypeA, false, false)
		if last.Err == nil {
			p.res.Reachable = true
			return last
		}
		if ctx.Err() != nil {
			break
		}
	}
	p.res.Errors = append(p.res.Errors, "reachability check failed: "+describeFailure(last.Err))
	return last
}

func (p *prober) checkBaseline(ctx context.Context, baseline model.QueryResult) {
	p.res.BaselineRcode = baseline.Rcode
	p.res.SupportsA = verdict(baseline.HasAnswerType("A"))
	if baseline.EDNSUDPSize > 0 {
		p.res.EDNS0 = model.VerdictYes
		p.res.AdvertisedUDPSize = baseline.EDNSUDPSize
	} else {
		p.res.EDNS0 = model.VerdictNo
	}
	aaaa := p.query(ctx, p.cfg.ReachabilityDomain, dns.TypeAAAA, false, false)
	if aaaa.Err == nil {
		p.res.SupportsAAAA = verdict(aaaa.HasAnswerType("AAAA"))
	}
}

func (p *prober) checkReverseName(ctx context.Context) {
	if _, ok := p.server.IP(); !ok {
		return
	}
	reverse, err := dns.ReverseAddr(p.server.Address)
	if err != nil {
		return
	}
	r := p.query(ctx, reverse, dns.TypePTR, false, false)
	if r.Err != nil {
		return
	}
	if ptr, ok := r.FirstAnswer("PTR"); ok {
		p.res.ReverseName = strings.TrimSuffix(strings.TrimSpace(ptr.Data), ".")
	}
}

func verdict(ok bool) model.Verdict {
	if ok {
		return model.VerdictYes
	}
	return model.VerdictNo
}

func describeFailure(e *model.QueryError) string {
	if e == nil {
		return "unknown error"
	}
	switch e.Kind {
	case model.ErrTimeout:
		return "timed out waiting for a response"
	case model.ErrNetwork:
		return "network error: " + e.Msg
	case model.ErrTLS:
		return "TLS handshake failed: " + e.Msg
	case model.ErrHTTP:
		return "HTTP error: " + e.Msg
	case model.ErrProtocol:
		return "protocol error: " + e.Msg
	case model.ErrCanceled:
		return "probe was canceled"
	}
	return e.Error()
}

func rcodeName(rcode int) string {
	if s, ok := dns.RcodeToString[rcode]; ok {
		return s
	}
	return "RCODE" + strconv.Itoa(rcode)
}

func answerSummary(r model.QueryResult) string {
	parts := make([]string, 0, len(r.Answers))
	for _, a := range r.Answers {
		parts = append(parts, a.Type+" "+a.Data)
	}
	return strings.Join(parts, ", ")
}

package bench

import (
	"context"
	"math/rand/v2"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"dnsbench/internal/model"
)

const (
	minCongestedFailureDomains = 3
	recentAnswerWindow         = 30 * time.Second
)

type paceAdjust struct {
	at                time.Time
	from              time.Duration
	to                time.Duration
	window            time.Duration
	timeouts          int
	servers           int
	cleanAnswers      int
	evidenceStartedAt time.Time
	evidenceEndedAt   time.Time
	serverIDs         []string
	failureDomains    []string
	categories        []model.Category
	protocols         []model.Protocol
	recovering        bool
}

type paceTimeout struct {
	serverID   string
	domainKey  string
	domainName string
	category   model.Category
	protocol   model.Protocol
	startedAt  time.Time
}

type pacer struct {
	mu       sync.Mutex
	interval time.Duration
	adaptive bool
	rng      *rand.Rand
	next     time.Time

	baseInterval  time.Duration
	maxInterval   time.Duration
	recoveryStep  time.Duration
	window        time.Duration
	healthyFor    time.Duration
	retainFor     time.Duration
	cooldown      time.Duration
	cleanTarget   int
	cleanCount    int
	suppressUntil time.Time
	timeouts      []paceTimeout
	answeredAt    map[string]time.Time
}

func newPacer(interval time.Duration, adaptive bool, seed int64, queryTimeout time.Duration) *pacer {
	if interval <= 0 {
		return nil
	}
	cooldown := 3 * time.Second
	if queryTimeout > cooldown {
		cooldown = queryTimeout
	}
	retainFor := max(queryTimeout, 0) + 2*time.Second
	return &pacer{
		interval:     interval,
		adaptive:     adaptive,
		rng:          rand.New(rand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15)),
		baseInterval: interval,
		maxInterval:  interval * 4,
		recoveryStep: max(interval/2, 1),
		window:       2 * time.Second,
		healthyFor:   recentAnswerWindow,
		retainFor:    retainFor,
		cooldown:     cooldown,
		cleanTarget:  50,
		answeredAt:   make(map[string]time.Time),
	}
}

func (p *pacer) wait(ctx context.Context) bool {
	if p == nil {
		return ctx.Err() == nil
	}
	p.mu.Lock()
	at := p.next
	if now := time.Now(); at.Before(now) {
		at = now
	}
	var jitter time.Duration
	if p.adaptive {
		jitter = time.Duration(p.rng.Int64N(int64(p.interval)/4 + 1))
	}
	p.next = at.Add(p.interval + jitter)
	p.mu.Unlock()
	return sleepCtx(ctx, time.Until(at))
}

func (p *pacer) observeAttempt(
	server model.Server,
	category model.Category,
	startedAt time.Time,
	res model.QueryResult,
) (paceAdjust, bool) {
	return p.observeAttemptAt(server, category, startedAt, time.Now(), res)
}

func (p *pacer) observeAttemptAt(
	server model.Server,
	category model.Category,
	startedAt time.Time,
	observedAt time.Time,
	res model.QueryResult,
) (paceAdjust, bool) {
	if p == nil || !p.adaptive {
		return paceAdjust{}, false
	}
	timedOut := res.Err.IsTimeout()
	if !timedOut && !res.Answered() {
		return paceAdjust{}, false
	}
	if startedAt.IsZero() {
		startedAt = observedAt
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if timedOut {
		return p.backOffLocked(server, category, startedAt, observedAt)
	}
	p.answeredAt[serverHealthKey(server)] = observedAt
	return p.recoverLocked(observedAt)
}

func (p *pacer) recoverLocked(now time.Time) (paceAdjust, bool) {
	if p.interval <= p.baseInterval || now.Before(p.suppressUntil) {
		return paceAdjust{}, false
	}
	p.cleanCount++
	if p.cleanCount < p.cleanTarget {
		return paceAdjust{}, false
	}
	p.cleanCount = 0
	from := p.interval
	p.interval = max(p.interval-p.recoveryStep, p.baseInterval)
	return paceAdjust{
		at:           now,
		from:         from,
		to:           p.interval,
		cleanAnswers: p.cleanTarget,
		recovering:   true,
	}, true
}

func (p *pacer) backOffLocked(
	server model.Server,
	category model.Category,
	startedAt time.Time,
	observedAt time.Time,
) (paceAdjust, bool) {
	if observedAt.Before(p.suppressUntil) {
		return paceAdjust{}, false
	}
	answeredAt, answered := p.answeredAt[serverHealthKey(server)]
	if !answered || answeredAt.After(startedAt) || startedAt.Sub(answeredAt) > p.healthyFor {
		return paceAdjust{}, false
	}
	p.cleanCount = 0
	cutoff := observedAt.Add(-p.retainFor)
	kept := p.timeouts[:0]
	for _, t := range p.timeouts {
		if !t.startedAt.Before(cutoff) {
			kept = append(kept, t)
		}
	}
	domainKey, domainName := serverFailureDomain(server)
	p.timeouts = append(kept, paceTimeout{
		serverID:   serverHealthKey(server),
		domainKey:  domainKey,
		domainName: domainName,
		category:   category,
		protocol:   server.Protocol,
		startedAt:  startedAt,
	})
	cluster := p.congestedClusterLocked()
	if len(cluster) == 0 {
		return paceAdjust{}, false
	}
	p.timeouts = p.timeouts[:0]
	p.cleanCount = 0
	p.suppressUntil = observedAt.Add(p.cooldown)
	from := p.interval
	firstBackoff := min(p.baseInterval*2, p.maxInterval)
	if p.interval < firstBackoff {
		p.interval = firstBackoff
	} else {
		p.interval = p.maxInterval
	}
	if p.interval == from {
		return paceAdjust{}, false
	}
	serverIDs, failureDomains, categories, protocols, firstStart, lastStart := paceEvidence(cluster)
	return paceAdjust{
		at:                observedAt,
		from:              from,
		to:                p.interval,
		window:            p.window,
		timeouts:          len(cluster),
		servers:           len(serverIDs),
		evidenceStartedAt: firstStart,
		evidenceEndedAt:   lastStart,
		serverIDs:         serverIDs,
		failureDomains:    failureDomains,
		categories:        categories,
		protocols:         protocols,
	}, true
}

func (p *pacer) congestedClusterLocked() []paceTimeout {
	slices.SortFunc(p.timeouts, func(a, b paceTimeout) int {
		return a.startedAt.Compare(b.startedAt)
	})
	var best []paceTimeout
	bestDomains := 0
	for left := range p.timeouts {
		domains := make(map[string]struct{})
		for right := left; right < len(p.timeouts); right++ {
			if p.timeouts[right].startedAt.Sub(p.timeouts[left].startedAt) > p.window {
				break
			}
			domains[p.timeouts[right].domainKey] = struct{}{}
			if len(domains) > bestDomains ||
				(len(domains) == bestDomains && right-left+1 > len(best)) {
				bestDomains = len(domains)
				best = p.timeouts[left : right+1]
			}
		}
	}
	if bestDomains < minCongestedFailureDomains {
		return nil
	}
	return append([]paceTimeout(nil), best...)
}

func paceEvidence(cluster []paceTimeout) (
	serverIDs []string,
	failureDomains []string,
	categories []model.Category,
	protocols []model.Protocol,
	firstStart time.Time,
	lastStart time.Time,
) {
	servers := make(map[string]struct{}, len(cluster))
	domains := make(map[string]string, len(cluster))
	cats := make(map[model.Category]struct{}, len(cluster))
	protos := make(map[model.Protocol]struct{}, len(cluster))
	for i, t := range cluster {
		servers[t.serverID] = struct{}{}
		domains[t.domainKey] = t.domainName
		if t.category != "" {
			cats[t.category] = struct{}{}
		}
		if t.protocol != "" {
			protos[t.protocol] = struct{}{}
		}
		if i == 0 || t.startedAt.Before(firstStart) {
			firstStart = t.startedAt
		}
		if i == 0 || t.startedAt.After(lastStart) {
			lastStart = t.startedAt
		}
	}
	for id := range servers {
		serverIDs = append(serverIDs, id)
	}
	for _, name := range domains {
		failureDomains = append(failureDomains, name)
	}
	for category := range cats {
		categories = append(categories, category)
	}
	for protocol := range protos {
		protocols = append(protocols, protocol)
	}
	slices.Sort(serverIDs)
	slices.Sort(failureDomains)
	slices.Sort(categories)
	slices.Sort(protocols)
	return serverIDs, failureDomains, categories, protocols, firstStart, lastStart
}

func serverHealthKey(server model.Server) string {
	if server.ID != "" {
		return server.ID
	}
	key, _ := serverFailureDomain(server)
	return key
}

func serverFailureDomain(server model.Server) (string, string) {
	if operator := normalizedLabel(server.Operator); operator != "" {
		return "operator:" + strings.ToLower(operator), operator
	}
	if server.Address != "" {
		address := strings.ToLower(strings.TrimSpace(server.Address))
		if ip, ok := server.IP(); ok {
			address = ip.Unmap().String()
		}
		return "address:" + address, address
	}
	if server.DoHURL != "" {
		if parsed, err := url.Parse(server.DoHURL); err == nil {
			if host := strings.ToLower(parsed.Hostname()); host != "" {
				return "host:" + host, host
			}
		}
	}
	if host := strings.ToLower(strings.TrimSpace(server.TLSHostname)); host != "" {
		return "host:" + host, host
	}
	label := normalizedLabel(server.DisplayName())
	if label == "" {
		label = "unknown resolver"
	}
	key := normalizedLabel(server.ID)
	if key == "" {
		key = label
	}
	return "server:" + strings.ToLower(key), label
}

func normalizedLabel(label string) string {
	return strings.Join(strings.Fields(label), " ")
}

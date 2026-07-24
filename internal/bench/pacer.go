package bench

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"
)

type paceAdjust struct {
	from     time.Duration
	to       time.Duration
	window   time.Duration
	timeouts int
	servers  int
	faster   bool
}

type paceTimeout struct {
	server string
	at     time.Time
}

type pacer struct {
	mu       sync.Mutex
	interval time.Duration
	rng      *rand.Rand
	next     time.Time

	minInterval   time.Duration
	maxInterval   time.Duration
	step          time.Duration
	window        time.Duration
	cooldown      time.Duration
	cleanTarget   int
	cleanCount    int
	suppressUntil time.Time
	timeouts      []paceTimeout
	answered      map[string]bool
}

func newPacer(interval time.Duration, seed int64, queryTimeout time.Duration) *pacer {
	if interval <= 0 {
		return nil
	}
	cooldown := 3 * time.Second
	if queryTimeout > cooldown {
		cooldown = queryTimeout
	}
	return &pacer{
		interval:    interval,
		rng:         rand.New(rand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15)),
		minInterval: max(interval/4, 1),
		maxInterval: interval * 8,
		step:        max(interval/8, 1),
		window:      2 * time.Second,
		cooldown:    cooldown,
		cleanTarget: 50,
		answered:    make(map[string]bool),
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
	jitter := time.Duration(p.rng.Int64N(int64(p.interval)/4 + 1))
	p.next = at.Add(p.interval + jitter)
	p.mu.Unlock()
	return sleepCtx(ctx, time.Until(at))
}

func (p *pacer) observe(serverID string, timedOut bool) (paceAdjust, bool) {
	if p == nil {
		return paceAdjust{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if timedOut {
		return p.backOffLocked(serverID, time.Now())
	}
	p.answered[serverID] = true
	return p.speedUpLocked()
}

func (p *pacer) speedUpLocked() (paceAdjust, bool) {
	if p.interval <= p.minInterval {
		return paceAdjust{}, false
	}
	p.cleanCount++
	if p.cleanCount < p.cleanTarget {
		return paceAdjust{}, false
	}
	p.cleanCount = 0
	from := p.interval
	p.interval = max(p.interval-p.step, p.minInterval)
	return paceAdjust{from: from, to: p.interval, faster: true}, true
}

func (p *pacer) backOffLocked(serverID string, now time.Time) (paceAdjust, bool) {
	if !p.answered[serverID] || now.Before(p.suppressUntil) {
		return paceAdjust{}, false
	}
	cutoff := now.Add(-p.window)
	kept := p.timeouts[:0]
	for _, t := range p.timeouts {
		if t.at.After(cutoff) {
			kept = append(kept, t)
		}
	}
	p.timeouts = append(kept, paceTimeout{server: serverID, at: now})
	distinct := make(map[string]struct{}, len(p.timeouts))
	for _, t := range p.timeouts {
		distinct[t.server] = struct{}{}
	}
	if len(distinct) < 3 {
		return paceAdjust{}, false
	}
	count := len(p.timeouts)
	p.timeouts = p.timeouts[:0]
	p.cleanCount = 0
	p.suppressUntil = now.Add(p.cooldown)
	from := p.interval
	p.interval = min(p.interval*2, p.maxInterval)
	if p.interval == from {
		return paceAdjust{}, false
	}
	return paceAdjust{
		from:     from,
		to:       p.interval,
		window:   p.window,
		timeouts: count,
		servers:  len(distinct),
	}, true
}

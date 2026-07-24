package bench

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"dnsbench/internal/model"
	"dnsbench/internal/transport"

	"github.com/miekg/dns"
)

type Engine struct {
	servers        []model.Server
	cfg            model.BenchConfig
	factory        transport.Factory
	events         chan model.Event
	seed           int64
	canaryInterval time.Duration
	gate           *pauseGate
	pace           *pacer
	slots          chan struct{}
	sessions       map[string]transport.Querier

	mu           sync.Mutex
	samples      []model.Sample
	triage       map[string]*model.TriageResult
	states       map[string]model.ServerState
	paceMu       sync.Mutex
	lastQueryEnd map[string]time.Time
}

func NewEngine(servers []model.Server, cfg model.BenchConfig, factory transport.Factory) *Engine {
	if factory == nil {
		factory = transport.New
	}
	seed := resolveSeed(cfg.Seed)
	return &Engine{
		servers:        append([]model.Server(nil), servers...),
		cfg:            cfg,
		factory:        factory,
		events:         make(chan model.Event, eventBufferSize(len(servers), cfg)),
		seed:           seed,
		canaryInterval: 5 * time.Second,
		gate:           &pauseGate{},
		pace:           newPacer(cfg.PaceInterval, seed, cfg.Timeout),
		slots:          make(chan struct{}, max(1, cfg.Concurrency)),
		triage:         make(map[string]*model.TriageResult),
		states:         make(map[string]model.ServerState),
		lastQueryEnd:   make(map[string]time.Time),
	}
}

func eventBufferSize(serverCount int, cfg model.BenchConfig) int {
	cats := len(cfg.Categories)
	if cats == 0 {
		cats = 1
	}
	rounds := cfg.Rounds + cfg.WarmupRounds
	size := serverCount*rounds*cats*2 + rounds + serverCount*2 + 64
	if size < 256 {
		return 256
	}
	if size > 65536 {
		return 65536
	}
	return size
}

func resolveSeed(configured int64) int64 {
	if configured != 0 {
		return configured
	}
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return time.Now().UnixNano() | 1
	}
	v := int64(binary.LittleEndian.Uint64(b[:]) >> 1)
	if v == 0 {
		return 1
	}
	return v
}

func (e *Engine) Events() <-chan model.Event { return e.events }

func (e *Engine) Pause() { e.gate.pause() }

func (e *Engine) Resume() { e.gate.unpause() }

func (e *Engine) Result() ([]model.Sample, map[string]*model.TriageResult, map[string]model.ServerState, int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	samples := make([]model.Sample, len(e.samples))
	copy(samples, e.samples)
	triage := make(map[string]*model.TriageResult, len(e.triage))
	for id, tr := range e.triage {
		triage[id] = tr
	}
	states := make(map[string]model.ServerState, len(e.states))
	for id, st := range e.states {
		states[id] = st
	}
	return samples, triage, states, e.seed
}

func (e *Engine) Run(ctx context.Context) error {
	defer close(e.events)
	active, err := e.prepare(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(active) == 0 {
		e.emit(ctx, model.Event{Type: model.EvWarn, Msg: "no active servers to benchmark"})
		return errors.New("no active servers to benchmark")
	}
	active = e.openSessions(ctx, active)
	defer e.closeSessions()
	if len(active) == 0 {
		return errors.New("no active servers to benchmark: every persistent session failed to open")
	}
	if err := e.runRounds(ctx, active); err != nil {
		return err
	}
	e.emit(ctx, model.Event{Type: model.EvDone})
	return nil
}

func (e *Engine) emit(ctx context.Context, ev model.Event) {
	select {
	case e.events <- ev:
		return
	default:
	}
	select {
	case e.events <- ev:
	case <-ctx.Done():
	}
}

func (e *Engine) record(ctx context.Context, sample model.Sample) {
	e.mu.Lock()
	e.samples = append(e.samples, sample)
	e.mu.Unlock()
	e.emit(ctx, model.Event{Type: model.EvSample, ServerID: sample.ServerID, Round: sample.Round, Sample: &sample})
}

func (e *Engine) setState(id string, st model.ServerState) {
	e.mu.Lock()
	e.states[id] = st
	e.mu.Unlock()
}

func (e *Engine) openSessions(ctx context.Context, active []model.Server) []model.Server {
	if e.cfg.Session != model.SessionPersistent {
		return active
	}
	e.sessions = make(map[string]transport.Querier, len(active))
	kept := make([]model.Server, 0, len(active))
	for _, s := range active {
		q, err := e.factory(s, transport.Options{Timeout: e.cfg.Timeout, Persistent: true})
		if err != nil {
			e.setState(s.ID, model.StateError)
			e.emit(ctx, model.Event{
				Type:     model.EvStateChange,
				ServerID: s.ID,
				State:    model.StateError,
				Msg:      "failed to open a persistent session: " + err.Error(),
			})
			continue
		}
		e.sessions[s.ID] = q
		kept = append(kept, s)
	}
	return kept
}

func (e *Engine) closeSessions() {
	for _, q := range e.sessions {
		q.Close()
	}
	e.sessions = nil
}

func (e *Engine) singleQuery(ctx context.Context, s model.Server, name string) model.QueryResult {
	q, err := e.factory(s, transport.Options{Timeout: e.cfg.Timeout})
	if err != nil {
		return model.QueryResult{Err: &model.QueryError{Kind: model.ErrNetwork, Msg: err.Error()}}
	}
	defer q.Close()
	return q.Query(ctx, transport.Question{Name: name, Qtype: dns.TypeA})
}

func (e *Engine) attemptQuery(ctx context.Context, s model.Server, name string) model.QueryResult {
	if e.cfg.Session == model.SessionPersistent {
		return e.sessions[s.ID].Query(ctx, transport.Question{Name: name, Qtype: dns.TypeA})
	}
	return e.singleQuery(ctx, s, name)
}

func (e *Engine) observePace(ctx context.Context, serverID string, res model.QueryResult) {
	timedOut := res.Err.IsTimeout()
	if !timedOut && !res.Answered() {
		return
	}
	adj, changed := e.pace.observe(serverID, timedOut)
	if !changed || adj.faster {
		return
	}
	e.emit(ctx, model.Event{
		Type: model.EvPaceAdjust,
		Msg: fmt.Sprintf("local congestion — pacing eased %s → %s (%d timeouts on %d servers)",
			adj.from, adj.to, adj.timeouts, adj.servers),
	})
}

func (e *Engine) acquireSlot(ctx context.Context) bool {
	select {
	case e.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (e *Engine) releaseSlot() { <-e.slots }

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (e *Engine) waitServerGap(ctx context.Context, serverID string) bool {
	e.paceMu.Lock()
	last := e.lastQueryEnd[serverID]
	e.paceMu.Unlock()
	if last.IsZero() {
		return ctx.Err() == nil
	}
	return sleepCtx(ctx, time.Until(last.Add(e.cfg.PerServerGap)))
}

func (e *Engine) markServerQueryDone(serverID string) {
	e.paceMu.Lock()
	e.lastQueryEnd[serverID] = time.Now()
	e.paceMu.Unlock()
}

type pauseGate struct {
	mu     sync.Mutex
	paused bool
	resume chan struct{}
}

func (g *pauseGate) pause() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.paused {
		g.paused = true
		g.resume = make(chan struct{})
	}
}

func (g *pauseGate) unpause() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused {
		g.paused = false
		close(g.resume)
	}
}

func (g *pauseGate) wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		g.mu.Lock()
		if !g.paused {
			g.mu.Unlock()
			return nil
		}
		resume := g.resume
		g.mu.Unlock()
		select {
		case <-resume:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

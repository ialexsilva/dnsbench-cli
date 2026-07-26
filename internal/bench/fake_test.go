package bench

import (
	"context"
	"sync"
	"time"

	"dnsbench/internal/model"
	"dnsbench/internal/transport"
)

type respondFunc func(ctx context.Context, n int, q transport.Question) model.QueryResult

type fakeScript struct {
	mu      sync.Mutex
	created int
	closed  int
	queries int
	respond respondFunc
}

type fakeFactory struct {
	mu      sync.Mutex
	scripts map[string]*fakeScript
}

func newFakeFactory() *fakeFactory {
	return &fakeFactory{scripts: make(map[string]*fakeScript)}
}

func (f *fakeFactory) script(id string, respond respondFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[id] = &fakeScript{respond: respond}
}

func (f *fakeFactory) counts(id string) (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sc := f.scripts[id]
	if sc == nil {
		return 0, 0
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.created, sc.closed
}

func (f *fakeFactory) factory(s model.Server, o transport.Options) (transport.Querier, error) {
	f.mu.Lock()
	sc := f.scripts[s.ID]
	if sc == nil {
		sc = &fakeScript{respond: staticResult(okResult(5 * time.Millisecond))}
		f.scripts[s.ID] = sc
	}
	f.mu.Unlock()
	sc.mu.Lock()
	sc.created++
	sc.mu.Unlock()
	return &fakeQuerier{script: sc}, nil
}

type fakeQuerier struct {
	script *fakeScript
}

func (q *fakeQuerier) Query(ctx context.Context, question transport.Question) model.QueryResult {
	sc := q.script
	sc.mu.Lock()
	n := sc.queries
	sc.queries++
	respond := sc.respond
	sc.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return model.QueryResult{Err: &model.QueryError{Kind: model.ErrCanceled, Msg: err.Error()}}
	}
	return respond(ctx, n, question)
}

func (q *fakeQuerier) Protocol() model.Protocol { return model.ProtoUDP }

func (q *fakeQuerier) Close() error {
	q.script.mu.Lock()
	q.script.closed++
	q.script.mu.Unlock()
	return nil
}

func staticResult(res model.QueryResult) respondFunc {
	return func(context.Context, int, transport.Question) model.QueryResult { return res }
}

func okResult(rtt time.Duration) model.QueryResult {
	return model.QueryResult{
		RTT:     rtt,
		Rcode:   0,
		Answers: []model.RR{{Type: "A", TTL: 300, Data: "192.0.2.1"}},
	}
}

func timeoutResult() model.QueryResult {
	return model.QueryResult{Err: &model.QueryError{Kind: model.ErrTimeout, Msg: "read: i/o timeout"}}
}

func networkResult(msg string) model.QueryResult {
	return model.QueryResult{Err: &model.QueryError{Kind: model.ErrNetwork, Msg: msg}}
}

func testServers(ids ...string) []model.Server {
	servers := make([]model.Server, 0, len(ids))
	for _, id := range ids {
		servers = append(servers, model.Server{
			ID:       id,
			Name:     id,
			Address:  "127.0.0.1",
			Protocol: model.ProtoUDP,
			Source:   model.SourceUser,
			Enabled:  true,
		})
	}
	return servers
}

func builtinTestServers(ids ...string) []model.Server {
	servers := testServers(ids...)
	for i := range servers {
		servers[i].Source = model.SourceBuiltin
	}
	return servers
}

func testConfig() model.BenchConfig {
	return model.BenchConfig{
		Rounds:        3,
		WarmupRounds:  1,
		Categories:    []model.Category{model.CatCached, model.CatTLD},
		CachedDomains: []string{"a.example", "b.example", "c.example"},
		TLDZone:       "com",
		Timeout:       200 * time.Millisecond,
		RetryInterval: time.Millisecond,
		Concurrency:   4,
		Session:       model.SessionCold,
		Seed:          1,
	}
}

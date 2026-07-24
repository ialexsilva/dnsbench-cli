package transport

import (
	"context"
	"sync"
	"testing"
	"time"

	"dnsbench/internal/model"

	"github.com/miekg/dns"
)

func TestUDPPersistentReusesOneSocketAndDrainsStaleReplies(t *testing.T) {
	var mu sync.Mutex
	var remotes []string
	base := testHandler(nil)
	h := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		mu.Lock()
		remotes = append(remotes, w.RemoteAddr().String())
		mu.Unlock()
		base(w, req)
	})
	port := startDualServer(t, h)
	srv := model.Server{ID: "u", Protocol: model.ProtoUDP, Address: "127.0.0.1", Port: port}
	q := mustNew(t, srv, Options{Timeout: 150 * time.Millisecond, Persistent: true})

	first := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, first)
	if first.Reused {
		t.Error("first query Reused = true, want false")
	}

	timedOut := q.Query(context.Background(), Question{Name: "slow.test", Qtype: dns.TypeA})
	if !timedOut.Err.IsTimeout() {
		t.Fatalf("slow query error = %v, want a timeout", timedOut.Err)
	}

	time.Sleep(250 * time.Millisecond)

	third := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
	mustAnswer(t, third)
	if !third.Reused {
		t.Error("third query Reused = false, want true")
	}
	if len(third.Answers) != 1 || third.Answers[0].Data != "192.0.2.1" {
		t.Fatalf("third answers = %+v, want the a.test A record", third.Answers)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(remotes) != 3 {
		t.Fatalf("server saw %d queries, want 3", len(remotes))
	}
	for _, r := range remotes[1:] {
		if r != remotes[0] {
			t.Fatalf("queries came from different source addresses: %v", remotes)
		}
	}
}

func TestUDPColdSessionDialsPerQuery(t *testing.T) {
	var mu sync.Mutex
	var remotes []string
	base := testHandler(nil)
	h := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		mu.Lock()
		remotes = append(remotes, w.RemoteAddr().String())
		mu.Unlock()
		base(w, req)
	})
	port := startDualServer(t, h)
	srv := model.Server{ID: "u", Protocol: model.ProtoUDP, Address: "127.0.0.1", Port: port}
	q := mustNew(t, srv, Options{Timeout: time.Second})

	for i := 0; i < 2; i++ {
		res := q.Query(context.Background(), Question{Name: "a.test", Qtype: dns.TypeA})
		mustAnswer(t, res)
		if res.Reused {
			t.Errorf("cold query %d Reused = true, want false", i)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(remotes) != 2 {
		t.Fatalf("server saw %d queries, want 2", len(remotes))
	}
	if remotes[0] == remotes[1] {
		t.Fatalf("cold session reused the source address %s, want a fresh socket per query", remotes[0])
	}
}

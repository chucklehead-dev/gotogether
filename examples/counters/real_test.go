package counters

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite" // the real tier's driver

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// realMailer is a mailer that takes 20 ms to send (the real tier's
// stand-in: nothing is lost, so every welcome is notified, but not at
// once, so a settle recorded before the processors caught up is seen).
type realMailer struct {
	mu   sync.Mutex
	sent []Mail
}

func (m *realMailer) Send(_ context.Context, key, msg string) error {
	time.Sleep(20 * time.Millisecond) //qenv:ok the real tier's mailer, outside any simulation
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, Mail{Key: key, Msg: msg})
	return nil
}

// The real tier, judged by the same bindings (gristtest.Tier): the app
// on SQLite (a temp file, sqlstore) served over real HTTP, a node
// (App.Start: the relay, the tenant watcher and the pollers, goroutines),
// four clients calling the API at once over TCP (grist/client: each call
// recorded where the client sees it, <tenant>/clients, retried under its
// Idempotency-Key), every step written to one PerStep file and validated
// with counters.binding.yaml (the store's side) and
// counters.calls.binding.yaml (the clients', linearized), as a
// simulation's are. Every fifth command's answer is lost after the
// command was served: its client retries under the same key, the marker
// answers (Idempotent-Replayed). Halfway through the first phase the first
// node is killed: its sink dies at that instant (whatever its relay had
// not recorded is lost with it, as with kill -9), its goroutines are
// stopped, and a second node takes the server's traffic, with a relay of
// its own that records the step log from its first row (its copies of
// the first node's records are Reconstruct's duplicates) and every row
// the first never recorded. The second phase creates new counters, whose
// welcomes only the second node sends. The tier then waits for the
// processors and the relay, probes every counter over the API and
// records the settle, so the eventual val (every counter welcomed) is
// judged; and every answer a client got is checked against the store's
// side (gristtest.Answered: a refusal the store did not record would pass
// both bindings).
func TestRealHTTP(t *testing.T) {
	qtest.RequireQuint(t)
	tenants := []string{"t1", "t2"}
	tier := gristtest.NewTier(t, App, gristtest.TierConfig{Tenants: tenants,
		Deps: func(ctx context.Context, st grist.Store) context.Context {
			return StoreDep.With(MailerDep.With(ctx, &realMailer{}), st)
		}})
	// clients runs four clients of 8 operations each on counters, names
	// from names; after the first client's half, kill runs (if set)
	clients := func(phase int, counters, names []string, kill func()) {
		var wg sync.WaitGroup
		for c := 1; c <= 4; c++ {
			wg.Go(func() {
				ctx := qobs.Thread(tier.Clients()) // one thread per goroutine
				r := rand.New(rand.NewPCG(uint64(phase), uint64(c)))
				for op := 1; op <= 8; op++ {
					if op == 5 && c == 1 && kill != nil {
						kill()
					}
					env := grist.Envelope{ID: fmt.Sprintf("p%d-c%d-%d", phase, c, op), Actor: fmt.Sprint("client", c)}
					env.Tenant = tenants[r.IntN(len(tenants))]
					counter, name := counters[r.IntN(len(counters))], names[r.IntN(len(names))]
					switch r.IntN(3) {
					case 0:
						tier.Do(ctx, env, "create-counter", CreateCounter{Counter: counter, Name: name})
					case 1:
						tier.Do(ctx, env, "increment-counter", IncrementCounter{Counter: counter, By: 1 + r.IntN(3)})
					default:
						tier.Do(ctx, env, "show-counter", ShowCounter{Counter: counter})
					}
				}
			})
		}
		wg.Wait()
	}

	first := tier.Start("node1")
	clients(1, []string{"a", "b", "c"}, []string{"a", "b"}, func() {
		first.Kill()
		tier.Start("node2") // its relay records the step log from its first row
	})
	behind, err := first.Relay.Behind(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if behind == 0 {
		t.Fatal("the killed node's relay recorded every row: the second node's relay has nothing of its own to record, and this run proves nothing of it")
	}
	clients(2, []string{"d", "e", "f"}, []string{"c", "d"}, nil)
	tier.Settle(func(ctx context.Context) { // the probe: every counter, on the settled system
		for _, tn := range tenants {
			for i, c := range []string{"a", "b", "c", "d", "e", "f"} {
				tier.Do(ctx, grist.Envelope{ID: fmt.Sprint("probe-", i), Tenant: tn, Actor: "probe"}, "show-counter", ShowCounter{Counter: c})
			}
		}
	})
	traces := tier.Check(commits, calls)

	steps := tier.Steps()
	relayed := map[string]int{}
	for _, s := range steps {
		if s.Recorder == grist.RecorderID(App.Name, s.Actor) {
			relayed[s.Actor]++
		}
	}
	if relayed["t1"] == 0 || relayed["t2"] == 0 {
		t.Fatalf("relayed steps by tenant %v: want both tenants' commits, from the step log", relayed)
	}
	kept := 0
	for _, tr := range traces {
		for _, s := range tr.Steps {
			if s.Recorder == grist.RecorderID(App.Name, s.Actor) {
				kept++
			}
		}
	}
	if kept >= relayed["t1"]+relayed["t2"] {
		t.Errorf("%d relayed records, %d kept: want the second node's copies of the first's rows dropped as duplicates", relayed["t1"]+relayed["t2"], kept)
	}
	t.Logf("%d relayed (%d kept after the second relay's copies); the killed node's relay was %d rows behind", relayed["t1"]+relayed["t2"], kept, behind)
}

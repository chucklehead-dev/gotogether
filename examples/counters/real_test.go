package counters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite" // the real tier's driver

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
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

// killable is a sink that dies with its process: after kill, nothing it
// is given is written, as a killed node's records after the instant of
// its death are not (its goroutines are stopped soon after; whatever
// they record meanwhile is lost with it).
type killable struct {
	mu   sync.Mutex
	dead bool
	s    qobs.Sink
}

func (k *killable) Record(ctx context.Context, s *qtrace.Step) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.dead {
		k.s.Record(ctx, s)
	}
}

func (k *killable) kill() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.dead = true
}

// The real tier, judged by the same bindings: the app on SQLite (a temp
// file, sqlstore) served over real HTTP (grist.NewHandler behind a real
// http.Server on a loopback port), a node (App.Start: the relay, the
// tenant watcher and the pollers, goroutines), four clients calling the
// API at once over TCP (grist/client: each call recorded where the
// client sees it, <tenant>/clients, retried under its Idempotency-Key),
// every step written to one file (qobs.FileSink, PerStep) and validated
// with counters.binding.yaml (the store's side) and
// counters.calls.binding.yaml (the clients', linearized), as a
// simulation's are. Every fifth command's answer is lost after the
// command was served (the connection closed without a response): its
// client retries under the same key, the marker answers
// (Idempotent-Replayed), and the client's call is judged with the
// answer it finally got. Halfway through the first phase the first node is
// killed: its sink dies at that instant (whatever its relay had not
// recorded is lost with it, as with kill -9), its goroutines are
// stopped, and a second node takes the server's traffic, with a relay of
// its own that records the step log from its first row (its copies of
// the first node's records are Reconstruct's duplicates) and every row
// the first never recorded. The second phase creates new counters, whose
// welcomes only the second node sends. The harness then waits for the
// processors and the relay (quiet), probes every counter over the API and
// records the settle, so the eventual val (every counter welcomed) is
// judged; and every answer a client got is checked against the store's
// side: each command answered has its step there, with the same outcome
// (a refusal's included: one the store did not record would pass both
// bindings).
func TestRealHTTP(t *testing.T) {
	qtest.RequireQuint(t)
	dir := t.TempDir()
	db, err := sql.Open("sqlite", sqlstore.SQLiteDSN(filepath.Join(dir, "counters.db"), 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	st, err := sqlstore.Open(ctx, db, sqlstore.SQLite, sqlstore.Options{Name: App.Name, Poll: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	tenants := []string{"t1", "t2"}
	for _, tn := range tenants {
		if err := st.CreateTenant(ctx, tn); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "steps.jsonl")
	sink, err := qobs.FileSink(path, qobs.PerStep)
	if err != nil {
		t.Fatal(err)
	}
	deps := WithDeps(ctx, Deps{Mailer: &realMailer{}, Store: st})
	clientsCtx := qobs.WithRecorder(deps, qobs.NewRecorder(sink, qobs.WithID("clients")))

	// the server: one listener, its traffic handed to the current node's
	// API, which records what it answers on return (queries) to that
	// node's recorder. Every fifth command's answer is lost on the way
	// back (served, then the connection closed without a response, as a
	// proxy or a network drops it): its client sends it again under the
	// same Idempotency-Key, and the key's marker answers the retry
	var current atomic.Pointer[http.Handler]
	var commandsServed, lost, replayed atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := current.Load()
		if h == nil {
			http.Error(w, `{"error":"no node"}`, http.StatusServiceUnavailable)
			return
		}
		if r.Method == http.MethodPost && commandsServed.Add(1)%5 == 0 {
			(*h).ServeHTTP(httptest.NewRecorder(), r)
			lost.Add(1)
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		(*h).ServeHTTP(w, r)
		if w.Header().Get(grist.ReplayedHeader) == "true" {
			replayed.Add(1)
		}
	}))
	defer srv.Close()
	// start is a node recording to its own sink: its relay (returned, to
	// wait for) and pollers until stop, and its API taking the server's
	// traffic
	start := func(id string, to qobs.Sink) (relay *grist.Relay, stop func()) {
		relay, err := grist.NewRelay(st)
		if err != nil {
			t.Fatal(err)
		}
		rec := qobs.NewRecorder(to, qobs.WithID(id))
		nctx, cancel := context.WithCancel(qobs.WithRecorder(deps, rec))
		if err := App.Start(nctx, st, grist.WithRelay(relay)); err != nil {
			t.Fatal(err)
		}
		api, err := grist.NewHandler(App, st, grist.ActorFromHeader("X-Actor"), grist.Middleware(func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				h.ServeHTTP(w, r.WithContext(qobs.WithRecorder(r.Context(), rec)))
			})
		}))
		if err != nil {
			t.Fatal(err)
		}
		current.Store(&api)
		return relay, cancel
	}
	cl := &client.Client{App: App, URL: srv.URL, ActorHeader: "X-Actor", RetryDelay: 10 * time.Millisecond}
	// clients runs four clients of 8 operations each on counters, names
	// from names; after the first client's half, kill runs (if set)
	clients := func(phase int, counters, names []string, kill func()) {
		var wg sync.WaitGroup
		for c := 1; c <= 4; c++ {
			wg.Go(func() {
				ctx := qobs.Thread(clientsCtx) // one thread per goroutine
				r := rand.New(rand.NewPCG(uint64(phase), uint64(c)))
				for op := 1; op <= 8; op++ {
					if op == 5 && c == 1 && kill != nil {
						kill()
					}
					env := grist.Envelope{ID: fmt.Sprintf("p%d-c%d-%d", phase, c, op), Actor: fmt.Sprint("client", c)}
					env.Tenant = tenants[r.IntN(len(tenants))]
					counter, name := counters[r.IntN(len(counters))], names[r.IntN(len(names))]
					var cmd string
					var args any
					switch r.IntN(3) {
					case 0:
						cmd, args = "create-counter", CreateCounter{Counter: counter, Name: name}
					case 1:
						cmd, args = "increment-counter", IncrementCounter{Counter: counter, By: 1 + r.IntN(3)}
					default:
						cmd, args = "show-counter", ShowCounter{Counter: counter}
					}
					b, _ := json.Marshal(args)
					err := cl.Do(ctx, env, cmd, b)
					var rej *grist.Rejected
					if err != nil && !errors.As(err, &rej) && !errors.Is(err, grist.ErrConflict) {
						t.Errorf("%s %s: %v", cmd, env.ID, err)
					}
				}
			})
		}
		wg.Wait()
	}

	first := &killable{s: sink}
	relay1, stop1 := start("node1", first)
	var relay *grist.Relay
	var stop func()
	clients(1, []string{"a", "b", "c"}, []string{"a", "b"}, func() {
		first.kill()
		stop1()
		relay, stop = start("node2", sink) // its relay records the step log from its first row
	})
	behind, err := relay1.Behind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if behind == 0 {
		t.Fatal("the killed node's relay recorded every row: the second node's relay has nothing of its own to record, and this run proves nothing of it")
	}
	clients(2, []string{"d", "e", "f"}, []string{"c", "d"}, nil)
	quiet(t, st, relay, tenants)
	for _, tn := range tenants { // the probe: every counter, on the settled system
		for i, c := range []string{"a", "b", "c", "d", "e", "f"} {
			b, _ := json.Marshal(ShowCounter{Counter: c})
			if err := cl.Do(clientsCtx, grist.Envelope{ID: fmt.Sprint("probe-", i), Tenant: tn, Actor: "probe"}, "show-counter", b); err != nil {
				t.Fatal(err)
			}
		}
	}
	time.Sleep(10 * time.Millisecond) // past RealTimeSlack after the last step
	qobs.Settled(clientsCtx)
	stop()
	if err := relay.Err(); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	steps, err := qtrace.ReadJSONL(f)
	if err != nil {
		t.Fatal(err)
	}
	relayed := map[string]int{}
	for _, s := range steps {
		if s.Recorder == grist.RecorderID(App.Name, s.Actor) {
			relayed[s.Actor]++
		}
	}
	if relayed["t1"] == 0 || relayed["t2"] == 0 {
		t.Fatalf("relayed steps by tenant %v: want both tenants' commits, from the step log", relayed)
	}
	traces, _ := qtrace.Reconstruct(steps)
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
	answered(t, traces)
	if lost.Load() == 0 || replayed.Load() == 0 {
		t.Errorf("%d answers lost, %d retries answered from their key's marker: want both, or the same-key retry was never exercised", lost.Load(), replayed.Load())
	}
	t.Logf("%d steps written, %d relayed (%d kept after the second relay's copies); the killed node's relay was %d rows behind; %d answers lost, %d retries replayed",
		len(steps), relayed["t1"]+relayed["t2"], kept, behind, lost.Load(), replayed.Load())
	qtest.Validate(t, commits, steps).Expect(t, qtest.Conforms(), qtest.Settles())
	qtest.Validate(t, calls, steps, qtest.Linearize()).Expect(t, qtest.Conforms())
}

// answered checks every command a client was answered against the
// store's side: the store recorded its step, once, with the outcome the
// client was told. Neither binding sees a store-side step missing (a
// refusal commits no event, so the model's log is the same without it),
// but the client's answer names it.
func answered(t *testing.T, traces []*qtrace.Trace) {
	t.Helper()
	atStore, atClients := map[string]string{}, map[string]string{}
	for _, tr := range traces {
		for _, s := range tr.Steps {
			id, _ := s.Args["id"].(string)
			if id == "" || s.Action != "createCounter" && s.Action != "incrementCounter" {
				continue
			}
			side := atStore
			if strings.HasSuffix(s.Actor, "/clients") {
				side = atClients
			}
			if prev, dup := side[id]; dup {
				t.Errorf("command %s has two steps on one side (%s, %s)", id, prev, s.Outcome)
			}
			side[id] = s.Outcome
		}
	}
	if len(atClients) == 0 {
		t.Fatal("no client was answered")
	}
	for id, outcome := range atClients {
		if got, ok := atStore[id]; !ok || got != outcome {
			t.Errorf("command %s: the client was told %s, the store recorded %q (found %v): a committed answer's step is missing or differs", id, outcome, got, ok)
		}
	}
}

// quiet waits until the processors have handled every trigger and the
// relay has recorded every committed step, twice in a row: a real tier's
// settle. A relay that cannot record fails the test at once.
func quiet(t *testing.T, st grist.Store, relay *grist.Relay, tenants []string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(30 * time.Second)
	for calm := 0; calm < 2; {
		if time.Now().After(deadline) {
			t.Fatal("the node did not catch up within 30 s")
		}
		time.Sleep(20 * time.Millisecond)
		done := true
		for _, tn := range tenants {
			for p, on := range map[string]string{"average": "counter-incremented", "welcome": "counter-created"} {
				last, _, err := st.Read(ctx, tn, grist.Selector{Types: []string{on}, Reverse: true, Limit: 1}, 0)
				if err != nil {
					t.Fatal(err)
				}
				cp, err := st.Checkpoint(ctx, tn, p)
				if err != nil {
					t.Fatal(err)
				}
				if len(last) > 0 && cp < last[0].Pos {
					done = false
				}
			}
		}
		if err := relay.Err(); err != nil {
			t.Fatal(err)
		}
		if n, err := relay.Behind(ctx); err != nil || n > 0 {
			done = false
		}
		if done {
			calm++
		} else {
			calm = 0
		}
	}
}

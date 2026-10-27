package gristtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// TierConfig is a real tier's shape.
type TierConfig struct {
	// Tenants are created before any node starts (default t1).
	Tenants []string
	// Deps attaches the application's dependencies to a context: every
	// node's, and the clients' (nil: none). It is given the tier's store.
	Deps func(ctx context.Context, st grist.Store) context.Context
	// LoseEvery loses every n-th command's answer after the command was
	// served (the connection closed without a response, as a proxy drops
	// it), so its client retries under the same Idempotency-Key and the
	// key's marker answers (default 5; negative: none). Check then
	// requires some answer lost and some retry replayed.
	LoseEvery int
	// Poll is how often the store reads other processes' appends (default
	// 10 ms).
	Poll time.Duration
}

// A Tier is an application's real tier in a test: the app on a SQLite
// file (grist/sqlstore) served over real HTTP (grist.NewHandler behind an
// httptest server, the actor from a header), its nodes (App.Start: the
// relay, the pollers, the tenant watcher; goroutines, each recording to
// the tier's PerStep step file), clients calling the API (grist/client:
// each call recorded where its client sees it). Settle waits for every
// processor and the relay before it records the settle, so a binding's
// eventual vals are judged; Check validates the file with the store's
// binding and, linearized, the clients', and checks every client's
// answer against the store's side. It is examples/counters' TestRealHTTP
// and examples/wallet-grist's, as a library.
type Tier struct {
	t   testing.TB
	app *grist.App
	cfg TierConfig
	// Store is the tier's store; Client calls its API (its actor is an
	// Envelope's Actor, sent in the X-Actor header).
	Store  grist.Store
	Client *client.Client

	path string
	sink interface {
		qobs.Sink
		Close() error
	}
	clients context.Context
	current atomic.Pointer[http.Handler]
	node    atomic.Pointer[Node] // the current one
	mu      sync.Mutex
	nodes   []*Node

	served, lost, replayed atomic.Int64
	closeOnce              sync.Once
	steps                  []qtrace.Step
}

// The header the tier's clients name their actor in.
const actorHeader = "X-Actor"

// NewTier makes app's real tier in a temporary directory, its tenants
// created, no node started (Start).
func NewTier(t testing.TB, app *grist.App, cfg TierConfig) *Tier {
	t.Helper()
	if len(cfg.Tenants) == 0 {
		cfg.Tenants = []string{"t1"}
	}
	if cfg.LoseEvery == 0 {
		cfg.LoseEvery = 5
	}
	if cfg.Poll == 0 {
		cfg.Poll = 10 * time.Millisecond
	}
	dir := t.TempDir()
	db, err := sql.Open("sqlite", sqlstore.SQLiteDSN(filepath.Join(dir, app.Name+".db"), 5*time.Second))
	if err != nil {
		t.Fatalf("the real tier's database: %v (the test imports _ \"modernc.org/sqlite\")", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	st, err := sqlstore.Open(ctx, db, sqlstore.SQLite, sqlstore.Options{Name: app.Name, Poll: cfg.Poll})
	if err != nil {
		t.Fatal(err)
	}
	for _, tn := range cfg.Tenants {
		if err := st.CreateTenant(ctx, tn); err != nil {
			t.Fatal(err)
		}
	}
	r := &Tier{t: t, app: app, cfg: cfg, Store: st, path: filepath.Join(dir, "steps.jsonl")}
	sink, err := qobs.FileSink(r.path, qobs.PerStep)
	if err != nil {
		t.Fatal(err)
	}
	r.sink = sink
	r.clients = qobs.WithRecorder(r.deps(ctx), qobs.NewRecorder(r.sink, qobs.WithID("clients")))
	srv := httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(srv.Close)
	r.Client = &client.Client{App: app, URL: srv.URL, ActorHeader: actorHeader, RetryDelay: 10 * time.Millisecond}
	t.Cleanup(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, n := range r.nodes {
			n.stop()
		}
	})
	return r
}

func (r *Tier) deps(ctx context.Context) context.Context {
	if r.cfg.Deps == nil {
		return ctx
	}
	return r.cfg.Deps(ctx, r.Store)
}

// serve hands the request to the current node's API, losing every
// LoseEvery-th command's answer after serving it.
func (r *Tier) serve(w http.ResponseWriter, req *http.Request) {
	h := r.current.Load()
	if h == nil {
		http.Error(w, `{"error":"no node"}`, http.StatusServiceUnavailable)
		return
	}
	if req.Method == http.MethodPost && r.cfg.LoseEvery > 0 && r.served.Add(1)%int64(r.cfg.LoseEvery) == 0 {
		(*h).ServeHTTP(httptest.NewRecorder(), req)
		r.lost.Add(1)
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			conn.Close()
		}
		return
	}
	(*h).ServeHTTP(w, req)
	if w.Header().Get(grist.ReplayedHeader) == "true" {
		r.replayed.Add(1)
	}
}

// A Node is one of the tier's nodes: the App started on its store with a
// relay of its own, recording to the tier's step file until Kill, its
// API taking the server's traffic from its start.
type Node struct {
	// Relay is its relay (Relay.Behind: how many committed rows it has not
	// recorded).
	Relay *grist.Relay
	sink  *killable
	stop  context.CancelFunc
}

// Start starts a node named id (its recorder's) and hands it the
// server's traffic: a later Start takes it over, as a second replica does
// when the first dies.
func (r *Tier) Start(id string) *Node {
	r.t.Helper()
	relay, err := grist.NewRelay(r.Store)
	if err != nil {
		r.t.Fatal(err)
	}
	n := &Node{Relay: relay, sink: &killable{s: r.sink}}
	rec := qobs.NewRecorder(n.sink, qobs.WithID(id))
	ctx, cancel := context.WithCancel(qobs.WithRecorder(r.deps(context.Background()), rec))
	n.stop = cancel
	if err := r.app.Start(ctx, r.Store, grist.WithRelay(relay)); err != nil {
		r.t.Fatal(err)
	}
	api, err := grist.NewHandler(r.app, r.Store, grist.ActorFromHeader(actorHeader), grist.Middleware(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			h.ServeHTTP(w, req.WithContext(qobs.WithRecorder(req.Context(), rec)))
		})
	}))
	if err != nil {
		r.t.Fatal(err)
	}
	r.current.Store(&api)
	r.node.Store(n)
	r.mu.Lock()
	r.nodes = append(r.nodes, n)
	r.mu.Unlock()
	return n
}

// Kill kills the node: its sink dies at that instant (whatever its relay
// had not recorded is lost with it, as with kill -9), and its goroutines
// are stopped. The next node's relay records the step log from its first
// row, so nothing committed is lost (Reconstruct drops the copies).
func (n *Node) Kill() {
	n.sink.kill()
	n.stop()
}

// killable is a sink that dies with its node.
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

// Clients is the context the clients record their calls in (actor
// <tenant>/clients): give each goroutine its own thread (qobs.Thread).
func (r *Tier) Clients() context.Context { return r.clients }

// Do calls name with args for env through the API (Client.Do), failing
// the test on any error but a refusal and a conflict.
func (r *Tier) Do(ctx context.Context, env grist.Envelope, name string, args any) {
	b, err := json.Marshal(args)
	if err != nil {
		r.t.Errorf("%s %s: %v", name, env.ID, err)
		return
	}
	err = r.Client.Do(ctx, env, name, b)
	var rej *grist.Rejected
	if err != nil && !errors.As(err, &rej) && !errors.Is(err, grist.ErrConflict) {
		r.t.Errorf("%s %s: %v", name, env.ID, err)
	}
}

// Settle waits until every processor of the App has handled every
// trigger of every tenant and the current node's relay has recorded
// every committed step, twice in a row (a real tier's settle); runs probe
// (reads on the settled system, through the clients' context: nil for
// none); and records the settle past RealTimeSlack, so a binding's
// eventual vals are judged on this run. A relay that cannot record fails
// at once; a node that does not catch up within 30 s fails.
func (r *Tier) Settle(probe func(ctx context.Context)) {
	r.t.Helper()
	n := r.node.Load()
	if n == nil {
		r.t.Fatal("Settle with no node started")
	}
	r.quiet(n.Relay)
	if probe != nil {
		probe(r.clients)
	}
	time.Sleep(10 * time.Millisecond) //qenv:ok past RealTimeSlack after the last step, on the real tier
	qobs.Settled(r.clients)
}

func (r *Tier) quiet(relay *grist.Relay) {
	r.t.Helper()
	ctx := context.Background()
	procs := r.app.Catalog().Processors
	deadline := time.Now().Add(30 * time.Second) //qenv:ok the real tier's own deadline
	for calm := 0; calm < 2; {
		if time.Now().After(deadline) { //qenv:ok the real tier's own deadline
			r.t.Fatal("the node did not catch up within 30 s: a processor or the relay is stuck")
		}
		time.Sleep(20 * time.Millisecond) //qenv:ok polling the real tier
		done := true
		for _, tn := range r.cfg.Tenants {
			for _, p := range procs {
				last, _, err := r.Store.Read(ctx, tn, grist.Selector{Types: []string{p.On}, Reverse: true, Limit: 1}, 0)
				if err != nil {
					r.t.Fatal(err)
				}
				cp, err := r.Store.Checkpoint(ctx, tn, p.Name)
				if err != nil {
					r.t.Fatal(err)
				}
				if len(last) > 0 && cp < last[0].Pos {
					done = false
				}
			}
		}
		if err := relay.Err(); err != nil {
			r.t.Fatal(err)
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

// Steps stops every node, closes the step file and reads it (once).
func (r *Tier) Steps() []qtrace.Step {
	r.t.Helper()
	r.closeOnce.Do(func() {
		r.mu.Lock()
		for _, n := range r.nodes {
			n.stop()
		}
		r.mu.Unlock()
		if n := r.node.Load(); n != nil {
			if err := n.Relay.Err(); err != nil {
				r.t.Fatal(err)
			}
		}
		if err := r.sink.Close(); err != nil {
			r.t.Fatal(err)
		}
		f, err := os.Open(r.path)
		if err != nil {
			r.t.Fatal(err)
		}
		defer f.Close()
		if r.steps, err = qtrace.ReadJSONL(f); err != nil {
			r.t.Fatal(err)
		}
	})
	return r.steps
}

// Lost counts the commands' answers lost after they were served.
func (r *Tier) Lost() int64 { return r.lost.Load() }

// Replayed counts the retries answered from their key's marker
// (Idempotent-Replayed).
func (r *Tier) Replayed() int64 { return r.replayed.Load() }

// Check judges the run: the step file conforms to commits (the store's
// binding) and settles, and, linearized, to calls (the clients'); every
// command a client was answered has its store-side step with the same
// outcome (Answered); and, when answers are lost (LoseEvery), some was
// lost and some retry replayed, or the same-key retry was never
// exercised. opts apply to the clients' validation (with
// qtest.Linearize): a real run's concurrent calls may need a larger
// qtest.LinearizeBudget than the default to find their order. It returns
// the run's traces, for an application's own checks.
func (r *Tier) Check(commits, calls string, opts ...qtest.Option) []*qtrace.Trace {
	r.t.Helper()
	steps := r.Steps()
	traces, _ := qtrace.Reconstruct(steps)
	Answered(r.t, r.app, traces)
	if r.cfg.LoseEvery > 0 && (r.Lost() == 0 || r.Replayed() == 0) {
		r.t.Errorf("%d answers lost, %d retries answered from their key's marker: want both, or the same-key retry was never exercised", r.Lost(), r.Replayed())
	}
	r.t.Logf("%d steps written; %d answers lost, %d retries replayed", len(steps), r.Lost(), r.Replayed())
	qtest.Validate(r.t, commits, steps).Expect(r.t, qtest.Conforms(), qtest.Settles())
	qtest.Validate(r.t, calls, steps, append([]qtest.Option{qtest.Linearize()}, opts...)...).Expect(r.t, qtest.Conforms())
	return traces
}

// Answered checks every command of app a client was answered against
// the store's side: the store recorded the outcome the client was told,
// and a key one outcome at most. Neither binding sees a store-side step
// missing (a refusal commits no event, so the model's log is the same
// without it), but the client's answer names it. Commands are matched by
// tenant and id (two tenants' clients may use one id). A key's first
// answer is final, so its calls may be answered alike more than once
// (from its marker); a refusal of the key reused with other arguments
// (idempotency-key-reused) is a step of its own, once per try. A call
// the API denied (403) ran nothing at the store, by right, and a pending
// one was answered nothing.
func Answered(t testing.TB, app *grist.App, traces []*qtrace.Trace) {
	t.Helper()
	commands := map[string]bool{}
	for _, c := range app.Catalog().Commands {
		commands[c.Quint] = true
	}
	type key struct{ tenant, id string }
	reused := grist.ErrKeyReused.(*grist.Rejected).Name
	atStore := map[key]map[string]int{}
	var told []key
	outcomes := map[key][]string{}
	for _, tr := range traces {
		for _, s := range tr.Steps {
			id, _ := s.Args["id"].(string)
			if id == "" || !commands[s.Action] {
				continue
			}
			tenant, clients := strings.CutSuffix(s.Actor, "/clients")
			k := key{tenant, id}
			switch {
			case clients && s.Outcome == "denied":
				// refused before any store ran it
			case clients:
				if len(outcomes[k]) == 0 {
					told = append(told, k)
				}
				outcomes[k] = append(outcomes[k], s.Outcome)
			default:
				if atStore[k] == nil {
					atStore[k] = map[string]int{}
				}
				atStore[k][s.Outcome]++
			}
		}
	}
	for k, byOutcome := range atStore {
		n := 0
		var got []string
		for o, c := range byOutcome {
			if o != reused {
				n += c
				got = append(got, o)
			}
		}
		if n > 1 {
			sort.Strings(got)
			t.Errorf("command %s of tenant %s has %d store-side steps (%s): a key names one outcome", k.id, k.tenant, n, strings.Join(got, ", "))
		}
	}
	if len(told) == 0 {
		t.Fatal("no client was answered")
	}
	for _, k := range told {
		for _, o := range outcomes[k] {
			if atStore[k][o] == 0 {
				t.Errorf("command %s of tenant %s: the client was told %s, the store recorded %v: a committed answer's step is missing or differs", k.id, k.tenant, o, atStore[k])
			}
		}
	}
}

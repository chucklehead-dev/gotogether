// Package gristsim runs a grist application in a qenv simulation: a
// simstore, or the SQL tier's sqlstore over qsql (Config.SQL), at a
// chosen isolation level, one node or more (Config.Nodes; a crashable process
// running the App's processors and serving its commands and queries over
// a lossy qenv.Service, or over its HTTP API on qhttp: Config.HTTP), and a workload of clients sending random
// commands and queries of the registry to every tenant. Each of the
// App's declared dependencies (grist.NewDependency) is served as a
// qenv.Service named after it, from the run's Config.Doubles (else its
// default double, grist.DefaultHandlers), its requests and answers lost,
// duplicated and late. Run's signature
// is a qtest.System's Run, and System is that System, so an application
// is explored, exhausted and shrunk with every claim of qtest:
//
//	gristsim.System("counters.binding.yaml", App, gristsim.Config{}).WithRates(qenv.DefaultRates.Set("nemesis", 0.02))
//
// Its end is ordered so the binding's eventual vals are judged: the
// clients finish, Sim.Settle drains the processors (faults off from
// there), probes run every query, and Sim.Settle again, so the run's last
// record is a settle (docs/grist.md §3.6).
package gristsim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/grist/internal/calls"
	"github.com/chucklehead-dev/gotogether/grist/internal/values"
	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// ClientsActor is the actor of tenant's client-side steps: a run records
// every command and query twice, at the store (actor tenant: commits from
// the step log, refusals at their commit, queries on return) and where
// the clients see them (actor tenant + "/clients": qobs.Invoke before the
// first request and Call.Done with the answer, every retry of a lost or
// late response inside the one call), so a commits binding (actors:
// ['!*/clients']) and a clients binding (actors: ['*/clients'], with
// qtest.Linearize) judge one run (System.WithBinding; docs/grist.md §3.1).
// No tenant's name holds a slash (grist.CheckTenant), so no tenant is
// another's clients.
func ClientsActor(tenant string) string { return calls.Actor(tenant) }

// Config is a run's shape.
type Config struct {
	// Isolation is the store's level (Serializable by default).
	Isolation qenv.Isolation
	// Tenants are the tenants (default t1 and t2); each client sends to
	// all of them.
	Tenants []string
	// Clients and Ops: how many clients, each sending how many commands or
	// queries (default 2 and 4).
	Clients, Ops int
	// Send are the commands and queries the clients send, by name
	// (default: every one of the registry). Each operation draws one
	// uniformly from the list, so a name listed twice is sent about twice
	// as often.
	Send []string
	// Prologue are calls made in order, each answered (or left pending
	// after MaxAttempts tries) before the next, before any client sends
	// its first: the state the workload's random operations need (a member
	// joined, a book catalogued), which random sends of a command that
	// needs earlier ones mostly refuse. Its calls are recorded as every
	// client's are (caller "prologue", ids <tenant>-prologue-<k>) and go
	// through the same faults; the clients wait for it.
	Prologue []Call
	// SQL runs the app on grist/sqlstore over qsql (qsql.Open on the run's
	// qenv.Store) instead of simstore: the statements the real tier runs
	// on PostgreSQL and SQLite, interleaved statement by statement at
	// Isolation, with qsql's SQLSTATEs (a lost race for the head lock is
	// a 40001 under Snapshot and Serializable) and the same commit faults.
	SQL bool
	// HTTP serves the node's commands and queries over its HTTP API
	// (grist.NewHandler on a qhttp.Server, "api") instead of a
	// qenv.Service of requests, the clients calling it through
	// grist/client over qhttp's Transport: the API's codes, its
	// Idempotency-Key and its 503s are what the clients see, and the
	// network's faults are qhttp's (rpc.api.request, rpc.api.delay,
	// rpc.api.response, the node's crashes). Every command and query the
	// clients send must declare Authorize (grist denies by default); a
	// client is its Envelope's caller (CallerHeader).
	HTTP bool
	// Nodes is how many node processes run the App on the one store
	// (default 1): node, node2, ...; each a crashable process starting the
	// App (its relay, pollers and tenant watcher: two pollers of every
	// processor and tenant then race for each checkpoint, as two
	// replicas of a real tier do), restarted when it crashes. Only the
	// first serves the commands and queries (the API), but under a
	// Cluster, where every node does; every node's relay is one Relay, so
	// each step row is recorded once.
	Nodes int
	// CommitFaults turns on the store's commit faults
	// (store.<app>.commit: a commit applied whose answer is lost).
	CommitFaults bool
	// SnapshotEvery is how many events a read folds before it takes a
	// read model's snapshot (grist.SnapshotEvery; 0: grist's default,
	// which a simulation's short logs never reach; negative: none). A
	// small one makes the runs read from snapshots, through the store's
	// snapshot faults (store.<app>.snapshot: a put torn or lost) and the
	// node's crashes, and the model judges every decision made from one
	// (not the HTTP tier's API, whose handler reads at grist's default).
	SnapshotEvery int
	// Check runs the scheduler's and the Store's own checks at every step
	// and commit, as GOTOGETHER_CHECK=1 does.
	Check bool
	// Alphabet is the strings a generated argument takes (default a, b,
	// c): few, so that names collide, which is what finds races.
	Alphabet []string
	// Ints are the integers a generated argument takes (default -1 to 3):
	// zero and a negative among them, the values a command's validation
	// is about, as the starter model's INTS; an unsigned field takes the
	// non-negative ones.
	Ints []int
	// Gen overrides a command's or query's generated arguments, by name:
	// pick(n) chooses one of n through the run's Chooser. A block it does
	// not name has its arguments drawn from their type (Gen, over Alphabet
	// and Ints), so most apps need none.
	Gen map[string]func(pick func(n int) int) any
	// Doubles builds, once per run, the handlers that serve each of the
	// App's dependencies (grist.NewDependency): each is served as a
	// qenv.Service named after the dependency, on no process, its faults
	// the Service's (rpc.<dependency>.request, .delay, .response), its
	// handlers recording each call's application at its site
	// (grist.Call.Handler). A dependency it gives no handlers is served by
	// its default double (grist.DefaultHandlers: a Keyed call applied once
	// per key, its later tries replayed; an unkeyed one every time, each
	// answered with its response, which has no fields); one with a call
	// whose response has fields has no default (what a dependency answers
	// there is its own), and is the run's error, as is one given handlers
	// lacking a call. Per run, so a double's state (the keys it
	// applied) is its run's.
	Doubles func() Doubles
	// Setup builds the application's other dependencies once per
	// simulation (a qenv.Service standing for a mailer, grist.NewDep's),
	// returning what attaches them to a context: the node's tasks run with
	// it, so a processor's effect reaches them through its ctx. nil: none.
	Setup func(s *qenv.Sim, st grist.Store) func(context.Context) context.Context
	// Horizon bounds the clients' phase in virtual time (default 2m).
	Horizon time.Duration
	// Cluster, if set, runs the nodes as an orchestrator does: every node
	// serves the API behind a lagging router, and is terminated
	// gracefully, draining as grist.Main does (Cluster); Nodes then
	// defaults to 3. Not with HTTP yet.
	Cluster *Cluster
	// Log, if set, receives the simulation's log (crashes, upgrades:
	// qenv.Sim.Log), for a failing run's story.
	Log func(string)
}

// Doubles are a run's handlers of the App's dependencies, by dependency.
type Doubles map[*grist.Dependency]grist.Handlers

// DependencyTimeout is how long a call of a simulated dependency waits for
// its answer (virtual time) before its outcome is unknown.
const DependencyTimeout = time.Second

// callRequest is a call as a simulated dependency's service takes it: its
// cause and its request as JSON, the bytes every transport sends.
type callRequest struct {
	Info grist.CallInfo
	Call string
	Body json.RawMessage
}

// callResponse is its answer: the response as JSON, a refusal's name, or
// an error's text.
type callResponse struct {
	Body     json.RawMessage
	Rejected string
	Err      string
}

// defaultDouble is d's default double, or why it has none: a call
// answering a response with fields (grist.DefaultHandlers' panic).
func defaultDouble(d *grist.Dependency) (h grist.Handlers, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	return grist.DefaultHandlers(d), nil
}

// simTransport is a dependency's qenv.Service as a grist.Transport.
type simTransport struct {
	svc *qenv.Service[callRequest, callResponse]
}

func (t simTransport) Call(ctx context.Context, c grist.CallInfo, call string, req json.RawMessage) (json.RawMessage, error) {
	resp, err := t.svc.Call(ctx, callRequest{Info: c, Call: call, Body: req}, DependencyTimeout)
	switch {
	case err != nil:
		return nil, err // a lost request or answer, a late one: unknown
	case resp.Rejected != "":
		return nil, &grist.Rejected{Name: resp.Rejected}
	case resp.Err != "":
		return nil, errors.New(resp.Err)
	}
	return resp.Body, nil
}

// serveDependencies serves each of app's dependencies with its doubles as
// a qenv.Service of s, returning what attaches them to a context.
func serveDependencies(s *qenv.Sim, app *grist.App, doubles func() Doubles) (func(context.Context) context.Context, error) {
	deps := app.Dependencies()
	if len(deps) == 0 {
		return func(ctx context.Context) context.Context { return ctx }, nil
	}
	var ds Doubles
	if doubles != nil {
		ds = doubles()
	}
	for d := range ds {
		if !slices.Contains(deps, d) {
			return nil, fmt.Errorf("gristsim: Config.Doubles has handlers of dependency %s, which is not %s's: declare it on the App (grist.NewDependency)", d.Name(), app.Name)
		}
	}
	trs := make([]grist.Transport, len(deps))
	for i, d := range deps {
		h, ok := ds[d]
		if !ok {
			// none given: the default double, this run's own
			var err error
			if h, err = defaultDouble(d); err != nil {
				return nil, fmt.Errorf("gristsim: dependency %s of %s has no double in Config.Doubles: %w", d.Name(), app.Name, err)
			}
		}
		if err := h.Serves(d); err != nil {
			return nil, fmt.Errorf("gristsim: Config.Doubles' handlers of %s: %w", d.Name(), err)
		}
		local := grist.Local(h)
		trs[i] = simTransport{svc: qenv.NewService(s, d.Name(), nil, func(ctx context.Context, r callRequest) (callResponse, error) {
			out, err := local.Call(ctx, r.Info, r.Call, r.Body)
			var rej *grist.Rejected
			switch {
			case errors.As(err, &rej):
				return callResponse{Rejected: rej.Name}, nil
			case err != nil:
				return callResponse{Err: err.Error()}, nil
			}
			return callResponse{Body: out}, nil
		})}
	}
	return func(ctx context.Context) context.Context {
		for i, d := range deps {
			ctx = d.With(ctx, trs[i])
		}
		return ctx
	}, nil
}

// A Call is one call of a Prologue: a command or query by name, with its
// arguments (a value of its arguments' type, or their JSON as a
// json.RawMessage or []byte; nil draws them as the workload does: Gen, else
// generated), to Tenant ("": each tenant, in Config.Tenants' order).
type Call struct {
	Tenant string
	Name   string
	Args   any
}

// request is a client's request to the node.
type request struct {
	Query bool // a query, else a command
	Name  string
	Env   grist.Envelope
	Args  json.RawMessage
}

// response is the node's answer: a command's receipt, a query's result
// and position, a rejection's name, an error's text, or Retry: the
// outcome is unknown (grist.ErrOutcomeUnknown) or nothing committed
// (grist.ErrUnavailable), and the client sends the request again under
// its id, as for a lost answer.
type response struct {
	Receipt  grist.Receipt
	Result   json.RawMessage
	Pos      grist.Position
	Rejected string
	Err      string
	Retry    bool
	// refused counts the client's tries no server took (a Cluster's)
	refused int
}

// CallerHeader is the header the HTTP tier's clients name their caller in
// (grist.CallerFromHeader).
const CallerHeader = "X-Gristsim-Caller"

// Timeouts: a client waits CallTimeout for an answer, then retries the
// same request (same id) after RetryDelay.
const (
	CallTimeout = 2 * time.Second
	RetryDelay  = 100 * time.Millisecond
	// MaxAttempts bounds a client's tries of one request: after it, the
	// call stays pending (qtest.Completes judges it).
	MaxAttempts = 50
)

// System is app under cfg, its runs judged by binding: a qtest.System
// whose Run is Run's, carrying the mutant the test binary was built with
// (qtest.Mutant: an overlay of testdata/mutants, as qtest.Mutants and
// run-demo.sh plant it). Set its Rates and Opts on the result:
//
//	gristsim.System("shop.binding.yaml", App, cfg).WithRates(faulty).
//		Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 3}).Expect(t, qtest.Conforms())
func System(binding string, app *grist.App, cfg Config) qtest.System {
	return qtest.System{Binding: binding, Mutant: qtest.Mutant(),
		Run: func(c qenv.Chooser) error { return Run(c, app, cfg) }}
}

// Run runs app on c under cfg: a qtest.System's Run (System's). A run that does not
// settle returns its *qenv.Unsettled, which qtest judges.
func Run(c qenv.Chooser, app *grist.App, cfg Config) error {
	if err := app.Verify(); err != nil {
		return err
	}
	cfg = defaults(cfg)
	for _, t := range cfg.Tenants {
		if err := grist.CheckTenant(t); err != nil {
			return fmt.Errorf("gristsim: Config.Tenants: %w", err)
		}
	}
	s := qenv.New(c)
	defer s.Close()
	if cfg.Check {
		s.Check = true
	}
	s.Log = cfg.Log
	db := qenv.NewStore(s, app.Name, cfg.Isolation)
	setup := s.Context()
	var st grist.Store = simstore.New(db)
	if cfg.SQL {
		sq, err := sqlstore.Open(setup, qsql.Open(s, db), sqlstore.QSQL, sqlstore.Options{Name: app.Name})
		if err != nil {
			return err
		}
		st = sq
	}
	// one relay for every incarnation of the node: a restarted node's
	// relay resumes where the crashed one stopped, and records what it
	// committed and did not record
	relay, err := grist.NewRelay(st)
	if err != nil {
		return err
	}
	for _, t := range cfg.Tenants {
		if err := st.CreateTenant(setup, t); err != nil {
			return err
		}
	}
	db.CommitFaults = cfg.CommitFaults
	served, err := serveDependencies(s, app, cfg.Doubles)
	if err != nil {
		return err
	}
	deps := served
	if cfg.Setup != nil {
		if d := cfg.Setup(s, st); d != nil {
			deps = func(ctx context.Context) context.Context { return d(served(ctx)) }
		}
	}
	if n := cfg.SnapshotEvery; n != 0 {
		d := deps
		deps = func(ctx context.Context) context.Context { return grist.SnapshotEvery(d(ctx), n) }
	}
	if cfg.Cluster != nil && cfg.HTTP {
		return errors.New("gristsim: Config.Cluster with Config.HTTP: a cluster's nodes serve the API as qenv.Services behind its router; the HTTP tier's cluster is not built yet")
	}
	var handler http.Handler
	if cfg.HTTP {
		h, err := grist.NewHandler(app, st, grist.CallerFromHeader(CallerHeader))
		if err != nil {
			return fmt.Errorf("gristsim: Config.HTTP: %w", err)
		}
		handler = h
	}
	w := &workload{app: app, cfg: cfg, s: s}
	nodes := map[*qenv.Process]bool{}
	if cfg.Cluster != nil {
		w.cluster = startCluster(s, app, st, relay, deps, cfg)
		for _, p := range w.cluster.nodes {
			nodes[p] = true
		}
	} else {
		w.startNodes(st, relay, deps, nodes, handler)
	}
	// a supervisor: a node that crashed itself (an effect's crash point)
	// restarts as one the nemesis crashed does
	s.OnCrash(func(p *qenv.Process) {
		if nodes[p] {
			p.Restart()
		}
	})
	names := cfg.Send
	if len(names) == 0 {
		names = append(app.Commands(), app.Queries()...)
	}
	// the prologue, if any, before every client's first call
	prologue, err := w.prologue()
	if err != nil {
		return err
	}
	done := len(prologue) == 0
	if !done {
		s.Spawn("prologue", func(ctx context.Context) {
			ctx = qobs.Thread(ctx)
			for _, req := range prologue {
				w.call(ctx, req)
			}
			done = true
		})
	}
	for i := 1; i <= cfg.Clients; i++ {
		s.Spawn(fmt.Sprintf("client%d", i), func(ctx context.Context) {
			ctx = qobs.Thread(ctx)
			qenv.Wait(ctx, func() bool { return done })
			for op := 1; op <= cfg.Ops; op++ {
				if w.cluster != nil && op > 1 {
					qenv.Sleep(ctx, w.cluster.c.Pace) // a cluster's calls span its drains
				}
				tenant := cfg.Tenants[s.Choose("gristsim.tenant", len(cfg.Tenants))]
				name := names[s.Choose("gristsim.op", len(names))]
				args := w.gen(name)
				w.call(ctx, request{Query: isQuery(app, name), Name: name,
					Env: grist.Envelope{ID: fmt.Sprintf("%s-c%d-%d", tenant, i, op), Tenant: tenant, Caller: fmt.Sprintf("client%d", i)}, Args: args})
			}
		})
	}
	if err := s.Run(cfg.Horizon); err != nil {
		return err
	}
	if err := s.Settle(time.Minute); err != nil {
		return err
	}
	// probes: every query, every argument the alphabet makes, on every
	// tenant, on the settled system; then settle again, so that the run's
	// last record is a settle
	s.Spawn("probe", func(ctx context.Context) {
		ctx = qobs.Thread(ctx)
		for _, t := range cfg.Tenants {
			for _, q := range app.Queries() {
				for k, args := range w.all(app.ArgsType(q)) {
					req := request{Query: true, Name: q, Env: grist.Envelope{ID: fmt.Sprintf("%s-probe-%s-%d", t, q, k), Tenant: t, Caller: "probe"}, Args: args}
					w.record(ctx, req, func() response { return serve(deps(ctx), app, st, req) })
				}
			}
		}
	})
	if err := s.Run(time.Minute); err != nil {
		return err
	}
	if err := s.Settle(time.Minute); err != nil {
		return err
	}
	// a relay that cannot read a row records none after it: the run's
	// commits are not all in its trace, and no verdict on it would hold
	return relay.Err()
}

// startNodes starts cfg.Nodes nodes on st (nodes gets them), the first
// serving the API: a qenv.Service of requests, or with HTTP handler on a
// qhttp.Server, which the clients' grist/client calls.
func (w *workload) startNodes(st grist.Store, relay *grist.Relay, deps func(context.Context) context.Context, nodes map[*qenv.Process]bool, handler http.Handler) {
	s, app, cfg := w.s, w.app, w.cfg
	var srv *qhttp.Server
	node := s.Process("node", true, func(ctx context.Context) {
		if err := app.Start(deps(ctx), st, grist.WithRelay(relay)); err != nil {
			panic(fmt.Sprintf("gristsim: the node could not start: %v", err))
		}
		if srv != nil {
			srv.Handle(ctx, handler) // every incarnation listens again
		}
	})
	nodes[node] = true
	for i := 2; i <= cfg.Nodes; i++ {
		nodes[s.Process(fmt.Sprintf("node%d", i), true, func(ctx context.Context) {
			if err := app.Start(deps(ctx), st, grist.WithRelay(relay)); err != nil {
				panic(fmt.Sprintf("gristsim: node %d could not start: %v", i, err))
			}
		})] = true
	}
	if cfg.HTTP {
		srv = qhttp.NewServer(s, "api", node)
		tr := qhttp.NewTransport(srv)
		tr.Timeout = CallTimeout
		// a budget every call refills: the workload's calls are judged by a
		// client-side model, and a call the default budget left pending
		// after its effect committed is one the model cannot place (each
		// call is still bounded by MaxAttempts; the budget's own test is
		// grist's TestClientBackoffAndBudget)
		w.http = &client.Client{App: app, URL: srv.URL(), HTTP: &http.Client{Transport: tr}, CallerHeader: CallerHeader,
			MaxAttempts: MaxAttempts, RetryDelay: RetryDelay, Budget: &client.Budget{Ratio: MaxAttempts, Max: MaxAttempts}}
		return
	}
	w.api = qenv.NewService(s, "api", node, func(ctx context.Context, req request) (response, error) {
		return serve(deps(ctx), app, st, req), nil
	})
}

func isQuery(app *grist.App, name string) bool { return calls.IsQuery(app, name) }

func defaults(cfg Config) Config {
	if len(cfg.Tenants) == 0 {
		cfg.Tenants = []string{"t1", "t2"}
	}
	if cfg.Clients == 0 {
		cfg.Clients = 2
	}
	if cfg.Ops == 0 {
		cfg.Ops = 4
	}
	if len(cfg.Alphabet) == 0 {
		cfg.Alphabet = []string{"a", "b", "c"}
	}
	if len(cfg.Ints) == 0 {
		cfg.Ints = []int{-1, 0, 1, 2, 3}
	}
	if cfg.Horizon == 0 {
		cfg.Horizon = 2 * time.Minute
	}
	if cfg.Nodes == 0 {
		cfg.Nodes = 1
		if cfg.Cluster != nil {
			cfg.Nodes = 3
		}
	}
	return cfg
}

// serve is the node's handling of a request.
func serve(ctx context.Context, app *grist.App, st grist.Store, req request) response {
	var rej *grist.Rejected
	if req.Query {
		res, pos, err := app.Query(ctx, st, req.Env, req.Name, req.Args)
		switch {
		case errors.Is(err, grist.ErrUnavailable):
			return response{Retry: true}
		case errors.As(err, &rej):
			return response{Rejected: rej.Name, Pos: pos}
		case err != nil:
			return response{Err: err.Error(), Pos: pos}
		}
		return response{Result: res, Pos: pos}
	}
	rc, err := app.Execute(ctx, st, req.Env, req.Name, req.Args)
	switch {
	case errors.As(err, &rej):
		return response{Rejected: rej.Name}
	case errors.Is(err, grist.ErrConflict):
		return response{Rejected: "conflict"}
	case errors.Is(err, grist.ErrOutcomeUnknown), errors.Is(err, grist.ErrUnavailable):
		return response{Retry: true}
	case err != nil:
		return response{Err: err.Error()}
	}
	return response{Receipt: rc}
}

// A workload is the clients' generator and caller.
type workload struct {
	app *grist.App
	cfg Config
	s   *qenv.Sim
	api *qenv.Service[request, response]
	// http is the HTTP tier's client (Config.HTTP), api nil
	http *client.Client
	// cluster is a Cluster's nodes and router (Config.Cluster), api nil
	cluster *cluster
}

// prologue are the Prologue's requests, in order, each call to each of
// its tenants: an unknown name, an unknown tenant or arguments that do
// not encode are the Config's error.
func (w *workload) prologue() ([]request, error) {
	var out []request
	known := map[string]bool{}
	for _, n := range append(w.app.Commands(), w.app.Queries()...) {
		known[n] = true
	}
	for k, c := range w.cfg.Prologue {
		if !known[c.Name] {
			return nil, fmt.Errorf("gristsim: Config.Prologue[%d]: %q is no command or query of %s (its blocks: %v)", k, c.Name, w.app.Name, append(w.app.Commands(), w.app.Queries()...))
		}
		tenants := w.cfg.Tenants
		if c.Tenant != "" {
			if !slices.Contains(tenants, c.Tenant) {
				return nil, fmt.Errorf("gristsim: Config.Prologue[%d]: tenant %q is not one of Config.Tenants %v", k, c.Tenant, tenants)
			}
			tenants = []string{c.Tenant}
		}
		for _, t := range tenants {
			var args json.RawMessage
			switch a := c.Args.(type) {
			case nil:
				args = w.gen(c.Name)
			case json.RawMessage:
				args = a
			case []byte:
				args = a
			default:
				b, err := json.Marshal(a)
				if err != nil {
					return nil, fmt.Errorf("gristsim: Config.Prologue[%d] (%s): its arguments: %w", k, c.Name, err)
				}
				args = b
			}
			out = append(out, request{Query: isQuery(w.app, c.Name), Name: c.Name, Args: args,
				Env: grist.Envelope{ID: fmt.Sprintf("%s-prologue-%d", t, k+1), Tenant: t, Caller: "prologue"}})
		}
	}
	return out, nil
}

// gen draws name's arguments through the run's Chooser.
func (w *workload) gen(name string) json.RawMessage {
	pick := func(n int) int { return w.s.Choose("gristsim.arg", n) }
	var v any
	if g := w.cfg.Gen[name]; g != nil {
		v = g(pick)
	} else {
		v = Gen(w.app.ArgsType(name), w.cfg.Alphabet, w.cfg.Ints, pick)
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("gristsim: arguments of %s: %v", name, err))
	}
	return b
}

// Gen is a value of typ drawn by pick: strings from alphabet, integers
// from ints (an unsigned one from its non-negative values), booleans,
// structs field by field (unexported fields left zero), slices of zero to
// two elements; anything else its zero value.
func Gen(typ reflect.Type, alphabet []string, ints []int, pick func(n int) int) any {
	return values.Gen(typ, alphabet, ints, pick)
}

// all are the values of typ a probe takes: every combination of its
// picks (strings over the alphabet, integers from Ints), at most 27.
func (w *workload) all(typ reflect.Type) []json.RawMessage {
	var radix []int
	Gen(typ, w.cfg.Alphabet, w.cfg.Ints, func(n int) int { radix = append(radix, n); return 0 })
	total := 1
	for _, r := range radix {
		total = min(total*r, 27)
	}
	out := make([]json.RawMessage, 0, total)
	for k := 0; k < total; k++ {
		n := k
		b, _ := json.Marshal(Gen(typ, w.cfg.Alphabet, w.cfg.Ints, func(m int) int { c := n % m; n /= m; return c }))
		out = append(out, b)
	}
	return out
}

// call sends req until it is answered (at most MaxAttempts tries, the
// same id each time: a lost or late answer, or one saying the outcome is
// unknown, is sent again), recording it as a client's call.
func (w *workload) call(ctx context.Context, req request) {
	if w.http != nil {
		w.http.Do(ctx, req.Env, req.Name, req.Args) // it records the call as below, retrying under the same key
		return
	}
	if w.cluster != nil {
		w.record(ctx, req, func() response { return w.cluster.send(ctx, req) })
		return
	}
	send := func() (response, bool) {
		for try := 1; try <= MaxAttempts; try++ {
			resp, err := w.api.Call(ctx, req, CallTimeout)
			if err == nil && !resp.Retry {
				return resp, true
			}
			qenv.Sleep(ctx, RetryDelay)
		}
		return response{}, false
	}
	w.record(ctx, req, func() response {
		resp, ok := send()
		if !ok {
			return response{Err: "unanswered"}
		}
		return resp
	})
}

// Do runs the command or query name of app on st for env, in the
// caller's goroutine (no API between: a real tier's client in process,
// as examples/counters' TestRealSQLite has), and records the call as a
// run's clients do: under ClientsActor(env.Tenant), qobs.Invoke before
// the first try and Call.Done with the answer, an unknown outcome or an
// unavailable store retried under the same id inside the one call (at
// most MaxAttempts tries, then pending). It returns the answer: nil, a
// *grist.Rejected, grist.ErrConflict, the error's text, or for a call
// never answered grist.ErrOutcomeUnknown.
func Do(ctx context.Context, app *grist.App, st grist.Store, env grist.Envelope, name string, args json.RawMessage) error {
	w := &workload{app: app}
	req := request{Query: isQuery(app, name), Name: name, Env: env, Args: args}
	var resp response
	w.record(ctx, req, func() response {
		for try := 1; try <= MaxAttempts; try++ {
			if resp = serve(ctx, app, st, req); !resp.Retry {
				return resp
			}
			qenv.Sleep(ctx, RetryDelay)
		}
		return resp // pending: never answered
	})
	switch {
	case resp.Retry:
		return grist.ErrOutcomeUnknown
	case resp.Rejected == "conflict":
		return grist.ErrConflict
	case resp.Rejected != "":
		return &grist.Rejected{Name: resp.Rejected}
	case resp.Err != "":
		return errors.New(resp.Err)
	}
	return nil
}

// record records req as a client's call: invoked before, done with the
// answer do gives (pending if none came).
func (w *workload) record(ctx context.Context, req request, do func() response) {
	calls.Record(ctx, w.app, req.Env, req.Name, req.Args, func() calls.Answer { return answerOf(do()) })
}

// answerOf is a response as its client saw it: a Retry or an unanswered
// request is pending.
func answerOf(r response) calls.Answer {
	switch {
	case r.Err == "unanswered", r.Retry:
		return calls.Answer{Pending: true, Refused: r.refused}
	case r.Err != "":
		return calls.Answer{Err: r.Err, Refused: r.refused}
	case r.Rejected != "":
		return calls.Answer{Rejected: r.Rejected, Refused: r.refused}
	}
	return calls.Answer{Result: r.Result, Refused: r.refused}
}

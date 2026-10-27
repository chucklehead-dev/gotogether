// Package gristsim runs a grist application in a qenv simulation: a
// simstore, or the SQL tier's sqlstore over qsql (Config.SQL), at a
// chosen isolation level, one node or more (Config.Nodes; a crashable process
// running the App's processors and serving its commands and queries over
// a lossy qenv.Service, or over its HTTP API on qhttp: Config.HTTP), and a workload of clients sending random
// commands and queries of the registry to every tenant. Run's signature
// is a qtest.System's Run, so an application is explored, exhausted and
// shrunk with every claim of qtest:
//
//	qtest.System{Binding: "counters.binding.yaml", Rates: qenv.DefaultRates.Set("nemesis", 0.02),
//		Run: func(c qenv.Chooser) error { return gristsim.Run(c, App, gristsim.Config{}) }}
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
	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
	"github.com/chucklehead-dev/gotogether/qobs"
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
	// client's are (actor "prologue", ids <tenant>-prologue-<k>) and go
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
	// client is its Envelope's actor (ActorHeader).
	HTTP bool
	// Nodes is how many node processes run the App on the one store
	// (default 1): node, node2, ...; each a crashable process starting the
	// App (its relay, pollers and tenant watcher: two pollers of every
	// processor and tenant then race for each checkpoint, as two
	// replicas of a real tier do), restarted when it crashes. Only the
	// first serves the commands and queries (the API); every node's relay
	// is one Relay, so each step row is recorded once.
	Nodes int
	// CommitFaults turns on the store's commit faults
	// (store.<app>.commit: a commit applied whose answer is lost).
	CommitFaults bool
	// Check runs the scheduler's and the Store's own checks at every step
	// and commit, as GOTOGETHER_CHECK=1 does.
	Check bool
	// Alphabet is the strings a generated argument takes (default a, b,
	// c): few, so that names collide, which is what finds races.
	Alphabet []string
	// Gen overrides a command's or query's generated arguments, by name:
	// pick(n) chooses one of n through the run's Chooser.
	Gen map[string]func(pick func(n int) int) any
	// Setup builds the application's dependencies once per simulation (a
	// qenv.Service standing for a mailer), returning what attaches them to
	// a context: the node's tasks run with it, so a processor's effect
	// reaches them through its ctx. nil: none.
	Setup func(s *qenv.Sim, st grist.Store) func(context.Context) context.Context
	// Horizon bounds the clients' phase in virtual time (default 2m).
	Horizon time.Duration
	// Log, if set, receives the simulation's log (crashes, upgrades:
	// qenv.Sim.Log), for a failing run's story.
	Log func(string)
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
}

// ActorHeader is the header the HTTP tier's clients name their actor in
// (grist.ActorFromHeader).
const ActorHeader = "X-Gristsim-Actor"

// Timeouts: a client waits CallTimeout for an answer, then retries the
// same request (same id) after RetryDelay.
const (
	CallTimeout = 2 * time.Second
	RetryDelay  = 100 * time.Millisecond
	// MaxAttempts bounds a client's tries of one request: after it, the
	// call stays pending (qtest.Completes judges it).
	MaxAttempts = 50
)

// Run runs app on c under cfg: a qtest.System's Run. A run that does not
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
	deps := func(ctx context.Context) context.Context { return ctx }
	if cfg.Setup != nil {
		if d := cfg.Setup(s, st); d != nil {
			deps = d
		}
	}
	var srv *qhttp.Server
	var handler http.Handler
	if cfg.HTTP {
		h, err := grist.NewHandler(app, st, grist.ActorFromHeader(ActorHeader))
		if err != nil {
			return fmt.Errorf("gristsim: Config.HTTP: %w", err)
		}
		handler = h
	}
	node := s.Process("node", true, func(ctx context.Context) {
		if err := app.Start(deps(ctx), st, grist.WithRelay(relay)); err != nil {
			panic(fmt.Sprintf("gristsim: the node could not start: %v", err))
		}
		if srv != nil {
			srv.Handle(ctx, handler) // every incarnation listens again
		}
	})
	nodes := map[*qenv.Process]bool{node: true}
	for i := 2; i <= cfg.Nodes; i++ {
		nodes[s.Process(fmt.Sprintf("node%d", i), true, func(ctx context.Context) {
			if err := app.Start(deps(ctx), st, grist.WithRelay(relay)); err != nil {
				panic(fmt.Sprintf("gristsim: node %d could not start: %v", i, err))
			}
		})] = true
	}
	// a supervisor: a node that crashed itself (an effect's crash point)
	// restarts as one the nemesis crashed does
	s.OnCrash(func(p *qenv.Process) {
		if nodes[p] {
			p.Restart()
		}
	})
	w := &workload{app: app, cfg: cfg, s: s}
	if cfg.HTTP {
		srv = qhttp.NewServer(s, "api", node)
		tr := qhttp.NewTransport(srv)
		tr.Timeout = CallTimeout
		// a budget every call refills: the workload's calls are judged by a
		// client-side model, and a call the default budget left pending
		// after its effect committed is one the model cannot place (each
		// call is still bounded by MaxAttempts; the budget's own test is
		// grist's TestClientBackoffAndBudget)
		w.http = &client.Client{App: app, URL: srv.URL(), HTTP: &http.Client{Transport: tr}, ActorHeader: ActorHeader,
			MaxAttempts: MaxAttempts, RetryDelay: RetryDelay, Budget: &client.Budget{Ratio: MaxAttempts, Max: MaxAttempts}}
	} else {
		w.api = qenv.NewService(s, "api", node, func(ctx context.Context, req request) (response, error) {
			return serve(deps(ctx), app, st, req), nil
		})
	}
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
				tenant := cfg.Tenants[s.Choose("gristsim.tenant", len(cfg.Tenants))]
				name := names[s.Choose("gristsim.op", len(names))]
				args := w.gen(name)
				w.call(ctx, request{Query: isQuery(app, name), Name: name,
					Env: grist.Envelope{ID: fmt.Sprintf("%s-c%d-%d", tenant, i, op), Tenant: tenant, Actor: fmt.Sprintf("client%d", i)}, Args: args})
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
					req := request{Query: true, Name: q, Env: grist.Envelope{ID: fmt.Sprintf("%s-probe-%s-%d", t, q, k), Tenant: t, Actor: "probe"}, Args: args}
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
	if cfg.Horizon == 0 {
		cfg.Horizon = 2 * time.Minute
	}
	if cfg.Nodes == 0 {
		cfg.Nodes = 1
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
				Env: grist.Envelope{ID: fmt.Sprintf("%s-prologue-%d", t, k+1), Tenant: t, Actor: "prologue"}})
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
		v = Gen(w.app.ArgsType(name), w.cfg.Alphabet, pick)
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("gristsim: arguments of %s: %v", name, err))
	}
	return b
}

// Gen is a value of typ drawn by pick: strings from alphabet, integers 1
// to 3, booleans, structs field by field (unexported fields left zero),
// slices of zero to two elements; anything else its zero value.
func Gen(typ reflect.Type, alphabet []string, pick func(n int) int) any {
	if typ == nil {
		return struct{}{}
	}
	return genValue(typ, alphabet, pick).Interface()
}

func genValue(typ reflect.Type, alphabet []string, pick func(n int) int) reflect.Value {
	v := reflect.New(typ).Elem()
	switch typ.Kind() {
	case reflect.String:
		v.SetString(alphabet[pick(len(alphabet))])
	case reflect.Bool:
		v.SetBool(pick(2) == 1)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(1 + pick(3)))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(1 + pick(3)))
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).IsExported() {
				v.Field(i).Set(genValue(typ.Field(i).Type, alphabet, pick))
			}
		}
	case reflect.Slice:
		n := pick(3)
		s := reflect.MakeSlice(typ, n, n)
		for i := 0; i < n; i++ {
			s.Index(i).Set(genValue(typ.Elem(), alphabet, pick))
		}
		v.Set(s)
	}
	return v
}

// all are the values of typ a probe takes: every combination of its
// picks (strings over the alphabet, integers 1 to 3), at most 27.
func (w *workload) all(typ reflect.Type) []json.RawMessage {
	var radix []int
	Gen(typ, w.cfg.Alphabet, func(n int) int { radix = append(radix, n); return 0 })
	total := 1
	for _, r := range radix {
		total = min(total*r, 27)
	}
	out := make([]json.RawMessage, 0, total)
	for k := 0; k < total; k++ {
		n := k
		b, _ := json.Marshal(Gen(typ, w.cfg.Alphabet, func(m int) int { c := n % m; n /= m; return c }))
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
		return calls.Answer{Pending: true}
	case r.Err != "":
		return calls.Answer{Err: r.Err}
	case r.Rejected != "":
		return calls.Answer{Rejected: r.Rejected}
	}
	return calls.Answer{Result: r.Result}
}

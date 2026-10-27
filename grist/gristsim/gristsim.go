// Package gristsim runs a grist application in a qenv simulation: a
// simstore at a chosen isolation level, one node (a crashable process
// running the App's processors and serving its commands and queries over
// a lossy qenv.Service), and a workload of clients sending random
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
	"reflect"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Recording says which side of a run records its steps: one binding sees
// every actor of a run, so a run records one (docs/grist.md §3.1).
type Recording int

const (
	// AtCommits: grist records each command and processor step at its
	// commit, each refusal and query on return (the default).
	AtCommits Recording = iota
	// AtClients: the clients record each command and query where they see
	// it, qobs.Invoke before the first request and Call.Done with the
	// answer, every retry of a lost or late response inside the one call,
	// for qtest.Linearize. Processors record nothing.
	AtClients
)

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
	// (default: every one of the registry).
	Send []string
	// Record is the side that records the run (AtCommits by default).
	Record Recording
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

// Request is a client's request to the node.
type Request struct {
	Query bool // a query, else a command
	Name  string
	Env   grist.Envelope
	Args  json.RawMessage
}

// Response is the node's answer: a command's receipt, a query's result
// and position, a rejection's name, or an error's text.
type Response struct {
	Receipt  grist.Receipt
	Result   json.RawMessage
	Pos      grist.Position
	Rejected string
	Err      string
}

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
	s := qenv.New(c)
	defer s.Close()
	if cfg.Check {
		s.Check = true
	}
	s.Log = cfg.Log
	db := qenv.NewStore(s, app.Name, cfg.Isolation)
	st := simstore.New(db)
	setup := s.Context()
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
	nodeCtx := func(ctx context.Context) context.Context {
		ctx = deps(ctx)
		if cfg.Record == AtClients {
			ctx = grist.Quiet(ctx)
		}
		return ctx
	}
	node := s.Process("node", true, func(ctx context.Context) {
		if err := app.Start(nodeCtx(ctx), st); err != nil {
			panic(fmt.Sprintf("gristsim: the node could not start: %v", err))
		}
	})
	// a supervisor: a node that crashed itself (an effect's crash point)
	// restarts as one the nemesis crashed does
	s.OnCrash(func(p *qenv.Process) {
		if p == node {
			p.Restart()
		}
	})
	api := qenv.NewService(s, "api", node, func(ctx context.Context, req Request) (Response, error) {
		return serve(nodeCtx(ctx), app, st, req), nil
	})
	w := &workload{app: app, cfg: cfg, s: s, api: api}
	names := cfg.Send
	if len(names) == 0 {
		names = append(app.Commands(), app.Queries()...)
	}
	for i := 1; i <= cfg.Clients; i++ {
		s.Spawn(fmt.Sprintf("client%d", i), func(ctx context.Context) {
			ctx = qobs.Thread(ctx)
			for op := 1; op <= cfg.Ops; op++ {
				tenant := cfg.Tenants[s.Choose("gristsim.tenant", len(cfg.Tenants))]
				name := names[s.Choose("gristsim.op", len(names))]
				args := w.gen(name)
				w.call(ctx, Request{Query: isQuery(app, name), Name: name,
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
					req := Request{Query: true, Name: q, Env: grist.Envelope{ID: fmt.Sprintf("%s-probe-%s-%d", t, q, k), Tenant: t, Actor: "probe"}, Args: args}
					if cfg.Record == AtClients {
						w.record(ctx, req, func() Response { return serve(deps(grist.Quiet(ctx)), app, st, req) })
					} else {
						serve(deps(ctx), app, st, req)
					}
				}
			}
		}
	})
	if err := s.Run(time.Minute); err != nil {
		return err
	}
	return s.Settle(time.Minute)
}

func isQuery(app *grist.App, name string) bool {
	for _, q := range app.Queries() {
		if q == name {
			return true
		}
	}
	return false
}

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
	return cfg
}

// serve is the node's handling of a request.
func serve(ctx context.Context, app *grist.App, st grist.Store, req Request) Response {
	var rej *grist.Rejected
	if req.Query {
		res, pos, err := app.Query(ctx, st, req.Env, req.Name, req.Args)
		switch {
		case errors.As(err, &rej):
			return Response{Rejected: rej.Name, Pos: pos}
		case err != nil:
			return Response{Err: err.Error(), Pos: pos}
		}
		return Response{Result: res, Pos: pos}
	}
	rec, err := app.Execute(ctx, st, req.Env, req.Name, req.Args)
	switch {
	case errors.As(err, &rej):
		return Response{Rejected: rej.Name}
	case errors.Is(err, grist.ErrConflict):
		return Response{Rejected: "conflict"}
	case err != nil:
		return Response{Err: err.Error()}
	}
	return Response{Receipt: rec}
}

// A workload is the clients' generator and caller.
type workload struct {
	app *grist.App
	cfg Config
	s   *qenv.Sim
	api *qenv.Service[Request, Response]
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
// same id each time), recording it under AtClients.
func (w *workload) call(ctx context.Context, req Request) {
	send := func() (Response, bool) {
		for try := 1; try <= MaxAttempts; try++ {
			resp, err := w.api.Call(ctx, req, CallTimeout)
			if err == nil {
				return resp, true
			}
			qenv.Sleep(ctx, RetryDelay)
		}
		return Response{}, false
	}
	if w.cfg.Record != AtClients {
		send()
		return
	}
	w.record(ctx, req, func() Response {
		resp, ok := send()
		if !ok {
			return Response{Err: "unanswered"}
		}
		return resp
	})
}

// record records req as a client's call: invoked before, done with the
// answer do gives (pending if none came).
func (w *workload) record(ctx context.Context, req Request, do func() Response) {
	q, _ := grist.QuintName(req.Name)
	kv := append([]any{"actor", req.Env.Tenant, "process", w.app.Name}, grist.StepFields(w.app.ArgsType(req.Name), req.Args, "")...)
	if !req.Query {
		kv = append(kv, "id", req.Env.ID)
	}
	call := qobs.Invoke(ctx, q, kv...)
	resp := do()
	switch {
	case resp.Err == "unanswered":
		return // pending: the caller never learned the outcome
	case resp.Err != "":
		call.Done(errors.New(resp.Err))
	case resp.Rejected != "":
		call.Done(nil, "outcome", resp.Rejected)
	case req.Query:
		call.Done(nil, grist.StepFields(w.app.ResultType(req.Name), resp.Result, "obs.")...)
	default:
		call.Done(nil)
	}
}

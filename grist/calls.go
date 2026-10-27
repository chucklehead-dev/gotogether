package grist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// A Dependency is a remote party an application's effects call (a bank,
// a mailer, a ledger): its operations are its calls (NewCall), each
// typed, recorded at both ends and modelled from the declaration
// (docs/grist-instrumentation.md §3). Which implementation answers is
// attached per run (With: gristsim serves each dependency as a
// qenv.Service named after it; Local serves Handlers in process).
type Dependency struct {
	app   *App
	name  string
	quint string
	calls []*callInfo
	key   *depKey
}

// callInfo is a call's registration, untyped.
type callInfo struct {
	dep         *Dependency
	name, quint string // quint: the dependency's Quint name, then the call's capitalized (bankPay)
	req, resp   reflect.Type
	opts        options // keyed, refuses
	users       []*processorInfo
}

// label names the call for errors: <dependency>/<call>.
func (c *callInfo) label() string { return c.dep.name + "/" + c.name }

// NewDependency registers a dependency named name on app: a lower-case
// name joined by dashes, which names its simulated service and its fault
// kinds (rpc.<name>.request, .delay, .response) and starts its calls'
// Quint names.
func NewDependency(app *App, name string) *Dependency {
	d := &Dependency{app: app, name: name, key: &depKey{name: "dependency " + name}}
	if app.deps == nil {
		app.deps = map[string]*Dependency{}
	}
	switch q, err := QuintName(name); {
	case app.deps[name] != nil:
		app.problems = append(app.problems, fmt.Sprintf("dependency %q: declared twice: declare each dependency once", name))
	case err != nil:
		app.problems = append(app.problems, fmt.Sprintf("dependency %q: %v", name, err))
	case name == "api":
		// gristsim's API is a qenv.Service named api: one name, one fault kind
		app.problems = append(app.problems, fmt.Sprintf("dependency %q: api is the name of the App's own API service (rpc.api.*): rename the dependency", name))
	default:
		d.quint = q
	}
	if err := checkText("dependency name", name); err != nil {
		app.problems = append(app.problems, err.Error())
	}
	app.deps[name] = d
	app.depOrder = append(app.depOrder, d)
	return d
}

// Name is the dependency's name.
func (d *Dependency) Name() string { return d.name }

// With is ctx with t answering d's calls: what runs the node attaches it
// (gristsim does, per run; Main's Dependencies, a real tier's harness).
func (d *Dependency) With(ctx context.Context, t Transport) context.Context {
	return context.WithValue(ctx, d.key, t)
}

// String names the dependency.
func (d *Dependency) String() string { return "grist dependency " + d.name }

// Dependencies are the App's dependencies, in registration order.
func (a *App) Dependencies() []*Dependency { return slices.Clone(a.depOrder) }

// A Call is one operation of a dependency, typed: its request Req and
// response Resp are structs of scalar fields (strings, integers,
// booleans) the model compares, by JSON name. Its caller's view is the
// step <call's Quint name> (Do), its application at the dependency the
// step <...>Served and an answer given again from an application the step
// <...>Replayed (the handler's wrapper: Handler).
type Call[Req, Resp any] struct{ info *callInfo }

// CallName is a declared call, of any types: what Uses takes.
type CallName interface {
	Name() string
	call() *callInfo
}

func (c *Call[Req, Resp]) call() *callInfo { return c.info }

// Name is the call's name.
func (c *Call[Req, Resp]) Name() string { return c.info.name }

// The fields every call step records beside its request's and response's:
// a request or response field of one of these names would be overwritten.
// Read-only.
var callFields = map[string]bool{"processor": true, "trigger": true, "key": true, "result": true}

// NewCall declares call name of dependency d, with request Req and
// response Resp. Its options are Keyed (the dependency applies each key
// once) and Refuses (the answers that say the call did not happen).
func NewCall[Req, Resp any](d *Dependency, name string, opts ...Option) *Call[Req, Resp] {
	a := d.app
	c := &callInfo{dep: d, name: name, req: reflect.TypeFor[Req](), resp: reflect.TypeFor[Resp](), opts: a.options("call", d.name+"/"+name, opts)}
	what := fmt.Sprintf("call %s", c.label())
	q, err := QuintName(name)
	switch {
	case err != nil:
		a.problems = append(a.problems, fmt.Sprintf("%s: %v", what, err))
	case slices.ContainsFunc(d.calls, func(o *callInfo) bool { return o.name == name }):
		a.problems = append(a.problems, fmt.Sprintf("%s: declared twice: declare each call of a dependency once", what))
	case d.quint != "":
		c.quint = d.quint + upper(q)
		for _, n := range []string{c.quint, c.quint + "Served", c.quint + "Replayed"} {
			if prev, ok := a.quintNames[n]; ok {
				a.problems = append(a.problems, fmt.Sprintf("%s maps to the Quint action %s, as %q does: rename one", what, n, prev))
			}
			a.quintNames[n] = c.label()
		}
	}
	fields := map[string]string{}
	for _, side := range []struct {
		name string
		typ  reflect.Type
	}{{"request", c.req}, {"response", c.resp}} {
		if side.typ.Kind() != reflect.Struct {
			a.problems = append(a.problems, fmt.Sprintf("%s: its %s %v is no struct: a call's request and response are structs of named fields", what, side.name, side.typ))
			continue
		}
		for _, p := range modelable(side.typ, side.typ.Name()) {
			a.problems = append(a.problems, fmt.Sprintf("%s: %s", what, p))
		}
		for i := 0; i < side.typ.NumField(); i++ {
			f := side.typ.Field(i)
			if !f.IsExported() {
				continue
			}
			n := rec.JSONName(f)
			switch {
			case callFields[n]:
				a.problems = append(a.problems, fmt.Sprintf("%s: its %s's field %s is named %q, which every call step records itself (processor, trigger, key, result): rename it (its JSON name)", what, side.name, f.Name, n))
			case fields[n] != "":
				a.problems = append(a.problems, fmt.Sprintf("%s: its request and its response both have a field %q: a step records both by name, so rename one (its JSON name)", what, n))
			}
			fields[n] = side.name
		}
	}
	for _, r := range c.opts.refuses {
		if r == unknownOutcome {
			a.problems = append(a.problems, fmt.Sprintf("%s: a refusal named %q: a call's outcome %s already means it may have happened: give the rejection another name", what, r, unknownOutcome))
		}
	}
	d.calls = append(d.calls, c)
	return &Call[Req, Resp]{info: c}
}

// unknownOutcome is a caller's outcome when the answer does not say
// whether the call happened (a timeout, a lost answer).
const unknownOutcome = "unknown"

// Keyed declares a call the dependency applies once per key (the
// effect's key, CallKey, sent with every try): its generated model
// admits no second served record of a key (<call>Admits).
func Keyed() Option {
	return Option{name: "Keyed", kinds: kindsOf("Keyed"), apply: func(o *options) string {
		o.keyed = true
		return ""
	}}
}

// Uses declares the calls a processor's effect makes, enforced as Emits
// is: Do of a call the processor does not list is the effect's error,
// naming the fix. The generator writes a rule hole per (processor, call),
// <processor><Call>: the request it makes for a trigger.
func Uses(calls ...CallName) Option {
	return Option{name: "Uses", kinds: kindsOf("Uses"), apply: func(o *options) string {
		for _, c := range calls {
			if c == nil || c.call() == nil {
				return "a nil call: give declared calls (NewCall's)"
			}
			o.uses = append(o.uses, c.call())
		}
		return ""
	}}
}

// CallInfo is a call's cause, carried from the effect that makes it to
// wherever it is served: the App and tenant it is made for, the
// processor and the trigger it is made for, the head the poller read when
// it took the trigger, and the call's key (the effect's). The site's
// record is placed at it (order.pos 2·Head+1), so it follows the commit
// holding the trigger.
type CallInfo struct {
	App, Tenant, Processor string
	Trigger, Head          Position
	Key                    string
}

// A Transport answers a dependency's calls: JSON requests and responses,
// so grist imports nothing heavy (gristsim's is a qenv.Service; Local
// serves Handlers in process). An error that is a *Rejected is the
// dependency's refusal; any other leaves the outcome unknown. A
// transport never retries an unknown outcome: an at-most-once effect's
// one call would become several.
type Transport interface {
	Call(ctx context.Context, c CallInfo, call string, req json.RawMessage) (json.RawMessage, error)
}

// effectCause is what an effect's context carries for its calls.
type effectCause struct {
	app     *App
	s       Store
	p       *processorInfo
	tenant  string
	trigger Position
	head    Position
	key     string
	slot    int64 // the effect step's place among the processor's: 0 at least once (2·T), 1 at most once (2·T+1)
}

type causeKey struct{}

// Do makes one call of c from an effect, req its request, and returns
// the answer: the response; a declared refusal as NotApplied of its
// *Rejected (the call did not happen: at most once, grist.failed); or an
// error of unknown outcome (a timeout, a lost answer: it may have
// happened; at most once, grist.unknown). It makes one attempt: an
// effect's retries are the effect's (EffectTries, at least once, under
// the same key). The caller's view is recorded as an invocation before
// the request and its completion with the answer (qobs.Invoke,
// Call.Done), so a node crashed during the call leaves it pending.
//
// Do outside an effect (a decision, a fold), or from a processor whose
// NewProcessor does not list c (Uses), is an error naming the fix, and
// records nothing.
func (c *Call[Req, Resp]) Do(ctx context.Context, req Req) (Resp, error) {
	var zero Resp
	ci := c.info
	ec, _ := ctx.Value(causeKey{}).(*effectCause)
	if ec == nil {
		return zero, fmt.Errorf("grist: call %s made outside an effect: Do runs in a processor's Effect (grist.Effect's f), whose context carries the call's cause; a decision, a fold or a handler calls no dependency", ci.label())
	}
	if !slices.Contains(ec.p.opts.uses, ci) {
		return zero, fmt.Errorf("grist: processor %s calls %s, which it does not declare: add grist.Uses(...) of the call to its NewProcessor", ec.p.name, ci.label())
	}
	t, _ := ctx.Value(ci.dep.key).(Transport)
	if t == nil {
		return zero, fmt.Errorf("grist: call %s: no transport is attached for dependency %s: gristsim serves it from Config.Doubles; elsewhere attach one where the node starts (the dependency's With(ctx, grist.Local(handlers)) serves it in process)", ci.label(), ci.dep.name)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return zero, NotApplied(fmt.Errorf("grist: call %s: its request does not encode: %w", ci.label(), err))
	}
	reqKV, err := rec.StepFields(ci.req, body, "")
	if err != nil {
		return zero, NotApplied(fmt.Errorf("grist: call %s: its request: %w", ci.label(), err))
	}
	info := CallInfo{App: ec.app.Name, Tenant: ec.tenant, Processor: ec.p.name, Trigger: ec.trigger, Head: ec.head, Key: ec.key}
	kv := append([]any{"actor", ec.tenant, "process", ec.s.Name(), "processor", ec.p.name, "trigger", int64(ec.trigger), "key", ec.key}, reqKV...)
	// ordered as the effect step it precedes on the poller's thread: after
	// the commits through the head, among the processor's steps at the
	// effect's place
	kv = append(kv, "order.pos", readOrder(ec.head), "order.p."+ec.p.quint, 2*int64(ec.trigger)+ec.slot)
	call := qobs.Invoke(ctx, ci.quint, kv...)
	out, err := t.Call(ctx, info, ci.name, body)
	zeroKV := ci.zeroResponse()
	var rej *Rejected
	switch {
	case errors.As(err, &rej) && slices.Contains(ci.opts.refuses, rej.Name):
		call.Done(nil, append([]any{"outcome", rej.Name}, zeroKV...)...)
		return zero, NotApplied(rej)
	case err != nil:
		call.Done(nil, append([]any{"outcome", unknownOutcome}, zeroKV...)...)
		return zero, err
	}
	var resp Resp
	respKV, derr := rec.StepFields(ci.resp, out, "")
	if derr == nil {
		derr = json.Unmarshal(out, &resp)
	}
	if derr != nil {
		// an answer that does not decode says nothing of whether it happened
		call.Done(nil, append([]any{"outcome", unknownOutcome}, zeroKV...)...)
		return zero, fmt.Errorf("grist: call %s: its answer: %w", ci.label(), derr)
	}
	call.Done(nil, respKV...)
	return resp, nil
}

// zeroResponse are the response's fields at their zero values: what a
// step without a response (a refusal, an unknown outcome) records, so
// every step of the call has every parameter of its action.
func (c *callInfo) zeroResponse() []any {
	b, _ := json.Marshal(reflect.New(c.resp).Elem().Interface())
	kv, _ := rec.StepFields(c.resp, b, "")
	return kv
}

// A CallHandler serves one call in process (Call.Handler): the double a
// simulation serves, or an implementation the team owns. Its wrapper
// records the call's application at its site. (A Handler is a fold's.)
type CallHandler struct {
	call  *callInfo
	serve func(ctx context.Context, req []byte) ([]byte, error)
}

// Handlers are the handlers of one dependency's calls.
type Handlers []CallHandler

// handlerCtx is what a handler's context carries: its call's cause, and
// whether the handler said its answer was given before (Replayed).
type handlerCtx struct {
	info     CallInfo
	replayed bool
}

// siteClock places a transport's site records of each key: at the call's
// cause (2·Head+1), but never before an earlier record of the key. A
// request from an earlier head may be served after a later one's (a
// request delayed past its caller's timeout and a restarted poller's
// retry, or two nodes' pollers): placed at its own cause it would come
// before the record it follows on the key's thread, a cycle, and a replay
// before the application it repeats.
type siteClock struct {
	mu   sync.Mutex
	last map[string]int64
}

// at is the place of a record of key caused at pos: pos, or the key's
// last record's if later.
func (c *siteClock) at(key string, pos int64) int64 {
	if c == nil {
		return pos
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if prev, ok := c.last[key]; ok && prev > pos {
		pos = prev
	}
	c.last[key] = pos
	return pos
}

// served is what a transport hands a handler's wrapper: the call's cause
// and the transport's site clock.
type served struct {
	info  CallInfo
	clock *siteClock
}

type handlerKey struct{}

// CallKey is the call's key: in an effect, the effect key (EffectKey),
// sent with every try; in a handler, the key the call was sent with, so
// a double or a real service can deduplicate by it.
func CallKey(ctx context.Context) string {
	if h, ok := ctx.Value(handlerKey{}).(*handlerCtx); ok {
		return h.info.Key
	}
	return EffectKey(ctx)
}

// Replayed says, from a handler, that its answer is one it gave before
// for the call's key (a deduplicating dependency answering a retry from
// its first answer): it applied nothing now, so the wrapper records the
// step <call>Replayed instead of a served record, and the model admits it
// only as an earlier served record of the key, request and answer alike
// (a replay of no application, or of another answer, is rejected). It is
// the handler's word: a double that applies a call again and says
// Replayed hides the second application. Outside a handler it does
// nothing.
func Replayed(ctx context.Context) {
	if h, ok := ctx.Value(handlerKey{}).(*handlerCtx); ok {
		h.replayed = true
	}
}

// Handler is f as c's handler. Its wrapper decodes the request, runs f
// with the call's cause in its context (CallKey), and records the call's
// application at its site, the step <call>Served: the request's and the
// response's fields, the processor, trigger and key, the outcome f
// answered (ok, or a declared refusal's name), placed at the call's
// cause (order.pos 2·Head+1: after the commit holding the trigger, and
// not before an earlier record of the key) on a thread of its own key. A
// handler that answers from an earlier application says so (Replayed):
// the step is <call>Replayed, with the same fields. One that returns
// another error is recorded with it, naming the fix.
func (c *Call[Req, Resp]) Handler(f func(ctx context.Context, req Req) (Resp, error)) CallHandler {
	ci := c.info
	return ci.handler(func(ctx context.Context, body []byte) ([]byte, error) {
		var req Req
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, &undecoded{fmt.Errorf("grist: call %s: its request does not decode: %w", ci.label(), err)}
		}
		resp, err := f(ctx, req)
		if err != nil {
			return nil, err
		}
		out, merr := json.Marshal(resp)
		if merr != nil {
			return nil, &unencoded{fmt.Errorf("grist: call %s: its response does not encode: %w", ci.label(), merr)}
		}
		return out, nil
	})
}

// undecoded is a request a handler could not decode: answered with it,
// recorded nowhere (no application happened, and its fields are unknown);
// ServeCalls answers it 400, which OverHTTP takes for NotApplied.
type undecoded struct{ error }

// unencoded is a response a handler could not encode: recorded with it.
type unencoded struct{ error }

// handler wraps f, which answers a request's JSON with the response's
// JSON or an error, as the call's handler: Handler's wrapper, and the
// default double's (DefaultHandlers), which has no types to decode into.
func (ci *callInfo) handler(f func(ctx context.Context, body []byte) ([]byte, error)) CallHandler {
	return CallHandler{call: ci, serve: func(ctx context.Context, body []byte) ([]byte, error) {
		hc := &handlerCtx{}
		sv, _ := ctx.Value(callInfoKey{}).(served)
		hc.info = sv.info
		out, err := f(context.WithValue(ctx, handlerKey{}, hc), body)
		var und *undecoded
		if errors.As(err, &und) {
			// its own type, so ServeCalls answers 400 (nothing applied)
			return nil, und
		}
		var rej *Rejected
		var une *unencoded
		var kv []any
		var recErr error
		switch {
		case errors.As(err, &une):
			err = une.error
			recErr = err
			kv = ci.zeroResponse()
		case err == nil:
			kv, recErr = rec.StepFields(ci.resp, out, "")
		case errors.As(err, &rej) && slices.Contains(ci.opts.refuses, rej.Name):
			kv = append([]any{"outcome", rej.Name}, ci.zeroResponse()...)
		default:
			recErr = fmt.Errorf("grist: call %s's handler returned %v, which is neither a response nor a refusal it declares: return a declared refusal (Refuses), or a response", ci.label(), err)
			kv = ci.zeroResponse()
			err = recErr
		}
		reqKV, _ := rec.StepFields(ci.req, body, "")
		i := hc.info
		all := append([]any{"actor", i.Tenant, "process", ci.dep.app.Name, "thread", "grist.served/" + i.Key,
			"processor", i.Processor, "trigger", int64(i.Trigger), "key", i.Key, "order.pos", sv.clock.at(i.Key, readOrder(i.Head))}, reqKV...)
		action := ci.quint + "Served"
		if hc.replayed {
			action = ci.quint + "Replayed"
		}
		qobs.Record(ctx, action, recErr, append(all, kv...)...)
		if err != nil {
			return nil, err
		}
		return out, nil
	}}
}

// DefaultHandlers are the default double of d: what gristsim serves a
// dependency the run gives no handlers (Config.Doubles), and what a test
// or a binary may attach in process (d.With(ctx, grist.Local(...))). A
// Keyed call is applied once per key and answered with its response,
// which has no fields (an acknowledgement); a later try of the key gets
// that first answer, as a replay (Replayed: recorded <call>Replayed and
// judged as one), so a key's first answer is final. An unkeyed call is
// applied, and answered so, every time. It refuses nothing. Each call of
// DefaultHandlers has its own state (the keys applied): one per run.
//
// It panics, naming the fix, if a call of d answers a response with
// fields (a payment's reference): what a dependency answers there is its
// own, and a made-up answer (the zero response, which no dependency
// gives) would exercise one branch of the effect's handling of it, a
// reference left empty unseen. Give such a dependency handlers that
// answer as it would (Call.Handler).
func DefaultHandlers(d *Dependency) Handlers {
	for _, ci := range d.calls {
		if fs, _ := scalarFields(ci.resp); len(fs) > 0 {
			names := make([]string, len(fs))
			for i, f := range fs {
				names[i] = f.json
			}
			panic(fmt.Sprintf("grist.DefaultHandlers: call %s answers a response with fields (%s), which a default double cannot know: give dependency %s handlers that answer as it would (Call.Handler; in a simulation gristsim.Config.Doubles)",
				ci.label(), strings.Join(names, ", "), d.name))
		}
	}
	hs := make(Handlers, 0, len(d.calls))
	for _, ci := range d.calls {
		zero, _ := json.Marshal(reflect.New(ci.resp).Elem().Interface())
		if !ci.opts.keyed {
			hs = append(hs, ci.handler(func(context.Context, []byte) ([]byte, error) { return zero, nil }))
			continue
		}
		var mu sync.Mutex
		applied := map[string]bool{}
		hs = append(hs, ci.handler(func(ctx context.Context, _ []byte) ([]byte, error) {
			key := CallKey(ctx)
			mu.Lock()
			again := applied[key]
			applied[key] = true
			mu.Unlock()
			if again {
				// its first answer, the acknowledgement: final for the key
				Replayed(ctx)
			}
			return zero, nil
		}))
	}
	return hs
}

type callInfoKey struct{}

// Local is a transport serving h in process: each call by its handler
// (Handler), recorded at its site. gristsim serves a simulated
// dependency's requests through it, one Local per run; a test or a
// binary may attach it directly (Dependency.With). A Local orders its
// site records of each key (never one before an earlier one), so one
// Local serves one run.
func Local(h Handlers) Transport {
	by := map[string]CallHandler{}
	for _, x := range h {
		if x.call == nil {
			panic("grist.Local: a zero CallHandler: build each with a call's Handler")
		}
		if _, dup := by[x.call.name]; dup {
			panic(fmt.Sprintf("grist.Local: two handlers of call %s: give each call one (Handlers serve one dependency)", x.call.label()))
		}
		by[x.call.name] = x
	}
	return &local{by: by, clock: &siteClock{last: map[string]int64{}}}
}

type local struct {
	by    map[string]CallHandler
	clock *siteClock
}

func (l *local) Call(ctx context.Context, c CallInfo, call string, req json.RawMessage) (json.RawMessage, error) {
	h, ok := l.by[call]
	if !ok {
		return nil, fmt.Errorf("grist: no handler of call %s: add the call's Handler to the Handlers", call)
	}
	return h.serve(context.WithValue(ctx, callInfoKey{}, served{info: c, clock: l.clock}), req)
}

// Serves reports whether h has a handler of each call of d, naming the
// first missing.
func (h Handlers) Serves(d *Dependency) error {
	for _, c := range d.calls {
		if !slices.ContainsFunc(h, func(x CallHandler) bool { return x.call == c }) {
			return fmt.Errorf("no handler of call %s: add it to the dependency's Handlers", c.label())
		}
	}
	for _, x := range h {
		if x.call != nil && x.call.dep != d {
			return fmt.Errorf("a handler of call %s, of another dependency than %s: give each dependency its own Handlers", x.call.label(), d.name)
		}
	}
	return nil
}

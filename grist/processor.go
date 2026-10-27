package grist

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// A Delivery is an effect's declared guarantee.
type Delivery int

const (
	// AtLeastOnce runs the effect, then commits the checkpoint (and the
	// outcome event): a crash between them runs it again, under the same
	// EffectKey. An error is tried EffectTries times.
	AtLeastOnce Delivery = iota + 1
	// AtMostOnce commits the checkpoint first, then runs the effect once,
	// then commits its outcome event (idempotent, under a marker of the
	// effect key): OnSuccess's after a success, grist.failed after an
	// error that says the effect did not happen (NotApplied), and
	// grist.unknown, carrying the error, after any other (it may have: a
	// payment whose answer was lost). A crash between the checkpoint and
	// the effect loses the effect, one after it the outcome, and no restart
	// runs it again. It is tried once (EffectTries does not apply: a failed
	// try may have taken effect).
	AtMostOnce
)

// String is the delivery's name (AtLeastOnce, AtMostOnce).
func (d Delivery) String() string {
	switch d {
	case AtLeastOnce:
		return "AtLeastOnce"
	case AtMostOnce:
		return "AtMostOnce"
	}
	return fmt.Sprintf("Delivery(%d)", int(d))
}

// notApplied is an effect's error that says it did not happen.
type notApplied struct{ err error }

func (e *notApplied) Error() string { return e.err.Error() }
func (e *notApplied) Unwrap() error { return e.err }

// NotApplied is err from an at-most-once effect's f saying the effect
// did not happen (the dependency refused it before acting: an invalid
// account, a declined card): its outcome is known, grist.failed. Any
// other error leaves it unknown (a timeout, a lost answer: it may have
// happened), and the outcome is grist.unknown with the error's text, for
// reconciliation. At least once, every error is retried alike.
func NotApplied(err error) error {
	if err == nil {
		return nil
	}
	return &notApplied{err: err}
}

type resultKind int

// The kinds start at 1, so a zero Result is no result.
const (
	emitResult resultKind = iota + 1
	effectResult
	failResult
)

// A Result is what a processor's handler returns for one trigger: Emit,
// Skip, Effect or Fail. The zero Result is invalid: the trigger fails
// (grist.failed), naming the fix.
type Result struct {
	kind      resultKind
	events    []Event
	effect    func(ctx context.Context) error
	delivery  Delivery
	onSuccess *Event
	err       error
}

// Emit appends events with the processor's checkpoint, in one
// transaction conditioned on the checkpoint alone: exactly once in the
// log.
func Emit(events ...Event) Result { return Result{kind: emitResult, events: events} }

// Skip checkpoints the trigger and does nothing.
func Skip() Result { return Result{kind: emitResult} }

// Fail is a handler's error: the trigger is handled again (EffectTries
// tries, with backoff), then checkpointed with a grist.failed event
// naming it, so a poison event stops nothing and is visible.
func Fail(err error) Result { return Result{kind: failResult, err: err} }

// An EffectOption configures an Effect.
type EffectOption func(*Result)

// OnSuccess appends e with the checkpoint once the effect succeeded.
func OnSuccess(e Event) EffectOption { return func(r *Result) { r.onSuccess = &e } }

// Effect runs f outside the log, with the delivery guarantee d
// (AtLeastOnce or AtMostOnce; any other is the handler's error). f's ctx
// carries the EffectKey. At least once, an error is retried (EffectTries
// tries, with backoff), then checkpointed with a grist.failed event; at
// most once, f runs once after the checkpoint, and its outcome (the
// success event, or grist.failed) is appended after it, if no crash came
// between.
func Effect(f func(ctx context.Context) error, d Delivery, opts ...EffectOption) Result {
	r := Result{kind: effectResult, effect: f, delivery: d}
	for _, o := range opts {
		o(&r)
	}
	return r
}

// check is the handler's error in r, if r is no result grist can carry
// out: the zero Result, an Effect without a function or of a delivery
// grist does not have.
func (r Result) check(processor string) error {
	switch {
	case r.kind == 0:
		return fmt.Errorf("grist: processor %s returned the zero Result: return Emit, Skip, Effect or Fail", processor)
	case r.kind == effectResult && r.effect == nil:
		return fmt.Errorf("grist: processor %s returned an Effect without a function", processor)
	case r.kind == effectResult && r.delivery != AtLeastOnce && r.delivery != AtMostOnce:
		return fmt.Errorf("grist: processor %s returned an Effect of delivery %d: give AtLeastOnce or AtMostOnce", processor, r.delivery)
	}
	return nil
}

type effectKey struct{}

// EffectKey is the idempotency key an effect hands its dependency:
// processor/tenant/trigger, the same on every run of the effect for that
// trigger, so a dependency that supports keys can make "at least once"
// "once".
func EffectKey(ctx context.Context) string { k, _ := ctx.Value(effectKey{}).(string); return k }

// processorInfo is a processor's registration, untyped.
type processorInfo struct {
	name, quint string
	on          string
	opts        options
	handle      func(ctx context.Context, r *Reads, s Stored) Result
}

// A Processor handles each event of its type, per tenant in position
// order, after its checkpoint. Its handler reads as of the trigger's
// position T, unconditioned: positions are dense and committed in order,
// so the prefix through T never changes.
type Processor struct{ info *processorInfo }

// NewProcessor registers a processor of events of type on. Its options
// are EffectTries and BatchSize.
func NewProcessor[E any](app *App, name string, on *EventType[E], h func(ctx context.Context, r *Reads, e E, m Meta) Result, opts ...Option) *Processor {
	app.register("processor", name)
	q, _ := QuintName(name)
	p := &processorInfo{name: name, quint: q, on: on.Name(), opts: app.options("processor", name, opts), handle: func(ctx context.Context, r *Reads, s Stored) Result {
		e, err := on.Decode(s)
		if err != nil {
			return Fail(err)
		}
		return h(ctx, r, e, s.Meta())
	}}
	for _, c := range p.opts.uses {
		switch {
		case c.dep.app != app:
			app.problems = append(app.problems, fmt.Sprintf("processor %q uses call %s, of another App's dependency: declare the dependency on %s", name, c.label(), app.Name))
		case p.opts.shape.kind != effectResult:
			app.problems = append(app.problems, fmt.Sprintf("processor %q uses call %s, but declares no Delivers: a call is made from an Effect, so declare grist.Delivers", name, c.label()))
		default:
			if !slices.Contains(c.users, p) {
				c.users = append(c.users, p)
			}
		}
	}
	app.processors = append(app.processors, p)
	return &Processor{info: p}
}

// Name is the processor's name.
func (p *Processor) Name() string { return p.info.name }

// EffectFaults is processor's effect crash points as a fault kind:
// "grist.<processor>.effect", crashBefore (just before f runs) and
// crashAfter (just after f returned and its step was recorded). At least
// once, both come before the checkpoint: crashAfter is the crash that
// duplicates the effect. At most once, both come after it: crashBefore
// loses the effect, crashAfter its outcome event. Each is one named
// option, which a qenv.Script asks for and Exhaust reaches with one
// fault.
func EffectFaults(processor string) qenv.FaultKind {
	return qenv.DeclareFaults("grist."+processor+".effect", "crashBefore", "crashAfter")
}

// Start starts the App's node on s: for every tenant, the relay that
// records every commit's step from s's step log (Relay; s must be a
// store.StepLog) and one poller per processor (each a daemon waiting on
// the tenant's appends), and a watcher that starts both for every tenant
// created later (s must be a store.TenantFeed: the watcher waits on
// WaitTenants). Every one is a task (qenv.Go: of the caller's process in
// a simulation, a goroutine outside one). It returns once the start's
// tenants are started; the node stops with ctx or its process. A tenant
// whose name grist refuses (CheckTenant) is not relayed: Relay.Err names
// it. Options: WithRelay.
func (a *App) Start(ctx context.Context, s Store, opts ...StartOption) error {
	if err := a.Verify(); err != nil {
		return err
	}
	var cfg startConfig
	for _, o := range opts {
		if o.apply != nil {
			o.apply(&cfg)
		}
	}
	r := cfg.relay
	if r == nil {
		var err error
		if r, err = NewRelay(s); err != nil {
			return err
		}
	} else if r.s != s {
		return fmt.Errorf("grist: WithRelay's relay is of store %s (%T), Start's store is %s (%T): a relay records one store's step log; give Start that store, or a relay of this one (NewRelay)", r.s.Name(), r.s, s.Name(), s)
	}
	feed, ok := s.(store.TenantFeed)
	if !ok {
		return fmt.Errorf("grist: store %s (%T) is no store.TenantFeed: a node relays and handles every tenant, those created after it started included, waiting on WaitTenants", s.Name(), s)
	}
	n := &node{app: a, s: s, relay: r, started: map[string]bool{}}
	if cfg.onNode != nil {
		cfg.onNode(n)
	}
	var tenants []string
	for try := 1; ; try++ {
		ts, err := s.Tenants(ctx)
		if err == nil {
			tenants = ts
			break
		}
		if !store.Transient(err) || try >= defaultTransientTries {
			return err
		}
		backoff(ctx, try)
	}
	n.count = len(tenants)
	for _, t := range tenants {
		n.start(ctx, t)
	}
	qenv.Go(ctx, "grist.tenants", func(ctx context.Context) {
		qenv.Daemon(ctx)
		n.watch(ctx, feed)
	})
	return nil
}

// A node is one App.Start: the tenants it relays and handles.
type node struct {
	app     *App
	s       Store
	relay   *Relay
	started map[string]bool // only the starting task and the watcher touch it, one after the other
	count   int             // the tenants the store had when last read
	// pollers is how many pollers the node started, waiting how many of
	// them wait on the store with nothing after their checkpoint: equal,
	// no processor has work in hand (an at-most-once effect in flight
	// after its checkpoint included), which a checkpoint alone cannot say
	pollers, waiting atomic.Int64
}

// idle reports whether every poller of the node waits with nothing to do.
func (n *node) idle() bool { return n.waiting.Load() == n.pollers.Load() }

// start starts tenant t's relay and pollers, once.
func (n *node) start(ctx context.Context, t string) {
	if n.started[t] {
		return
	}
	n.started[t] = true
	if err := CheckTenant(t); err != nil {
		n.relay.setErr(t, err, true) // never relayed: Err names it
		return
	}
	r := n.relay
	qenv.Go(ctx, "grist.relay."+t, func(ctx context.Context) {
		qenv.Daemon(ctx)
		r.run(ctx, t)
	})
	for _, p := range n.app.processors {
		p := p
		n.pollers.Add(1)
		qenv.Go(ctx, "grist."+p.name+"."+t, func(ctx context.Context) {
			qenv.Daemon(ctx)
			(&poller{app: n.app, s: n.s, p: p, tenant: t, waiting: &n.waiting}).run(qobs.Thread(ctx))
		})
	}
}

// watch starts every tenant created after the node started, waiting on
// the store's feed.
func (n *node) watch(ctx context.Context, feed store.TenantFeed) {
	tries := 0
	for ctx.Err() == nil {
		if err := feed.WaitTenants(ctx, n.count); err != nil {
			if ctx.Err() == nil {
				tries++
				backoff(ctx, min(tries, 6))
			}
			continue
		}
		ts, err := n.s.Tenants(ctx)
		if err != nil {
			tries++
			backoff(ctx, min(tries, 6))
			continue
		}
		tries, n.count = 0, len(ts)
		for _, t := range ts {
			n.start(ctx, t)
		}
	}
}

// A poller runs one processor for one tenant.
type poller struct {
	app     *App
	s       Store
	p       *processorInfo
	tenant  string
	tries   int           // transient errors in a row
	waiting *atomic.Int64 // the node's count of idle pollers (node.idle)
}

// pause backs off after a transient error.
func (pl *poller) pause(ctx context.Context) {
	pl.tries++
	backoff(ctx, min(pl.tries, 6))
}

func (pl *poller) run(ctx context.Context) {
	for ctx.Err() == nil {
		cp, err := pl.s.Checkpoint(ctx, pl.tenant, pl.p.name)
		if err != nil {
			pl.pause(ctx)
			continue
		}
		evs, head, err := pl.s.Read(ctx, pl.tenant, store.Selector{Types: []string{pl.p.on}, After: cp, Limit: pl.p.opts.batchSize}, 0)
		if err != nil {
			pl.pause(ctx)
			continue
		}
		pl.tries = 0
		if len(evs) == 0 {
			pl.waiting.Add(1)
			pl.s.Wait(ctx, pl.tenant, head)
			pl.waiting.Add(-1)
			continue
		}
		// the checkpoint and the read are two reads: another poller (a
		// second node's) may have moved the checkpoint between them, and a
		// trigger handled on that stale checkpoint with the newer head
		// records an effect step after the other's checkpoint on the
		// tenant's clock (order.pos) but before it among the processor's
		// (order.p): a cycle, and an effect the model's next trigger is not.
		// Unmoved at this read, it was cp when head was read
		if now, err := pl.s.Checkpoint(ctx, pl.tenant, pl.p.name); err != nil || now != cp {
			continue
		}
		pl.handle(ctx, cp, head, evs)
	}
}

// pending is one trigger's part of an Emit batch. skip marks a Delivers
// processor's trigger handled without its effect (a Skip, or a Fail's
// grist.failed): its step is the processor's skip action
// (<processor>Skip), not its checkpoint, which follows effect steps.
type pending struct {
	trigger Position
	events  []Event
	skip    bool
}

// handle handles evs (triggers after checkpoint cp, read with the head
// at head) until one fails to commit: consecutive Emits and Skips commit
// in one transaction, an Effect alone.
func (pl *poller) handle(ctx context.Context, cp, head Position, evs []Stored) {
	var group []pending
	flush := func() bool {
		if len(group) == 0 {
			return true
		}
		h, ok := pl.commit(ctx, cp, group)
		if ok {
			cp, head, group = group[len(group)-1].trigger, h, nil
		}
		return ok
	}
	for _, e := range evs {
		res, ok := pl.decide(ctx, e)
		if !ok {
			flush()
			return // a store error: read again
		}
		delivers := pl.p.opts.shape.kind == effectResult
		switch res.kind {
		case emitResult:
			group = append(group, pending{trigger: e.Pos, events: res.events, skip: delivers})
			continue
		case failResult:
			group = append(group, pending{trigger: e.Pos, events: []Event{failedEvent(pl.p.name, e.Pos)}, skip: delivers})
			continue
		}
		if !flush() {
			return
		}
		h, ok := pl.effect(ctx, cp, head, e, res)
		if !ok {
			return
		}
		cp, head = e.Pos, h
	}
	flush()
}

// decide runs the handler for trigger e at its position, trying a
// failure EffectTries times; false on a store error (read again).
func (pl *poller) decide(ctx context.Context, e Stored) (Result, bool) {
	for try := 1; ; try++ {
		r := readsAt(ctx, pl.app, pl.s, pl.tenant, e.Pos, pl.key(e.Pos))
		res := pl.p.handle(ctx, r, e)
		if r.err != nil {
			pl.pause(ctx)
			return Result{}, false
		}
		if r.bad != nil {
			res = Fail(r.bad)
		} else if err := res.check(pl.p.name); err != nil {
			res = Fail(err)
		} else if err := pl.p.opts.shape.conforms(pl.p.name, res); err != nil {
			res = Fail(err)
		}
		for _, ev := range res.events {
			if pl.app.events[ev.Type] == nil {
				res = Fail(fmt.Errorf("grist: processor %s emits %q, which is not registered on %s: register it (NewEvent)", pl.p.name, ev.Type, pl.app.Name))
			}
		}
		if err := checkEvents("processor "+pl.p.name, res.events); err != nil {
			res = Fail(err)
		}
		if res.kind != failResult || try >= pl.p.opts.effectTries {
			return res, true
		}
		backoff(ctx, try)
	}
}

// effectContext is ctx as an effect's f gets it: its key (EffectKey) and
// its calls' cause (Call.Do): trigger, the head the poller read (the
// effect step's place on the tenant's clock, 2·head+1, and the site's),
// and slot, the effect step's place among the processor's steps.
func (pl *poller) effectContext(ctx context.Context, trigger, head Position, key string, slot int64) context.Context {
	ctx = context.WithValue(ctx, effectKey{}, key)
	return context.WithValue(ctx, causeKey{}, &effectCause{app: pl.app, s: pl.s, p: pl.p, tenant: pl.tenant, trigger: trigger, head: head, key: key, slot: slot})
}

func (pl *poller) key(t Position) string { return effectKeyOf(pl.p.name, pl.tenant, t) }

// effectKeyOf is the effect key of processor's effect for trigger t in
// tenant (EffectKey): what ServeCalls requires a call's key to be.
func effectKeyOf(processor, tenant string, t Position) string {
	return fmt.Sprintf("%s/%s/%d", processor, tenant, t)
}

// commit appends a group's events with the checkpoint moved from cp to
// its last trigger, recording one step per trigger (with kv, an effect's
// outcome); false if it did not commit (read again). A transient error
// is retried here: the handlers are functions of the prefix through
// their triggers, which never changes.
func (pl *poller) commit(ctx context.Context, cp Position, group []pending, kv ...any) (Position, bool) {
	return pl.commitAt(ctx, cp, group, 1, kv...)
}

// commitAt is commit with each step's order among the processor's at
// 2·trigger+slot: 1 after the trigger's effect steps (at least once,
// Emit), 0 before them (at most once's checkpoint).
func (pl *poller) commitAt(ctx context.Context, cp Position, group []pending, slot int64, kv ...any) (Position, bool) {
	var events []Event
	triggers := []int64{}
	for _, g := range group {
		events = append(events, g.events...)
		triggers = append(triggers, int64(g.trigger))
	}
	// the batch's row holds one step per trigger, in trigger order, each
	// with its own events: the relay gives each the head after them
	steps := make([]rowStep, 0, len(group))
	for _, g := range group {
		steps = append(steps, pl.step(g.trigger, g.events, slot, g.skip, kv...))
	}
	b := store.Batch{Events: events, Checkpoint: &store.CheckpointMove{Processor: pl.p.name, From: cp, To: group[len(group)-1].trigger},
		Time: qenv.Now(ctx).UnixMilli(), Cause: "processor:" + pl.p.name, Step: encodeRow(steps)}
	for {
		head, err := pl.s.Append(ctx, pl.tenant, b)
		if err == nil {
			pl.tries = 0
			return head, true
		}
		if !store.Transient(err) {
			var cm *store.CheckpointMoved
			if !errors.As(err, &cm) {
				pl.pause(ctx) // a permanent error: read again, but not at once
			}
			return 0, false // the checkpoint moved: another poller's
		}
		pl.pause(ctx)
		if ctx.Err() != nil {
			// the node stops: its backoff returns at once now, and a store
			// failing alike (one answering an ended context with a transient
			// error, a killed tier node's) would be retried in a spin. Not
			// committed, as far as this poller knows: the next node reads
			// the checkpoint again
			return 0, false
		}
		err0 := err
		// a commit of unknown outcome may have moved the checkpoint: the
		// next Append's compare-and-set tells
		if now, err := pl.s.Checkpoint(ctx, pl.tenant, pl.p.name); err == nil && now != cp {
			// not this poller's to know whether it was its own commit: it
			// counts as another poller's, whose trigger it leaves alone (at
			// most once, an effect then left to the other poller, or lost if
			// the commit was this one's)
			if sim := qenv.From(ctx); sim != nil && sim.Log != nil {
				sim.Log(fmt.Sprintf("%v grist: %s/%s: the checkpoint moved to %d after an append that failed (%v): another poller's", sim.Elapsed(), pl.p.name, pl.tenant, now, err0))
			}
			return 0, false
		}
	}
}

// step is a processor's handling of trigger, committing events, as its
// batch's row holds it (the relay adds obs.head and order.pos); extra
// are the step's other fields (an effect's checkpoint records ok). Its
// order among the processor's steps is 2·trigger+slot: 1, after the
// trigger's at-least-once effect steps (2·trigger); 0, before its
// at-most-once effect step (2·trigger+1). A checkpoint and an effect may
// share a head, and are recorded on two threads (the relay's and the
// poller's).
func (pl *poller) step(trigger Position, events []Event, slot int64, skip bool, extra ...any) rowStep {
	kv := append(append([]any{"trigger", int64(trigger)}, extra...), pl.app.batchObs(events, "processor", pl.p.name)...)
	kv = append(kv, "obs.checkpoint", int64(trigger), "order.p."+pl.p.quint, 2*int64(trigger)+slot)
	action := pl.p.quint
	if skip {
		action += "Skip"
	}
	return newRowStep(action, len(events), kv...)
}

// effect runs an Effect for trigger e (checkpoint cp before it, head
// read at head), then commits the checkpoint with its outcome event; it
// returns the head after, false if the checkpoint did not commit.
func (pl *poller) effect(ctx context.Context, cp, head Position, e Stored, res Result) (Position, bool) {
	if res.delivery == AtMostOnce {
		return pl.atMostOnce(ctx, cp, e, res)
	}
	key := pl.key(e.Pos)
	kind := EffectFaults(pl.p.name)
	sim, proc := qenv.From(ctx), qenv.ProcessOf(ctx)
	var outcome []Event
	ok := false
	for try := 1; ; try++ {
		if sim != nil && proc != nil && sim.FaultOf(kind.Only("crashBefore")) != "" {
			proc.Crash()
		}
		err := res.effect(pl.effectContext(ctx, e.Pos, head, key, 0))
		record(ctx, pl.s, pl.tenant, pl.p.quint+"Effect", nil, "trigger", int64(e.Pos), "key", key, "ok", err == nil,
			"order.pos", readOrder(head), "order.p."+pl.p.quint, 2*int64(e.Pos))
		if sim != nil && proc != nil && sim.FaultOf(kind) == "crashAfter" {
			proc.Crash()
		}
		if err == nil {
			ok = true
			if res.onSuccess != nil {
				outcome = []Event{*res.onSuccess}
			}
			break
		}
		if try >= pl.p.opts.effectTries {
			outcome = []Event{failedEvent(pl.p.name, e.Pos)}
			break
		}
		backoff(ctx, try)
	}
	for _, ev := range outcome {
		if pl.app.events[ev.Type] == nil || checkEvents("processor "+pl.p.name, outcome) != nil {
			outcome, ok = []Event{failedEvent(pl.p.name, e.Pos)}, false // an unregistered or unstorable outcome event: the code's fault
		}
	}
	// the checkpoint's step records which outcome it commits: under at
	// least once, a run that had an ok effect and then failed every try
	// (a crash between) commits grist.failed, and the model admits either
	return pl.commit(ctx, cp, []pending{{trigger: e.Pos, events: outcome}}, "ok", ok)
}

// atMostOnce runs an at-most-once Effect for trigger e (checkpoint cp
// before it): the checkpoint first (its step records ok false: no
// outcome is known when it commits), then f once, its effect step, then
// the outcome event, if any, in a commit of its own under a marker of
// the effect key, so a retry of an outcome whose commit's answer was lost
// appends it once. It returns the head after, false if the checkpoint did
// not commit (read again: a commit of unknown outcome counts as another
// poller's, and its effect is not run).
func (pl *poller) atMostOnce(ctx context.Context, cp Position, e Stored, res Result) (Position, bool) {
	head, ok := pl.commitAt(ctx, cp, []pending{{trigger: e.Pos}}, 0, "ok", false)
	if !ok {
		return 0, false
	}
	key := pl.key(e.Pos)
	kind := EffectFaults(pl.p.name)
	sim, proc := qenv.From(ctx), qenv.ProcessOf(ctx)
	if sim != nil && proc != nil && sim.FaultOf(kind.Only("crashBefore")) != "" {
		proc.Crash() // the checkpoint moved, the effect never runs: lost, by right
	}
	err := res.effect(pl.effectContext(ctx, e.Pos, head, key, 1))
	record(ctx, pl.s, pl.tenant, pl.p.quint+"Effect", nil, "trigger", int64(e.Pos), "key", key, "ok", err == nil,
		"order.pos", readOrder(head), "order.p."+pl.p.quint, 2*int64(e.Pos)+1)
	if sim != nil && proc != nil && sim.FaultOf(kind) == "crashAfter" {
		proc.Crash() // the effect ran once, its outcome is lost
	}
	var outcome []Event
	var na *notApplied
	switch {
	case errors.As(err, &na):
		outcome = []Event{failedEvent(pl.p.name, e.Pos)} // known: it did not happen
	case err != nil:
		outcome = []Event{unknownEvent(pl.p.name, e.Pos, modelText(err.Error()))} // it may have happened
	case res.onSuccess != nil:
		outcome = []Event{*res.onSuccess}
	}
	if len(outcome) > 0 && (pl.app.events[outcome[0].Type] == nil || checkEvents("processor "+pl.p.name, outcome) != nil) {
		// the code's fault: the effect succeeded (or failed known), but its
		// outcome event cannot be stored
		outcome, err = []Event{failedEvent(pl.p.name, e.Pos)}, NotApplied(errors.New("an unregistered or unstorable outcome event"))
	}
	if len(outcome) == 0 {
		return head, true
	}
	known, text := err == nil || outcome[0].Type == failedType, ""
	if !known {
		text = modelText(err.Error())
	}
	step := newRowStep(pl.p.quint+"Outcome", len(outcome), append([]any{"trigger", int64(e.Pos), "key", key, "ok", err == nil, "known", known, "error", text},
		pl.app.batchObs(outcome, "processor", pl.p.name)...)...)
	b := store.Batch{Events: outcome, Marker: &store.Marker{ID: reservedID + key, Hash: "outcome"},
		Time: qenv.Now(ctx).UnixMilli(), Cause: "processor:" + pl.p.name, Step: encodeRow([]rowStep{step})}
	// the outcome outlives the node's stop: f ran, and an outcome not
	// appended is a payment recorded nowhere, so a context ended (a node
	// stopping) still gets defaultTransientTries tries; a crash (a
	// simulation's) stops the task where it is
	octx := context.WithoutCancel(ctx)
	for try := 1; ctx.Err() == nil || try <= defaultTransientTries; try++ {
		h, err := pl.s.Append(octx, pl.tenant, b)
		var cf *store.ConditionFailed
		switch {
		case err == nil:
			pl.tries = 0
			return h, true
		case errors.As(err, &cf) && cf.Marker != "":
			return head, true // committed by an earlier try whose answer was lost
		case !store.Transient(err):
			pl.pause(octx)
			return head, true // a permanent error: the outcome is lost, as after a crash
		}
		pl.pause(octx)
	}
	return head, true
}

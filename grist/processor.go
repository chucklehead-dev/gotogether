package grist

import (
	"context"
	"fmt"

	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// A Delivery is an effect's declared guarantee.
type Delivery int

const (
	// AtLeastOnce runs the effect, then commits the checkpoint (and the
	// outcome event): a crash between them runs it again, under the same
	// EffectKey.
	AtLeastOnce Delivery = iota + 1
)

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

// Effect runs f outside the log, with the delivery guarantee d (slice 1
// has AtLeastOnce; any other is the handler's error). f's ctx carries the
// EffectKey. An error is retried (EffectTries tries, with backoff), then
// checkpointed with a grist.failed event.
func Effect(f func(ctx context.Context) error, d Delivery, opts ...EffectOption) Result {
	r := Result{kind: effectResult, effect: f, delivery: d}
	for _, o := range opts {
		o(&r)
	}
	return r
}

// check is the handler's error in r, if r is no result grist can carry
// out: the zero Result, an Effect without a function or of a delivery
// slice 1 does not have.
func (r Result) check(processor string) error {
	switch {
	case r.kind == 0:
		return fmt.Errorf("grist: processor %s returned the zero Result: return Emit, Skip, Effect or Fail", processor)
	case r.kind == effectResult && r.effect == nil:
		return fmt.Errorf("grist: processor %s returned an Effect without a function", processor)
	case r.kind == effectResult && r.delivery != AtLeastOnce:
		return fmt.Errorf("grist: processor %s returned an Effect of delivery %d: slice 1 has AtLeastOnce only", processor, r.delivery)
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
	app.processors = append(app.processors, p)
	return &Processor{info: p}
}

// Name is the processor's name.
func (p *Processor) Name() string { return p.info.name }

// EffectFaults is processor's effect crash points as a fault kind:
// "grist.<processor>.effect", crashBefore (before f runs) and crashAfter
// (after f returned and its step was recorded, before the checkpoint).
// The crash that duplicates an at-least-once effect is one named option,
// which a qenv.Script asks for and Exhaust reaches with one fault.
func EffectFaults(processor string) qenv.FaultKind {
	return qenv.DeclareFaults("grist."+processor+".effect", "crashBefore", "crashAfter")
}

// Start starts the App's processors on s, one poller task per processor
// and tenant (qenv.Go: a task of the caller's process in a simulation, a
// goroutine outside one), each a daemon waiting on the store's appends.
// Tenants are the store's at the start (slice 1: one node, tenants
// created before it starts). It returns once they are started; they stop
// with ctx or their process.
func (a *App) Start(ctx context.Context, s Store) error {
	if err := a.Verify(); err != nil {
		return err
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
	for _, p := range a.processors {
		for _, t := range tenants {
			p, t := p, t
			qenv.Go(ctx, "grist."+p.name+"."+t, func(ctx context.Context) {
				qenv.Daemon(ctx)
				(&poller{app: a, s: s, p: p, tenant: t}).run(qobs.Thread(ctx))
			})
		}
	}
	return nil
}

// A poller runs one processor for one tenant.
type poller struct {
	app    *App
	s      Store
	p      *processorInfo
	tenant string
	tries  int // transient errors in a row
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
			pl.s.Wait(ctx, pl.tenant, head)
			continue
		}
		pl.handle(ctx, cp, head, evs)
	}
}

// pending is one trigger's part of an Emit batch.
type pending struct {
	trigger Position
	events  []Event
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
		switch res.kind {
		case emitResult:
			group = append(group, pending{trigger: e.Pos, events: res.events})
			continue
		case failResult:
			group = append(group, pending{trigger: e.Pos, events: []Event{failedEvent(pl.p.name, e.Pos)}})
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
		}
		for _, ev := range res.events {
			if pl.app.events[ev.Type] == nil {
				res = Fail(fmt.Errorf("grist: processor %s emits %q, which is not registered on %s: register it (NewEvent)", pl.p.name, ev.Type, pl.app.Name))
			}
		}
		if res.kind != failResult || try >= pl.p.opts.effectTries {
			return res, true
		}
		backoff(ctx, try)
	}
}

func (pl *poller) key(t Position) string { return fmt.Sprintf("%s/%s/%d", pl.p.name, pl.tenant, t) }

// commit appends a group's events with the checkpoint moved from cp to
// its last trigger, recording one step per trigger (with kv, an effect's
// outcome); false if it did not commit (read again). A transient error
// is retried here: the handlers are functions of the prefix through
// their triggers, which never changes.
func (pl *poller) commit(ctx context.Context, cp Position, group []pending, kv ...any) (Position, bool) {
	var events []Event
	triggers := []int64{}
	for _, g := range group {
		events = append(events, g.events...)
		triggers = append(triggers, int64(g.trigger))
	}
	// the step row of a batch of triggers names them all: one step each
	b := store.Batch{Events: events, Checkpoint: &store.CheckpointMove{Processor: pl.p.name, From: cp, To: group[len(group)-1].trigger},
		Time: qenv.Now(ctx).UnixMilli(), Cause: "processor:" + pl.p.name,
		Step: stepRow(pl.p.quint, append([]any{"triggers", triggers}, kv...))}
	b.OnCommit = func(head Position) {
		p := head - Position(len(events)) // the head before the batch
		for _, g := range group {
			p += Position(len(g.events))
			pl.step(ctx, g.trigger, g.events, p, kv...)
		}
	}
	for {
		head, err := pl.s.Append(ctx, pl.tenant, b)
		if err == nil {
			pl.tries = 0
			return head, true
		}
		if !store.Transient(err) {
			return 0, false // the checkpoint moved: another poller's
		}
		pl.pause(ctx)
		// a commit of unknown outcome may have moved the checkpoint: the
		// next Append's compare-and-set tells
		if now, err := pl.s.Checkpoint(ctx, pl.tenant, pl.p.name); err == nil && now != cp {
			return 0, false
		}
	}
}

// step records a processor's handling of trigger, committed with events
// ending at head (or, with none, reading through head); extra are the
// step's other fields (an effect's checkpoint records ok).
func (pl *poller) step(ctx context.Context, trigger Position, events []Event, head Position, extra ...any) {
	kv := append(append([]any{"trigger", int64(trigger)}, extra...), pl.app.batchObs(events, head, "processor", pl.p.name)...)
	kv = append(kv, "obs.checkpoint", int64(trigger), "order.pos", commitOrder(len(events), head), "order.p."+pl.p.quint, int64(trigger))
	record(ctx, pl.s, pl.tenant, pl.p.quint, nil, kv...)
}

// effect runs an Effect for trigger e (checkpoint cp before it, head
// read at head), then commits the checkpoint with its outcome event; it
// returns the head after, false if the checkpoint did not commit.
func (pl *poller) effect(ctx context.Context, cp, head Position, e Stored, res Result) (Position, bool) {
	key := pl.key(e.Pos)
	kind := EffectFaults(pl.p.name)
	sim, proc := qenv.From(ctx), qenv.ProcessOf(ctx)
	var outcome []Event
	ok := false
	for try := 1; ; try++ {
		if sim != nil && proc != nil && sim.FaultOf(kind.Only("crashBefore")) != "" {
			proc.Crash()
		}
		err := res.effect(context.WithValue(ctx, effectKey{}, key))
		record(ctx, pl.s, pl.tenant, pl.p.quint+"Effect", nil, "trigger", int64(e.Pos), "key", key, "ok", err == nil,
			"order.pos", readOrder(head), "order.p."+pl.p.quint, int64(e.Pos))
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
		if pl.app.events[ev.Type] == nil {
			outcome, ok = []Event{failedEvent(pl.p.name, e.Pos)}, false // an unregistered outcome event: the code's fault
		}
	}
	// the checkpoint's step records which outcome it commits: under at
	// least once, a run that had an ok effect and then failed every try
	// (a crash between) commits grist.failed, and the model admits either
	return pl.commit(ctx, cp, []pending{{trigger: e.Pos, events: outcome}}, "ok", ok)
}

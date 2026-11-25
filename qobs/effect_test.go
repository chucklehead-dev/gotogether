package qobs

import (
	"context"
	"reflect"
	"testing"
)

// An effect under a call's scope is the call's (its Call), whatever
// follows; one outside any scope is its goroutine's next step's (Made),
// and an invocation takes none (made before the call began, no step's).
// Effect records take seqs and no place in Count; a recorder made with
// ObserveEffects marks its steps observed.
func TestEffectAttribution(t *testing.T) {
	t.Parallel()
	sink := &MemorySink{}
	r := NewRecorder(sink, WithID("r"), ObserveEffects())
	ctx := WithRecorder(context.Background(), r)

	Effect(ctx, "mail.send", "to", "a") // seq 1: before the call, no step's
	c := Invoke(ctx, "pay", "order", "o1")
	sctx := c.Scope(ctx)
	Effect(sctx, "rpc.p.request", "Amount", 10) // seq 3: the call's
	Effect(ctx, "queue.q.publish", "value", "x")
	c.Done(nil)                                 // seq 5: Made [4]
	Effect(sctx, "rpc.p.request", "Amount", 11) // seq 6: the call's, late
	r.Record(ctx, "mark", nil)                  // seq 7: Made none

	steps := sink.Steps()
	if len(steps) != 7 {
		t.Fatalf("%d records: %v", len(steps), steps)
	}
	if e := steps[0]; e.Effect != "mail.send" || e.Call != 0 || e.Seq != 1 || e.Args["to"] != "a" {
		t.Errorf("the first effect %+v", e)
	}
	if e := steps[2]; e.Effect != "rpc.p.request" || e.Call != 2 || e.Args["Amount"] != int64(10) {
		t.Errorf("the scoped effect %+v: want call 2", e)
	}
	if s := steps[4]; s.Action != "pay" || s.Call != 2 || !reflect.DeepEqual(s.Made, []uint64{4}) || !s.EffectsObserved {
		t.Errorf("the completion %+v: want Made [4], observed", s)
	}
	if e := steps[5]; e.Call != 2 || e.Seq != 6 {
		t.Errorf("the late effect %+v", e)
	}
	if s := steps[6]; s.Action != "mark" || len(s.Made) != 0 || !s.EffectsObserved {
		t.Errorf("the last step %+v", s)
	}
	if steps[1].Made != nil {
		t.Errorf("the invocation took effects: %v", steps[1].Made)
	}
	if n := r.Count(); n != 3 {
		t.Errorf("Count %d, want 3: the invocation and two steps, no effect", n)
	}
	// an effect's keys are all its fields: a request's "actor" is no override
	r.Effect(ctx, "rpc.p.request", "actor", "x")
	if e := sink.Steps()[7]; e.Actor != "default" || e.Args["actor"] != "x" {
		t.Errorf("an effect's actor field: %+v", e)
	}
}

// Without ObserveEffects, outside a simulation a step does not say its
// effects were observed: none known is not none made.
func TestEffectsNotObservedOutsideASimulation(t *testing.T) {
	t.Parallel()
	sink := &MemorySink{}
	r := NewRecorder(sink)
	r.Record(context.Background(), "mark", nil)
	if s := sink.Steps()[0]; s.EffectsObserved {
		t.Errorf("%+v: observed without a simulation or ObserveEffects", s)
	}
}

// EffectFunc records where Effect does, building the record only then:
// under a scope on the call's recorder (over WithRecorder(nil) too), else
// on the context's recorder, and with neither builds nothing.
func TestEffectFunc(t *testing.T) {
	t.Parallel()
	sink := &MemorySink{}
	r := NewRecorder(sink, WithID("r"))
	built := 0
	build := func() (string, []any) { built++; return "mail.send", []any{"to", "a"} }

	none := WithRecorder(context.Background(), nil) // shadows any global one
	EffectFunc(none, build)
	if built != 0 || len(sink.Steps()) != 0 {
		t.Fatalf("with no recorder: built %d, recorded %v", built, sink.Steps())
	}
	c := r.Invoke(none, "pay")
	EffectFunc(c.Scope(none), build)
	EffectFunc(WithRecorder(context.Background(), r), build)
	steps := sink.Steps()
	if built != 2 || len(steps) != 3 {
		t.Fatalf("built %d, recorded %v", built, steps)
	}
	if e := steps[1]; e.Record.Effect != "mail.send" || e.Call != steps[0].Seq || e.Args["to"] != "a" {
		t.Errorf("the scoped effect %+v", e)
	}
	if e := steps[2]; e.Record.Effect != "mail.send" || e.Call != 0 {
		t.Errorf("the context's effect %+v", e)
	}
}

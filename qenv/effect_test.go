package qenv_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

type capture struct {
	Key    string
	Amount int
	secret string // unexported: no field of the effect's
}

// The doubles record what the code sends, at the send: a Service call's
// request by its exported fields (a lost one too: the network's fate is
// not the code's), a Queue's publish by its body. A step the task
// records next is observed, and names them (rule 2); a call's scope
// takes them as its own (rule 1).
func TestDoublesRecordEffects(t *testing.T) {
	t.Parallel()
	sink := &qobs.MemorySink{}
	rec := qobs.NewRecorder(sink)
	s := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(t.Context(), rec)))
	svc := qenv.NewService(s, "provider", nil, func(context.Context, capture) (string, error) { return "ok", nil })
	q := qenv.NewQueue(s, "mail", nil)
	s.Spawn("shop", func(ctx context.Context) {
		if _, err := svc.Call(ctx, capture{Key: "k1", Amount: 10, secret: "s"}, time.Second); err != nil {
			t.Errorf("call: %v", err)
		}
		q.Publish(ctx, map[string]any{"order": "o1"})
		qobs.Record(ctx, "mark", nil)
		c := qobs.Invoke(ctx, "pay")
		_, _ = svc.Call(c.Scope(ctx), capture{Key: "k2", Amount: 20}, time.Second)
		c.Done(nil)
	})
	if err := s.Run(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	s.Close()
	trs, _ := qtrace.Reconstruct(sink.Steps())
	if len(trs) != 1 || len(trs[0].Steps) != 2 {
		t.Fatalf("traces %v", trs)
	}
	mark, pay := trs[0].Steps[0], trs[0].Steps[1]
	if !mark.EffectsObserved || len(mark.Effects) != 2 {
		t.Fatalf("mark: observed %v, effects %v", mark.EffectsObserved, mark.Effects)
	}
	call, pub := mark.Effects[0], mark.Effects[1]
	if call.Kind != "rpc.provider.request" || call.By != "task" || call.Fields["Key"] != "k1" || call.Fields["Amount"] != int64(10) || len(call.Fields) != 2 {
		t.Errorf("the call's effect %v", call)
	}
	if pub.Kind != "queue.mail.publish" || pub.Fields["order"] != "o1" {
		t.Errorf("the publish's effect %v", pub)
	}
	if len(pay.Effects) != 1 || pay.Effects[0].By != "call" || pay.Effects[0].Fields["Key"] != "k2" {
		t.Errorf("pay's effects %v", pay.Effects)
	}
}

// EffectFields: a struct's exported fields by name, a map's entries in
// order, anything else its value.
func TestEffectFields(t *testing.T) {
	t.Parallel()
	got := qenv.EffectFields(&capture{Key: "k", Amount: 1})
	if len(got) != 4 || got[0] != "Key" || got[1] != "k" || got[2] != "Amount" || got[3] != 1 {
		t.Errorf("struct: %v", got)
	}
	if got := qenv.EffectFields(map[string]int{"b": 2, "a": 1}); len(got) != 4 || got[0] != "a" || got[2] != "b" {
		t.Errorf("map: %v", got)
	}
	if got := qenv.EffectFields("x"); len(got) != 2 || got[0] != "value" || got[1] != "x" {
		t.Errorf("scalar: %v", got)
	}
	if got := qenv.EffectFields(nil); got != nil {
		t.Errorf("nil: %v", got)
	}
}

// A recorder only a task's context carries (one qenv.Go was given, the
// simulation's own has none) is found through the task's goroutine, by a
// step and a double's send with context.Background(): the simulation
// counts it among the runs a goroutine's lookup may find a recorder in,
// which a lookup skips when there are none.
//
// It guards that fast path, so it runs alone (no t.Parallel) and from no
// count: any other run in the process (a parallel test's simulation, an
// explicit start) would keep the lookup on, and the test would pass
// with spawn's count removed.
func TestRecorderOfATaskFoundThroughItsGoroutine(t *testing.T) {
	noRecorderRuns(t)
	sink := &qobs.MemorySink{}
	rec := qobs.NewRecorder(sink)
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	svc := qenv.NewService(s, "provider", nil, func(context.Context, capture) (string, error) { return "ok", nil })
	s.Spawn("driver", func(ctx context.Context) {
		qenv.Go(qobs.WithRecorder(ctx, rec), "shop", func(context.Context) {
			bg := context.Background()
			if _, err := svc.Call(bg, capture{Key: "k1", Amount: 10}, time.Second); err != nil {
				t.Errorf("call: %v", err)
			}
			qobs.Record(bg, "mark", nil)
		})
	})
	if err := s.Run(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	trs, _ := qtrace.Reconstruct(sink.Steps())
	if len(trs) != 1 || len(trs[0].Steps) != 1 {
		t.Fatalf("traces %v", trs)
	}
	if mark := trs[0].Steps[0]; mark.Action != "mark" || len(mark.Effects) != 1 || mark.Effects[0].Kind != "rpc.provider.request" {
		t.Errorf("mark %+v, its effects %v", mark, mark.Effects)
	}
}

// noRecorderRuns waits for the process to count no run a goroutine's
// lookup may find a recorder in (hooks.MayResolveRecorder), as a test of
// the lookup's fast path needs: only its own counts may turn the lookup
// on. Such a test is not parallel, so what is left is an earlier test's
// native goroutines still unwinding; a count that stays is a leak (a
// simulation never closed, a start never ended), which would also make
// every lookup in the process pay.
func noRecorderRuns(t *testing.T) {
	t.Helper()
	settled := time.Now().Add(5 * time.Second)                     //qenv:ok a deadline outside any simulation
	for hooks.MayResolveRecorder() && time.Now().Before(settled) { //qenv:ok against that deadline
		time.Sleep(time.Millisecond) //qenv:ok waiting, outside any simulation, for another test's runs to end
	}
	if hooks.MayResolveRecorder() {
		t.Fatal("a run a goroutine's lookup may find a recorder in is still counted 5s into a test that runs alone: " +
			"an earlier test leaked one (a Sim without Close, a qrun.Start not ended); close it")
	}
}

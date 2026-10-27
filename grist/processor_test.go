package grist_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// effectTries is grist's default EffectTries.
const effectTries = 3

type Added struct {
	By int `json:"by"`
}

type Summed struct {
	Sum   int `json:"sum"`
	Count int `json:"count"`
}

type Noted struct {
	Of int64 `json:"of"`
}

// procApp is an app whose "sum" processor emits, on each added, the sum
// and count as of it, and whose "note" processor runs effect, at least
// once, noting success.
func procApp(effect func(ctx context.Context) error) (*grist.App, *grist.EventType[Added], *grist.EventType[Summed]) {
	a := grist.New("procs")
	added := grist.NewEvent[Added](a, "added")
	summed := grist.NewEvent[Summed](a, "summed")
	noted := grist.NewEvent[Noted](a, "noted")
	grist.NewProcessor(a, "sum", added, func(ctx context.Context, r *grist.Reads, e Added, m grist.Meta) grist.Result {
		sum, n := 0, 0
		for _, s := range r.Select(grist.Selector{Types: []string{"added"}}) {
			v, _ := added.Decode(s)
			sum, n = sum+v.By, n+1
		}
		return grist.Emit(summed.Of(Summed{Sum: sum, Count: n}))
	})
	if effect != nil {
		grist.NewProcessor(a, "note", added, func(ctx context.Context, r *grist.Reads, e Added, m grist.Meta) grist.Result {
			return grist.Effect(effect, grist.AtLeastOnce, grist.OnSuccess(noted.Of(Noted{Of: int64(m.Pos)})))
		})
	}
	return a, added, summed
}

// simulate appends three addeds to t1, then runs a's processors on a
// node, restarted when it crashes, until quiet, choices made by c; it
// returns the store's log of t1 and the steps.
func simulate(t *testing.T, a *grist.App, added *grist.EventType[Added], c qenv.Chooser) ([]grist.Stored, []qtrace.Step) {
	t.Helper()
	sink := &qobs.MemorySink{}
	s := qenv.New(qenv.WithContext(c, qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))))
	defer s.Close()
	s.Check = true
	st := simstore.New(qenv.NewStore(s, "procs", qenv.Serializable))
	ctx := s.Context()
	st.CreateTenant(ctx, "t1")
	for i := 1; i <= 3; i++ {
		st.Append(ctx, "t1", store.Batch{Events: []grist.Event{added.Of(Added{By: i})}})
	}
	node := s.Process("node", true, func(ctx context.Context) {
		if err := a.Start(ctx, st); err != nil {
			t.Error(err)
		}
	})
	s.OnCrash(func(p *qenv.Process) {
		if p == node {
			p.Restart()
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	log, _, err := st.Read(ctx, "t1", grist.Selector{Types: []string{"added", "summed", "noted", "grist.failed"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return log, sink.Steps()
}

// Emit: each trigger's events and the checkpoint commit together (one
// transaction for the batch of triggers read), the handler reading as of
// its trigger, whatever came after; one step per trigger, on the domain
// clock at 2·(the last position of its own events).
func TestEmitAsOfTrigger(t *testing.T) {
	a, added, summed := procApp(nil)
	log, steps := simulate(t, a, added, qenv.NewSeeded(1))
	var sums []Summed
	for _, e := range log {
		if e.Type == "summed" {
			v, _ := summed.Decode(e)
			sums = append(sums, v)
		}
	}
	if want := []Summed{{1, 1}, {3, 2}, {6, 3}}; len(sums) != 3 || sums[0] != want[0] || sums[1] != want[1] || sums[2] != want[2] {
		t.Errorf("sums %v: want %v (each as of its trigger, not of the head)", sums, want)
	}
	var orders, triggers []int64
	for _, s := range steps {
		if s.Action == "sum" {
			orders, triggers = append(orders, s.Order["pos"]), append(triggers, s.Args["trigger"].(int64))
			if s.Obs["checkpoint"] != s.Args["trigger"] || s.Order["p.sum"] != 2*s.Args["trigger"].(int64)+1 {
				t.Errorf("step %v: its checkpoint is its trigger T, its order.p.sum 2·T+1 (after T's effect steps, at 2·T)", s)
			}
		}
	}
	if len(orders) != 3 || orders[0] != 8 || orders[1] != 10 || orders[2] != 12 || triggers[0] != 1 || triggers[2] != 3 {
		t.Errorf("sum steps at order.pos %v for triggers %v: want 8, 10, 12 for 1, 2, 3 (events at 4, 5, 6)", orders, triggers)
	}
}

// Effect, at least once: an effect that fails is tried EffectTries times,
// then the trigger is checkpointed with a grist.failed event naming it;
// one that succeeds appends its outcome event. Each try is an effect
// step, under the trigger's effect key.
func TestEffectAtLeastOnce(t *testing.T) {
	keys := map[string]int{}
	errDown := errors.New("mailer down")
	a, added, _ := procApp(func(ctx context.Context) error {
		k := grist.EffectKey(ctx)
		keys[k]++
		if k == "note/t1/1" || k == "note/t1/2" && keys[k] == 1 {
			return errDown // the first trigger's always fails, the second's once
		}
		return nil
	})
	log, steps := simulate(t, a, added, qenv.NewSeeded(1))
	if keys["note/t1/1"] != effectTries || keys["note/t1/2"] != 2 || keys["note/t1/3"] != 1 {
		t.Errorf("effect runs by key %v: want %d for the failing trigger, 2 and 1", keys, effectTries)
	}
	var outcomes []string
	for _, e := range log {
		if e.Type == "noted" || e.Type == "grist.failed" {
			outcomes = append(outcomes, e.Type+":"+string(e.Body))
		}
	}
	want := []string{`grist.failed:{"processor":"note","trigger":1}`, `noted:{"of":2}`, `noted:{"of":3}`}
	if len(outcomes) != 3 || outcomes[0] != want[0] || outcomes[1] != want[1] || outcomes[2] != want[2] {
		t.Errorf("outcome events %v, want %v", outcomes, want)
	}
	effects := 0
	for _, s := range steps {
		trigger, _ := s.Args["trigger"].(int64)
		switch {
		case s.Action == "noteEffect" && s.Order["p.note"] != 2*trigger:
			t.Errorf("effect step %v: want order.p.note 2·trigger, before the trigger's checkpoint (2·trigger+1)", s)
		case s.Action == "note" && s.Order["p.note"] != 2*trigger+1:
			t.Errorf("checkpoint step %v: want order.p.note 2·trigger+1", s)
		}
		if s.Action == "noteEffect" {
			effects++
		}
	}
	if effects != effectTries+3 {
		t.Errorf("%d effect steps, want one per try (%d)", effects, effectTries+3)
	}
}

// The crash after an effect and before its checkpoint runs the effect
// again on restart, under the same key: what at least once means, one
// named fault a Script asks for.
func TestEffectCrashAfter(t *testing.T) {
	runs := map[string]int{}
	a, added, _ := procApp(func(ctx context.Context) error { runs[grist.EffectKey(ctx)]++; return nil })
	c := &qenv.Script{Base: qenv.NewSeeded(1), Faults: []qenv.Scripted{{Kind: "grist.note.effect", Name: "crashAfter"}}}
	log, _ := simulate(t, a, added, c)
	if len(c.Remaining()) != 0 {
		t.Fatalf("the crash never happened: %v", c.Remaining())
	}
	if runs["note/t1/1"] != 2 || runs["note/t1/2"] != 1 {
		t.Errorf("effect runs %v: want the first trigger's twice (the crash after it), the others once", runs)
	}
	noted := 0
	for _, e := range log {
		if e.Type == "noted" {
			noted++
		}
	}
	if noted != 3 {
		t.Errorf("%d noted events, want one per trigger (the checkpoint commits once)", noted)
	}
}

// A handler's failure is tried EffectTries times, then its trigger is
// checkpointed with a grist.failed event: a poison event stops nothing.
func TestHandlerFailure(t *testing.T) {
	a, added, _ := procApp(nil)
	calls := map[grist.Position]int{}
	grist.NewProcessor(a, "flop", added, func(ctx context.Context, r *grist.Reads, e Added, m grist.Meta) grist.Result {
		calls[m.Pos]++
		if m.Pos == 2 {
			return grist.Fail(errors.New("poison"))
		}
		return grist.Skip()
	})
	log, _ := simulate(t, a, added, qenv.NewSeeded(1))
	if calls[2] != effectTries || calls[1] != 1 || calls[3] != 1 {
		t.Errorf("handler calls by trigger %v: want %d for the poison one, one for the others", calls, effectTries)
	}
	failed := 0
	for _, e := range log {
		if e.Type == "grist.failed" && string(e.Body) == `{"processor":"flop","trigger":2}` {
			failed++
		}
	}
	if failed != 1 {
		t.Errorf("%d grist.failed events for flop's trigger 2, want 1", failed)
	}
}

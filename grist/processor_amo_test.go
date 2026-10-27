package grist_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// amoApp is procApp's events with a "note" processor running effect at
// most once, noting success.
func amoApp(effect func(ctx context.Context, m grist.Meta) error) (*grist.App, *grist.EventType[Added]) {
	a := grist.New("procs")
	added := grist.NewEvent[Added](a, "added")
	grist.NewEvent[Summed](a, "summed")
	noted := grist.NewEvent[Noted](a, "noted")
	grist.NewProcessor(a, "note", added, func(ctx context.Context, r *grist.Reads, e Added, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error { return effect(ctx, m) }, grist.AtMostOnce, grist.OnSuccess(noted.Of(Noted{Of: int64(m.Pos)})))
	})
	return a, added
}

func quiet(seed uint64, faults ...qenv.Scripted) qenv.Chooser {
	base := qenv.NewSeeded(seed)
	base.Rates = qenv.NoFaults
	if len(faults) == 0 {
		return base
	}
	return &qenv.Script{Base: base, Faults: faults}
}

// outcomes are log's noted and grist.failed events, as type:body.
func outcomes(log []grist.Stored) []string {
	var out []string
	for _, e := range log {
		if e.Type == "noted" || e.Type == "grist.failed" || e.Type == "grist.unknown" {
			out = append(out, e.Type+":"+string(e.Body))
		}
	}
	return out
}

// At most once: the checkpoint commits before the effect runs (the
// effect sees its trigger checkpointed), the effect runs once, an error
// is not tried again (it may have taken effect); a success's outcome is
// the success event, a NotApplied error's grist.failed (known: it did
// not happen), any other error's grist.unknown with its text (it may
// have: grist.failed once said "not paid" of a payment whose answer was
// lost). The steps are ordered
// checkpoint (order.p 2·T, ok false), effect (2·T+1), outcome
// (noteOutcome, after the effect on the domain clock).
func TestEffectAtMostOnce(t *testing.T) {
	runs := map[string]int{}
	var st grist.Store
	a, added := amoApp(func(ctx context.Context, m grist.Meta) error {
		runs[grist.EffectKey(ctx)]++
		if cp, err := st.Checkpoint(ctx, "t1", "note"); err != nil || cp != m.Pos {
			t.Errorf("trigger %d's effect ran with the checkpoint at %d (%v): want it committed first", m.Pos, cp, err)
		}
		switch m.Pos {
		case 2:
			return grist.NotApplied(errors.New("declined"))
		case 3:
			return errors.New("bank down")
		}
		return nil
	})
	log, steps := simulateOn(t, a, added, quiet(1), func(s grist.Store) grist.Store { st = s; return s })
	if runs["note/t1/1"] != 1 || runs["note/t1/2"] != 1 || runs["note/t1/3"] != 1 {
		t.Errorf("effect runs by key %v: want each once, the failing one too", runs)
	}
	want := []string{`noted:{"of":1}`, `grist.failed:{"processor":"note","trigger":2}`, `grist.unknown:{"processor":"note","error":"bank down","trigger":3}`}
	if got := outcomes(log); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("outcome events %v, want %v", got, want)
	}
	byTrigger := map[int64][]qtrace.Step{}
	for _, s := range steps {
		if strings.HasPrefix(s.Action, "note") {
			tr, _ := s.Args["trigger"].(int64)
			byTrigger[tr] = append(byTrigger[tr], s)
		}
	}
	for tr := int64(1); tr <= 3; tr++ {
		var acts []string
		for _, s := range byTrigger[tr] {
			acts = append(acts, s.Action)
			switch s.Action {
			case "note":
				if s.Order["p.note"] != 2*tr || s.Args["ok"] != false {
					t.Errorf("checkpoint step %v: want order.p.note 2·T and ok false", s)
				}
			case "noteEffect":
				if s.Order["p.note"] != 2*tr+1 {
					t.Errorf("effect step %v: want order.p.note 2·T+1", s)
				}
			case "noteOutcome":
				wantErr := map[int64]string{3: "bank down"}[tr]
				if s.Args["ok"] != (tr == 1) || s.Args["known"] != (tr != 3) || s.Args["error"] != wantErr {
					t.Errorf("outcome step %v: want ok %v, known %v, error %q", s, tr == 1, tr != 3, wantErr)
				}
			}
		}
		if len(acts) != 3 {
			t.Errorf("trigger %d's steps %v: want a checkpoint, an effect and an outcome", tr, acts)
		}
	}
}

// A crash between the checkpoint and the effect loses the effect (no
// restart runs it), one after the effect its outcome event; neither runs
// anything twice.
func TestEffectAtMostOnceCrashes(t *testing.T) {
	for _, crash := range []string{"crashBefore", "crashAfter"} {
		runs := map[string]int{}
		a, added := amoApp(func(ctx context.Context, m grist.Meta) error { runs[grist.EffectKey(ctx)]++; return nil })
		c := quiet(1, qenv.Scripted{Kind: "grist.note.effect", Name: crash})
		log, _ := simulate(t, a, added, c)
		if r := c.(*qenv.Script).Remaining(); len(r) != 0 {
			t.Fatalf("%s: the crash never happened: %v", crash, r)
		}
		wantRuns := 0
		if crash == "crashAfter" {
			wantRuns = 1
		}
		if runs["note/t1/1"] != wantRuns || runs["note/t1/2"] != 1 || runs["note/t1/3"] != 1 {
			t.Errorf("%s: effect runs %v: want the first trigger's %d times, the others once", crash, runs, wantRuns)
		}
		want := `noted:{"of":2} noted:{"of":3}`
		if got := strings.Join(outcomes(log), " "); got != want {
			t.Errorf("%s: outcomes %s, want %s (the first trigger's lost)", crash, got, want)
		}
	}
}

// lostOutcome commits the first outcome append it sees and says its
// outcome is unknown: the retry finds the outcome's marker, so the
// outcome is appended once.
type lostOutcome struct {
	*simstore.Store
	lost bool
}

func (s *lostOutcome) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	p, err := s.Store.Append(ctx, t, b)
	if err == nil && b.Marker != nil && strings.HasPrefix(b.Marker.ID, "grist:") && !s.lost {
		s.lost = true
		return 0, errors.New("connection lost: the commit's outcome is unknown")
	}
	return p, err
}

func TestEffectAtMostOnceOutcomeOnce(t *testing.T) {
	a, added := amoApp(func(ctx context.Context, m grist.Meta) error { return nil })
	var w *lostOutcome
	log, _ := simulateOn(t, a, added, quiet(1), func(s grist.Store) grist.Store { w = &lostOutcome{Store: s.(*simstore.Store)}; return w })
	if !w.lost {
		t.Fatal("no outcome's answer was lost")
	}
	want := `noted:{"of":1} noted:{"of":2} noted:{"of":3}`
	if got := strings.Join(outcomes(log), " "); got != want {
		t.Errorf("outcomes %s, want %s: each once, the lost answer's retry answered by its marker", got, want)
	}
}

// raced commits the first checkpoint move of processor "note" as another
// poller would, then answers this poller's append with a transient error
// before its commit: the checkpoint moved, but not by this poller.
type raced struct {
	*simstore.Store
	done bool
}

func (s *raced) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	if b.Checkpoint != nil && b.Checkpoint.Processor == "note" && !s.done {
		s.done = true
		if _, err := s.Store.Append(ctx, t, b); err != nil {
			return 0, err
		}
		return 0, store.NotCommitted(errors.New("lost the race for the head lock"))
	}
	return s.Store.Append(ctx, t, b)
}

// A checkpoint commit whose answer was an error, after which the
// checkpoint is found moved, counts as another poller's: its effect is
// not run (the branch two pollers need: a poller that took the moved
// checkpoint for its own ran the other poller's effect again,
// examples/counters' TestPayoutTwoNodes). The other triggers run once.
func TestEffectAtMostOnceLostCheckpoint(t *testing.T) {
	runs := map[string]int{}
	a, added := amoApp(func(ctx context.Context, m grist.Meta) error { runs[grist.EffectKey(ctx)]++; return nil })
	var w *raced
	_, steps := simulateOn(t, a, added, quiet(1), func(s grist.Store) grist.Store { w = &raced{Store: s.(*simstore.Store)}; return w })
	if !w.done {
		t.Fatal("no checkpoint append was raced")
	}
	if runs["note/t1/1"] != 0 || runs["note/t1/2"] != 1 || runs["note/t1/3"] != 1 {
		t.Errorf("effect runs %v: want the raced trigger's skipped (another poller's), the others once", runs)
	}
	for _, s := range steps {
		if s.Action == "noteEffect" && s.Args["trigger"] == int64(1) {
			t.Errorf("an effect step of the raced trigger: %v", s)
		}
	}
}

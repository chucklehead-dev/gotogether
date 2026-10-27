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
		if e.Type == "noted" || e.Type == "grist.failed" {
			out = append(out, e.Type+":"+string(e.Body))
		}
	}
	return out
}

// At most once: the checkpoint commits before the effect runs (the
// effect sees its trigger checkpointed), the effect runs once, an error
// is not tried again (it may have taken effect) and its outcome is
// grist.failed, a success's the success event. The steps are ordered
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
		if m.Pos == 2 {
			return errors.New("bank down")
		}
		return nil
	})
	log, steps := simulateOn(t, a, added, quiet(1), func(s grist.Store) grist.Store { st = s; return s })
	if runs["note/t1/1"] != 1 || runs["note/t1/2"] != 1 || runs["note/t1/3"] != 1 {
		t.Errorf("effect runs by key %v: want each once, the failing one too", runs)
	}
	want := []string{`noted:{"of":1}`, `grist.failed:{"processor":"note","trigger":2}`, `noted:{"of":3}`}
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
				if s.Args["ok"] != (tr != 2) {
					t.Errorf("outcome step %v: want ok %v", s, tr != 2)
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

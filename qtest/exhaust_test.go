package qtest

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// A schedule is reached once: from the nominal run every choice is varied
// in each other option, and from a run with deviations only the choices
// after its last, within the bound and the kinds left on.
func TestChildren(t *testing.T) {
	choices := []qenv.Choice{{Kind: "sched.task", N: 2}, {Kind: "fault.rpc.x", N: 3}, {Kind: "sched.task", N: 3}}
	b := Bound{Delays: 1, Faults: 1}
	none := qenv.Rates{Kinds: map[string]float64{}}
	got := children(nil, choices, b, none)
	want := [][]deviation{{{0, 1}}, {{1, 1}}, {{1, 2}}, {{2, 1}}, {{2, 2}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("from the nominal run: %v, want %v", got, want)
	}
	// a delay spent: only the fault after it
	if got, want := children([]deviation{{0, 1}}, choices, b, none), [][]deviation{{{0, 1}, {1, 1}}, {{0, 1}, {1, 2}}}; !reflect.DeepEqual(got, want) {
		t.Errorf("after a delay: %v, want %v", got, want)
	}
	// a kind left nominal
	if got := children(nil, choices, b, none.Set("rpc", 1)); len(got) != 3 {
		t.Errorf("without rpc: %v, want the schedule choices' 3", got)
	}
}

func TestParseSchedule(t *testing.T) {
	ds := []deviation{{3, 1}, {17, 2}}
	if got, err := parseSchedule(schedule(ds)); err != nil || !reflect.DeepEqual(got, ds) {
		t.Errorf("round trip: %v, %v", got, err)
	}
	for _, bad := range []string{"3", "3:0", "17:1,3:1", "a:1"} {
		if _, err := parseSchedule(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// race withdraws without checking the balance: correct only while the
// deposit runs first, as it does in the nominal schedule (the oldest task
// first).
func race(c qenv.Chooser) error {
	s := qenv.New(c)
	defer s.Close()
	balance := 0
	s.Spawn("depositor", func(ctx context.Context) {
		balance += 60
		qobs.Record(ctx, "deposit", nil, "amount", 60, "obs.balance", balance)
	})
	s.Spawn("withdrawer", func(ctx context.Context) {
		balance -= 50
		qobs.Record(ctx, "withdraw", nil, "amount", 50, "obs.balance", balance)
	})
	return s.Run(time.Second)
}

// Without delays the race never shows, and the search says it saw all
// there was; one delay runs the withdrawal first, and the failing run's
// schedule replays it alone.
func TestExhaust(t *testing.T) {
	t.Setenv("GOTOGETHER_KEEP", t.TempDir()) // its failures and runs alone are on purpose: keep nothing after it
	RequireQuint(t)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: race}
	x := sys.Exhaust(t, Bound{})
	x.Expect(t, Conforms(), Complete())
	if len(x.Runs) != 1 {
		t.Errorf("%d runs with no deviation, want the nominal one", len(x.Runs))
	}
	x = sys.Exhaust(t, Bound{Delays: 1})
	x.Expect(t, Rejects("withdraw"), Some(Conforms()), Complete())
	if e := x.Evidence().String(); !strings.Contains(e, "search    complete: every run within 1 delays and 0 faults") {
		t.Errorf("evidence:\n%s", e)
	}
	bad := x.Failing()[0]
	// the test's third exploration, run alone as a report names it
	t.Setenv("GOTOGETHER_RUN", t.Name()+"#3:schedule="+bad.Schedule)
	again := sys.Exhaust(t, Bound{Delays: 1})
	log := &logTB{TB: t}
	again.Expect(log, All(Rejects("withdraw")), Complete())
	if len(again.Runs) != 1 || again.Runs[0].Schedule != bad.Schedule {
		t.Errorf("GOTOGETHER_RUN schedule=%s ran %d runs", bad.Schedule, len(again.Runs))
	}
	// one run is not a search: Complete is not judged, and the evidence
	// does not claim one
	if !strings.Contains(log.logs, "not judged on one run (about the runs it leaves out): a complete search") {
		t.Errorf("logs: %s", log.logs)
	}
	if sr := again.Evidence().Search; sr == nil || sr.Complete {
		t.Errorf("a run alone's evidence claims a complete search: %+v", sr)
	}
}

// A search MaxRuns cut short is not complete, and Complete says how to
// finish it; Complete on seeded runs is a mistake.
func TestExhaustTruncated(t *testing.T) {
	RequireQuint(t)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: race}
	x := sys.Exhaust(t, Bound{Delays: 1, MaxRuns: 1})
	if e := x.Evidence(); e.Search == nil || e.Search.Complete || e.Search.Left == 0 {
		t.Errorf("search %+v, want truncated", e.Search)
	}
	for _, x := range []*Exploration{x, sys.Explore(t, ExploreConfig{Seeds: 1})} {
		f := &failTB{TB: t}
		done := make(chan struct{})
		go func() { //qenv:ok failTB's FailNow ends the goroutine (runtime.Goexit)
			defer close(done)
			x.Expect(f, Complete())
		}()
		<-done
		if !f.failed || !strings.Contains(strings.Join(f.errs, "\n"), "want a complete search") {
			t.Errorf("Complete: %q", f.errs)
		}
	}
}

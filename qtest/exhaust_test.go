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
	t.Parallel()
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
	t.Parallel()
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
	x.Expect(t, Conforms())
	if len(x.Runs) != 1 {
		t.Errorf("%d runs with no deviation, want the nominal one", len(x.Runs))
	}
	x = sys.Exhaust(t, Bound{Delays: 1})
	x.Expect(t, Rejects("withdraw"), Some(Conforms()))
	if e := x.Evidence().String(); !strings.Contains(e, "search    complete: every run within 1 delays and 0 faults") {
		t.Errorf("evidence:\n%s", e)
	}
	bad := x.Failing()[0]
	// the test's third exploration, run alone as a report names it
	t.Setenv("GOTOGETHER_RUN", t.Name()+"#3:schedule="+bad.schedule)
	again := sys.Exhaust(t, Bound{Delays: 1})
	log := &logTB{TB: t}
	again.Expect(log, All(Rejects("withdraw")), Some(Conforms()))
	if len(again.Runs) != 1 || again.Runs[0].schedule != bad.schedule {
		t.Errorf("GOTOGETHER_RUN schedule=%s ran %d runs", bad.schedule, len(again.Runs))
	}
	// one run is not a search: claims about other runs are not judged,
	// and the evidence claims no complete search
	if !strings.Contains(log.logs, "not judged on one run (about the runs it leaves out): conformance") {
		t.Errorf("logs: %s", log.logs)
	}
	if sr := again.Evidence().Search; sr == nil || sr.Complete {
		t.Errorf("a run alone's evidence claims a complete search: %+v", sr)
	}
}

// A search MaxRuns cuts short fails the test, saying how to finish it,
// unless the bound accepts a sample (Partial); its Evidence says it is
// truncated either way.
func TestExhaustTruncated(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: race}
	f := &failTB{TB: t}
	done := make(chan struct{})
	var x *Exploration
	go func() { //qenv:ok failTB's FailNow ends the goroutine (runtime.Goexit)
		defer close(done)
		x = sys.Exhaust(f, Bound{Delays: 1, MaxRuns: 1})
	}()
	<-done
	if !f.failed || !strings.Contains(strings.Join(f.errs, "\n"), "the search stopped at MaxRuns (1 runs) with at least") ||
		!strings.Contains(strings.Join(f.errs, "\n"), "set Bound.Partial to accept a sample") {
		t.Errorf("a truncated search: %q", f.errs)
	}
	if e := x.Evidence(); e.Search == nil || e.Search.Complete || e.Search.Left == 0 {
		t.Errorf("search %+v, want truncated", e.Search)
	}
	sample := sys.Exhaust(t, Bound{Delays: 1, MaxRuns: 1, Partial: true}) // t: passes
	if e := sample.Evidence(); e.Search.Complete || e.Search.Left == 0 {
		t.Errorf("a partial search's evidence: %+v", e.Search)
	}
}

// A passing claim met by a failing run logs the run as the claim it
// satisfies, in the claims' words, without the paths of files the test's
// end removes: its conformance FAIL once read as the test's.
func TestExpectedFailureLogged(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	log := &logTB{TB: t}
	System{Binding: "../validate/testdata/account.yaml", Run: race}.Exhaust(t, Bound{Delays: 1}).Expect(log, Rejects("withdraw"))
	for _, want := range []string{"as expected, a withdraw step the model cannot take: 1 of 2 runs reject withdraw; the first:",
		"    schedule ", "its files are not kept: GOTOGETHER_KEEP=dir keeps them", "1 conform, 1 reject withdraw"} {
		if !strings.Contains(log.logs, want) {
			t.Errorf("logs lack %q:\n%s", want, log.logs)
		}
	}
	for _, path := range []string{"module: ", "step log: "} {
		if strings.Contains(log.logs, path) {
			t.Errorf("logs name a %s:\n%s", path, log.logs)
		}
	}
}

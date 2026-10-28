package qtest

import (
	"context"
	"reflect"
	"regexp"
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
	if e := x.Evidence().String(); !strings.Contains(e, "search    complete within 1 delay and 0 faults: every run within that bound, none beyond it") {
		t.Errorf("evidence:\n%s", e)
	}
	bad := x.Failing()[0]
	// the test's third exploration, run alone as a report names it
	t.Setenv("GOTOGETHER_RUN", t.Name()+"#3:schedule="+bad.origin.value)
	again := sys.Exhaust(t, Bound{Delays: 1})
	log := &logTB{TB: t}
	again.Expect(log, All(Rejects("withdraw")), Some(Conforms()))
	if len(again.Runs) != 1 || again.Runs[0].origin.value != bad.origin.value {
		t.Errorf("GOTOGETHER_RUN schedule=%s ran %d runs", bad.origin.value, len(again.Runs))
	}
	// one run is not a search: claims about other runs are not judged,
	// and the evidence claims no complete search
	if !strings.Contains(log.logs, "not judged on one run (about the runs it leaves out): conformance") {
		t.Errorf("logs: %s", log.logs)
	}
	if sr := again.Evidence().Exhaust; sr == nil || sr.Complete {
		t.Errorf("a run alone's evidence claims a complete search: %+v", sr)
	}
	// the nominal run's reproduce line names the empty schedule: it too
	// runs alone, not the whole search
	t.Setenv("GOTOGETHER_RUN", t.Name()+"#4:schedule=")
	nominal := sys.Exhaust(t, Bound{Delays: 1})
	if len(nominal.Runs) != 1 || nominal.Runs[0].origin.value != "" {
		t.Errorf("GOTOGETHER_RUN schedule= ran %d runs, want the nominal one alone", len(nominal.Runs))
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
	if e := x.Evidence(); e.Exhaust == nil || e.Exhaust.Complete || e.Exhaust.Left == 0 {
		t.Errorf("search %+v, want truncated", e.Exhaust)
	}
	sample := sys.Exhaust(t, Bound{Delays: 1, MaxRuns: 1, Partial: true}) // t: passes
	if e := sample.Evidence(); e.Exhaust.Complete || e.Exhaust.Left == 0 {
		t.Errorf("a partial search's evidence: %+v", e.Exhaust)
	}
}

// A passing claim met by a failing run logs the run as the claim it
// satisfies, in the claims' words, without the paths of files the test's
// end removes: its conformance FAIL once read as the test's. It logs the
// catch in three lines (which run, the step and why, where to look), the
// whole report under GOTOGETHER_VERBOSE=1: hello's output showed no catch
// at all, where its README promised one.
func TestExpectedFailureLogged(t *testing.T) {
	RequireQuint(t)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: race}
	log := &logTB{TB: t}
	sys.Exhaust(t, Bound{Delays: 1}).Expect(log, Rejects("withdraw"))
	brief := "gotogether: as expected, a withdraw step the model cannot take: 1 of 2 runs (failing: 1 rejects withdraw); the first, schedule "
	head, _, _ := strings.Cut(log.logs, "\n")
	lines := strings.Split(strings.TrimSpace(log.logs), "\n")
	if !strings.HasPrefix(head, brief) || len(lines) != 4 ||
		!strings.HasPrefix(lines[1], "    step ") || !strings.Contains(lines[1], ", withdraw(") || !strings.Contains(lines[1], "the model's withdraw(") ||
		!strings.HasPrefix(lines[2], "    navigate: gotogether nav -run 'TestExpectedFailureLogged#1:schedule=") ||
		!strings.HasSuffix(lines[2], "; GOTOGETHER_VERBOSE=1 prints the whole report") ||
		!regexp.MustCompile(`1 conform( \([^)]*\))?, 1 reject withdraw`).MatchString(lines[3]) { // who judged the pass: the sample's
		t.Errorf("want the catch in three lines, then the summary:\n%s", log.logs)
	}
	t.Setenv("GOTOGETHER_VERBOSE", "1")
	log = &logTB{TB: t}
	sys.Exhaust(t, Bound{Delays: 1}).Expect(log, Rejects("withdraw"))
	for _, want := range []string{"as expected, a withdraw step the model cannot take: 1 of 2 runs (failing: 1 rejects withdraw); the first:",
		"    schedule ", "its files are not kept: GOTOGETHER_KEEP=dir keeps them", "1 conform, 1 reject withdraw"} {
		if !strings.Contains(log.logs, want) {
			t.Errorf("verbose logs lack %q:\n%s", want, log.logs)
		}
	}
	for _, path := range []string{"module: ", "step log: "} {
		if strings.Contains(log.logs, path) {
			t.Errorf("logs name a %s:\n%s", path, log.logs)
		}
	}
}

package qtest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Claims over runs: a bare expectation is about some run (Conforms about
// every run); several are judged one by one; Some(a, b) wants one run
// with both; None wants no run with either.
func TestClaims(t *testing.T) {
	// three runs: one shows a, one shows a and b, one shows neither
	a := Expectation{desc: "a", check: func(r *validate.Report) bool { return len(r.Issues) == 1 || len(r.Issues) == 3 }}
	b := Expectation{desc: "b", check: func(r *validate.Report) bool { return len(r.Issues) == 3 }}
	c := Expectation{desc: "c", check: func(r *validate.Report) bool { return false }}
	runs := []*validate.Report{{Issues: make([]qtrace.Issue, 1)}, {Issues: make([]qtrace.Issue, 3)}, {Issues: make([]qtrace.Issue, 5)}}
	for _, k := range []struct {
		claim Claim
		ok    bool
		show  int
	}{
		{a, true, 0},
		{c, false, -1},
		{Some(a, b), true, 1},
		{Some(b, c), false, -1},
		{All(a), false, 2},
		{None(c), true, -1},
		{None(b), false, 1},
		{None(c, a), false, 0},
	} {
		j := k.claim.judge(runs)
		if j.ok != k.ok || j.show != k.show {
			t.Errorf("%s: ok %v show %d, want %v %d", k.claim, j.ok, j.show, k.ok, k.show)
		}
	}
	every := Expectation{desc: "every", check: func(r *validate.Report) bool { return len(r.Issues) > 0 }, every: true}
	if !every.judge(runs).ok {
		t.Error("an every-run expectation alone is not judged over every run")
	}
}

// A Reach is judged over the runs' choices: how many runs reached (or
// faulted) the kind, against the share wanted.
func TestReach(t *testing.T) {
	runs := []*Run{
		{Choices: []qenv.Choice{{Kind: "fault.rpc.x.request", N: 3, Value: 0}}},
		{Choices: []qenv.Choice{{Kind: "fault.rpc.x.request", N: 3, Value: 2}}},
		{Choices: []qenv.Choice{{Kind: "sched.task", N: 2}}},
	}
	for _, c := range []struct {
		r  Reach
		ok bool
	}{
		{Reaches("rpc.x.request"), true},
		{Reaches("rpc.x.request").InAtLeast(0.6), true},
		{Reaches("rpc.x.request").InAtLeast(0.8), false},
		{Reaches("rpc.x.request").Faulted().InAtLeast(0.5), false},
		{Reaches("rpc.x.request").Faulted(), true},
		{Reaches("rpc.y.request"), false},
	} {
		if msg := c.r.judgeRuns(runs, nil); (msg == "") != c.ok {
			t.Errorf("%v: %q", c.r, msg)
		}
	}
}

// Reaches names a kind as reports print it; the chooser's "fault." prefix
// fails with the fix rather than being read two ways.
func TestReachesWantsTheBareKind(t *testing.T) {
	defer func() {
		if msg := fmt.Sprint(recover()); !strings.Contains(msg, `("rpc.x")`) {
			t.Errorf("panic %q, want the bare kind", msg)
		}
	}()
	Reaches("fault.rpc.x")
	t.Error("a prefixed kind was accepted")
}

// ShrunkTo judges a run's faults, and refuses an exploration, where it
// would hold for nothing.
func TestShrunkTo(t *testing.T) {
	expect := func(f func(t testing.TB)) []string {
		ft := &failTB{TB: t}
		done := make(chan struct{})
		go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
			defer close(done)
			f(ft)
		}()
		<-done
		return ft.errs
	}
	fault := qenv.Choice{Kind: "fault.rpc.x.request", N: 3, Value: 1}
	run := &Run{Report: &validate.Report{}, Choices: []qenv.Choice{fault, {Kind: "sched.task", N: 2, Value: 1}, fault}}
	if errs := expect(func(t testing.TB) { run.Expect(t, ShrunkTo(2)) }); len(errs) != 0 {
		t.Errorf("two faults, ShrunkTo(2): %v", errs)
	}
	if errs := expect(func(t testing.TB) { run.Expect(t, ShrunkTo(1)) }); len(errs) != 1 || !strings.Contains(errs[0], "2 faults") {
		t.Errorf("two faults, ShrunkTo(1): %v, want a failure naming them", errs)
	}
	x := &Exploration{Runs: []*Run{run}}
	if errs := expect(func(t testing.TB) { x.Expect(t, ShrunkTo(5)) }); len(errs) != 1 || !strings.Contains(errs[0], "claim on one run") {
		t.Errorf("an exploration: %v, want the claim refused", errs)
	}
}

// Violates(...).At(action) wants the invariant to fail first after a step
// of that action; At on anything but Violates is a mistake, said so.
func TestViolatesAt(t *testing.T) {
	rep := &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass, Invariants: []validate.InvariantResult{
		{Name: "exactlyOnce", Verdict: validate.Fail, Failure: &validate.Failure{Step: &qtrace.Step{Action: "perform"}}},
	}}}}
	for _, c := range []struct {
		e  Expectation
		ok bool
	}{
		{Violates("exactlyOnce"), true},
		{Violates("exactlyOnce").At("perform"), true},
		{Violates("exactlyOnce").At("ack"), false},
		{Violates("other").At("perform"), false},
	} {
		if got := c.e.check(rep); got != c.ok {
			t.Errorf("%s: %v, want %v", c.e, got, c.ok)
		}
	}
	defer func() {
		if msg := fmt.Sprint(recover()); !strings.Contains(msg, "narrows Violates(invariant) only") {
			t.Errorf("Rejects(...).At: panic %q", msg)
		}
	}()
	Rejects("cas").At("cas")
}

// Takes and Exercised are judged on the model actions the runs' replays
// took: up to a step the model could not take, through a step whose state
// did not match.
func TestTakesAndExercised(t *testing.T) {
	call := func(a string) binding.Call { return binding.Call{Action: a} }
	ok := &Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass,
		Calls: []binding.Call{call("init"), call("apply"), call("replay")}}}},
		Choices: []qenv.Choice{{Kind: "fault.rpc.x.request", N: 3, Value: 1}}}
	rejected := &Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Fail,
		Failure: &validate.Failure{Kind: "disabled", Index: 1}, Calls: []binding.Call{call("init"), call("cancel")}}}}}
	model := map[string]bool{"init": true, "step": true, "apply": true, "replay": true, "cancel": true}
	for _, c := range []struct {
		claim runsClaim
		runs  []*Run
		ok    bool
	}{
		{Takes("apply"), []*Run{ok}, true},
		{Takes("cancel"), []*Run{ok, rejected}, false}, // rejected: not taken
		{Takes("apply").InAtLeast(0.6), []*Run{ok, rejected}, false},
		{Takes("refund"), []*Run{ok}, false},       // no such action
		{Exercised(), []*Run{ok, rejected}, false}, // cancel never taken
		{Exercised("cancel"), []*Run{ok, rejected}, true},
		{Exercised("cancel", "nope"), []*Run{ok, rejected}, false}, // a misspelled exception
	} {
		if msg := c.claim.judgeRuns(c.runs, model); (msg == "") != c.ok {
			t.Errorf("%s: %q", c.claim, msg)
		}
	}
	if m := modelCoverage([]*Run{ok, rejected}, model); m.Taken["apply"] != 1 || len(m.Never) != 1 || m.Never[0] != "cancel" {
		t.Errorf("model coverage %+v, want apply once and cancel never", m)
	}
}

package qtest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Claims over runs: several are judged one by one; Some(a, b) wants one
// run with both; None wants no run with either.
func TestClaims(t *testing.T) {
	t.Parallel()
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
		{Some(a), true, 0},
		{Some(c), false, -1},
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
	alone := Expectation{desc: "every", check: func(r *validate.Report) bool { return len(r.Issues) > 0 }, every: true, alone: true}
	if !alone.judge(runs).ok {
		t.Error("an expectation that stands alone is not judged over every run")
	}
}

// Over runs, only Conforms, TransitionsConform, Settles, Completes and Finishes stand alone
// (All of themselves); any other expectation alone panics, naming the
// quantifiers and what it meant alone before: Some for a failure's, All
// for Satisfies'. PerMutant's claims panic as they are made.
func TestBareClaims(t *testing.T) {
	t.Parallel()
	for _, e := range []Expectation{Conforms(), TransitionsConform(), TransitionsConform("actor"), Settles(), Completes(), Finishes()} {
		if q, ok := quantify(e).(quantified); !ok || q.kind != "all" {
			t.Errorf("%s alone: %v, want All of it", e, quantify(e))
		}
	}
	few := Satisfies("few issues", func(*validate.Report) error { return nil })
	for _, c := range []struct {
		e     Expectation
		meant string
	}{
		{Rejects("apply"), "Some"}, {Violates("inv").At("apply"), "Some"}, {Scenario(Takes("a")), "Some"},
		{AnyOf(Rejects("a"), Mismatch("a")), "Some"}, {few, "All"}, {Undecided(Completes()), "All"}, {Not(few), "Some"},
	} {
		for _, f := range []func(){
			func() { quantify(c.e) },
			func() { (&Exploration{}).Expect(t, c.e) },
			func() { PerMutant(c.e) },
		} {
			func() {
				defer func() {
					p := fmt.Sprint(recover())
					if !strings.Contains(p, "write qtest.Some(...) (some run shows it), qtest.All(...) (every run) or qtest.None(...) (no run)") ||
						!strings.Contains(p, "alone it meant "+c.meant+"(...)") {
						t.Errorf("%s alone: panic %q, want the quantifiers and %s named", c.e, p, c.meant)
					}
				}()
				f()
			}()
		}
	}
}

// Not is a run's other verdict: met where e is missed, missed where e is
// met, unknown where e is. Not(Conforms()) panics, naming the failures.
func TestNot(t *testing.T) {
	t.Parallel()
	few := Satisfies("few issues", func(r *validate.Report) error {
		switch {
		case r.Issues == nil:
			return fmt.Errorf("%w: none recorded", NoVerdict)
		case len(r.Issues) > 2:
			return fmt.Errorf("%d issues", len(r.Issues))
		}
		return nil
	})
	for _, c := range []struct {
		r    *validate.Report
		want tri
	}{
		{&validate.Report{Issues: make([]qtrace.Issue, 1)}, no},
		{&validate.Report{Issues: make([]qtrace.Issue, 3)}, yes},
		{&validate.Report{}, unknown},
	} {
		if got := Not(few).value(c.r); got != c.want {
			t.Errorf("Not over %d issues: %v, want %v", len(c.r.Issues), got, c.want)
		}
	}
	if why := Not(few).explain(&validate.Report{Issues: make([]qtrace.Issue, 1)}); !strings.Contains(why, "it meets few issues") {
		t.Errorf("Not's explanation of a run meeting e: %q", why)
	}
	defer func() {
		if p := fmt.Sprint(recover()); !strings.Contains(p, "qtest.Rejects(action), qtest.Mismatch(action) or qtest.Violates(invariant)") {
			t.Errorf("Not(Conforms()): panic %q, want the failures named", p)
		}
	}()
	Not(Conforms())
}

// HasIssue holds of a run whose report fails on a reconstruction error
// of its kind alone: not of a run without one, of one whose error is of
// another kind or only a warning, nor of one with an actor failing
// without it; a run-wide error covers every actor. An unknown kind panics.
func TestHasIssue(t *testing.T) {
	t.Parallel()
	dup := qtrace.Issue{Severity: "error", Kind: "duplicate", Msg: "recorder n seq 1 is two different records"}
	conforming := func(is ...qtrace.Issue) *validate.Result {
		return &validate.Result{Actor: "a", Conforms: validate.Pass, Issues: is}
	}
	rejecting := &validate.Result{Actor: "b", Conforms: validate.Fail, Failure: &validate.Failure{Kind: "disabled"}}
	for name, c := range map[string]struct {
		r    *validate.Report
		want bool
	}{
		"the duplicate":                  {&validate.Report{Actors: []*validate.Result{conforming(dup), conforming()}}, true},
		"the duplicate on every failing": {&validate.Report{Actors: []*validate.Result{conforming(dup), {Actor: "b", Conforms: validate.Fail, Issues: []qtrace.Issue{dup}}}}, true},
		"run-wide":                       {&validate.Report{Issues: []qtrace.Issue{dup}, Actors: []*validate.Result{rejecting}}, true},
		"none":                           {&validate.Report{Actors: []*validate.Result{conforming()}}, false},
		"a copy (info)":                  {&validate.Report{Actors: []*validate.Result{conforming(qtrace.Issue{Severity: "info", Kind: "duplicate"})}}, false},
		"another kind":                   {&validate.Report{Actors: []*validate.Result{conforming(qtrace.Issue{Severity: "error", Kind: "cycle"})}}, false},
		"a failure without it":           {&validate.Report{Actors: []*validate.Result{conforming(dup), rejecting}}, false},
	} {
		if got := HasIssue("duplicate").value(c.r) == yes; got != c.want {
			t.Errorf("%s: HasIssue(duplicate) %v, want %v", name, got, c.want)
		}
	}
	defer func() {
		if p := fmt.Sprint(recover()); !strings.Contains(p, "the kinds are cycle, duplicate") {
			t.Errorf("HasIssue(duplicat): panic %q, want the kinds named", p)
		}
	}()
	HasIssue("duplicat")
}

// Satisfies is its function's verdict on each run, quantified as any
// expectation, its error the failure's explanation.
func TestSatisfies(t *testing.T) {
	t.Parallel()
	few := Satisfies("few issues", func(r *validate.Report) error {
		if len(r.Issues) > 2 {
			return fmt.Errorf("%d issues", len(r.Issues))
		}
		return nil
	})
	runs := []*validate.Report{{Issues: make([]qtrace.Issue, 1)}, {Issues: make([]qtrace.Issue, 3)}}
	if j := All(few).judge(runs); j.ok || j.show != 1 {
		t.Errorf("All: ok %v show %d, want a failure showing run 1", j.ok, j.show)
	}
	if !Some(few).judge(runs).ok || None(few).judge(runs).ok {
		t.Error("Some and None do not quantify it")
	}
	if w := few.explain(runs[1]); w != " (3 issues)" {
		t.Errorf("explanation %q, want its error's", w)
	}
	// a report it cannot judge: no claim holds by it, None and its
	// negation's Some alike
	judged := Satisfies("issues judged", func(r *validate.Report) error {
		if r.Issues == nil {
			return fmt.Errorf("%w: no issues recorded", NoVerdict)
		}
		return nil
	})
	none := []*validate.Report{{}, {}}
	for _, c := range []Claim{Some(judged), All(judged), None(judged)} {
		if c.judge(none).ok {
			t.Errorf("%v held of runs it cannot judge", c)
		}
	}
	if !All(judged).judge(runs).ok {
		t.Error("All of it failed on runs it judges")
	}
}

// A Reach is judged over the runs' choices: how many runs reached (or
// faulted) the kind, against the share wanted.
func TestReach(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	// an exploration's Expect does not take it: ShrunkTo is a RunClaim,
	// not a Claim, which the compiler checks (it was refused at run time)
	var _ RunClaim = ShrunkTo(1)
}

// Violates(...).At(action) wants the invariant to fail first after a step
// of that action; At on anything but Violates is a mistake, said so.
func TestViolatesAt(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	// the failure writes the claim that would except what is missing,
	// the exceptions it already has kept
	if msg := Exercised().judgeRuns([]*Run{ok, rejected}, model); !strings.Contains(msg, `in the claim: qtest.Exercised("cancel") // why:`) {
		t.Errorf("Exercised() names no exception line: %q", msg)
	}
	if msg := Exercised("replay").judgeRuns([]*Run{rejected}, model); !strings.Contains(msg, `qtest.Exercised("apply", "cancel", "replay") // why:`) {
		t.Errorf("Exercised(replay) names no exception line keeping replay: %q", msg)
	}
	if m := modelCoverage([]*Run{ok, rejected}, model); m.Taken["apply"] != 1 || len(m.Never) != 1 || m.Never[0] != "cancel" {
		t.Errorf("model coverage %+v, want apply once and cancel never", m)
	}
}

// Takes(...).Outcome(o) counts a run only if it took the action by a step
// recorded with outcome o (a contrast test's refusal); TakesEach names
// every Take no run met, each with its hint, and a name the model lacks.
func TestTakesOutcomeAndEach(t *testing.T) {
	t.Parallel()
	step := func(a, o string) binding.Call {
		return binding.Call{Action: a, Step: &qtrace.Step{Action: a, Outcome: o}}
	}
	okRun := &Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass,
		Calls: []binding.Call{{Action: "init"}, step("borrow", ""), step("borrow", "unavailable")}}}}}
	// refused at its limit, but the model rejected that step: not taken
	rejected := &Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Fail,
		Failure: &validate.Failure{Kind: "disabled", Index: 1}, Calls: []binding.Call{{Action: "init"}, step("borrow", "limit")}}}}}
	model := map[string]bool{"init": true, "step": true, "borrow": true, "receipts": true}
	runs := []*Run{okRun, rejected}
	for _, c := range []struct {
		claim runsClaim
		want  string // in the message; "" for a pass
	}{
		{Takes("borrow").Outcome("ok"), ""},
		{Takes("borrow").Outcome("unavailable"), ""},
		{Takes("borrow").Outcome("limit"), "some run taking borrow with outcome limit; 0 of 2 runs did"},
		{Takes("borrow").Outcome("limit").Hint("send borrows past the limit"), "send borrows past the limit"},
		{Takes("borrow").Outcome("ok").InAtLeast(1), "1 of 2 runs did"},
		{TakesEach(Takes("borrow").Outcome("ok"), Takes("borrow").Outcome("unavailable")), ""},
		{TakesEach(Takes("borrow").Outcome("ok"), Takes("borrow").Outcome("limit").Hint("past the limit"), Takes("receipts").Hint("commit a borrow")),
			"2 of 3 not taken:\n  borrow with outcome limit (0 of 2 runs): past the limit\n  receipts (0 of 2 runs): commit a borrow"},
		{TakesEach(Takes("borow")), "borow is not an action of the model (did you mean borrow?"},
	} {
		msg := c.claim.judgeRuns(runs, model)
		switch {
		case c.want == "" && msg != "":
			t.Errorf("%s: %q, want a pass", c.claim, msg)
		case c.want != "" && !strings.Contains(msg, c.want):
			t.Errorf("%s: %q, want %q in it", c.claim, msg, c.want)
		}
	}
	if n := (Takes("borrow").Outcome("limit")).Name(); n != "borrow.limit" {
		t.Errorf("Name %q, want borrow.limit", n)
	}
}

// Exercised counts each option of a declared kind by name, among those a
// choice offered, and each nemesis a run enabled by name; an exception
// covers the names below it.
func TestExercisedByName(t *testing.T) {
	t.Parallel()
	qenv.DeclareFaults("test.exercised", "drop", "duplicate", "never")
	qenv.DeclareFaults("test.unmet", "lost")
	actions := map[string]bool{"init": true}
	took := &Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass, Calls: []binding.Call{{Action: "init"}}}}},
		Choices: []qenv.Choice{
			{Kind: "fault.test.exercised", N: 3, Value: 1},                 // drop; "never" (option 3) is never offered
			{Kind: "fault.nemesis.crash", N: 3, Value: 1, Name: "crash.a"}, // crash.b enabled, never fired
		},
		Nemeses: []string{"crash.a", "crash.b", "heal.a.b"}} // a heal need not fire
	for _, c := range []struct {
		claim Exercise
		want  string // in the message, or "" for a pass
	}{
		{Exercised(), "fault test.exercised option duplicate (reached, never taken); nemesis crash.b (enabled, never fired)"},
		{Exercised("test.exercised.duplicate", "nemesis.crash.b"), ""},
		{Exercised("test.exercised", "nemesis.crash"), ""},
		{Exercised("test.exercised.never", "test.exercised.duplicate", "nemesis"), ""}, // a declared option no choice offered is known
		{Exercised("test.exercised.nope", "test.exercised.duplicate", "nemesis"), "test.exercised.nope (excepted, but neither"},
		{Exercised("test.unmet", "test.exercised.duplicate", "nemesis"), ""}, // a declared kind no run met
	} {
		msg := c.claim.judgeRuns([]*Run{took}, actions)
		if c.want == "" && msg != "" || c.want != "" && !strings.Contains(msg, c.want) {
			t.Errorf("%s: %q, want %q", c.claim, msg, c.want)
		}
	}
}

// An exception list shared by runs that reached nothing (a sweep row with
// every fault off) is judged by what they declared: prefixes, kinds,
// options and nemeses no run met are known, a misspelling is not.
func TestExercisedExceptionsOfAnEmptyRow(t *testing.T) {
	t.Parallel()
	names := &hooks.FaultNames{} // the run's, as runOne gives it
	s := qenv.New(qenv.WithContext(qenv.Nominal{}, hooks.WithFaultNames(context.Background(), names)))
	qenv.DeclareFaults("test.row.queue", "lost")         // a double's, made in the run
	s.Process("rowproc", true, func(context.Context) {}) // registers nemesis crash.rowproc
	s.Close()
	empty := &Run{faults: names, Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass, Calls: []binding.Call{{Action: "init"}}}}}}
	actions := map[string]bool{"init": true}
	if msg := Exercised("test.row", "test.row.queue.lost", "nemesis", "nemesis.crash.rowproc").judgeRuns([]*Run{empty}, actions); msg != "" {
		t.Errorf("declared exceptions refused: %s", msg)
	}
	if msg := Exercised("test.rwo").judgeRuns([]*Run{empty}, actions); !strings.Contains(msg, "test.rwo (excepted, but neither") {
		t.Errorf("a misspelling: %q", msg)
	}
}

// A claim some runs meet says when its seeds would often miss it: 1 run
// of 20 (fleetrollout's 6.5%, near enough) is missed by 20 seeds about a
// third of the time, and 135 would miss it one time in a thousand; half
// of 20 is no luck. A bounded search's count is no sample.
func TestClaimPower(t *testing.T) {
	x := &Exploration{Runs: make([]*Run, 20)}
	if got, want := x.power(1), "; at that rate 20 runs miss it 35.8% of the time: 135 seeds miss it one time in a thousand (356 at the rate's lower bound, 1.9%)"; got != want {
		t.Errorf("1 of 20: %q, want %q", got, want)
	}
	for _, n := range []int{10, 20} {
		if got := x.power(n); got != "" {
			t.Errorf("%d of 20: %q", n, got)
		}
	}
	x.search = &search{}
	if got := x.power(1); got != "" {
		t.Errorf("a search: %q", got)
	}
}

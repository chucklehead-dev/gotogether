package qtest

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A claim that no run violates an invariant (or an eventual val) does not
// hold of a run whose verdict for it a lost tail left unknown: the
// deposit to 105 may follow a lost withdrawal, and the lost tail may have
// emptied the account before a settle. Such a run kills no mutant either.
// A claim the run does decide (no rejection: it conforms) still holds.
func TestClaimsOverLostTails(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	c, err := validate.NewChecker("../validate/testdata/funds.binding.yaml", validate.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	at := func(ms int) time.Time { return time.Unix(0, int64(ms)*int64(time.Millisecond)) }
	acct := func(rec string, ms int, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: "deposit", Recorder: rec, Seq: 1, Time: at(ms), Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	lost := qtrace.Lost{Recorder: "a", After: 1}
	over := qtrace.MarkLost([]qtrace.Step{acct("a", 10, 5, 5), acct("b", 30, 100, 105)}, lost)
	settled := qtrace.MarkLost([]qtrace.Step{acct("a", 10, 5, 5), {Action: "settled", Recorder: "s", Seq: 1, Settled: true, Time: at(50)}}, lost)
	for _, x := range []struct {
		name  string
		steps []qtrace.Step
		claim Claim
		ok    bool
	}{
		{"an invariant after the cut", over, None(Violates("underLimit")), false},
		{"an eventual val", settled, None(Violates(validate.EventuallyName("funded"))), false},
		{"a rejection, judged", over, None(Rejects("")), true},
	} {
		rep, err := c.Check(context.Background(), x.steps)
		if err != nil {
			t.Fatal(err)
		}
		if j := x.claim.judge([]*validate.Report{rep}); j.ok != x.ok {
			t.Errorf("%s: %s holds %v (%d unjudged), want %v:\n%s", x.name, x.claim, j.ok, j.unjudged, x.ok, rep.Summary())
		}
	}
	rep, err := c.Check(context.Background(), over)
	if err != nil {
		t.Fatal(err)
	}
	if runs := []*Run{{Report: rep}}; decidedFailing(runs) || !anyUndecided(runs) {
		t.Errorf("a run whose only failure has no verdict killed a mutant:\n%s", rep.Summary())
	}
}

// A call invoked before its recorder's cut may have completed in the lost
// tail: it is neither pending nor complete, so neither Completes() nor
// None(Pending(...)) holds of the run, and Some(Pending(...)) is not
// shown by it. The same call with no loss is pending.
func TestPendingOverLostTails(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	c, err := validate.NewChecker("../validate/testdata/funds.binding.yaml", validate.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	at := func(ms int) time.Time { return time.Unix(0, int64(ms)*int64(time.Millisecond)) }
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "a", Seq: 1, Time: at(10), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}},
		{Action: "withdraw", Recorder: "a", Seq: 2, Time: at(15), Invocation: true, Args: map[string]any{"amount": int64(5)}},
	}
	for _, x := range []struct {
		lost                          bool
		completes, nonePending, shown bool
	}{{true, false, false, false}, {false, false, false, true}} {
		run := steps
		if x.lost {
			run = qtrace.MarkLost(steps, qtrace.Lost{Recorder: "a", After: 2})
		}
		rep, err := c.Check(context.Background(), run)
		if err != nil {
			t.Fatal(err)
		}
		reps := []*validate.Report{rep}
		completes, none, some := All(Completes()).judge(reps), None(Pending("")).judge(reps), Some(Pending("withdraw")).judge(reps)
		if completes.ok != x.completes || none.ok != x.nonePending || some.ok != x.shown || x.lost && (completes.unjudged != 1 || none.unjudged != 1) {
			t.Errorf("lost %v: Completes %+v, None(Pending) %+v, Some(Pending) %+v:\n%s", x.lost, completes, none, some, rep.Summary())
		}
	}
}

// Wherever a claim is read, a run with no verdict for it neither meets
// nor misses it: Holds is false, Some does not hold and None counts it
// unjudged; a part that holds decides AnyOf whatever the others; and a
// call certainly pending misses Completes whatever another may have done.
func TestUnknownClaimsEverywhere(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	c, err := validate.NewChecker("../validate/testdata/funds.binding.yaml", validate.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	at := func(ms int) time.Time { return time.Unix(0, int64(ms)*int64(time.Millisecond)) }
	deposit := qtrace.Step{Action: "deposit", Recorder: "a", Seq: 1, Time: at(10), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}
	maybe := qtrace.Step{Action: "withdraw", Recorder: "a", Seq: 2, Time: at(15), Invocation: true, Args: map[string]any{"amount": int64(5)}}
	certain := qtrace.Step{Action: "withdraw", Recorder: "b", Seq: 1, Time: at(30), Invocation: true, Args: map[string]any{"amount": int64(1)}}
	lost := qtrace.Lost{Recorder: "a", After: 2}
	check := func(steps []qtrace.Step) *Run {
		rep, err := c.Check(context.Background(), qtrace.MarkLost(steps, lost))
		if err != nil {
			t.Fatal(err)
		}
		return &Run{Report: rep}
	}
	r := check([]qtrace.Step{deposit, maybe})
	reps := []*validate.Report{r.Report}
	if Completes().Holds(r) {
		t.Errorf("Completes holds of a run whose call may have completed")
	}
	if j := Some(Completes()).judge(reps); j.ok {
		t.Errorf("Some(Completes()) %+v", j)
	}
	if j := None(Completes()).judge(reps); j.ok || j.n != 0 || j.unjudged != 1 {
		t.Errorf("None(Completes()) %+v, want unjudged", j)
	}
	if j := All(AnyOf(Completes(), Conforms())).judge(reps); !j.ok {
		t.Errorf("All(AnyOf(Completes(), Conforms())) %+v: Conforms holds", j)
	}
	if j := All(AnyOf(Rejects(""), Completes())).judge(reps); j.ok || j.unjudged != 1 {
		t.Errorf("All(AnyOf(Rejects, Completes())) %+v, want unjudged", j)
	}
	r = check([]qtrace.Step{deposit, maybe, certain})
	if j := All(Completes()).judge([]*validate.Report{r.Report}); j.ok || j.n != 1 || j.unjudged != 0 {
		t.Errorf("All(Completes()) with a call certainly pending %+v, want missed", j)
	}
}

// A mismatch after a gap is no verdict, for every claim that reads it:
// the review's log (deposits seq 1, 2 and 4, a settle at 5, seq 3
// dropped) once met Mismatch("deposit") and the SameFailure of a run
// that does mismatch there, and Finishes read it as certainly not
// finished; one that conforms beside the gap neither shows the failure
// nor certainly lacks it. A run that mismatches before the gap still
// meets both, and is certainly not finished. Over runs, None(Mismatch) does not hold of
// it, and it kills no mutant. Ablation: with validate's gap judgment off
// every claim below reads the gapped run as a FAIL.
func TestClaimsOverGaps(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	dep := func(seq uint64, balance int64) qtrace.Step {
		return qtrace.Step{Action: "deposit", Recorder: "a", Thread: "a", Seq: seq, Time: time.Unix(int64(seq), 0),
			Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": balance}}
	}
	settle := qtrace.Step{Action: "settled", Settled: true, Recorder: "a", Thread: "a", Seq: 5, Time: time.Unix(5, 0)}
	funds := "../validate/testdata/funds.binding.yaml"
	gapped := Validate(t, funds, []qtrace.Step{dep(1, 5), dep(2, 10), dep(4, 20), settle})
	beside := Validate(t, funds, []qtrace.Step{dep(1, 5), dep(2, 10), dep(4, 15), settle}) // conforms as recorded
	before := Validate(t, funds, []qtrace.Step{dep(1, 5), dep(2, 11), dep(4, 21), settle})
	denseSettle := settle
	denseSettle.Seq = 4
	dense := Validate(t, funds, []qtrace.Step{dep(1, 5), dep(2, 10), dep(3, 20), denseSettle})
	denseBefore := Validate(t, funds, []qtrace.Step{dep(1, 5), dep(2, 11), dep(3, 16), denseSettle})
	for _, x := range []struct {
		name string
		e    Expectation
		run  *Run
		want tri
	}{
		{"Mismatch after the gap", Mismatch("deposit"), gapped, no},
		{"SameFailure after the gap", SameFailure(dense), gapped, unknown},
		{"Finishes after the gap", Finishes(), gapped, unknown},
		// no failure shown, but the dropped step may have been it: a
		// shrink's candidate that does not reproduce, not one that passes
		{"SameFailure beside the gap", SameFailure(dense), beside, unknown},
		{"Mismatch before the gap", Mismatch("deposit"), before, yes},
		{"Finishes before the gap", Finishes(), before, no},
		// the failure stands, and the gap after it decides nothing (it
		// once made every failure beside a gap unknown, while Check
		// said FAIL)
		{"SameFailure before the gap", SameFailure(denseBefore), before, yes},
	} {
		if got := x.e.value(x.run.Report); got != x.want {
			t.Errorf("%s: %v, want %v:\n%s", x.name, got, x.want, x.run.Report.Summary())
		}
	}
	if j := None(Mismatch("deposit")).judge([]*validate.Report{gapped.Report}); j.ok || j.unjudged != 1 {
		t.Errorf("None(Mismatch) over the gapped run: %+v, want unjudged", j)
	}
	if runs := []*Run{gapped}; decidedFailing(runs) || !anyUndecided(runs) {
		t.Errorf("a run whose only failure a gap explains killed a mutant:\n%s", gapped.Report.Summary())
	}
	// a FAIL anywhere kills: one actor with no verdict beside another
	// that fails (it once made the mutant undecided)
	both := &Run{Report: &validate.Report{Actors: []*validate.Result{gapped.Report.Actors[0], before.Report.Actors[0]}}}
	if runs := []*Run{both}; !decidedFailing(runs) || anyUndecided(runs) {
		t.Errorf("a run with a FAILing actor beside one with no verdict killed no mutant:\n%s", both.Report.Summary())
	}
	if got := gapped.Report.Actors[0].Reason(); got != "no verdict (dropped steps)" {
		t.Errorf("the gapped run's reason %q", got)
	}
}

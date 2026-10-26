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

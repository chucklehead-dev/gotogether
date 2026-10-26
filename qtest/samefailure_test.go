package qtest

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// SameFailure keeps each failing actor's first failure's kind and name,
// whatever the arguments and actors' names, and has no verdict on a run
// with a lost tail or an undecided actor.
func TestSameFailure(t *testing.T) {
	t.Parallel()
	step := func(action string, arg int) *qtrace.Step {
		return &qtrace.Step{Action: action, Args: map[string]any{"amount": arg}}
	}
	disabled := func(actor string, arg int) *validate.Result {
		return &validate.Result{Actor: actor, Conforms: validate.Fail, Failure: &validate.Failure{Kind: "disabled", Call: "withdraw(...)", Step: step("withdraw", arg)}}
	}
	violates := func(actor string, names ...string) *validate.Result {
		r := &validate.Result{Actor: actor, Conforms: validate.Pass}
		for i, n := range names {
			r.Invariants = append(r.Invariants, validate.InvariantResult{Name: n, Verdict: validate.Fail, Within: strings.HasPrefix(n, "within "), Failure: &validate.Failure{Index: 10 - i}})
		}
		return r
	}
	rep := func(rs ...*validate.Result) *validate.Report { return &validate.Report{Actors: rs} }
	orig := SameFailure(&Run{Report: rep(disabled("acct-1", 5), violates("acct-2", "solvent", "within 1s"))})
	if want := "the same failure (disabled withdraw; within within 1s)"; orig.String() != want {
		t.Fatalf("got %q, want %q", orig, want)
	}
	lost := violates("x", "solvent", "within 1s")
	lost.Lost = []qtrace.Lost{{Recorder: "x"}}
	undecided := disabled("acct-1", 5)
	undecided.Conforms = validate.NotEvaluated
	for _, c := range []struct {
		rep  *validate.Report
		want tri
	}{
		{rep(disabled("acct-9", 70), violates("acct-3", "solvent", "within 1s")), yes}, // other ids and arguments
		{rep(violates("b", "within 1s"), disabled("a", 1), violates("c", "solvent")), yes},
		{rep(disabled("acct-1", 5)), no},
		{rep(disabled("acct-1", 5), violates("acct-2", "solvent")), no}, // the first invariant failing is another
		{rep(disabled("acct-1", 5), lost), unknown},
		{rep(undecided, violates("acct-2", "within 1s")), unknown},
		{nil, unknown},
	} {
		if got := orig.value(c.rep); got != c.want {
			t.Errorf("%v: got %v, want %v", c.rep, got, c.want)
		}
	}
	if none := SameFailure(&Run{Report: rep(&validate.Result{Conforms: validate.Pass})}); none.value(rep(disabled("a", 1))) != no {
		t.Error("the same failure as a passing run held")
	}
}

package qtest

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestModelChoiceBoundsCannotKillOrPreserveFailure(t *testing.T) {
	t.Parallel()
	rejected := &validate.Report{Actors: []*validate.Result{{Conforms: validate.Fail,
		Failure: &validate.Failure{Kind: "disabled", Step: &qtrace.Step{Action: "pick"}},
		Choices: &validate.ChoiceSearch{Status: "rejected", Exhaustive: true, Confirmation: "not_performed"},
	}}}
	keep := SameFailure(&Run{Report: rejected})
	if !decidedFailing([]*Run{{Report: rejected}}) || anyUndecided([]*Run{{Report: rejected}}) || !keep.Holds(&Run{Report: rejected}) {
		t.Fatal("exhaustive finite rejection lost its decided failure without a Quint nonexistence proof")
	}
	for _, status := range []string{"exhausted", "unsupported"} {
		t.Run(status, func(t *testing.T) {
			report := &validate.Report{Actors: []*validate.Result{{Conforms: validate.NotEvaluated,
				Choices:    &validate.ChoiceSearch{Status: status, Confirmation: "not_performed"},
				Invariants: []validate.InvariantResult{{Name: "safe", Verdict: validate.NotEvaluated}},
			}}}
			run := &Run{Report: report}
			if decidedFailing([]*Run{run}) || !anyUndecided([]*Run{run}) {
				t.Fatal("incomplete model-choice search counted as a mutant kill or survival")
			}
			if keep.value(report) != unknown || keep.Holds(run) {
				t.Fatal("incomplete search preserved a decided failure")
			}
			if SameFailure(run).Holds(&Run{Report: rejected}) {
				t.Fatal("incomplete search manufactured a failure fingerprint")
			}
		})
	}
}

func TestUnjudgedChoicesCannotProveNegatedClaims(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"exhausted", "unsupported", "rejected"} {
		t.Run(status, func(t *testing.T) {
			rep := &validate.Report{Actors: []*validate.Result{{Conforms: validate.NotEvaluated,
				Choices:    &validate.ChoiceSearch{Status: status, Exhaustive: status == "rejected", Confirmation: "not_performed"},
				Invariants: []validate.InvariantResult{{Name: "safe", Verdict: validate.NotEvaluated}},
			}}}
			run := &Run{Report: rep}
			for _, e := range []Expectation{Mismatch("pick"), Rejects("pick"), Violates("safe")} {
				if e.value(rep) != unknown || e.Holds(run) || Not(e).Holds(run) || AnyOf(e, Not(e)).value(rep) != unknown {
					t.Fatalf("%s turned unknown choice evidence into certainty", e)
				}
				for _, claim := range []Claim{Some(e), Some(Not(e)), All(e), All(Not(e)), None(e), None(Not(e))} {
					if claim.judge([]*validate.Report{rep}).ok {
						t.Fatalf("%s held without a model judgment", claim)
					}
				}
			}
			// Lifecycle evidence has its own scope: no settle was recorded, regardless
			// of whether the model can judge this complete input.
			if Settles().value(rep) != no || !Not(Settles()).Holds(run) {
				t.Fatal("model uncertainty erased an independent lifecycle judgment")
			}
			mixed := &validate.Report{Actors: append(append([]*validate.Result(nil), rep.Actors...), &validate.Result{Conforms: validate.Fail,
				Failure: &validate.Failure{Kind: "state", Step: &qtrace.Step{Action: "pick"}},
			})}
			if !Some(Mismatch("pick")).judge([]*validate.Report{mixed}).ok {
				t.Fatal("an unrelated unknown actor erased a known mismatch")
			}
		})
	}
}

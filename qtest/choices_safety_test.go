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

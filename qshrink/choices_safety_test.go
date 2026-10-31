package qshrink

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestSameFailureRequiresDecidedModelChoiceSearch(t *testing.T) {
	t.Parallel()
	rejected := &validate.Report{Actors: []*validate.Result{{Conforms: validate.Fail,
		Failure: &validate.Failure{Kind: "disabled", Step: &qtrace.Step{Action: "pick"}},
		Choices: &validate.ChoiceSearch{Status: "rejected", Exhaustive: true, Confirmation: "not_performed"},
	}}}
	keep := SameFailure(rejected)
	if keep.Shows(rejected) != Reproduced {
		t.Fatal("exhaustive finite rejection must remain shrinkable")
	}
	for _, status := range []string{"exhausted", "unsupported"} {
		t.Run(status, func(t *testing.T) {
			unknown := &validate.Report{Actors: []*validate.Result{{Conforms: validate.NotEvaluated,
				Choices: &validate.ChoiceSearch{Status: status, Confirmation: "not_performed"},
			}}}
			if keep.Shows(unknown) != Undecided {
				t.Fatal("incomplete search substituted for a model failure")
			}
			if SameFailure(unknown).Shows(rejected) == Reproduced {
				t.Fatal("incomplete search invented an original failure fingerprint")
			}
		})
	}
}

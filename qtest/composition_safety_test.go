package qtest

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func composedSafetyReport(status receipt.Status) *validate.Report {
	return &validate.Report{Actors: []*validate.Result{{Actor: "local", Conforms: validate.Pass}}, Composition: &receipt.Composition{
		Local:    []receipt.Component{{Target: "local", Applicable: true, Status: status, Reason: "registered check"}},
		Protocol: receipt.Protocol{Component: receipt.Component{Status: receipt.NotChecked}},
	}}
}

// A model counterexample can kill a mutant. An unjudged model or broken
// evaluator cannot become a successful mutation test merely because OK is false.
func TestCompositionMutationUsesOnlyDefinitiveFailures(t *testing.T) {
	for _, status := range []receipt.Status{receipt.Pass, receipt.Fail, receipt.Inconclusive, receipt.NotChecked, receipt.Error} {
		t.Run(string(status), func(t *testing.T) {
			rep := composedSafetyReport(status)
			runs := []*Run{{Report: rep}}
			if killed := decidedFailing(runs); killed != (status == receipt.Fail) {
				t.Fatalf("%s killed=%v", status, killed)
			}
			wantUnknown := status == receipt.Inconclusive || status == receipt.NotChecked || status == receipt.Error
			if unknown := anyUndecided(runs); unknown != wantUnknown {
				t.Fatalf("%s unknown=%v", status, unknown)
			}
			// A definite primary violation still proves failure, even beside a broken
			// additional checker or an exhausted composition search.
			rep.Actors[0].Invariants = []validate.InvariantResult{{Name: "safe", Verdict: validate.Fail, Failure: &validate.Failure{Kind: "invariant"}}}
			if !decidedFailing(runs) || anyUndecided(runs) {
				t.Fatalf("primary counterexample lost to %s", status)
			}
		})
	}
}

func TestCompositionConformanceQuantification(t *testing.T) {
	for _, status := range []receipt.Status{receipt.Pass, receipt.Fail, receipt.Inconclusive, receipt.NotChecked, receipt.Error} {
		t.Run(string(status), func(t *testing.T) {
			rep := composedSafetyReport(status)
			want := unknown
			if status == receipt.Pass {
				want = yes
			}
			if status == receipt.Fail {
				want = no
			}
			if got := Conforms().value(rep); got != want {
				t.Fatalf("%s: got %v, want %v", status, got, want)
			}
			none := None(Conforms()).judge([]*validate.Report{rep})
			if none.ok != (status == receipt.Fail) {
				t.Fatalf("None(Conforms) accepted %s: %+v", status, none)
			}
			// Primary-specific claims keep their own scope. A composition error is
			// neither evidence of a primary invariant failure nor a reason to hide one.
			if Some(Violates("safe")).judge([]*validate.Report{rep}).ok {
				t.Fatalf("composition %s invented primary violation", status)
			}
			rep.Actors[0].Invariants = []validate.InvariantResult{{Name: "safe", Verdict: validate.Fail, Failure: &validate.Failure{Kind: "invariant"}}}
			if !Some(Violates("safe")).judge([]*validate.Report{rep}).ok || !None(Conforms()).judge([]*validate.Report{rep}).ok {
				t.Fatalf("primary violation was hidden by %s", status)
			}
		})
	}
}

func TestEvaluatorErrorsAreNotMutationCounterexamples(t *testing.T) {
	rep := &validate.Report{Actors: []*validate.Result{{Conforms: validate.Fail, Failure: &validate.Failure{Kind: "error"}}}}
	if decidedFailing([]*Run{{Report: rep}}) || !anyUndecided([]*Run{{Report: rep}}) || Conforms().value(rep) != unknown {
		t.Fatal("evaluator error counted as a model counterexample")
	}
}

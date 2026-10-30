package qshrink

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func compositionShrinkReport(status receipt.Status) *validate.Report {
	return &validate.Report{Actors: []*validate.Result{{Actor: "local", Conforms: validate.Pass}}, Composition: &receipt.Composition{
		Local:    []receipt.Component{{Target: "protocol", Applicable: true, Status: status}},
		Protocol: receipt.Protocol{Component: receipt.Component{Status: receipt.NotChecked}},
	}}
}

func TestSameFailureRefusesCompositionOnlyFingerprint(t *testing.T) {
	for _, status := range []receipt.Status{receipt.Fail, receipt.Inconclusive, receipt.NotChecked, receipt.Error} {
		t.Run(string(status), func(t *testing.T) {
			defer func() {
				p := recover()
				message := fmt.Sprint(p)
				if p == nil || !strings.Contains(message, "composition-only failure") || !strings.Contains(message, "component-aware criterion") {
					t.Fatalf("composition was silently treated as no failure: %v", p)
				}
			}()
			SameFailure(compositionShrinkReport(status))
		})
	}
}

func TestSameFailureWithCompositionNamesItsPrimaryScope(t *testing.T) {
	original := compositionShrinkReport(receipt.Fail)
	original.Actors[0].Conforms = validate.Fail
	original.Actors[0].Failure = &validate.Failure{Kind: "disabled", Step: &qtrace.Step{Action: "place"}}
	criterion := SameFailure(original)
	if !strings.Contains(criterion.String(), "primary actor failure") {
		t.Fatalf("criterion overclaims composition preservation: %s", criterion)
	}
	// This criterion intentionally preserves the primary refusal, not another
	// service's failure or a protocol's counterexample ordering.
	candidate := compositionShrinkReport(receipt.Pass)
	candidate.Actors[0].Conforms = validate.Fail
	candidate.Actors[0].Failure = &validate.Failure{Kind: "disabled", Step: &qtrace.Step{Action: "place"}}
	if criterion.Shows(candidate) != Reproduced {
		t.Fatal("primary failure changed scope")
	}
	onlyComposition := compositionShrinkReport(receipt.Fail)
	if criterion.Shows(onlyComposition) != NotReproduced || !strings.Contains(criterion.Why(onlyComposition), "composition") {
		t.Fatal("composition-only failure substituted for primary fingerprint")
	}
}

func TestEvaluatorErrorIsNotPrimaryFailureFingerprint(t *testing.T) {
	broken := &validate.Report{Actors: []*validate.Result{{Conforms: validate.Fail, Failure: &validate.Failure{Kind: "error", Index: 5, Step: &qtrace.Step{Action: "place"}}}}}
	if got := firstFailures(broken); len(got) != 0 || !unjudgedFailure(broken) {
		t.Fatalf("tool error became a fingerprint: %v", got)
	}
	// A real model violation before the evaluator failed remains a fingerprint.
	broken.Actors[0].Invariants = []validate.InvariantResult{{Name: "safe", Verdict: validate.Fail, Failure: &validate.Failure{Kind: "invariant", Index: 1}}}
	if got := firstFailures(broken); len(got) != 1 || got[0] != "invariant safe" || unjudgedFailure(broken) {
		t.Fatalf("valid earlier failure was hidden: %v", got)
	}
	broken.Actors[0].Failure.Index = 0
	if !unjudgedFailure(broken) {
		t.Fatal("an earlier evaluator error claimed a later primary failure was determined")
	}
}

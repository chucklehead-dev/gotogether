package qtest

import (
	"context"
	"encoding/json"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qshrink"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func TestComponentShrinkIdentitySurvivesExpectationAndArtifactWrappers(t *testing.T) {
	history := &receipt.History{Status: receipt.Fail, Actors: []receipt.Actor{{Status: receipt.Fail, Checks: []receipt.Check{{Kind: "conformance", Name: "conformance", Status: receipt.Pass}, {Kind: "invariant", Name: "exactlyOnce", Status: receipt.Fail}}}}}
	rep := &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass}}, Composition: &receipt.Composition{Local: []receipt.Component{{Target: "consumer", Binding: "consumer.yaml", Applicable: true, Status: receipt.Fail, Judgment: history}}}}
	expectation := SameFailure(&Run{Report: rep})
	wrapped := shrinkKeep{expectation}
	identities := qshrink.ComponentFailures(wrapped)
	if len(identities) != 1 || identities[0].Target != "consumer" || identities[0].Name != "exactlyOnce" {
		t.Fatalf("expectation erased component identity: %+v", identities)
	}
	identities[0].Target = "changed"
	if qshrink.ComponentFailures(wrapped)[0].Target != "consumer" {
		t.Fatal("identity snapshot shares mutable storage")
	}
	result := &qshrink.Result{Keep: wrapped, ComponentFailures: qshrink.ComponentFailures(wrapped)}
	// The artifact writer replaces Keep with this wrapper; explicit result data
	// must retain identity even when a caller supplies only a presentation Keep.
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Keep              string                     `json:"keep"`
		ComponentFailures []qshrink.ComponentFailure `json:"componentFailures"`
	}
	if err = json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.ComponentFailures) != 1 || document.ComponentFailures[0].Binding != "consumer.yaml" || !strings.Contains(document.Keep, "consumer") {
		t.Fatalf("artifact lost typed failure scope: %s", encoded)
	}
	shrunk := shrunkOf([]string{"deliver"}, result, expectation)
	result.ComponentFailures[0].Name = "changed"
	if shrunk.ComponentFailures[0].Name != "exactlyOnce" {
		t.Fatal("public result shares fingerprint storage")
	}
}

func TestAssessmentSnapshotsReusableComponentData(t *testing.T) {
	shared := &receipt.History{Status: receipt.Fail, Actors: []receipt.Actor{{Status: receipt.Fail, Checks: []receipt.Check{{Kind: "conformance", Status: receipt.Pass}, {Kind: "invariant", Status: receipt.Fail}}}}}
	local := []receipt.Component{{Target: "consumer", Binding: "consumer.yaml", Applicable: true, Status: receipt.Fail, Judgment: shared}}
	c := settings{assess: func(ctx context.Context, steps []qtrace.Step) (receipt.Composition, error) {
		result, err := testAssessment(ctx, steps)
		shared.Actors[0].Checks[1].Name = steps[0].Action
		result.Local, result.Peers = local, nil
		return result, err
	}}
	first := &Run{Recorded: []qtrace.Step{{Action: "firstBug"}}, Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass}}}}
	assessHistory(t.Context(), c, first)
	first.adjust(nil)
	second := &Run{Recorded: []qtrace.Step{{Action: "secondBug"}}, Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass}}}}
	assessHistory(t.Context(), c, second)
	second.adjust(nil)
	got := qshrink.ComponentFailures(qshrink.SameFailure(first.Report))
	if len(got) != 1 || got[0].Name != "firstBug" {
		t.Fatalf("later callback rewrote earlier evidence: %+v", got)
	}
	if second.Report.Composition.Local[0].Judgment.Actors[0].Checks[1].Name != "secondBug" {
		t.Fatal("second assessment not retained")
	}
}

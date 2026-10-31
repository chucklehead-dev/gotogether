package qshrink

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func componentFailureReport(role, target, property string, count int) *validate.Report {
	var actors []receipt.Actor
	for i := 0; i < count; i++ {
		actors = append(actors, receipt.Actor{Actor: fmt.Sprint(i), Status: receipt.Fail, Checks: []receipt.Check{{Kind: "conformance", Name: "history", Status: receipt.Pass}, {Kind: "invariant", Name: property, Status: receipt.Fail}}})
	}
	j := &receipt.History{Status: receipt.Fail, Actors: actors}
	c := receipt.Component{Target: target, Binding: "consumer.yaml", Applicable: true, Status: receipt.Fail, Judgment: j}
	out := &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass}}, Composition: &receipt.Composition{Protocol: receipt.Protocol{Component: receipt.Component{Status: receipt.NotChecked}}}}
	if role == "protocol" {
		out.Composition.Protocol = receipt.Protocol{Component: c, Configured: true, Checked: 1, Limit: 1, Counterexample: []receipt.Record{{ID: "record1"}}}
	} else {
		out.Composition.Local = []receipt.Component{c}
	}
	return out
}

func TestComponentFailurePreservesIdentityAndMultiplicity(t *testing.T) {
	original := componentFailureReport("local", "consumer", "exactlyOnce", 2)
	keep := SameFailure(original)
	if !decided(original) || !strings.Contains(signatureOf(original), "consumer") {
		t.Fatal("default baseline omitted component failure")
	}
	renamed := componentFailureReport("local", "consumer", "exactlyOnce", 2)
	renamed.Composition.Local[0].Judgment.Actors[0].Actor = "fresh actor"
	renamed.Composition.Local[0].Reason = "another native record and order"
	if keep.Shows(renamed) != Reproduced {
		t.Fatal("transient names changed failure")
	}
	if keep.Shows(componentFailureReport("local", "consumer", "exactlyOnce", 1)) != NotReproduced {
		t.Fatal("multiplicity collapsed")
	}
	for _, change := range []func(*validate.Report){
		func(r *validate.Report) { r.Composition.Local[0].Target = "other" },
		func(r *validate.Report) { r.Composition.Local[0].Binding = "other.yaml" },
		func(r *validate.Report) {
			for i := range r.Composition.Local[0].Judgment.Actors {
				r.Composition.Local[0].Judgment.Actors[i].Checks[1].Name = "other"
			}
		},
		func(r *validate.Report) { r.Composition.Local[0].Status = receipt.Inconclusive },
		func(r *validate.Report) {
			r.Composition.Local[0].Applicable = false
			r.Composition.Local[0].Status = receipt.NotChecked
		},
		func(r *validate.Report) { r.Composition.Local[0].Judgment = nil },
	} {
		candidate := componentFailureReport("local", "consumer", "exactlyOnce", 2)
		change(candidate)
		if keep.Shows(candidate) != Undecided {
			t.Fatalf("missing/unchecked scope became reproduction: %+v", candidate.Composition)
		}
	}
	passed := componentFailureReport("local", "consumer", "exactlyOnce", 2)
	for i := range passed.Composition.Local[0].Judgment.Actors {
		a := &passed.Composition.Local[0].Judgment.Actors[i]
		a.Checks[1].Status = receipt.Pass
		a.Status = receipt.Pass
	}
	passed.Composition.Local[0].Status = receipt.Pass
	passed.Composition.Local[0].Judgment.Status = receipt.Pass
	if keep.Shows(passed) != NotReproduced {
		t.Fatal("judged pass did not lose failure")
	}
	passed.Composition.Local[0].Judgment.Actors[0].Search = &receipt.Search{BudgetExhausted: true}
	if keep.Shows(passed) != Undecided {
		t.Fatal("capped pass claimed a counterexample was removed")
	}
	passed.Composition.Local[0].Judgment.Actors[0].Search = nil
	passed.Composition.Local[0].Judgment.Actors[0].Checks = nil
	if keep.Shows(passed) != Undecided {
		t.Fatal("missing conformance check claimed pass")
	}

	fields := ComponentFailures(keep)
	fields[0].Target = "mutated"
	if ComponentFailures(keep)[0].Target != "consumer" {
		t.Fatal("snapshot aliased criterion")
	}
}

func TestComponentProtocolCounterexampleIsDecidedWithoutExhaustion(t *testing.T) {
	r := componentFailureReport("protocol", "posting", "conserved", 1)
	keep := SameFailure(r)
	if !decided(r) || keep.Shows(r) != Reproduced {
		t.Fatal("checked counterexample wrongly required exhaustive search")
	}
	candidate := componentFailureReport("protocol", "posting", "conserved", 1)
	candidate.Composition.Protocol.Counterexample = []receipt.Record{{ID: "another", Action: "another"}}
	if keep.Shows(candidate) != Reproduced {
		t.Fatal("counterexample order became identity")
	}
	candidate.Composition.Protocol.Counterexample = nil
	if keep.Shows(candidate) != Reproduced || !decided(candidate) {
		t.Fatal("initial-state counterexample required a native record")
	}
	candidate.Composition.Protocol.Checked = 0
	if keep.Shows(candidate) != Undecided || decided(candidate) {
		t.Fatal("unchecked ordering claimed failure")
	}
	candidate.Composition.Protocol.Status = receipt.Inconclusive
	if keep.Shows(candidate) != Undecided {
		t.Fatal("exhaustion claimed reproduction")
	}
}

func TestComponentMixedPrimaryAndLocalBothPreserved(t *testing.T) {
	r := componentFailureReport("local", "consumer", "exactlyOnce", 1)
	r.Actors[0].Conforms = validate.Fail
	r.Actors[0].Failure = &validate.Failure{Kind: "disabled", Step: &qtrace.Step{Action: "deliver"}}
	keep := SameFailure(r)
	if keep.Shows(r) != Reproduced {
		t.Fatal("mixed failure not reproduced")
	}
	primaryPass := componentFailureReport("local", "consumer", "exactlyOnce", 1)
	if keep.Shows(primaryPass) != NotReproduced {
		t.Fatal("primary failure dropped")
	}
	localPass := componentFailureReport("local", "consumer", "exactlyOnce", 1)
	localPass.Actors = r.Actors
	localPass.Composition.Local[0].Judgment.Actors[0].Checks[1].Status = receipt.Pass
	localPass.Composition.Local[0].Judgment.Actors[0].Status = receipt.Pass
	localPass.Composition.Local[0].Judgment.Status = receipt.Pass
	localPass.Composition.Local[0].Status = receipt.Pass
	if keep.Shows(localPass) != NotReproduced {
		t.Fatal("component failure dropped")
	}
}

func TestComponentReceiptContradictionsAndErrorsAreNotFingerprint(t *testing.T) {
	changes := []func(*validate.Report){
		func(r *validate.Report) { r.Composition.Local[0].Judgment.Status = receipt.Pass },
		func(r *validate.Report) { r.Composition.Local[0].Judgment.Actors[0].Status = receipt.Pass },
		func(r *validate.Report) { r.Composition.Local[0].Judgment.Actors[0].Checks[0].Status = receipt.Fail },
		func(r *validate.Report) {
			r.Composition.Local = append(r.Composition.Local, receipt.Component{Target: "other", Applicable: true, Status: receipt.Error})
		},
		func(r *validate.Report) {
			r.Composition.Peers = []receipt.Component{{Target: "edge", Applicable: true, Status: receipt.Fail}}
		},
	}
	for _, change := range changes {
		r := componentFailureReport("local", "consumer", "exactlyOnce", 1)
		change(r)
		if decided(r) {
			t.Fatal("invalid receipt became baseline")
		}
	}
}

func TestCompositionJudgeHistoryIsFreshAndImmutable(t *testing.T) {
	steps := []qtrace.Step{{Action: "deliver", Args: map[string]any{"id": "a"}}}
	raw, _ := json.Marshal(steps)
	fresh := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	for _, kind := range []string{"fresh", "stale", "missing", "mutated"} {
		t.Run(kind, func(t *testing.T) {
			captured := slices.Clone(steps)
			captured[0].Args = map[string]any{"id": "a"}
			s := &shrinker{judge: func(_ context.Context, input []qtrace.Step) (*validate.Report, error) {
				r := componentFailureReport("local", "consumer", "exactlyOnce", 1)
				r.Composition.History = fresh
				switch kind {
				case "stale":
					r.Composition.History = "sha256:old"
				case "missing":
					r.Composition.History = ""
				case "mutated":
					input[0].Args["id"] = "changed"
				}
				return r, nil
			}}
			_, err := s.judgeHistory(t.Context(), captured)
			if (err == nil) != (kind == "fresh") {
				t.Fatalf("history guard: %v", err)
			}
		})
	}
}

func TestComponentFailureArtifactSurvivesKeepWrapper(t *testing.T) {
	fields := ComponentFailures(SameFailure(componentFailureReport("local", "consumer", "exactlyOnce", 1)))
	r := &Result{ComponentFailures: fields}
	data, err := json.Marshal(r)
	if err != nil || !strings.Contains(string(data), `"componentFailures":[{"role":"local","target":"consumer"`) {
		t.Fatalf("typed identity missing: %v %s", err, data)
	}
}

func TestCompositionJudgeSnapshotsCallbackAssessment(t *testing.T) {
	steps := []qtrace.Step{{Action: "deliver"}}
	raw, _ := json.Marshal(steps)
	shared := componentFailureReport("local", "consumer", "firstBug", 1).Composition
	s := &shrinker{judge: func(_ context.Context, _ []qtrace.Step) (*validate.Report, error) {
		shared.History = fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
		return &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass}}, Composition: shared}, nil
	}}
	first, err := s.judgeHistory(t.Context(), steps)
	if err != nil {
		t.Fatal(err)
	}
	shared.Local[0].Judgment.Actors[0].Checks[1].Name = "secondBug"
	second, err := s.judgeHistory(t.Context(), steps)
	if err != nil {
		t.Fatal(err)
	}
	if signatureOf(first) == signatureOf(second) || !strings.Contains(signatureOf(first), "firstBug") {
		t.Fatalf("callback rewrote prior baseline: %s, %s", signatureOf(first), signatureOf(second))
	}
}

func TestDuplicateComponentScopeIsNotBaseline(t *testing.T) {
	r := componentFailureReport("local", "consumer", "exactlyOnce", 1)
	r.Composition.Local = append(r.Composition.Local, r.Composition.Local[0])
	if decided(r) {
		t.Fatal("ambiguous duplicate registration became a baseline")
	}
	defer func() {
		if p := recover(); p == nil || !strings.Contains(fmt.Sprint(p), `local component "consumer" binding "consumer.yaml" is registered more than once`) {
			t.Fatalf("duplicate refusal lost scope: %v", p)
		}
	}()
	SameFailure(r)
}

func TestComponentFailureNeedsCompleteModelChoiceProof(t *testing.T) {
	original := componentFailureReport("local", "consumer", "exactlyOnce", 1)
	keep := SameFailure(original)
	for _, choice := range []receipt.ChoiceSearch{
		{Status: "matched", Exhaustive: false, Confirmation: "path_confirmed"},
		{Status: "matched", Exhaustive: true, Confirmation: "not_performed"},
		{Status: "rejected", Exhaustive: true, Confirmation: "path_confirmed"},
		{Status: "", Exhaustive: true, Confirmation: "path_confirmed"},
	} {
		r := componentFailureReport("local", "consumer", "exactlyOnce", 1)
		r.Composition.Local[0].Judgment.Actors[0].Choices = &choice
		if decided(r) || keep.Shows(r) != Undecided {
			t.Fatalf("unproved choice-negative became a failure: %+v", choice)
		}
	}
	proved := componentFailureReport("local", "consumer", "exactlyOnce", 1)
	proved.Composition.Local[0].Judgment.Actors[0].Choices = &receipt.ChoiceSearch{Status: "matched", Exhaustive: true, Confirmation: "path_confirmed"}
	if !decided(proved) || keep.Shows(proved) != Reproduced {
		t.Fatal("complete confirmed choice proof was refused")
	}
	// Finding one positive path does not require exhaustive enumeration.
	passed := componentFailureReport("local", "consumer", "exactlyOnce", 1)
	component := &passed.Composition.Local[0]
	component.Status = receipt.Pass
	component.Judgment.Status = receipt.Pass
	actor := &component.Judgment.Actors[0]
	actor.Status = receipt.Pass
	actor.Checks[1].Status = receipt.Pass
	actor.Choices = &receipt.ChoiceSearch{Status: "matched", Exhaustive: false, Confirmation: "path_confirmed"}
	if keep.Shows(passed) != NotReproduced {
		t.Fatal("confirmed positive witness incorrectly required exhaustive enumeration")
	}
}

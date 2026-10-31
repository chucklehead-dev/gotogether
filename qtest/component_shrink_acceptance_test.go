package qtest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qshrink"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

const componentProducerBinding = "testdata/component-shrink/producer.yaml"
const componentConsumerBinding = "testdata/component-shrink/consumer.yaml"

// A fresh producer/consumer deployment for each real rerun. The consumer carries
// the planted no-dedupe bug: replay applies a delivered message again. Removing
// any trigger action makes that operation harmless, so each smaller workload can
// run to completion and be judged rather than merely diverging.
func componentCounterRerun(ctx context.Context, actions []string) ([]qtrace.Step, error) {
	sink := &qobs.MemorySink{}
	producer := qobs.NewRecorder(sink, qobs.WithActor("producer"))
	consumer := qobs.NewRecorder(sink, qobs.WithActor("consumer"))
	sim := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(ctx, producer)))
	defer sim.Close()
	sim.Spawn("producer-consumer", func(ctx context.Context) {
		queued, delivered, count := false, false, 0
		consumer.Record(ctx, "observe", nil, "obs.count", count)
		for _, action := range actions {
			switch action {
			case "publish":
				queued = true
				producer.Record(ctx, "publish", nil)
			case "deliver":
				name := "observe"
				if queued {
					queued = false
					delivered = true
					count++
					name = "apply"
				}
				consumer.Record(ctx, name, nil, "obs.count", count)
			case "replay":
				name := "observe"
				if delivered {
					count++
					name = "apply"
				}
				consumer.Record(ctx, name, nil, "obs.count", count)
			default:
				producer.Record(ctx, "noise", nil)
			}
		}
	})
	if err := sim.Settle(time.Second); err != nil {
		return nil, err
	}
	return sink.Steps(), nil
}

func componentAssessment(checker *validate.Checker) func(context.Context, []qtrace.Step) (receipt.Composition, error) {
	return func(ctx context.Context, steps []qtrace.Step) (receipt.Composition, error) {
		raw, err := json.Marshal(steps)
		if err != nil {
			return receipt.Composition{}, err
		}
		rep, err := checker.Check(ctx, steps)
		if err != nil {
			return receipt.Composition{}, err
		}
		history := rep.Receipt().History()
		return receipt.Composition{History: fmt.Sprintf("sha256:%x", sha256.Sum256(raw)), Local: []receipt.Component{{Target: "consumer", Binding: componentConsumerBinding, Status: history.Status, Reason: history.Reason, Applicable: true, Judgment: &history}}, Peers: []receipt.Component{}, Protocol: receipt.Protocol{Component: receipt.Component{Target: "system protocol", Status: receipt.NotChecked}}}, nil
	}
}

func TestComponentShrinkRealRerunsKeepConsumerObligation(t *testing.T) {
	RequireQuint(t)
	checker, err := validate.NewChecker(componentConsumerBinding, validate.Options{Dir: t.TempDir(), Eval: "go", NoITF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	assessments := 0
	requiredAssessments, performedAssessments := map[string]bool{}, map[string]bool{}
	captureID := func(steps []qtrace.Step) string {
		raw, err := json.Marshal(steps)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	}
	assess := componentAssessment(checker)
	sys := System{Binding: componentProducerBinding, Assess: func(ctx context.Context, steps []qtrace.Step) (receipt.Composition, error) {
		assessments++
		performedAssessments[captureID(steps)] = true
		return assess(ctx, steps)
	}}
	journey := []string{"noise-a", "publish", "noise-b", "deliver", "noise-c", "replay", "noise-d"}
	shrunk := ShrinkActions(t, journey, ActionShrink[string]{System: &sys, Rerun: func(ctx context.Context, actions []string) ([]qtrace.Step, error) {
		steps, err := componentCounterRerun(ctx, actions)
		if err == nil && len(actions) > 0 {
			requiredAssessments[captureID(steps)] = true
		}
		return steps, err
	}, Calibration: Calibration{Baseline: 2, Reruns: 1}})
	if !slices.Equal(shrunk.Actions, []string{"publish", "deliver", "replay"}) || !shrunk.Minimal {
		t.Fatalf("did not isolate the duplicate delivery trigger: %s", shrunk)
	}
	wantIdentity := []qshrink.ComponentFailure{{Role: "local", Target: "consumer", Binding: componentConsumerBinding, Kind: "invariant", Name: "exactlyOnce"}}
	if !slices.Equal(shrunk.ComponentFailures, wantIdentity) {
		t.Fatalf("shrunk evidence lost the selected component obligation: %+v", shrunk.ComponentFailures)
	}
	if assessments == 0 || shrunk.Run.Report.Composition == nil {
		t.Fatal("component assessment was not retained")
	}
	for history := range requiredAssessments {
		if !performedAssessments[history] {
			t.Fatalf("candidate captured history %s was not assessed", history)
		}
	}
	if shrunk.Run.Report.Actors[0].Conforms != validate.Pass || shrunk.Run.Report.Composition.Local[0].Status != receipt.Fail {
		t.Fatalf("expected primary pass and actual consumer failure: %s", shrunk.Run.Report.Summary())
	}
	if !shrunk.keep.Holds(shrunk.Run) || !SameFailure(shrunk.Run).Holds(shrunk.Run) {
		t.Fatal("the selected component failure does not reproduce itself")
	}
	// A real rerun with either required delivery removed is a complete model pass.
	for _, candidate := range [][]string{{"publish", "deliver"}, {"publish", "replay"}, {"deliver", "replay"}} {
		steps, err := componentCounterRerun(t.Context(), candidate)
		if err != nil {
			t.Fatal(err)
		}
		result, err := assess(t.Context(), steps)
		if err != nil {
			t.Fatal(err)
		}
		if result.Local[0].Status != receipt.Pass {
			t.Fatalf("one-removal candidate still fails: %v %+v", candidate, result.Local[0])
		}
	}
}

func TestComponentFailureDoesNotDriftAcrossTargetsOrProperties(t *testing.T) {
	RequireQuint(t)
	checker, err := validate.NewChecker(componentConsumerBinding, validate.Options{Dir: t.TempDir(), Eval: "go", NoITF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	steps, err := componentCounterRerun(t.Context(), []string{"publish", "deliver", "replay"})
	if err != nil {
		t.Fatal(err)
	}
	component, err := componentAssessment(checker)(t.Context(), steps)
	if err != nil {
		t.Fatal(err)
	}
	original := &validate.Report{Actors: []*validate.Result{{Actor: "producer", Conforms: validate.Pass}}, Composition: &component}
	keep := qshrink.SameFailure(original)
	for _, status := range []receipt.Status{receipt.NotChecked, receipt.Inconclusive, receipt.Error} {
		changed := component
		changed.Local = slices.Clone(component.Local)
		changed.Local[0].Status = status
		if got := keep.Shows(&validate.Report{Actors: original.Actors, Composition: &changed}); got == qshrink.Reproduced {
			t.Fatalf("%s became reproduction", status)
		}
	}
	for _, mode := range []string{"missing judgment", "capped history", "missing component"} {
		changed := component
		changed.Local = slices.Clone(component.Local)
		switch mode {
		case "missing judgment":
			changed.Local[0].Judgment = nil
		case "missing component":
			changed.Local = nil
		case "capped history":
			judgment := *component.Local[0].Judgment
			judgment.Actors = slices.Clone(judgment.Actors)
			for i := range judgment.Actors {
				actor := &judgment.Actors[i]
				actor.Status = receipt.Inconclusive
				actor.Search = &receipt.Search{Mode: "linearize", Goal: "one_conforming_history", BudgetExhausted: true, OrderBudget: 1}
				actor.Checks = slices.Clone(actor.Checks)
				for j := range actor.Checks {
					actor.Checks[j].Status = receipt.NotChecked
				}
			}
			judgment.Status = receipt.Inconclusive
			changed.Local[0].Status = receipt.Inconclusive
			changed.Local[0].Judgment = &judgment
		}
		if got := keep.Shows(&validate.Report{Actors: original.Actors, Composition: &changed}); got != qshrink.Undecided {
			t.Fatalf("%s must remain undecided, got %v", mode, got)
		}
	}
	prerequisiteRefused := false
	func() {
		defer func() { prerequisiteRefused = recover() != nil }()
		SameFailure(&Run{Report: original, Err: missedWorkload{missing: true}})
	}()
	if !prerequisiteRefused {
		t.Fatal("component failure bypassed unreached workload prerequisite")
	}
	changed := component
	changed.Local = slices.Clone(component.Local)
	changed.Local[0].Target = "another-consumer"
	if got := keep.Shows(&validate.Report{Actors: original.Actors, Composition: &changed}); got == qshrink.Reproduced {
		t.Fatal("same property on a different component reproduced")
	}
	other, err := checker.WithInvariants("neverDelivered")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	otherComponent, err := componentAssessment(other)(t.Context(), steps)
	if err != nil {
		t.Fatal(err)
	}
	// Binding identity stays the same here to isolate the named-obligation control.
	if got := keep.Shows(&validate.Report{Actors: original.Actors, Composition: &otherComponent}); got == qshrink.Reproduced {
		t.Fatal("a different failed property reproduced exactlyOnce")
	}
}

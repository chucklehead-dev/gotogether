package qtest

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/validate"
)

type missedWorkload struct{ missing bool }

func (m missedWorkload) Error() string            { return "reservation was not observed before its deadline" }
func (m missedWorkload) WorkloadNotReached() bool { return m.missing }

func TestMutationRefusesUnreachedWorkloadInsteadOfKilling(t *testing.T) {
	t.Parallel()
	missing := fmt.Errorf("phase reserve: %w", missedWorkload{missing: true})
	for _, tc := range []struct {
		name string
		run  func(qenv.Chooser) error
	}{
		{"returned prerequisite", func(qenv.Chooser) error { return missing }},
		{"panicked prerequisite", func(qenv.Chooser) error { panic(missing) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var broke atomic.Bool
			r := runOne(settings{}, false, qenv.Nominal{}, mutantScenario(tc.run, 42, &broke))
			if r.Err == nil || !workloadNotReached(r.Err) || broke.Load() {
				t.Fatalf("unreached workload became a kill: err=%v broke=%t", r.Err, broke.Load())
			}
			if !strings.Contains(r.Err.Error(), "mutant 42 not assessed") || !strings.Contains(r.Err.Error(), "establish the prerequisite") {
				t.Fatalf("refusal does not explain the remedy: %v", r.Err)
			}
			x := &Exploration{test: "TestMutation", id: 1, Runs: []*Run{r}}
			e := x.failedEvidence([]origin{{kind: "seed", seed: 1}})
			if e.Unjudged != 1 || e.Conform != 0 || len(e.FailingBy) != 0 || len(e.RunErrors) != 1 {
				t.Fatalf("mutation refusal invented a judgment: %+v", e)
			}
		})
	}
}

func TestMutationStillKillsActualErrorsAndPanics(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		run  func(qenv.Chooser) error
	}{
		{"application error", func(qenv.Chooser) error { return errors.New("write failed") }},
		{"application panic", func(qenv.Chooser) error { panic("broken application") }},
		{"marker explicitly false", func(qenv.Chooser) error { return missedWorkload{missing: false} }},
		{"similar words without marker", func(qenv.Chooser) error { return errors.New("workload prerequisite was not reached") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var broke atomic.Bool
			r := runOne(settings{}, false, qenv.Nominal{}, mutantScenario(tc.run, 42, &broke))
			if r.Err != nil || !broke.Load() {
				t.Fatalf("real application failure lost its kill: err=%v broke=%t", r.Err, broke.Load())
			}
		})
	}
}

func TestSameFailureRefusesUnreachedWorkload(t *testing.T) {
	t.Parallel()
	defer func() {
		p := recover()
		if p == nil || !strings.Contains(fmt.Sprint(p), "workload prerequisites were not reached; establish setup before shrinking a model failure") {
			t.Fatalf("expected an actionable prerequisite refusal, got %v", p)
		}
	}()
	SameFailure(&Run{Err: fmt.Errorf("phase prepare: %w", missedWorkload{missing: true})})
}

func TestSameFailureStillPreservesPrimaryFailure(t *testing.T) {
	t.Parallel()
	r := &Run{Report: &validate.Report{Actors: []*validate.Result{{Actor: "a", Conforms: validate.Pass, Invariants: []validate.InvariantResult{{Name: "safe", Verdict: validate.Fail}}}}}}
	if !SameFailure(r).Holds(r) {
		t.Fatal("ordinary primary model failure lost its fingerprint")
	}
	keep := SameFailure(&Run{})
	if keep.Holds(r) || !strings.Contains(keep.String(), "a run that does not fail") {
		t.Fatal("ordinary report-free behavior changed")
	}
}

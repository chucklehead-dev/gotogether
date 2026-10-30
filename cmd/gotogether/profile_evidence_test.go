package main

import (
	"strings"
	"testing"

	ev "github.com/chucklehead-dev/gotogether/internal/evidence"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

func TestSuiteReportPreservesUnjudgedExecutionLimits(t *testing.T) {
	e := &suiteEvidence{Role: ev.RoleFaithful, Test: "TestBusy#1", Runs: 1, Unjudged: 1, RunErrors: map[string]string{"TestBusy#1:seed=1": "scheduler limit"}, Profiles: []runprofile.Profile{{ID: "TestBusy#1", Mode: "seeded", Chooser: "seeded", Parallel: 1}}, Executions: []runprofile.Execution{{Run: "TestBusy#1:seed=1", Simulations: []runprofile.Simulation{{Simulation: 1, Runtime: "deterministic", Horizon: "1s", Scheduler: &runprofile.Scheduler{MaxSteps: 2, StepLimitReached: true}}}}}}
	text, gaps := suiteReport([]*suiteEvidence{e})
	for _, want := range []string{"0 conform, 1 unjudged", "effective run profile TestBusy#1", "execution error TestBusy#1:seed=1", "step limit reached"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if gaps == 0 {
		t.Fatal("strict evidence accepted unjudged executions")
	}
}

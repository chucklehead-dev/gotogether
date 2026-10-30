package qtest

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

func TestMutationEvidenceRetainsMutantLimitsWithoutChangingBaselineCounts(t *testing.T) {
	makeExploration := func(id int, mutant string, limited bool) *Exploration {
		x := &Exploration{test: "TestMutations", id: id, mutation: mutant}
		x.profile = &runprofile.Profile{ID: x.ref(), Mutant: mutant}
		x.Runs = []*Run{{origin: origin{kind: "seed", seed: 1}, Report: &validate.Report{}, simulations: []runprofile.Simulation{{Simulation: 1, Runtime: "deterministic", Horizon: "1s", Scheduler: &runprofile.Scheduler{MaxSteps: 100, StepLimitReached: limited}}}}}
		return x
	}
	baseline := makeExploration(1, "", false)
	mutant := makeExploration(2, "busy-loop", true)
	report := &MutationReport{Baseline: baseline}
	report.retainExecution(mutant)
	e := report.Evidence()
	if e.Runs != 1 || len(e.Executions) != 2 || len(e.Profiles) != 2 || e.Profiles[1].Mutant != "busy-loop" {
		t.Fatalf("mutant policy/counts: %+v", e)
	}
	if e.Executions[0].Run != baseline.ref()+":seed=1" || e.Executions[1].Run != mutant.ref()+":seed=1" || e.Executions[0].Simulations[0].Scheduler.StepLimitReached || !e.Executions[1].Simulations[0].Scheduler.StepLimitReached {
		t.Fatalf("mutant limits lost or relabeled: %+v", e.Executions)
	}
	if !strings.Contains(e.String(), "step limit reached") {
		t.Fatal("mutant exhaustion is invisible in aggregate text")
	}
	e.Executions[1].Simulations[0].Scheduler.MaxSteps = 999
	e.Profiles[1].Mutant = "changed"
	if next := report.Evidence(); next.Executions[1].Simulations[0].Scheduler.MaxSteps != 100 || next.Profiles[1].Mutant != "busy-loop" || mutant.Runs[0].simulations[0].Scheduler.MaxSteps != 100 {
		t.Fatal("aggregate evidence shares mutant observation storage")
	}
}

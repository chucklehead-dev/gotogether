package runprofile

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestExecutionTextFoldsCallsButKeepsLimitExhaustionVisible(t *testing.T) {
	e := Execution{Run: "TestLimits#1:seed=2"}
	for i := range 100 {
		e.Simulations = append(e.Simulations, Simulation{Simulation: 1, Runtime: "deterministic", Horizon: "1s", Scheduler: &Scheduler{MaxSteps: 1000, Hang: "30s", StepsBefore: i, StepsAfter: i + 1}})
	}
	for i := range 9 {
		e.Simulations = append(e.Simulations, Simulation{Simulation: 1, Runtime: "deterministic", Horizon: fmt.Sprintf("%ds", i+2), Scheduler: &Scheduler{MaxSteps: 1000, Hang: "30s", StepsBefore: 100, StepsAfter: 1000, StepLimitReached: i == 8}})
	}
	s := e.String()
	if !strings.Contains(s, "100 deterministic calls") || !strings.Contains(s, "2 more simulation policies") || !strings.Contains(s, "step limit reached in 1 call") || len(strings.Split(s, "\n")) > 10 {
		t.Fatalf("folded text: %s", s)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Execution
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Simulations) != 109 || !decoded.Simulations[108].Scheduler.StepLimitReached {
		t.Fatal("JSON lost observed calls")
	}
}

func TestExecutionCloneOwnsSchedulerPointers(t *testing.T) {
	e := Execution{Run: "first", Simulations: []Simulation{{Runtime: "deterministic", Scheduler: &Scheduler{MaxSteps: 100}}}}
	copy := e.Clone()
	copy.Simulations[0].Runtime = "changed"
	copy.Simulations[0].Scheduler.MaxSteps = 999
	if e.Simulations[0].Runtime != "deterministic" || e.Simulations[0].Scheduler.MaxSteps != 100 {
		t.Fatal("clone shares observation storage")
	}
}

func TestExecutionsSummaryFoldsAcrossRuns(t *testing.T) {
	var executions []Execution
	for i := range 100 {
		executions = append(executions, Execution{Run: fmt.Sprint(i), Simulations: []Simulation{{Runtime: "deterministic", Horizon: "1s", Scheduler: &Scheduler{MaxSteps: 100, Hang: "0s", StepsAfter: i, StepLimitReached: i == 99}}}})
	}
	s := ExecutionsString(executions)
	if !strings.Contains(s, "100 runs: 100 calls") || !strings.Contains(s, "100 deterministic calls") || !strings.Contains(s, "step limit reached in 1 call") || len(strings.Split(s, "\n")) != 2 {
		t.Fatalf("suite summary: %s", s)
	}
}

package runprofile

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func phaseFailure() Phase {
	return Phase{Name: "observe allocation", Status: "failed", Faults: "configured", Timeout: "5s", Started: "1s", Deadline: "6s", Finished: "6s", Reason: "phase deadline reached", Requirements: []Requirement{
		{Name: "reservation held", Kind: "query_result", App: "inventory", Tenant: "t1", Action: "allocation", RequestID: "t1-phase2-query1", Arguments: json.RawMessage(`{"order":"a"}`), Status: "timed_out", ExpectedOutcome: "ok", ObservedOutcome: "ok", Expected: json.RawMessage(`{"status":"held","units":1}`), Observed: json.RawMessage(`{"status":"pending","units":0}`), Attempts: 4},
		{Name: "decision accepted", Status: "not_started"},
	}}
}

func TestWorkloadTextLeadsWithUnreachedPrerequisiteAndRetainsObservation(t *testing.T) {
	e := Execution{Run: "TestCheckout#1:seed=1", Phases: []Phase{
		{Name: "place", Status: "completed", Faults: "disabled", Timeout: "1s"},
		phaseFailure(),
		{Name: "decide", Status: "not_started", Faults: "configured", Timeout: "2s"},
	}}
	text := e.PhasesString()
	for _, want := range []string{"workload prerequisites TestCheckout#1:seed=1: not reached", "phase observe allocation: failed", "faults configured", "started 1s; deadline 6s; finished 6s", "reservation held: timed out (inventory/t1 allocation); 4 logical calls", `arguments {"order":"a"}`, "driver request t1-phase2-query1", "expected outcome ok", `expected result {"status":"held","units":1}`, `last result {"status":"pending","units":0}`, "last outcome ok", "1 later requirement not started", "phase decide: not started"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "reservation held: timed out") > strings.Index(text, "phase place: completed") {
		t.Fatalf("successful setup hid failed prerequisite:\n%s", text)
	}
	if e.Phases[0].Name != "place" || string(e.Phases[1].Requirements[0].Observed) != `{"status":"pending","units":0}` {
		t.Fatal("rendering changed recorded execution evidence")
	}
}

func TestRunningWorkloadDoesNotClaimPrerequisiteWasReached(t *testing.T) {
	e := Execution{Run: "interrupted", Phases: []Phase{{Name: "setup", Status: "running", Faults: "disabled", Timeout: "2s", Requirements: []Requirement{{Name: "accepted", Kind: "call_outcome", App: "checkout", Tenant: "t1", Action: "place", Status: "running", ExpectedOutcome: "ok", Attempts: 1}}}}}
	text := e.PhasesString()
	if !strings.Contains(text, ": not reached") || !strings.Contains(text, "accepted: running") || !strings.Contains(text, "no answer observed") {
		t.Fatalf("interrupted workload became success:\n%s", text)
	}
	if (Execution{}).PhasesString() != "" {
		t.Fatal("an absent workload fabricated prerequisites")
	}
}

func TestWorkloadSummaryFoldsSuccessfulRunsAndRetainsFailureDetails(t *testing.T) {
	var executions []Execution
	for i := range 100 {
		executions = append(executions, Execution{Run: fmt.Sprint(i), Phases: []Phase{{Name: "setup", Status: "completed", Faults: "disabled", Timeout: "1s"}}})
	}
	executions = append(executions, Execution{Run: "failed", Phases: []Phase{phaseFailure()}})
	text := PhasesString(executions)
	for _, want := range []string{"101 runs; 100 reached; 1 not reached", "phase setup completed 100 times", "reservation held: timed out", "last result"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "phase setup") != 1 {
		t.Fatalf("summary repeated every successful run:\n%s", text)
	}
}

func TestWorkloadTextCapsLargeValueWithoutChangingJSON(t *testing.T) {
	value := json.RawMessage(`{"id":9007199254740993,"body":"` + strings.Repeat("x", 1000) + `"}`)
	p := phaseFailure()
	p.Requirements[0].Observed = value
	e := Execution{Run: "long", Phases: []Phase{p}}
	text := e.PhasesString()
	if !strings.Contains(text, "9007199254740993") || !strings.Contains(text, "JSON retains the full value") || strings.Contains(text, strings.Repeat("x", 1000)) {
		t.Fatalf("large value was rounded or not folded:\n%s", text)
	}
	encoded, err := json.Marshal(e)
	if err != nil || !strings.Contains(string(encoded), strings.Repeat("x", 1000)) {
		t.Fatal("text folding lost raw machine-readable evidence")
	}
}

func TestWorkloadTextSeparatesInFlightRequestFromLastQueryAnswer(t *testing.T) {
	p := phaseFailure()
	r := &p.Requirements[0]
	r.RequestID, r.ObservedRequestID, r.InFlight = "poll-2", "poll-1", true
	e := Execution{Run: "pending query", Phases: []Phase{p}}
	text := e.PhasesString()
	for _, want := range []string{"driver request poll-2 is in flight", "last answered driver request poll-1", "last outcome ok", `last result {"status":"pending","units":0}`} {
		if !strings.Contains(text, want) {
			t.Fatalf("pending request was conflated with an earlier answer; missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "no answer observed") {
		t.Fatal("current pending query erased a previously observed answer")
	}
}

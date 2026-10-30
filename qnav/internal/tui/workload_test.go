package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

func TestWorkloadFailurePrecedesCursorAndPreservesModelJudgment(t *testing.T) {
	n := &navproto.Node{
		Run: "TestCheckout#1:seed=1", ID: "@0",
		Receipt: receipt.Summary{Status: receipt.Pass, Scope: "collected_history", OtherExecutions: receipt.NotChecked},
		Execution: &runprofile.Execution{Run: "TestCheckout#1:seed=1", Phases: []runprofile.Phase{
			{Name: "reserve", Status: "failed", Faults: "configured", Timeout: "1s", Started: "0s", Deadline: "1s", Finished: "1s", Reason: "phase deadline reached", Requirements: []runprofile.Requirement{{Name: "allocation held", Kind: "query_result", App: "inventory", Tenant: "t1", Action: "allocation", Status: "timed_out", Expected: json.RawMessage(`{"status":"held"}`), Observed: json.RawMessage(`{"status":"pending"}`), ObservedOutcome: "ok", Attempts: 3}}},
			{Name: "cancel", Status: "not_started", Faults: "disabled", Timeout: "1s"},
		}},
		Model: []navproto.ActorState{{Actor: "checkout", State: []navproto.NamedValue{{Name: "placed", Value: "true"}}}},
	}
	m := New(Config{NoColor: true})
	m.w, m.h = 100, 40
	screen := m.runScreen(&entry{r: n})
	detail := strings.Join(screen.detail, "\n")
	for _, want := range []string{"whole-run judgment", "workload prerequisites TestCheckout#1:seed=1: not reached", "allocation held: timed out", `expected result {"status":"held"}`, `last result {"status":"pending"}`, "deadline 1s", "phase cancel: not started"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("missing %q:\n%s", want, detail)
		}
	}
	workloadAt := strings.Index(detail, n.Execution.PhasesString())
	if workloadAt < strings.Index(detail, "whole-run judgment") || workloadAt > strings.Index(detail, "steps so far") || workloadAt > strings.Index(detail, "model (checkout)") {
		t.Fatalf("failed prerequisite hidden behind cursor state:\n%s", detail)
	}
	if strings.Contains(strings.Join(screen.head, "\n"), "workload prerequisites") {
		t.Fatal("workload details expanded the fixed navigation header")
	}
	if !strings.Contains(strings.Join(screen.head, "\n"), "model pass · workload not reached: reserve") {
		t.Fatal("fixed header implied overall success despite unreached workload")
	}
	n.At = 9
	n.Model[0].State[0].Value = "false"
	next := m.runScreen(&entry{r: n})
	if !strings.Contains(strings.Join(next.detail, "\n"), n.Execution.PhasesString()) || n.Receipt.Status != receipt.Pass {
		t.Fatal("cursor movement changed workload evidence or promoted it into a model judgment")
	}
}

func TestWorkloadStatusRemainsVisibleAboveLongModelDetail(t *testing.T) {
	composition := &receipt.Composition{History: "sha256:run"}
	for i := range 40 {
		composition.Local = append(composition.Local, receipt.Component{Target: fmt.Sprintf("service%d", i), Applicable: true, Status: receipt.Pass})
	}
	n := &navproto.Node{Run: "long", Receipt: receipt.Summary{Status: receipt.Pass, Composition: composition}, Execution: &runprofile.Execution{Run: "long", Phases: []runprofile.Phase{{Name: "prepare", Status: "completed"}, {Name: "observe\n  reservation", Status: "running"}, {Name: "confirm", Status: "not_started"}}}}
	m := New(Config{NoColor: true})
	m.w, m.h = 100, 24
	screen := m.runScreen(&entry{r: n})
	if len(screen.detail) <= m.h {
		t.Fatal("test requires model detail longer than the visible terminal")
	}
	if len(screen.head) != 3 || !strings.Contains(strings.Join(screen.head, "\n"), "model pass · workload not reached: observe reservation") {
		t.Fatalf("phase status was hidden in scrollable model detail: %v", screen.head)
	}
	// Reaching prerequisites changes only the driver status, not the model
	// judgment or the meaning of the model's pass.
	for i := range n.Execution.Phases {
		n.Execution.Phases[i].Status = "completed"
	}
	complete := m.runScreen(&entry{r: n})
	if !strings.Contains(strings.Join(complete.head, "\n"), "model pass · prerequisites reached") || n.Receipt.Status != receipt.Pass {
		t.Fatalf("completed workload did not preserve model/driver scope: %v", complete.head)
	}
}

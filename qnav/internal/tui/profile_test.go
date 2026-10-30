package tui

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

func TestRunProfileIsWholeRunConfigurationInScrollableDetail(t *testing.T) {
	p := &runprofile.Profile{
		ID: "TestCheckout#2", Mode: "seeded", Chooser: "seeded", Parallel: 4,
		Validation: runprofile.Validation{Backend: "automatic", Eval: "go", Quint: "quint"},
		Seeds:      &runprofile.Seeds{Count: 5, Offset: 100, Short: true, TopUpTo: 50},
	}
	n := &navproto.Node{
		Run: "TestCheckout#2:seed=101", Profile: p,
		Execution: &runprofile.Execution{Run: "TestCheckout#2:seed=101", Simulations: []runprofile.Simulation{{Simulation: 1, Runtime: "deterministic", Horizon: "1s", Scheduler: &runprofile.Scheduler{MaxSteps: 100, Hang: "2s", StepsAfter: 100, StepLimitReached: true}}}},
		Receipt:   receipt.Summary{Status: receipt.Pass, Scope: "collected_history", OtherExecutions: receipt.NotChecked},
		Model:     []navproto.ActorState{{Actor: "checkout", State: []navproto.NamedValue{{Name: "balance", Value: "0"}}}},
	}
	m := New(Config{NoColor: true})
	m.w, m.h = 100, 30
	screen := m.runScreen(&entry{r: n})
	detail := strings.Join(screen.detail, "\n")
	profileText := strings.TrimSpace(p.String())
	profileAt := strings.Index(detail, profileText)
	if profileAt < 0 || profileAt < strings.Index(detail, "whole-run judgment") || profileAt > strings.Index(detail, "steps so far") {
		t.Fatalf("profile must appear between judgment and cursor steps:\n%s", detail)
	}
	if strings.Contains(strings.Join(screen.head, "\n"), profileText) {
		t.Fatal("profile expanded the fixed navigation header")
	}
	if !strings.Contains(detail, strings.TrimSpace(n.Execution.String())) || !strings.Contains(detail, "step limit reached") {
		t.Fatalf("whole-run simulator limits or exhaustion missing:\n%s", detail)
	}
	// The selected cursor changes what model state is shown, not the policy
	// that produced this run. A profile is configuration, never a new verdict.
	n.At = 10
	n.Model[0].State[0].Value = "999"
	next := m.runScreen(&entry{r: n})
	if !strings.Contains(strings.Join(next.detail, "\n"), profileText) || !strings.Contains(strings.Join(next.detail, "\n"), n.Execution.String()) || n.Receipt.Status != receipt.Pass {
		t.Fatal("cursor changed effective configuration or judgment")
	}
}

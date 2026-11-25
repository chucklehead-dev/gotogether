package tui

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// A node's effects are listed under the step they are attributed to, and
// the effects made by no step after the steps, each labelled an effect;
// the count is the steps'. Past the eight steps shown, a step's effects
// stay with their step.
func TestStepEffectsUnderTheirSteps(t *testing.T) {
	n := &navproto.Node{Run: "effects", Receipt: receipt.Summary{Status: receipt.Pass, Scope: "collected_history", OtherExecutions: receipt.NotChecked},
		Steps:        []string{"place(order=o1)", "pay(order=o1)"},
		StepEffects:  [][]string{nil, {"rpc.provider.request(amount=10) at seq 3", "rpc.provider.request(amount=10) at seq 5"}},
		Unattributed: []string{"queue.ship.publish(order=o1) at seq 7"},
	}
	m := New(Config{NoColor: true})
	m.w, m.h = 100, 40
	detail := strings.Join(m.runScreen(&entry{r: n}).detail, "\n")
	want := strings.Join([]string{
		"steps so far (2)",
		"  place(order=o1)",
		"  pay(order=o1)",
		"    ↳ effect rpc.provider.request(amount=10) at seq 3",
		"    ↳ effect rpc.provider.request(amount=10) at seq 5",
		"  effects made by no step so far (1)",
		"    ↳ effect queue.ship.publish(order=o1) at seq 7",
	}, "\n")
	if !strings.Contains(detail, want) {
		t.Fatalf("want\n%s\nin\n%s", want, detail)
	}

	n.Steps = []string{"s0", "s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9"}
	n.StepEffects = make([][]string, 10)
	n.StepEffects[1] = []string{"of s1"}
	n.StepEffects[9] = []string{"of s9"}
	n.Unattributed = nil
	detail = strings.Join(m.runScreen(&entry{r: n}).detail, "\n")
	if strings.Contains(detail, "of s1") || !strings.Contains(detail, "  s9\n    ↳ effect of s9") || !strings.Contains(detail, "steps so far (10)\n  … 2 before\n  s2") {
		t.Fatalf("a shown step's effects should follow it, a hidden one's be hidden:\n%s", detail)
	}
}

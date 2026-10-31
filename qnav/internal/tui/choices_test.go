package tui

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func TestModelChoiceScopeIsVisibleIndependentOfCursor(t *testing.T) {
	n := &navproto.Node{Run: "sample", Receipt: receipt.Summary{Status: receipt.Inconclusive, Scope: "collected_history", OtherExecutions: receipt.NotChecked, Actors: []receipt.Actor{{Actor: "a", Status: receipt.Inconclusive, Ordering: "reconstructed", Choices: &receipt.ChoiceSearch{Status: "exhausted", Limit: 3, Explored: 3, Confirmation: "not_performed"}}}}, Model: []navproto.ActorState{{Actor: "a"}}}
	m := New(Config{NoColor: true})
	m.w, m.h = 100, 60
	for _, at := range []int{0, 9} {
		n.At = at
		text := strings.Join(m.runScreen(&entry{r: n}).detail, "\n")
		for _, want := range []string{"whole-run judgment · independent of cursor", "model choices: exhausted", "3/3 expanded states", "selected path not confirmed"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %s: %s", want, text)
			}
		}
	}
}

func TestInitializationWitnessIsVisibleIndependentOfCursor(t *testing.T) {
	n := &navproto.Node{Run: "sample", Receipt: receipt.Summary{Status: receipt.Pass, Scope: "collected_history", OtherExecutions: receipt.NotChecked, Actors: []receipt.Actor{{Actor: "a", Status: receipt.Pass, Ordering: "reconstructed", Choices: &receipt.ChoiceSearch{Status: "matched", Limit: 10, Explored: 3, Confirmation: "path_confirmed", Initialization: &receipt.ChoiceInitialization{Action: "init", Expression: `all { leader' = 2 }`}}}}}, Model: []navproto.ActorState{{Actor: "a"}}}
	m := New(Config{NoColor: true})
	m.w, m.h = 100, 60
	for _, at := range []int{0, 9} {
		n.At = at
		text := strings.Join(m.runScreen(&entry{r: n}).detail, "\n")
		for _, want := range []string{"whole-run judgment · independent of cursor", "model choices: matched", "selected path confirmed by Quint", "initialization witness (init): all { leader' = 2 }"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %s: %s", want, text)
			}
		}
	}
}

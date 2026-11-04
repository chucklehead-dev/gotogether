package tui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// Clock evidence describes the whole judged history. A cursor's model state
// neither changes that evidence nor turns its conditional failure into a pass.
func TestClockReceiptWireAndCursorScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status receipt.Status
		clock  receipt.ClockEvidence
		want   []string
	}{
		{"conditional failure", receipt.Fail, receipt.ClockEvidence{Skew: "1ms", Source: "default", Constraints: 1, Recorders: []string{"host-a", "host-b"}, ClosestGap: "2ms", Conditional: true}, []string{"1ms", "default", "host-a", "host-b", "2ms", "negative conclusion assumes clocks within 1ms"}},
		{"zero-edge pass", receipt.Pass, receipt.ClockEvidence{Skew: "5ms", Source: "configured", Constraints: 0}, []string{"clock skew 5ms", "configured", "0 cross-recorder clock constraints"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := navproto.Node{
				Run: "clock history", ID: "0@2", At: 2,
				Receipt: receipt.Summary{Status: tc.status, Scope: "collected_history", OtherExecutions: receipt.NotChecked,
					Actors: []receipt.Actor{{Actor: "kv", Status: tc.status, Scope: "collected_history", Ordering: "searched", Search: &receipt.Search{Mode: "linearize", Goal: "one_conforming_history", Clock: &tc.clock}}}},
				Model: []navproto.ActorState{{Actor: "kv", State: []navproto.NamedValue{{Name: "revision", Value: "1"}}}},
			}
			wire, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(wire), `"clock":{`) {
				t.Fatalf("navigation JSON omitted the typed clock member: %s", wire)
			}
			var node navproto.Node
			if err := json.Unmarshal(wire, &node); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(node.Receipt, original.Receipt) {
				t.Fatalf("clock receipt changed across navigation JSON: %s", wire)
			}
			before, err := json.Marshal(node.Receipt)
			if err != nil {
				t.Fatal(err)
			}
			m := New(Config{NoColor: true})
			m.w, m.h = 120, 80
			for _, at := range []int{2, 0, 7} {
				node.At = at
				node.Model[0].State[0].Value = "999"
				detail := strings.Join(m.runScreen(&entry{r: &node}).detail, "\n")
				for _, want := range append([]string{"whole-run judgment · independent of cursor", "other executions were not checked"}, tc.want...) {
					if !strings.Contains(detail, want) {
						t.Fatalf("cursor %d lost clock evidence %q:\n%s", at, want, detail)
					}
				}
				if !strings.Contains(detail, node.Receipt.String()) {
					t.Fatalf("cursor %d dropped the whole clock receipt:\n%s", at, detail)
				}
				if node.Receipt.Status != tc.status {
					t.Fatal("model cursor changed whole-history judgment")
				}
			}
			after, err := json.Marshal(node.Receipt)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatalf("cursor movement rewrote clock evidence:\nbefore %s\nafter %s", before, after)
			}
		})
	}
}

// The receipt intentionally caps actor detail, but configuration must remain
// visible when the only history search belongs to a later actor.
func TestClockBoundVisibleBeyondActorDetailCap(t *testing.T) {
	actors := []receipt.Actor{
		{Actor: "first", Status: receipt.Pass},
		{Actor: "second", Status: receipt.Pass},
		{Actor: "third", Status: receipt.Pass},
		{Actor: "fourth", Status: receipt.Pass, Search: &receipt.Search{Mode: "linearize", Goal: "one_conforming_history", Clock: &receipt.ClockEvidence{Skew: "5ms", Source: "configured"}}},
	}
	n := &navproto.Node{Receipt: receipt.Summary{Status: receipt.Pass, Scope: "collected_history", OtherExecutions: receipt.NotChecked, Actors: actors}}
	m := New(Config{NoColor: true})
	m.w, m.h = 120, 80
	detail := strings.Join(m.runScreen(&entry{r: n}).detail, "\n")
	for _, want := range []string{"history clock skew bounds: 5ms (configured)", "1 more actors or partitions in the structured receipt"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("actor detail cap hid clock bound %q:\n%s", want, detail)
		}
	}
}

package receipt

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSearchCloneSnapshotsClockEvidence(t *testing.T) {
	var absent *Search
	if absent.Clone() != nil {
		t.Fatal("nil search became nonnil")
	}
	original := &Search{Mode: "linearize", Clock: &ClockEvidence{Skew: "1ms", Source: "default", Constraints: 2, Recorders: []string{"a", "b"}, ClosestGap: "3ms", Conditional: true}}
	clone := original.Clone()
	original.Clock.Recorders[0] = "changed"
	original.Clock.Skew = "5ms"
	if clone.Clock.Recorders[0] != "a" || clone.Clock.Skew != "1ms" {
		t.Fatal("search clone shares clock evidence")
	}
	clone.Clock.Recorders[1] = "snapshot edit"
	if original.Clock.Recorders[1] != "b" {
		t.Fatal("snapshot clock edit changed source")
	}
	zero := &Search{Mode: "linearize", Clock: &ClockEvidence{Skew: "5ms", Source: "configured"}}
	b, err := json.Marshal(zero)
	if err != nil || !strings.Contains(string(b), `"constraints":0`) || !strings.Contains(string(b), `"conditional":false`) || !strings.Contains(string(b), `"skew":"5ms"`) {
		t.Fatalf("zero-edge JSON lost explicit scope: %s, %v", b, err)
	}
}

func TestCompositionCloneSnapshotsClockEvidence(t *testing.T) {
	originalClock := &ClockEvidence{Skew: "1ms", Source: "default", Constraints: 1, Recorders: []string{"a", "b"}, ClosestGap: "2ms", Conditional: true}
	history := &History{Actors: []Actor{{Search: &Search{Mode: "linearize", Clock: originalClock}}}}
	original := &Composition{Local: []Component{{Judgment: history}}, Peers: []Component{{Judgment: history}}, Protocol: Protocol{Component: Component{Judgment: history}}}
	snapshot := original.Clone()
	originalClock.Recorders[0] = "reused"
	originalClock.Skew = "10ms"
	for _, h := range []*History{snapshot.Local[0].Judgment, snapshot.Peers[0].Judgment, snapshot.Protocol.Judgment} {
		clock := h.Actors[0].Search.Clock
		if clock.Recorders[0] != "a" || clock.Skew != "1ms" {
			t.Fatal("composition shares nested clock storage")
		}
	}
	snapshot.Local[0].Judgment.Actors[0].Search.Clock.Recorders[0] = "local snapshot"
	if snapshot.Peers[0].Judgment.Actors[0].Search.Clock.Recorders[0] != "a" || snapshot.Protocol.Judgment.Actors[0].Search.Clock.Recorders[0] != "a" || originalClock.Recorders[0] != "reused" {
		t.Fatal("component clock snapshots alias one another or source")
	}
}

func TestCompositionRendersComponentClockEvidence(t *testing.T) {
	negative := &History{Actors: []Actor{{Actor: "shared-actor", Search: &Search{Clock: &ClockEvidence{Skew: "1ms", Source: "default", Constraints: 1, Recorders: []string{"host-a", "host-b"}, ClosestGap: "2ms", Conditional: true}}}}}
	zero := &History{Actors: []Actor{{Actor: "shared-actor", Search: &Search{Clock: &ClockEvidence{Skew: "5ms", Source: "configured"}}}}}
	c := Composition{
		Local:    []Component{{Target: "ledger", Status: Fail, Applicable: true, Judgment: negative}},
		Peers:    []Component{{Target: "ledger->bank", Status: Pass, Applicable: true, Judgment: zero}},
		Protocol: Protocol{Component: Component{Target: "protocol", Status: Fail, Applicable: true, Judgment: negative}, Configured: true, Checked: 1, Limit: 1, Exhaustive: true},
	}
	text := c.String()
	for _, want := range []string{
		"component ledger actor shared-actor: clock skew 1ms (default); 1 cross-recorder clock constraints; recorders host-a, host-b; closest gap 2ms; negative conclusion assumes clocks within 1ms",
		"component ledger->bank actor shared-actor: clock skew 5ms (configured); 0 cross-recorder clock constraints",
		"component protocol actor shared-actor: clock skew 1ms (default); 1 cross-recorder clock constraints; recorders host-a, host-b; closest gap 2ms; negative conclusion assumes clocks within 1ms",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("component clock scope missing %q:\n%s", want, text)
		}
	}
	// An unnamed protocol still has a useful identity, and passing components
	// retain their effective bound even when it imposes no clock constraints.
	c.Protocol.Component = Component{Status: Pass, Applicable: true, Judgment: zero}
	if text := c.String(); !strings.Contains(text, "component system protocol actor shared-actor: clock skew 5ms (configured); 0 cross-recorder clock constraints") {
		t.Fatalf("unnamed passing protocol lost its clock bound:\n%s", text)
	}
}

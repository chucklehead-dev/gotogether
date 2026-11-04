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

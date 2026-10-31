package receipt

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
)

func TestCompositionCloneSnapshotsReusableAssessment(t *testing.T) {
	history := &History{Status: Fail, Actors: []Actor{{Actor: "worker", Status: Fail,
		Checks: []Check{{Kind: "conformance", Name: "history", Status: Pass}, {Kind: "invariant", Name: "firstBug", Status: Fail}},
		Search: &Search{Mode: "linearize"}, Choices: &ChoiceSearch{Status: "matched", Witness: []ChoiceStep{{Action: "apply", Seq: math.MaxUint64}}},
		Diagnostics:    []diagnostic.Result{{Property: "firstBug", Items: []diagnostic.Item{{Intent: "first", Evidence: diagnostic.Evidence{Matches: []diagnostic.Match{{Record: diagnostic.RecordRef{Seq: math.MaxUint64}}}}}}}},
		AssumedEffects: []Effect{{Args: map[string]any{"nested": map[string]any{"large": uint64(math.MaxUint64)}, "values": []int64{math.MaxInt64}}}},
	}}}
	original := &Composition{History: "sha256:first", Local: []Component{{Target: "consumer", Binding: "consumer.yaml", Status: Fail, Applicable: true, Judgment: history}}, Peers: []Component{{Target: "peer", Judgment: history}}, Protocol: Protocol{Component: Component{Target: "system", Judgment: history}, Counterexample: []Record{{ID: "first"}}}}
	snapshot := original.Clone()
	before, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	// A subsequent synchronous Assess call may update its reusable work buffers.
	original.Local[0].Target = "other"
	history.Actors[0].Checks[1].Name = "secondBug"
	history.Actors[0].Search.BudgetExhausted = true
	history.Actors[0].Choices.Witness[0].Seq = 2
	history.Actors[0].Diagnostics[0].Items[0].Intent = "second"
	history.Actors[0].Diagnostics[0].Items[0].Evidence.Matches[0].Record.Seq = 2
	history.Actors[0].AssumedEffects[0].Args["nested"].(map[string]any)["large"] = uint64(2)
	history.Actors[0].AssumedEffects[0].Args["values"].([]int64)[0] = 2
	original.Protocol.Counterexample[0].ID = "second"
	original.Peers[0].Target = "another"
	after, err := json.Marshal(snapshot)
	if err != nil || string(before) != string(after) {
		t.Fatalf("reused callback storage changed snapshot: %v\nbefore%s\nafter%s", err, before, after)
	}
	args := snapshot.Local[0].Judgment.Actors[0].AssumedEffects[0].Args
	if got := args["nested"].(map[string]any)["large"]; got != uint64(math.MaxUint64) {
		t.Fatalf("large argument changed type/precision: %T %v", got, got)
	}
	if got := args["values"]; !reflect.DeepEqual(got, []int64{math.MaxInt64}) {
		t.Fatalf("typed slice changed: %T %v", got, got)
	}
	// Snapshot edits must not mutate the returned assessment either.
	snapshot.Protocol.Judgment.Actors[0].Checks[0].Name = "changed"
	if history.Actors[0].Checks[0].Name != "history" {
		t.Fatal("snapshot shares nested check storage")
	}
}

func TestCompositionClonePreservesArgumentContainersAndCycles(t *testing.T) {
	type payload struct {
		Values []uint64
		Next   *payload
	}
	type namedPointer *payload
	value := &payload{Values: []uint64{math.MaxUint64}}
	value.Next = value
	cycle := map[string]any{"number": uint64(math.MaxUint64)}
	cycle["self"] = cycle
	args := map[string]any{"cycle": cycle, "payload": value, "namedPointer": namedPointer(value), "nilSlice": ([]int)(nil), "array": [1][]int{{3}}}
	original := &Composition{Local: []Component{{Judgment: &History{Actors: []Actor{{AssumedEffects: []Effect{{Args: args}}}}}}}}
	snapshot := original.Clone()
	copy := snapshot.Local[0].Judgment.Actors[0].AssumedEffects[0].Args
	copiedCycle := copy["cycle"].(map[string]any)
	copiedCycle["self"].(map[string]any)["number"] = uint64(7)
	if copiedCycle["number"] != uint64(7) || cycle["number"] != uint64(math.MaxUint64) {
		t.Fatal("cycle aliases source or lost its own identity")
	}
	copiedPayload := copy["payload"].(*payload)
	if copiedPayload == value || copiedPayload.Next != copiedPayload {
		t.Fatal("pointer cycle did not become independent")
	}
	if _, ok := copy["namedPointer"].(namedPointer); !ok {
		t.Fatalf("named pointer type lost: %T", copy["namedPointer"])
	}
	copiedPayload.Values[0] = 7
	if value.Values[0] != math.MaxUint64 {
		t.Fatal("payload slice aliases source")
	}
	copy["array"].([1][]int)[0][0] = 7
	if args["array"].([1][]int)[0][0] != 3 {
		t.Fatal("array's nested slice aliases source")
	}
	if got := copy["nilSlice"].([]int); got != nil {
		t.Fatal("nil slice became nonnil")
	}
}

func TestCompositionCloneNil(t *testing.T) {
	var c *Composition
	if c.Clone() != nil {
		t.Fatal("nil composition became an assessment")
	}
}

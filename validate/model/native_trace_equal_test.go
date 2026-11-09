package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/itf"
)

func nativeWireValue(raw string) (itf.Value, error) {
	trace, err := itf.Parse([]byte(`{"vars":["v"],"states":[{"v":` + raw + `}]}`))
	if err != nil {
		return itf.Value{}, err
	}
	return trace.States[0].Vars["v"], nil
}
func nativeUnitRecord() itf.Value {
	return itf.Value{Kind: itf.Record, Fields: map[string]itf.Value{"tag": {Kind: itf.Str, S: "UNIT"}}}
}

func TestNativeTraceUnitMarkerIsDirectionalAndExact(t *testing.T) {
	unit := itf.Value{Kind: itf.Tuple}
	for _, tc := range []struct {
		name, raw string
		expected  itf.Value
		equal     bool
	}{
		{"unit marker", `{"tag":"UNIT"}`, unit, true},
		{"ordinary empty tuple", `{"#tup":[]}`, unit, true},
		{"genuine tag record", `{"tag":"UNIT"}`, nativeUnitRecord(), true},
		{"record stays distinct from tuple", `{"#tup":[]}`, nativeUnitRecord(), false},
		{"record is not empty record", `{"tag":"UNIT"}`, itf.Value{Kind: itf.Record, Fields: map[string]itf.Value{}}, false},
		{"record is not empty list", `{"tag":"UNIT"}`, itf.Value{Kind: itf.List}, false},
		{"marker is not nonempty tuple", `{"tag":"UNIT"}`, itf.Value{Kind: itf.Tuple, Elems: []itf.Value{unit}}, false},
		{"empty record is not unit", `{}`, unit, false},
		{"empty list is not unit", `[]`, unit, false},
		{"case-sensitive tag", `{"tag":"Unit"}`, unit, false},
		{"non-string tag", `{"tag":0}`, unit, false},
		{"null tag", `{"tag":null}`, unit, false},
		{"extra field", `{"tag":"UNIT","extra":0}`, unit, false},
		{"metadata field", `{"tag":"UNIT","#meta":{"id":0}}`, unit, false},
		{"internal-looking field", `{"tag":"UNIT","__extra":0}`, unit, false},
		{"variant is not marker", `{"tag":"UNIT","value":{"#tup":[]}}`, unit, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := nativeWireValue(tc.raw)
			got := err == nil && nativeTraceEqual(tc.expected, actual)
			if got != tc.equal {
				t.Fatalf("equal=%v want=%v actual=%s error=%v", got, tc.equal, actual, err)
			}
		})
	}
}

func TestNativeTraceUnitNestedCollectionsAndBijections(t *testing.T) {
	unit, record := itf.Value{Kind: itf.Tuple}, nativeUnitRecord()
	text := itf.Value{Kind: itf.Str, S: "x"}
	variant := itf.Value{Kind: itf.Variant, Tag: "Encoded", Inner: &unit}
	expected := itf.Value{Kind: itf.Record, Fields: map[string]itf.Value{
		"phase":   variant,
		"literal": record,
		"list":    {Kind: itf.List, Elems: []itf.Value{unit, record}},
		"tuple":   {Kind: itf.Tuple, Elems: []itf.Value{variant, unit}},
		"set":     {Kind: itf.Set, Elems: []itf.Value{unit, record}},
		"map":     {Kind: itf.Map, Pairs: [][2]itf.Value{{unit, text}, {record, text}}},
	}}
	raw := `{"phase":{"tag":"Encoded","value":{"tag":"UNIT"}},"literal":{"tag":"UNIT"},"list":[{"tag":"UNIT"},{"tag":"UNIT"}],"tuple":{"#tup":[{"tag":"Encoded","value":{"tag":"UNIT"}},{"tag":"UNIT"}]},"set":{"#set":[{"tag":"UNIT"},{"#tup":[]}]},"map":{"#map":[[{"tag":"UNIT"},"x"],[{"#tup":[]},"x"]]}}`
	actual, err := nativeWireValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	// A greedy first match assigns the set/map marker to the expected tuple,
	// stranding the real tag record. Augmenting paths must reassign that choice.
	if !nativeTraceEqual(expected, actual) {
		t.Fatal("valid nested/reordered counterexample rejected")
	}
	for _, bad := range []string{strings.Replace(raw, `"Encoded"`, `"Other"`, 1), strings.Replace(raw, `"literal":{"tag":"UNIT"}`, `"literal":{"#tup":[]}`, 1), strings.Replace(raw, `"set":{"#set":[{"tag":"UNIT"},{"#tup":[]}]}`, `"set":{"#set":[{"#tup":[]},{"#tup":[]}]}`, 1)} {
		changed, err := nativeWireValue(bad)
		if err == nil && nativeTraceEqual(expected, changed) {
			t.Fatal("changed label or actual record accepted")
		}
	}
	// Maps must match whole pairs, never separately matched key/value bags.
	left, right := itf.Value{Kind: itf.Str, S: "left"}, itf.Value{Kind: itf.Str, S: "right"}
	pairs := itf.Value{Kind: itf.Map, Pairs: [][2]itf.Value{{unit, left}, {record, right}}}
	crossed, err := nativeWireValue(`{"#map":[[{"tag":"UNIT"},"left"],[{"#tup":[]},"right"]]}`)
	if err != nil {
		t.Fatal(err)
	}
	if nativeTraceEqual(pairs, crossed) {
		t.Fatal("map values crossed their keys")
	}
	missing, err := nativeWireValue(`{"#set":[{"tag":"UNIT"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if nativeTraceEqual(expected.Fields["set"], missing) {
		t.Fatal("set cardinality changed")
	}
	ordered := itf.Value{Kind: itf.List, Elems: []itf.Value{unit, text}}
	reversed, err := nativeWireValue(`["x",{"tag":"UNIT"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if nativeTraceEqual(ordered, reversed) {
		t.Fatal("ordered list became an unordered collection")
	}
}

func TestNativeEdgepublishCounterexampleUnitEncoding(t *testing.T) {
	dir := filepath.Join("testdata", "native")
	result, err := os.ReadFile(filepath.Join(dir, "edgepublish-unit.result.json"))
	if err != nil {
		t.Fatal(err)
	}
	trace, err := os.ReadFile(filepath.Join(dir, "edgepublish-unit.itf.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := classifyNative(result, trace, 1, 8)
	if err != nil || !got.violated {
		t.Fatalf("retained seven-state counterexample rejected: %+v %v", got, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(trace, &raw); err != nil {
		t.Fatal(err)
	}
	states := raw["states"].([]any)
	if len(states) != 7 {
		t.Fatalf("fixture lost actual native states: %d", len(states))
	}
	writer := states[1].(map[string]any)["simulate::edgePublish::writer"].(map[string]any)
	writer["gen"] = map[string]any{"#bigint": "999"}
	changed, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := classifyNative(result, changed, 1, 8); err == nil {
		t.Fatal("actual writer state mutation was accepted")
	}
	if _, err := classifyNative(result, trace, 0, 8); err == nil {
		t.Fatal("counterexample with proof exit was accepted")
	}
	if _, err := classifyNative(result, trace, 1, 5); err == nil {
		t.Fatal("counterexample beyond bound was accepted")
	}
}

package receipt

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPartitionCountsUseTypedSourceIdentities(t *testing.T) {
	s := Summary{Actors: []Actor{
		{Actor: "display acct=a", SourceActor: "ledger", PartitionInfo: &PartitionInfo{Key: "acct", Value: "a", Instance: 1}},
		{Actor: "display acct=b", SourceActor: "ledger", PartitionInfo: &PartitionInfo{Key: "acct", Value: "b", Instance: 1}},
		{Actor: "another", SourceActor: "another"},
	}}
	want := JudgmentCounts{Actors: 2, Judgments: 3, Partitions: 2}
	if got := s.Counts(); got != want {
		t.Fatalf("counts %+v want %+v", got, want)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Counts JudgmentCounts `json:"counts"`
	}
	if err := json.Unmarshal(data, &wire); err != nil || wire.Counts != want {
		t.Fatalf("wire %s: %v", data, err)
	}
	s.Actors = s.Actors[:1]
	data, _ = json.Marshal(s)
	json.Unmarshal(data, &wire)
	if wire.Counts != s.Counts() {
		t.Fatal("serialized stale counts")
	}
	unnamed := Summary{Actors: []Actor{
		{Actor: "default acct=a", PartitionInfo: &PartitionInfo{Key: "acct", Value: "a"}},
		{Actor: "default acct=b", PartitionInfo: &PartitionInfo{Key: "acct", Value: "b"}},
	}}
	if got := unnamed.Counts(); got.Actors != 1 || got.Partitions != 2 {
		t.Fatalf("empty source counted display names: %+v", got)
	}
}

func TestPartitionHistoryAndCompositionCloneRetainContext(t *testing.T) {
	original := PartitionInstance{ID: 1, SourceActor: "ledger", Key: "acct", Factoring: "checked_on_evaluated_prefixes", OriginalConstants: map[string]string{"ACCTS": `Set("a", "b")`, "LIMIT": "5"}, ReplaySubstitutions: []ConstantSubstitution{{Name: "ACCTS", Value: `Set("a")`, Basis: "unread_by_evaluated_prefixes"}}}
	s := Summary{Actors: []Actor{{SourceActor: "ledger", PartitionInfo: &PartitionInfo{Key: "acct", Value: json.Number("9007199254740993"), Instance: 1}}}, PartitionInstances: []PartitionInstance{original}}
	h := s.History()
	if len(h.PartitionInstances) != 1 || h.Counts() != s.Counts() {
		t.Fatal("history lost context")
	}
	c := &Composition{Local: []Component{{Judgment: &h}}}
	copy := c.Clone()
	ch := copy.Local[0].Judgment
	ch.PartitionInstances[0].OriginalConstants["LIMIT"] = "changed"
	ch.PartitionInstances[0].ReplaySubstitutions[0].Value = "changed"
	ch.Actors[0].PartitionInfo.Value = "changed"
	if h.PartitionInstances[0].OriginalConstants["LIMIT"] != "5" || h.PartitionInstances[0].ReplaySubstitutions[0].Value != `Set("a")` || h.Actors[0].PartitionInfo.Value != json.Number("9007199254740993") {
		t.Fatal("clone shares provenance")
	}
	replay := original.ReplayConstants()
	if !reflect.DeepEqual(replay, map[string]string{"ACCTS": `Set("a")`, "LIMIT": "5"}) {
		t.Fatalf("replay %+v", replay)
	}
	replay["LIMIT"] = "changed"
	if original.OriginalConstants["LIMIT"] != "5" {
		t.Fatal("replay mutates original")
	}
	text := original.String()
	for _, want := range []string{`source actor "ledger"`, `original whole-history instance: ACCTS = Set("a", "b")`, `effective replay override: ACCTS = Set("a")`, "evaluated prefixes of this run"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
}

func TestPartitionIdentityCloneRetainsExactJSONTypes(t *testing.T) {
	value := map[string]any{"n": json.Number("9007199254740993"), "items": []any{"a"}}
	info := &PartitionInfo{Key: "acct", Value: value, Instance: 2}
	copy := info.Clone()
	copy.Value.(map[string]any)["items"].([]any)[0] = "changed"
	if value["items"].([]any)[0] != "a" || copy.Value.(map[string]any)["n"] != value["n"] {
		t.Fatal("identity clone mutated/coerced value")
	}
}

func TestPartitionIdentityJSONRetainsExactNumbers(t *testing.T) {
	data := []byte(`{"key":"acct","value":9007199254740993,"instance":1}`)
	var info PartitionInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	if info.Value != json.Number("9007199254740993") {
		t.Fatalf("numeric identity coerced: %T %v", info.Value, info.Value)
	}
	roundTrip, err := json.Marshal(info)
	if err != nil || string(roundTrip) != string(data) {
		t.Fatalf("round trip %s: %v", roundTrip, err)
	}
	if err := json.Unmarshal([]byte(`{"key":"acct","value":"9007199254740993","instance":1}`), &info); err != nil {
		t.Fatal(err)
	}
	if info.Value != "9007199254740993" {
		t.Fatalf("string identity coerced: %T %v", info.Value, info.Value)
	}
}

func TestPartitionLabelsDoNotInventSourceIdentities(t *testing.T) {
	s := Summary{Actors: []Actor{
		{Actor: "ledger acct=a", Partition: "acct=a"},
		{Actor: "ledger acct=b", Partition: "acct=b"},
		{Actor: "ledger acct=c", SourceActor: "ledger", PartitionInfo: &PartitionInfo{Key: "acct", Value: "c"}},
		{Actor: "other"},
	}}
	want := JudgmentCounts{Actors: 2, Judgments: 4, Partitions: 3, UnknownSourceJudgments: 2}
	if got := s.Counts(); got != want {
		t.Fatalf("guessed/erased sources: %+v, want %+v", got, want)
	}
	text := s.String()
	if !strings.Contains(text, "2 known source actors") || !strings.Contains(text, "source identities unavailable for 2 judgments") {
		t.Fatalf("missing identity uncertainty: %s", text)
	}
}

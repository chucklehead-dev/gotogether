package tui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// The navigation protocol carries whole-history judgments alongside a model
// cursor. Decoding the wire representation must preserve their distinct scopes.
func TestPartitionReceiptWireAndCursorScope(t *testing.T) {
	const fixture = `{
  "run":"ledger seed=1", "id":"0@2", "at":2,
  "receipt":{
   "status":"pass", "scope":"collected_history", "otherExecutions":"not_checked",
   "actors":[
    {"actor":"ledger acct=a", "sourceActor":"ledger", "partition":"acct", "partitionInfo":{"key":"acct","value":"a","instance":1}, "status":"pass", "scope":"collected_history", "ordering":"reconstructed", "judgedBy":"ledger"},
    {"actor":"ledger acct=b", "sourceActor":"ledger", "partition":"acct", "partitionInfo":{"key":"acct","value":"b","instance":1}, "status":"pass", "scope":"collected_history", "ordering":"reconstructed", "judgedBy":"ledger"}
   ],
   "partitionInstances":[{
    "id":1, "sourceActor":"ledger", "key":"acct", "factoring":"checked_on_evaluated_prefixes",
    "originalConstants":{"ACCTS":"Set(\"a\", \"b\")", "LIMIT":"100"},
    "replaySubstitutions":[{"name":"ACCTS", "value":"Set(\"a\")", "basis":"unread_by_evaluated_prefixes"}]
   }]
  },
  "model":[{"actor":"ledger acct=a", "state":[{"name":"bal","value":"Map(\"a\" -> 5)"}]}]
 }`
	var n navproto.Node
	if err := json.Unmarshal([]byte(fixture), &n); err != nil {
		t.Fatal(err)
	}
	wantCounts := receipt.JudgmentCounts{Actors: 1, Partitions: 2, Judgments: 2}
	if got := n.Receipt.Counts(); got != wantCounts {
		t.Fatalf("partition display labels became source actors: %+v", got)
	}
	before, err := json.Marshal(n.Receipt)
	if err != nil {
		t.Fatal(err)
	}
	m := New(Config{NoColor: true})
	m.w, m.h = 120, 80
	detail := strings.Join(m.runScreen(&entry{r: &n}).detail, "\n")
	for _, want := range []string{
		"whole-run judgment · independent of cursor",
		"1 source actor, 2 partition judgments, 2 judgments total",
		`source actor "ledger", key acct; factoring checked on evaluated prefixes of this run`,
		`original whole-history instance: ACCTS = Set("a", "b"), LIMIT = 100`,
		`effective replay override: ACCTS = Set("a") (unread by evaluated prefixes, this run only)`,
		"other executions were not checked",
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("missing %q:\n%s", want, detail)
		}
	}
	// At init, the displayed model no longer contains either account. The receipt
	// still describes both partitions and the full history's original instance.
	n.At, n.ID = 0, "@0"
	n.Model[0].State = []navproto.NamedValue{{Name: "bal", Value: "Map()"}}
	next := strings.Join(m.runScreen(&entry{r: &n}).detail, "\n")
	if !strings.Contains(next, n.Receipt.String()) {
		t.Fatalf("cursor dropped whole-history partition receipt:\n%s", next)
	}
	after, err := json.Marshal(n.Receipt)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("cursor rewrote judgment:\nbefore %s\nafter %s", before, after)
	}
	// Re-encoding an agent-facing node must retain exact literals and typed links,
	// as well as deriving counts from the judgments rather than the visible cursor.
	wire, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var decoded navproto.Node
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Receipt, n.Receipt) {
		t.Fatalf("receipt changed across JSON round trip:\n%s", wire)
	}
	var fields struct {
		Receipt struct {
			Counts receipt.JudgmentCounts `json:"counts"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	if fields.Receipt.Counts != wantCounts {
		t.Fatalf("wire scope used cursor counts: %+v", fields.Receipt.Counts)
	}
}

func TestComponentPartitionScopeVisibleInNavigator(t *testing.T) {
	history := &receipt.History{Status: receipt.Pass, Scope: "collected_history", OtherExecutions: receipt.NotChecked,
		Actors:             []receipt.Actor{{Actor: "component acct=a", SourceActor: "component", Partition: "acct=a", PartitionInfo: &receipt.PartitionInfo{Key: "acct", Value: "a", Instance: 1}, Status: receipt.Pass}},
		PartitionInstances: []receipt.PartitionInstance{{ID: 1, SourceActor: "component", Key: "acct", Factoring: "checked_on_evaluated_prefixes", OriginalConstants: map[string]string{"ACCTS": `Set("a", "b")`}, ReplaySubstitutions: []receipt.ConstantSubstitution{{Name: "ACCTS", Value: `Set("a")`, Basis: "unread_by_evaluated_prefixes"}}}},
	}
	node := &navproto.Node{Run: "run", Receipt: receipt.Summary{Status: receipt.Pass, Scope: "collected_history", OtherExecutions: receipt.NotChecked, Composition: &receipt.Composition{Local: []receipt.Component{{Target: "component", Status: receipt.Pass, Judgment: history}}}}}
	m := New(Config{NoColor: true})
	m.w, m.h = 100, 60
	text := strings.Join(m.runScreen(&entry{r: node}).detail, "\n")
	for _, want := range []string{"component component partition scope (instance IDs local to this history)", `original whole-history instance: ACCTS = Set("a", "b")`, `effective replay override: ACCTS = Set("a")`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
}

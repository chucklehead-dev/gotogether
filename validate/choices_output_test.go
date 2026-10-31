package validate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func TestChoicesDocumentAndReceiptKeepExactWitness(t *testing.T) {
	r := &Result{Actor: "a", Conforms: Pass, Choices: &ChoiceSearch{Status: "matched", Limit: 20, Explored: 4, Exhaustive: true, Confirmation: "path_confirmed", Witness: []ChoiceStep{{Index: 1, Action: "pick", Expression: `all { x' = 9007199254740993 }`, Recorder: "source", Seq: ^uint64(0)}}}}
	d := r.Doc()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"choices"`, `"confirmation":"path_confirmed"`, `9007199254740993`, `18446744073709551615`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s: %s", want, b)
		}
	}
	if d.Receipt.Search != nil || d.Receipt.Ordering != "reconstructed" || d.Receipt.Choices == nil {
		t.Fatalf("model choices relabeled as history ordering: %+v", d.Receipt)
	}
	d.Choices.Witness[0].Expression = "changed"
	if r.Choices.Witness[0].Expression == "changed" || d.Receipt.Choices.Witness[0].Expression == "changed" {
		t.Fatal("output snapshots share mutable witness storage")
	}
	text := (&Report{Actors: []*Result{r}}).Receipt().String()
	if !strings.Contains(text, "model choices: matched") || !strings.Contains(text, "selected path confirmed by Quint") || !strings.Contains(text, "exhaustive Go search") {
		t.Fatalf("scope omitted from shared rendering: %s", text)
	}
}

func TestChoicesExhaustionIsNotBackendConfirmedFailure(t *testing.T) {
	r := &Result{Actor: "a", Conforms: NotEvaluated, noVerdict: "finite model-choice limit exhausted", Choices: &ChoiceSearch{Status: "exhausted", Limit: 2, Explored: 2, Confirmation: "not_performed"}}
	a := r.Receipt()
	if a.Status != receipt.Inconclusive || a.Checks[0].Status != receipt.Inconclusive || a.Choices.Exhaustive {
		t.Fatalf("exhaustion became a conclusion: %+v", a)
	}
	text := a.Choices.String()
	if !strings.Contains(text, "incomplete") || !strings.Contains(text, "selected path not confirmed") || strings.Contains(text, "confirmed by Quint") {
		t.Fatalf("confirmation overstated: %s", text)
	}
}

package binding

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A parameter whose field the step lacks says which field, what the step
// has, and the args: fix, not text/template's "map has no entry for key".
func TestMissingFieldMessage(t *testing.T) {
	skip.Quint(t)
	m, err := LoadModel("quint", "testdata/procarg.qnt", "procarg")
	if err != nil {
		t.Fatal(err)
	}
	tr := &qtrace.Trace{Actor: "a", Processes: []string{"p"}, Steps: []qtrace.Step{
		{Action: "crash", Process: "p", Outcome: "ok", Seq: 1, Source: "line 1", Args: map[string]any{"node": "b"}},
	}}
	_, err = (&Binding{Spec: "testdata/procarg.qnt", Module: "procarg"}).Plan(m, tr)
	for _, want := range []string{`step 1 (crash, line 1): parameter process`, `reads the step's field "process"`, "it has: node", "args: { process: '{{.FIELD}}' }"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in %v", want, err)
		}
	}
	if err != nil && strings.Contains(err.Error(), "no entry for key") {
		t.Errorf("template text in %v", err)
	}
}

// A process change without a restart action numbers the step as every
// other message does (from 1), says where it was recorded, and names the
// binding key to add with the model's candidates for it.
func TestProcessChangeMessage(t *testing.T) {
	m := &Model{Module: "m", Actions: map[string][]Param{
		"init": nil, "step": nil, "Deposit": {{Name: "amount"}}, "Restart": nil, "keep": nil,
	}, Helpers: map[string]bool{"keep": true}}
	tr := &qtrace.Trace{Actor: "a", Processes: []string{"p1", "p2"}, Steps: []qtrace.Step{
		{Action: "Deposit", Process: "p1", Outcome: "ok", Seq: 1, Source: "run.jsonl:1", Args: map[string]any{"amount": 1}},
		{Action: "Deposit", Process: "p1", Outcome: "ok", Seq: 2, Source: "run.jsonl:2", Args: map[string]any{"amount": 2}},
		{Action: "Deposit", Process: "p2", Outcome: "ok", Seq: 3, Source: "run.jsonl:3", Args: map[string]any{"amount": 3}},
	}}
	_, err := (&Binding{Module: "m"}).Plan(m, tr)
	for _, want := range []string{"step 3 (Deposit, seq 3, run.jsonl:3): process changed from p1 to p2",
		"add `restart: <action>` to the binding", "(the model's: Restart)"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in %v", want, err)
		}
	}
}

// A step whose outcome the binding cannot map is numbered from 1, with
// its seq and where it was recorded.
func TestOutcomeErrorNumbersSteps(t *testing.T) {
	tr := &qtrace.Trace{Actor: "a", Processes: []string{"p"}, Steps: []qtrace.Step{
		{Action: "Deposit", Process: "p", Outcome: "ok", Seq: 1, Source: "run.jsonl:1", Args: map[string]any{"amount": 1}},
		{Action: "Deposit", Process: "p", Outcome: "error", Error: "boom", Seq: 2, Source: "run.jsonl:2", Args: map[string]any{"amount": 2}},
	}}
	b := &Binding{Module: "m", Actions: map[string]Action{"Deposit": {Args: map[string]string{"amount": "{{.amount}}"}}}}
	_, err := b.Plan(nil, tr)
	if want := "step 2 (seq 2, run.jsonl:2): Deposit failed"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("want %q in %v", want, err)
	}
}

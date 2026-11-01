package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
)

func TestModelDocumentUsesFactsRatherThanDisplayText(t *testing.T) {
	r := &Report{Binding: "model.yaml", Code: 0, Text: "  FAIL    misleading: changed English text\n", Graph: &GraphResult{States: 17, Starts: 1, Transitions: 272, Depth: 1, Complete: true, Bound: GraphBound{States: 100000}, Unfair: []string{"retry"}}, Invariants: []Verdict{{Name: "safe", Class: "holds", Held: true, Bound: Bound{Kind: "complete", States: 17}}}, Possibles: []PossibleVerdict{{Possible: binding.Possible{Name: "accept"}, Found: true}}}
	d := r.Doc()
	if d.Graph == nil || d.Graph.Scope != "model_state_graph" || !d.Graph.Complete || d.Graph.States != 17 || d.Graph.Transitions != 272 || d.Graph.Bound.States != 100000 {
		t.Fatalf("lost native graph: %+v", d.Graph)
	}
	if d.Checks[0].Verdict != "holds" || !d.Checks[0].Bound.Proves || d.Checks[1].Verdict != "reached" || d.Verdict != "pass" {
		t.Fatalf("text overrode facts: %+v", d)
	}
	r.Text = "entirely different words"
	again := r.Doc()
	if !reflect.DeepEqual(d.Checks, again.Checks) || !reflect.DeepEqual(d.Graph, again.Graph) {
		t.Fatal("wording changed machine facts")
	}
	r.Graph.Unfair[0] = "changed"
	if d.Graph.Unfair[0] != "retry" {
		t.Fatal("graph snapshot shares policy slices")
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err = json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["graph"].(map[string]any)["complete"] != true {
		t.Fatalf("graph absent on wire: %s", b)
	}
}

func TestModelDocumentPreservesPartialAndSampledScope(t *testing.T) {
	for _, cut := range []string{"states", "depth", "unsupported", "error"} {
		r := &Report{Code: 2, Graph: &GraphResult{States: 3, Starts: 1, Transitions: 2, Depth: 2, Cut: cut, Stopped: "source detail", Bound: GraphBound{States: 3, Depth: 2}}}
		d := r.Doc()
		if d.Graph.Complete || d.Graph.Cut != cut || d.Graph.Stopped != "source detail" || d.Graph.Bound.Depth != 2 {
			t.Fatalf("partial scope changed: %+v", d.Graph)
		}
	}
	sampled := (&Report{Invariants: []Verdict{{Name: "safe", Class: "holds", Held: true, Bound: Bound{Kind: "sampled", Traces: 11, Steps: 7}}}}).Doc()
	if sampled.Graph != nil || sampled.Checks[0].Bound.Proves || sampled.Checks[0].Bound.Kind != "sampled" || sampled.Checks[0].Bound.Traces != 11 {
		t.Fatalf("sample became graph proof: %+v", sampled)
	}
}

func TestModelDocumentPossibleSearchAndDisagreement(t *testing.T) {
	r := &Report{Possibles: []PossibleVerdict{{Possible: binding.Possible{Name: "partial"}, Eval: &PossibleResult{Nodes: 2, Depth: 1, Cut: "states"}}, {Possible: binding.Possible{Name: "impossible"}, Impossible: true, Eval: &PossibleResult{Complete: true, Nodes: 3, Depth: 2}}}}
	d := r.Doc()
	if d.Checks[0].Possible.Impossible || d.Checks[0].Possible.Search.Complete || d.Checks[0].Possible.Search.Cut != "states" || !d.Checks[1].Possible.Impossible || !d.Checks[1].Possible.Search.Complete {
		t.Fatalf("partial absence confused with impossible: %+v", d.Checks)
	}
	r.EvaluatorBug = "a reached state was missing"
	r.Invariants = []Verdict{{Name: "safe", Held: true, Bound: Bound{Kind: "complete", States: 3}}}
	bad := r.Doc()
	if bad.Checks[0].Verdict != "unchecked" || bad.Checks[0].Bound.Proves {
		t.Fatalf("voided results still assert proof: %+v", bad.Checks)
	}
}

func TestModelDocumentNoVerdictAndSkipAreNotToolFailureOrSuccess(t *testing.T) {
	unknown := (&Report{Code: 2, Graph: &GraphResult{Cut: "unsupported", Stopped: "unsupportedoperator"}, Invariants: []Verdict{{Name: "safe", Unchecked: "unsupportedoperator", Bound: Bound{Kind: "stopped"}}}}).Doc()
	if unknown.Verdict != "no verdict" || unknown.Error != "" {
		t.Fatalf("unperformed evidence became toolfailure: %+v", unknown)
	}
	skipped := (&Report{Skipped: "replay-only model"}).Doc()
	if skipped.Verdict != "no verdict" || skipped.SkipReason != "replay-only model" || len(skipped.Checks) != 0 {
		t.Fatalf("skip became performed success: %+v", skipped)
	}
	failed := (&Report{Code: 1, Invariants: []Verdict{{Name: "bad", Error: "division by zero"}}}).Doc()
	if failed.Verdict != "error" || failed.Error != "division by zero" {
		t.Fatalf("runtime error became modelviolation: %+v", failed)
	}
}

func TestPossibleDocumentSeparatesGoalFromExactWitness(t *testing.T) {
	literal := `"` + strings.Repeat("long exact value ", 15) + `"`
	goal := PossibleGoal{Kind: "action_sequence", Steps: []PossibleAction{{Action: "withdraw", Args: []PossibleArgument{{Name: "result", Value: `"refused"`}}}}}
	witness := []PossibleWitnessStep{{Kind: "action", Action: "deposit", Args: []PossibleArgument{{Name: "memo", Value: literal}, {Name: "amount", Value: "9007199254740993"}}}, {Kind: "model_step"}, {Kind: "action", Action: "withdraw", Args: []PossibleArgument{{Name: "result", Value: `"refused"`}, {Name: "readPos", Value: "1"}}}}
	r := &Report{Possibles: []PossibleVerdict{{Possible: binding.Possible{Name: "misleading predicate name"}, Goal: goal, Found: true, Eval: &PossibleResult{Found: true, Path: []string{"abbreviated display"}, Witness: witness}}}}
	d := r.Doc()
	p := d.Checks[0].Possible
	if p.Goal.Kind != "action_sequence" || len(p.Goal.Steps[0].Args) != 1 || p.Goal.Steps[0].Args[0].Name != "result" {
		t.Fatalf("goal constraints lost: %+v", p.Goal)
	}
	if len(p.Search.Path) != 3 || p.Search.Path[0].Args[0].Value != literal || p.Search.Path[0].Args[1].Value != "9007199254740993" || p.Search.Path[1].Kind != "model_step" || p.Search.Path[1].Action != "" || p.Search.Path[2].Args[1].Name != "readPos" {
		t.Fatalf("exact/unknown witness lost: %+v", p.Search.Path)
	}
	r.Possibles[0].Eval.Path[0] = "entirely different display"
	if again := r.Doc(); !reflect.DeepEqual(p.Search.Path, again.Checks[0].Possible.Search.Path) {
		t.Fatal("human display path changed structured facts")
	}
	goal.Steps[0].Args[0].Value = "changed"
	witness[0].Args[0].Value = "changed"
	if p.Goal.Steps[0].Args[0].Value != `"refused"` || p.Search.Path[0].Args[0].Value != literal {
		t.Fatal("document aliases native goal or witness")
	}
	var wire map[string]any
	if err := json.Unmarshal(d.JSON(), &wire); err != nil {
		t.Fatal(err)
	}
	if wire["format"] != "gotogether.report/2" {
		t.Fatalf("wrong report version: %s", d.JSON())
	}
}

func TestPossibleDocumentKeepsPredicateAndBackendEvidenceSeparate(t *testing.T) {
	goal := PossibleGoal{Kind: "predicate", Predicate: "ready"}
	r := &Report{Possibles: []PossibleVerdict{
		{Found: true, Eval: &PossibleResult{Goal: goal, Found: true}},
		{Goal: goal, Found: true, Eval: &PossibleResult{Found: false, Cut: "depth", Nodes: 3, Depth: 1}},
		{Goal: goal, Found: true, EvalErr: "unsupported by evaluator"},
	}}
	d := r.Doc()
	initial := d.Checks[0].Possible
	if initial.Goal.Kind != "predicate" || initial.Goal.Predicate != "ready" || !initial.Search.Found || len(initial.Search.Path) != 0 {
		t.Fatalf("initial predicate became execution: %+v", initial)
	}
	sampled := d.Checks[1].Possible
	if !sampled.Found || sampled.Search.Found || sampled.Search.Complete || sampled.Impossible || len(sampled.Search.Path) != 0 {
		t.Fatalf("Quint witness became evaluator witness: %+v", sampled)
	}
	unsupported := d.Checks[2].Possible
	if unsupported.Goal.Kind != "predicate" || unsupported.Search != nil || unsupported.EvaluatorError == "" {
		t.Fatalf("unsupported evaluator lost goal: %+v", unsupported)
	}
}

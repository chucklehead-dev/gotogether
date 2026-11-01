package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A check's name is application vocabulary, not evidence that its named action
// ran. The JSON goal and witness must distinguish readiness from execution.
func TestSimulatePossibleGoalAndWitnessSemantics(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	spec := `module goals {
 var count: int
 action init = count' = 0
 action increment(amount: int, result: str): bool = all {
  amount > 0, result == "ok", count' = count + amount,
 }
 action step = increment(9007199254740993, "ok")
 val nonNegative = count >= 0
}
`
	binding := `spec: goals.qnt
module: goals
actions:
 increment: {}
invariants: [nonNegative]
simulate:
 holds: [nonNegative]
 samples: 1
 steps: 1
 search: {states: 10, depth: 1}
 possible:
  - name: increment.ok
    when: count == 0
  - name: increased
    when: count == 9007199254740993
  - name: performed
    steps: ['increment(result = "ok")']
  - name: negative
    when: count < 0
`
	for name, data := range map[string]string{"goals.qnt": spec, "goals.yaml": binding} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"-binding", filepath.Join(dir, "goals.yaml")}
	run := func(jsonOutput bool) string {
		t.Helper()
		argv := append([]string(nil), args...)
		if jsonOutput {
			argv = append(argv, "-json")
		}
		return captureStdout(t, func() {
			if code, err := simulate(argv); code != 1 || err != nil {
				t.Errorf("bounded unmet requirement: exit %d, %v", code, err)
			}
		})
	}
	out := run(true)
	var doc validate.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("JSON report: %v\n%s", err, out)
	}
	if doc.Format != "gotogether.report/2" || doc.Command != "simulate" || doc.Exit != 1 {
		t.Fatalf("report envelope: %s", out)
	}
	possibles := map[string]*validate.DocPossible{}
	for _, check := range doc.Checks {
		if check.Possible != nil {
			possibles[check.Name] = check.Possible
		}
	}
	initial, after, sequence, missing := possibles["increment.ok"], possibles["increased"], possibles["performed"], possibles["negative"]
	if initial == nil || after == nil || sequence == nil || missing == nil {
		t.Fatalf("missing checks: %s", out)
	}
	if initial.Goal == nil || initial.Goal.Kind != "predicate" || initial.Goal.Predicate != "count == 0" || !initial.Found || initial.Search == nil || !initial.Search.Found || len(initial.Search.Path) != 0 {
		t.Fatalf("initial predicate invented execution: %+v", initial)
	}
	if after.Goal == nil || after.Goal.Kind != "predicate" || after.Goal.Predicate != "count == 9007199254740993" || !after.Found {
		t.Fatalf("later predicate: %+v", after)
	}
	if sequence.Goal == nil || sequence.Goal.Kind != "action_sequence" || len(sequence.Goal.Steps) != 1 || sequence.Goal.Steps[0].Action != "increment" || len(sequence.Goal.Steps[0].Args) != 1 || sequence.Goal.Steps[0].Args[0] != (validate.DocPossibleArgument{Name: "result", Value: `"ok"`}) || !sequence.Found {
		t.Fatalf("sequence constraint: %+v", sequence)
	}
	for name, p := range map[string]*validate.DocPossible{"predicate": after, "sequence": sequence} {
		if p.Search == nil || !p.Search.Found || len(p.Search.Path) != 1 {
			t.Fatalf("%s missing witness: %+v", name, p.Search)
		}
		step := p.Search.Path[0]
		if step.Kind != "action" || step.Action != "increment" || len(step.Args) != 2 || step.Args[0] != (validate.DocPossibleArgument{Name: "amount", Value: "9007199254740993"}) || step.Args[1] != (validate.DocPossibleArgument{Name: "result", Value: `"ok"`}) {
			t.Fatalf("%s lost exact executed arguments: %+v", name, step)
		}
	}
	if missing.Goal == nil || missing.Goal.Kind != "predicate" || missing.Found || missing.Impossible || missing.Search == nil || missing.Search.Found || missing.Search.Complete || missing.Search.Cut != "depth" {
		t.Fatalf("bounded absence became impossibility: %+v", missing)
	}
	text := run(false)
	for _, want := range []string{"possible increment.ok: predicate holds initially", "possible increased: predicate holds after increment(amount = 9007199254740993, result = \"ok\")", "possible performed: action sequence executed by increment(amount = 9007199254740993, result = \"ok\")", "not found within the bound"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}
}

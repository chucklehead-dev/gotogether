package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

func TestStepActionCoverageCompositeCalls(t *testing.T) {
	skip.Quint(t)
	for _, tc := range []struct {
		name, step string
		want       []string
	}{
		{"composite", "all { first, second }", []string{"first", "second"}},
		{"duplicate", "all { guard, guard, first, second }", []string{"guard", "first", "second"}},
		{"wrapper", "all { wrapper, second }", []string{"wrapper", "second"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := "module step_actions {\nvar left: bool\nvar right: bool\naction init = all { left' = false, right' = false }\naction first = left' = true\naction second = right' = true\naction wrapper = first\naction guard = true\naction step = " + tc.step + "\n}\n"
			if err := os.WriteFile(filepath.Join(dir, "model.qnt"), []byte(src), 0600); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "model.yaml")
			if err := os.WriteFile(file, []byte("spec: model.qnt\nmodule: step_actions\nactions:\n  first: {}\n  second: {}\n  wrapper: {}\n  guard: {}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			graph := graphOf(t, file, GraphSearch{})
			if !graph.Complete || len(graph.Actions) != len(tc.want) {
				t.Fatalf("graph: %+v", graph)
			}
			for i, name := range tc.want {
				if got := graph.Actions[i]; got.Action != name || got.Transitions != graph.Transitions || got.States != graph.States {
					t.Fatalf("action %s: %+v; graph states=%d transitions=%d", name, got, graph.States, graph.Transitions)
				}
			}
		})
	}
}

func TestCountStepActionsUsesStaticRoster(t *testing.T) {
	actions := []ActionCoverage{{Action: "first"}, {Action: "second"}, {Action: "never"}}
	graph := &qeval.Graph{Labels: [][]qeval.Event{
		{{Action: "helper"}, {Action: "first"}, {Action: "first"}, {Action: "second"}},
		{{Action: "first"}},
	}, Out: [][]qeval.Edge{{{Label: 0}, {Label: 1}}, {{Label: 0}}}}
	countStepActions(actions, graph)
	want := []ActionCoverage{{Action: "first", Transitions: 3, States: 2}, {Action: "second", Transitions: 2, States: 2}, {Action: "never"}}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("coverage=%+v, want %+v", actions, want)
	}
}

func TestStepActionReachabilitySummary(t *testing.T) {
	for _, tc := range []struct {
		name           string
		report         *Report
		status, reason string
	}{
		{"nil", nil, "unknown", "no state graph"},
		{"no graph", &Report{}, "unknown", "no state graph"},
		{"empty", &Report{Graph: &GraphResult{Complete: true}}, "unknown", "no identified"},
		{"complete reached", &Report{Graph: &GraphResult{Complete: true, Actions: []ActionCoverage{{Action: "go", Transitions: 1}}}}, "reachable", "witnessed within"},
		{"partial reached", &Report{Graph: &GraphResult{Cut: "states", Actions: []ActionCoverage{{Action: "go", Transitions: 1}}}}, "reachable", "witnessed within"},
		{"disabled", &Report{Graph: &GraphResult{Complete: true, Actions: []ActionCoverage{{Action: "go"}}}}, "unreachable", "never executes: go"},
		{"depth reached", &Report{Graph: &GraphResult{Cut: "depth", Bound: GraphBound{Depth: 2}, Actions: []ActionCoverage{{Action: "go", Transitions: 1}}}}, "reachable", "witnessed within"},
		{"depth missing", &Report{Graph: &GraphResult{Cut: "depth", Bound: GraphBound{Depth: 2}, Actions: []ActionCoverage{{Action: "go"}}}}, "unknown", "go"},
		{"unsupported missing", &Report{Graph: &GraphResult{Cut: "unsupported", Stopped: "unsupported op", Actions: []ActionCoverage{{Action: "go"}}}}, "unknown", "unsupported op"},
		{"error reached", &Report{Graph: &GraphResult{Cut: "error", Stopped: "bad model", Actions: []ActionCoverage{{Action: "go", Transitions: 1}}}}, "unknown", "bad model"},
		{"disagreement reached", &Report{EvaluatorBug: "wrong successor", Graph: &GraphResult{Actions: []ActionCoverage{{Action: "go", Transitions: 1}}}}, "unknown", "wrong successor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := tc.report.StepActionReachability()
			if strings.Contains(summary.Reason, "proof") {
				t.Fatalf("reachability reason claims a proof: %s", summary.Reason)
			}
			if summary.Status != tc.status || !strings.Contains(summary.Reason, tc.reason) {
				t.Fatalf("summary %+v, want %s with %s", summary, tc.status, tc.reason)
			}
		})
	}
}

func TestStepActionDocumentPreservesFactsAndCopies(t *testing.T) {
	report := &Report{Code: 0, Graph: &GraphResult{Cut: "states", Bound: GraphBound{States: 1}, Actions: []ActionCoverage{{Action: "first", Transitions: 1, States: 1}, {Action: "never"}}}}
	summary := report.StepActionReachability()
	doc := report.Doc()
	if doc.Verdict != "pass" || len(doc.Checks) != 0 {
		t.Fatal("observational coverage changed model verdict", doc)
	}
	got := doc.Graph.StepActions
	if got.Status != "unknown" || len(got.Missing) != 1 || got.Missing[0] != "never" || len(got.Actions) != 2 || got.Actions[1].Transitions != 0 {
		t.Fatalf("lost native evidence: %+v", got)
	}
	report.Graph.Actions[0].Action = "changed"
	summary.Actions[1].Action = "also changed"
	summary.Missing[0] = "changed missing"
	if got.Actions[0].Action != "first" || got.Actions[1].Action != "never" || got.Missing[0] != "never" {
		t.Fatal("document shares source slices")
	}
	b, err := json.Marshal((&Report{Graph: &GraphResult{Complete: true}}).Doc())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"actions":[]`) || !strings.Contains(string(b), `"missing":[]`) {
		t.Fatalf("empty evidence must be arrays: %s", b)
	}
	report.EvaluatorBug = "disagreement"
	if got := report.Doc().Graph.StepActions; got.Status != "unknown" || !strings.Contains(got.Reason, "disagreement") {
		t.Fatalf("evaluator disagreement: %+v", got)
	}
}

// A step that delegates to a wrapper has its wrapper's static roster, while
// liveness still labels its edges with the outer wrapper. Keep those contracts
// separate: wrapper fairness remains valid without inventing another reachability
// obligation. Frame helpers called inside an action are not obligations either.
func TestStepActionCoveragePreservesWrapperFairness(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	source := `module wrapped {
  var x: bool
  var y: bool
  action init = all { x' = false, y' = false }
  action keepY = y' = y
  action first = all { x' = true, keepY }
  action second = all { x' = false, keepY }
  action wrapper = any { first, second }
  action step = wrapper
  val done = x
 }`
	if err := os.WriteFile(filepath.Join(dir, "model.qnt"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "model.yaml")
	if err := os.WriteFile(file, []byte("spec: model.qnt\nmodule: wrapped\nactions:\n  first: {}\n  second: {}\n  wrapper: {}\neventually: [done]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	graph := graphOf(t, file, GraphSearch{Eventually: []string{"done"}, Unfair: []string{"wrapper"}})
	if !graph.Complete || len(graph.Actions) != 2 || graph.Actions[0].Action != "first" || graph.Actions[1].Action != "second" {
		t.Fatalf("unexpected roster: %+v", graph.Actions)
	}
	if got := (&Report{Graph: graph}).StepActionReachability(); got.Status != "reachable" {
		t.Fatalf("summary: %+v", got)
	}
	if !reflect.DeepEqual(graph.Unfair, []string{"wrapper"}) {
		t.Fatalf("fairness changed: %v", graph.Unfair)
	}
	report, err := Simulate(file, SimulateOptions{Graph: true, Invariants: []string{}, Eventually: []string{}, Possibles: []binding.Possible{}, Samples: 1, Steps: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report.Text, "outside wrapper") {
		t.Fatalf("actual wrapper call classified outside step: %s", report.Text)
	}
}

func TestStepActionGraphOnlySimulation(t *testing.T) {
	skip.Quint(t)
	report, err := Simulate("../testdata/vending.yaml", SimulateOptions{Graph: true, Invariants: []string{}, Eventually: []string{}, Possibles: []binding.Possible{}, Samples: 1, Steps: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Graph == nil || len(report.Invariants) != 0 || len(report.Eventually) != 0 || len(report.Possibles) != 0 {
		t.Fatalf("graph-only simulation changed requested checks: %+v", report)
	}
	if got := report.StepActionReachability(); got.Status != "unreachable" || !reflect.DeepEqual(got.Missing, []string{"repair"}) {
		t.Fatalf("summary: %+v", got)
	}
}

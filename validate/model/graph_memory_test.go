package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A memory cutoff is bounded evidence, never a whole-graph proof. Even a
// cutoff while collecting init states must not invent a deadlock or pass.
func TestGraphMemoryBudgetPreservesPartialScope(t *testing.T) {
	skip.Quint(t)
	full := graphOf(t, "../testdata/vending.yaml", GraphSearch{})
	if full.MemoryUsed <= 1 || full.Bound.MemoryBytes != DefaultGraphMemoryBytes || !full.Complete {
		t.Fatalf("full graph has no charged/default budget: %+v", full)
	}
	cut := graphOf(t, "../testdata/vending.yaml", GraphSearch{MemoryBytes: full.MemoryUsed - 1})
	if cut.Complete || cut.Cut != "memory" || cut.MemoryUsed > cut.Bound.MemoryBytes {
		t.Fatalf("memory cutoff: %s", cut.Extent())
	}
	if !strings.Contains(cut.Extent(), "temporary evaluation") || !strings.Contains(cut.Extent(), "process heap/RSS") {
		t.Fatalf("memory extent omits scope: %s", cut.Extent())
	}
	b := graphBound(cut)
	if b.Kind == "complete" || (b.Proves() && (b.Kind != "depth" || b.Depth != cut.CompleteDepth)) {
		t.Fatalf("partial memory graph overclaims: %+v", b)
	}
	tiny := graphOf(t, "../testdata/vending.yaml", GraphSearch{MemoryBytes: 1})
	if tiny.Cut != "memory" || tiny.CompleteDepth != -1 || graphBound(tiny).Proves() || tiny.Deadlocks != 0 {
		t.Fatalf("initial cutoff overclaims: %+v", tiny)
	}
	report, err := Simulate(t.Context(), "../testdata/vending.yaml", SimulateOptions{Graph: true, MemoryBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Code != 2 || report.Doc().Exit != 2 || report.Graph == nil || report.Graph.Cut != "memory" {
		t.Fatalf("incomplete graph must not pass: %+v", report)
	}
	if len(report.Possibles) == 0 {
		t.Fatal("fixture has no possible obligations")
	}
	for _, p := range report.Possibles {
		if p.Unchecked == "" || p.Eval != nil || p.Found || p.Impossible {
			t.Fatalf("memory cutoff restarted a possible search or invented a verdict: %+v", p)
		}
	}
	for _, check := range report.Doc().Checks {
		if check.Kind == "possible" && check.Verdict != "unchecked" {
			t.Fatalf("unattempted possible became a machine verdict: %+v", check)
		}
	}
	allowed, err := Simulate(t.Context(), "../testdata/graph_ends.yaml", SimulateOptions{Graph: true, MemoryBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if allowed.Code != 2 || allowed.Invariants[0].Unchecked == "" {
		t.Fatalf("resource cutoff invented a missing-witness failure: %+v", allowed)
	}
	encoded, err := json.Marshal((&Report{Graph: cut}).Doc())
	if err != nil {
		t.Fatal(err)
	}
	var doc validate.Document
	if err := json.Unmarshal(encoded, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Graph == nil || doc.Graph.MemoryUsed != cut.MemoryUsed || doc.Graph.Bound.MemoryBytes != cut.Bound.MemoryBytes || !strings.Contains(doc.Graph.MemoryScope, "temporary_evaluation") {
		t.Fatalf("lost memory evidence in JSON: %s", encoded)
	}
}

func TestGraphMemoryBudgetRejectsNegative(t *testing.T) {
	skip.Quint(t)
	c, err := newChecker(t.Context(), "../testdata/vending.yaml", validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if graph, err := c.Graph(t.Context(), GraphSearch{MemoryBytes: -1}); err == nil || graph != nil {
		t.Fatalf("negative graph memory budget accepted: graph=%v err=%v", graph, err)
	}
}

func TestGraphMemoryComparisonIsInconclusive(t *testing.T) {
	skip.Quint(t)
	full := comparisonGraph(t, "before", GraphSearch{})
	cut := comparisonGraph(t, "after", GraphSearch{MemoryBytes: 1})
	report, err := Compare(t.Context(), full, cut, CompareOptions{Observe: []string{"phase"}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "inconclusive" || report.Added.Witness != nil || report.Removed.Witness != nil || report.After.Cut != "memory" {
		t.Fatalf("memory-incomplete graph yielded comparison verdict: %+v", report)
	}
	doc := report.Doc()
	if doc.Exit != 2 || doc.Comparison.After.Bound.MemoryBytes != 1 || doc.Comparison.After.MemoryUsed != cut.MemoryUsed || doc.Comparison.After.MemoryScope == "" {
		t.Fatalf("comparison omitted memory scope: %+v", doc.Comparison)
	}
}

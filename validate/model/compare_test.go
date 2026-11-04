package model

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/internal/evalshare"
)

func comparisonGraph(t *testing.T, file string, cfg GraphSearch) *GraphResult {
	t.Helper()
	c, err := validate.NewChecker(t.Context(), "testdata/compare/"+file+".yaml", validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	r, err := For(c).Graph(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCompareHandshakeAddedRemovedAndGhost(t *testing.T) {
	skip.Quint(t)
	t.Parallel()
	before := comparisonGraph(t, "before", GraphSearch{})
	after := comparisonGraph(t, "after", GraphSearch{})
	ghost := comparisonGraph(t, "ghost", GraphSearch{})
	opt := CompareOptions{Observe: []string{"phase", "retries"}}
	r, err := Compare(t.Context(), before, after, opt)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "inconclusive" || r.Added.Status != "witness" || r.Removed.Status != "included" {
		t.Fatalf("comparison before native confirmation: %+v", r)
	}
	w := r.Added.Witness
	if w == nil || w.Native != "not_run" || len(w.Observed) != 2 || w.Observed[1][0].Value != "2" {
		t.Fatalf("early acknowledgment witness: %+v", w)
	}
	if got := w.Source.Replay; got != "init.expect(and { phase == 0, retries == 0 }).then(step).expect(and { phase == 2, retries == 0 })" {
		t.Fatalf("strict original-step replay: %s", got)
	}
	if r.Before.Module != "before" || r.After.Module != "after" || r.Before.States != 5 || r.After.States != 5 {
		t.Fatalf("instance extents: %+v / %+v", r.Before, r.After)
	}
	reversed, err := Compare(t.Context(), after, before, opt)
	if err != nil || reversed.Removed.Status != "witness" || reversed.Added.Status != "included" {
		t.Fatalf("tightening removes behavior: %+v, %v", reversed, err)
	}
	equal, err := Compare(t.Context(), before, ghost, opt)
	if err != nil || equal.Status != "equivalent" || equal.Added.Status != "included" || equal.Removed.Status != "included" {
		t.Fatalf("same-transition ghost update changes no observations: %+v, %v", equal, err)
	}
}

func TestCompareRefusesIncompleteGraphAndProductBound(t *testing.T) {
	skip.Quint(t)
	t.Parallel()
	full := comparisonGraph(t, "before", GraphSearch{})
	cut := comparisonGraph(t, "after", GraphSearch{States: 1})
	// Exported summary mutation must not promote an incomplete underlying graph.
	cut.Complete, cut.Cut = true, ""
	r, err := Compare(t.Context(), full, cut, CompareOptions{Observe: []string{"phase"}})
	if err != nil || r.Status != "inconclusive" || r.Added.Witness != nil || r.Removed.Witness != nil || r.After.Complete {
		t.Fatalf("truncated graph: %+v, %v", r, err)
	}
	bounded, err := Compare(t.Context(), full, full, CompareOptions{Observe: []string{"phase"}, Pairs: 1})
	if err != nil || bounded.Status != "inconclusive" || bounded.Added.Status != "bounded" || bounded.Removed.Status != "bounded" || bounded.Added.Witness != nil {
		t.Fatalf("product bound: %+v, %v", bounded, err)
	}
	work, err := Compare(t.Context(), full, full, CompareOptions{Observe: []string{"phase"}, Work: 1})
	if err != nil || work.Status != "inconclusive" || work.Added.Cut != "work" || work.Added.Work > 1 || work.WorkLimit != 1 {
		t.Fatalf("target-work bound: %+v, %v", work, err)
	}
}

func syntheticComparisonGraph(vars []string, states []qeval.State, starts int, out [][]qeval.Edge) *GraphResult {
	return &GraphResult{Complete: true, graph: &qeval.Graph{Complete: true, States: states, Starts: starts, Out: out},
		e: &evalshare.Eval{Model: &qeval.Model{Vars: vars}}}
}

func TestCompareProjectionValidationAndTypedValues(t *testing.T) {
	intState := syntheticComparisonGraph([]string{"m::x"}, []qeval.State{{"m::x": qeval.Int(1)}}, 1, [][]qeval.Edge{{}})
	strState := syntheticComparisonGraph([]string{"n::x"}, []qeval.State{{"n::x": qeval.Str("1")}}, 1, [][]qeval.Edge{{}})
	for _, observe := range [][]string{nil, {""}, {"x", "x"}, {" x"}, {"missing"}, {"x + 1"}} {
		if _, err := Compare(t.Context(), intState, strState, CompareOptions{Observe: observe}); err == nil {
			t.Fatalf("invalid projection %q accepted", observe)
		}
	}
	r, err := Compare(t.Context(), intState, strState, CompareOptions{Observe: []string{"x"}})
	if err != nil || r.Added.Status != "witness" || len(r.Added.Witness.Observed) != 1 || r.Removed.Status != "witness" {
		t.Fatalf("typed initial difference: %+v, %v", r, err)
	}
	if r.Added.Witness.Observed[0][0].Value != `"1"` || r.Removed.Witness.Observed[0][0].Value != "1" {
		t.Fatal("typed observations conflated")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Compare(ctx, intState, strState, CompareOptions{Observe: []string{"x"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := Compare(t.Context(), intState, strState, CompareOptions{Observe: []string{"x"}, Pairs: -1}); err == nil {
		t.Fatal("negative pair bound accepted")
	}
	if _, err := Compare(t.Context(), intState, strState, CompareOptions{Observe: []string{"x"}, Work: -1}); err == nil {
		t.Fatal("negative target-work bound accepted")
	}
}

func TestCompareEmptyInitialLanguage(t *testing.T) {
	empty := syntheticComparisonGraph([]string{"x"}, nil, 0, nil)
	one := syntheticComparisonGraph([]string{"x"}, []qeval.State{{"x": qeval.Int(1)}}, 1, [][]qeval.Edge{{}})
	r, err := Compare(t.Context(), empty, one, CompareOptions{Observe: []string{"x"}})
	if err != nil || r.Added.Status != "witness" || r.Removed.Status != "included" || len(r.Added.Witness.Observed) != 1 {
		t.Fatalf("empty initial language: %+v, %v", r, err)
	}
	equal, err := Compare(t.Context(), empty, empty, CompareOptions{Observe: []string{"x"}})
	if err != nil || equal.Status != "equivalent" {
		t.Fatalf("two empty languages: %+v, %v", equal, err)
	}
}

func TestCompareRecordsEffectiveGraphConstants(t *testing.T) {
	skip.Quint(t)
	t.Parallel()
	defaultInstance := comparisonGraph(t, "constants", GraphSearch{})
	overridden := comparisonGraph(t, "constants", GraphSearch{Constants: []string{"LIMIT=0"}})
	r, err := Compare(t.Context(), defaultInstance, overridden, CompareOptions{Observe: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Before.Constants; len(got) != 1 || got[0].Name != "LIMIT" || got[0].Value != "2" {
		t.Fatalf("simulation constant overrides binding: %+v", got)
	}
	if got := r.After.Constants; len(got) != 1 || got[0].Name != "LIMIT" || got[0].Value != "0" {
		t.Fatalf("graph constant overrides simulation: %+v", got)
	}
	if r.Before.States != 3 || r.After.States != 1 || r.Removed.Status != "witness" || r.Added.Status != "included" {
		t.Fatalf("different instantiated behavior: %+v", r)
	}
}

// The distinguishing prefix must survive a revisit to the same source state;
// the graph's ordinary shortest parent path would erase the loop's history.
func TestCompareWitnessKeepsProductPath(t *testing.T) {
	vars := []string{"x"}
	states := []qeval.State{{"x": qeval.Int(0)}, {"x": qeval.Int(1)}}
	source := syntheticComparisonGraph(vars, states, 1, [][]qeval.Edge{{{To: 1}}, {{To: 0}}})
	target := syntheticComparisonGraph(vars, append(slices.Clone(states), qeval.State{"x": qeval.Int(0)}), 1,
		[][]qeval.Edge{{{To: 1}}, {{To: 2}}, {}})
	r, err := Compare(t.Context(), target, source, CompareOptions{Observe: vars})
	if err != nil || r.Added.Status != "witness" {
		t.Fatalf("loop history comparison: %+v, %v", r, err)
	}
	w := r.Added.Witness
	if len(w.Observed) != 4 || !strings.Contains(w.Source.Replay, ".expect(and { x == 0 }).then(step).expect(and { x == 1 })") {
		t.Fatalf("lost distinguishing loop: %+v", w)
	}
}

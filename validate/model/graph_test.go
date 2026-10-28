package model

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

func graphOf(t *testing.T, path string, cfg GraphSearch) *GraphResult {
	t.Helper()
	c, err := newChecker(path, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	r, err := c.Graph(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Analyzer.Graph on graph.yaml's model, its facts known by hand: twelve
// states from two initial ones, small violated three steps from x = 1
// (its replay starting in that state's values, since init has two), the
// deadlock where it is violated (a terminal state in graph_ends.yaml),
// never never enabled, and each bound saying it cut the graph.
func TestCheckerGraph(t *testing.T) {
	skip.Quint(t)
	t.Parallel()
	r := graphOf(t, "../testdata/graph.yaml", GraphSearch{})
	if !r.Complete || r.Cut != "" || r.States != 12 || r.Starts != 2 || r.Depth != 3 {
		t.Fatalf("extent %s (complete %v, cut %q)", r.Extent(), r.Complete, r.Cut)
	}
	small, positive := r.Invariants[0], r.Invariants[1]
	if !small.Violated || small.Counterexample == nil || !positive.Held() || positive.Checked != 12 {
		t.Fatalf("invariants %+v", r.Invariants)
	}
	want := "all { x' = 1, y' = 0 }.then(inc).then(inc).then(bump(2)).expect(not(small))"
	if got := small.Counterexample.Replay; got != want {
		t.Fatalf("small's replay\n%s\nwant\n%s", got, want)
	}
	var calls []string
	for _, st := range small.Counterexample.Steps {
		calls = append(calls, st.Call)
	}
	if got := strings.Join(calls, "; "); got != "init (x = 1, y = 0); inc; inc; bump(by = 2)" {
		t.Fatalf("small's steps %s", got)
	}
	if r.Deadlocks != 1 || r.Terminals != 0 || r.Deadlock == nil || !strings.HasSuffix(r.Deadlock.Replay, ".then(step.fail())") {
		t.Fatalf("deadlocks %d, terminals %d, %+v", r.Deadlocks, r.Terminals, r.Deadlock)
	}
	if never := r.NeverEnabled(); !slices.Equal(never, []string{"never"}) {
		t.Fatalf("never enabled %v", never)
	}
	cov := map[string]ActionCoverage{}
	for _, a := range r.Actions {
		cov[a.Action] = a
	}
	// reset only from (2, 2); bump(1) and bump(2) from y = 0, bump(1) from y = 1
	if cov["reset"].Transitions != 1 || cov["bump"].States != 8 || cov["bump"].Transitions != 12 || cov["inc"].Transitions != 9 {
		t.Fatalf("coverage %+v", r.Actions)
	}

	ends := graphOf(t, "../testdata/graph_ends.yaml", GraphSearch{})
	if ends.Deadlocks != 0 || ends.Terminals != 1 || ends.Deadlock != nil {
		t.Fatalf("graph_ends: deadlocks %d, terminals %d", ends.Deadlocks, ends.Terminals)
	}

	// the depth bound short of the violation: small held within it, the
	// graph cut, its states at depth 2 not expanded (so no deadlock)
	d := graphOf(t, "../testdata/graph.yaml", GraphSearch{Depth: 2})
	if d.Complete || d.Cut != "depth" || !d.Invariants[0].Held() || d.Deadlocks != 0 || !strings.HasPrefix(d.Extent(), "truncated by the depth bound (2)") {
		t.Fatalf("depth 2: %s, small %+v, deadlocks %d", d.Extent(), d.Invariants[0], d.Deadlocks)
	}
	s := graphOf(t, "../testdata/graph.yaml", GraphSearch{States: 5})
	if s.Complete || s.Cut != "states" || s.States != 5 {
		t.Fatalf("states 5: %s", s.Extent())
	}
	v := graphOf(t, "../testdata/vending.yaml", GraphSearch{})
	if !v.Complete || v.States != 25 || !v.Invariants[0].Held() || !slices.Equal(v.NeverEnabled(), []string{"repair"}) || v.Deadlocks != 0 {
		t.Fatalf("vending: %s, %+v, never %v", v.Extent(), v.Invariants, v.NeverEnabled())
	}
}

// A model the evaluator cannot follow to the end (an integer past 64
// bits) is no error and never complete: the graph found before it, cut
// "unsupported", Stopped naming the construct.
func TestCheckerGraphUnsupported(t *testing.T) {
	skip.Quint(t)
	t.Parallel()
	dir := t.TempDir()
	spec, _ := filepath.Abs("../../internal/qeval/testdata/grow.qnt")
	b := filepath.Join(dir, "grow.yaml")
	if err := os.WriteFile(b, []byte("spec: "+spec+"\nmodule: grow\nactions: {step: {}}\ninvariants: [positive]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := graphOf(t, b, GraphSearch{})
	if r.Complete || r.Cut != "unsupported" || r.States != 6 || !strings.Contains(r.Stopped, "outside the Go evaluator's fragment") {
		t.Fatalf("%s: complete %v, cut %q, stopped %q", r.Extent(), r.Complete, r.Cut, r.Stopped)
	}
	if !r.Invariants[0].Held() || r.Invariants[0].Checked != 6 {
		t.Fatalf("positive in the states found: %+v", r.Invariants[0])
	}
}

// A counterexample's replay calls a branch's action only where the
// evaluator finds that call alone reaches the path's state: graph_wrap's
// step counts its steps itself, so inc alone leaves n unassigned and
// each step is replayed as step, exactly (dec is never enabled, so step
// has one successor). Its end, n = 3, is terminal, not a deadlock.
func TestCheckerGraphReplaysStep(t *testing.T) {
	skip.Quint(t)
	t.Parallel()
	r := graphOf(t, "../testdata/graph_wrap.yaml", GraphSearch{})
	small := r.Invariants[0]
	if !r.Complete || !small.Violated {
		t.Fatalf("%s, small %+v", r.Extent(), small)
	}
	want := "init.then(step).then(step).then(step).expect(not(small))"
	if got := small.Counterexample.Replay; got != want || len(small.Counterexample.Sampled) > 0 {
		t.Fatalf("small's replay %s (sampled %v), want %s exactly", got, small.Counterexample.Sampled, want)
	}
	if r.Deadlocks != 0 || r.Terminals != 1 {
		t.Fatalf("deadlocks %d, terminals %d", r.Deadlocks, r.Terminals)
	}
}

// An invariant the evaluator cannot evaluate in one state is not held,
// and is still checked in the others: graph_partial's small, unevaluable
// at x = 1, is violated at x = 3. The state at x = 4, which only read
// leaves, is quiescent, not a deadlock.
func TestCheckerGraphPartlyUnsupported(t *testing.T) {
	skip.Quint(t)
	t.Parallel()
	r := graphOf(t, "../testdata/graph_partial.yaml", GraphSearch{})
	small := r.Invariants[0]
	if small.Held() || !small.Violated || small.Unchecked != 1 || small.Unsupported == "" {
		t.Fatalf("small %+v", small)
	}
	if got := small.Counterexample.Replay; got != "init.then(inc).then(inc).then(inc).expect(not(small))" {
		t.Fatalf("small's replay %s", got)
	}
	if r.Deadlocks != 0 || r.Quiescent != 1 {
		t.Fatalf("deadlocks %d, quiescent %d", r.Deadlocks, r.Quiescent)
	}
}

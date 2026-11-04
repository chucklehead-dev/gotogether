package tracecompare

import (
	"context"
	"errors"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
)

func TestInclusionKeepsHiddenStateCorrelations(t *testing.T) {
	source := Graph{Observations: []string{"a", "b", "c", "d"}, Starts: []int{0}, Out: [][]int{{1}, {2}, {3}, {}}}
	// Target contains all three projected edges a→b, b→c and c→d, but
	// the c reached through b cannot take d. The other c requires y.
	target := Graph{Observations: []string{"a", "b", "c", "x", "y", "c", "d"}, Starts: []int{0}, Out: [][]int{{1, 4}, {2}, {3}, {}, {5}, {6}, {}}}
	r, err := Inclusion(t.Context(), source, target, Budget{100, 10000})
	if err != nil || r.Status != "witness" || !slices.Equal(r.Path, []int{0, 1, 2, 3}) {
		t.Fatalf("comparison = %+v, %v", r, err)
	}
	// Add a compatible alternative initial state. Target now admits the
	// whole source path even though its first initial branch still cannot.
	target.Observations = append(target.Observations, "a", "b")
	target.Starts = append(target.Starts, 7)
	target.Out = append(target.Out, []int{8}, []int{5})
	r, err = Inclusion(t.Context(), source, target, Budget{100, 10000})
	if err != nil || r.Status != "included" || r.Path != nil {
		t.Fatalf("compatible initial branch = %+v, %v", r, err)
	}
}

// The same concrete source state can recur with a smaller compatible target
// subset. Its shortest graph path is not the shortest product witness path.
func TestInclusionPreservesWitnessProductPath(t *testing.T) {
	source := Graph{Observations: []string{"a", "b", "c"}, Starts: []int{0}, Out: [][]int{{1, 2}, {0}, {}}}
	target := Graph{Observations: []string{"a", "b", "c", "a"}, Starts: []int{0}, Out: [][]int{{1, 2}, {3}, {}, {1}}}
	r, err := Inclusion(t.Context(), source, target, Budget{100, 10000})
	if err != nil || r.Status != "witness" || !slices.Equal(r.Path, []int{0, 1, 0, 2}) {
		t.Fatalf("cyclic product witness = %+v, %v", r, err)
	}
}

func TestInclusionStuttersCyclesAndMultipleStarts(t *testing.T) {
	source := Graph{Observations: []string{"a", "a", "b"}, Starts: []int{2, 0}, Out: [][]int{{1}, {0, 2}, {2}}}
	target := Graph{Observations: []string{"a", "b"}, Starts: []int{0, 1}, Out: [][]int{{0, 1}, {1}}}
	r, err := Inclusion(t.Context(), source, target, Budget{100, 10000})
	if err != nil || r.Status != "included" {
		t.Fatalf("hidden stuttering cycle = %+v, %v", r, err)
	}
	// Stutters are observations, not epsilon transitions. Removing the
	// target a→a edge removes the source's observed prefix a,a.
	target.Out[0] = []int{1}
	r, err = Inclusion(t.Context(), source, target, Budget{100, 10000})
	if err != nil || r.Status != "witness" || !slices.Equal(r.Path, []int{0, 1}) {
		t.Fatalf("removed stutter = %+v, %v", r, err)
	}
}

func TestInclusionBudget(t *testing.T) {
	source := Graph{Observations: []string{"a", "b"}, Starts: []int{0}, Out: [][]int{{1}, {}}}
	target := Graph{Observations: []string{"a"}, Starts: []int{0}, Out: [][]int{{0}}}
	for _, tc := range []struct {
		limit int
		want  Result
	}{
		{1, Result{Status: "bounded", Explored: 1, Work: 2, Cut: "pairs"}},
		{2, Result{Status: "witness", Explored: 2, Work: 2, Path: []int{0, 1}}},
	} {
		r, err := Inclusion(t.Context(), source, target, Budget{tc.limit, 10000})
		if err != nil || !reflect.DeepEqual(r, tc.want) {
			t.Fatalf("limit %d = %+v, %v; want %+v", tc.limit, r, err, tc.want)
		}
	}
	// An initial mismatch consumes one product state, just like later ones.
	target.Starts = nil
	if r, err := Inclusion(t.Context(), source, target, Budget{1, 10000}); err != nil || !reflect.DeepEqual(r, Result{Status: "witness", Explored: 1, Path: []int{0}}) {
		t.Fatalf("initial mismatch = %+v, %v", r, err)
	}
	// Revisited nodes consume no budget, so the one-state cycle completes.
	cycle := Graph{Observations: []string{"a"}, Starts: []int{0, 0}, Out: [][]int{{0, 0}}}
	if r, err := Inclusion(t.Context(), cycle, cycle, Budget{1, 10000}); err != nil || !reflect.DeepEqual(r, Result{Status: "included", Explored: 1, Work: 4}) {
		t.Fatalf("one-state cycle = %+v, %v", r, err)
	}
}

func TestInclusionValidatesInputsAndCancellation(t *testing.T) {
	good := Graph{Observations: []string{"a"}, Starts: []int{0}, Out: [][]int{{}}}
	for _, bad := range []Graph{
		{Observations: []string{"a"}},
		{Observations: []string{"a"}, Starts: []int{-1}, Out: [][]int{{}}},
		{Observations: []string{"a"}, Starts: []int{1}, Out: [][]int{{}}},
		{Observations: []string{"a"}, Starts: []int{0}, Out: [][]int{{1}}},
	} {
		for _, graphs := range [][2]Graph{{bad, good}, {good, bad}} {
			if _, err := Inclusion(t.Context(), graphs[0], graphs[1], Budget{1, 10000}); err == nil {
				t.Fatalf("accepted malformed graph %+v", bad)
			}
		}
	}
	if _, err := Inclusion(t.Context(), good, good, Budget{0, 10000}); err == nil {
		t.Fatal("accepted nonpositive budget")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Inclusion(ctx, good, good, Budget{1, 10000}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	// Cancel after validation, during traversal, rather than only before
	// entry. Context errors must never become an inclusion verdict.
	counting := &cancelContext{Context: t.Context(), after: 9}
	cycle := Graph{Observations: []string{"a"}, Starts: []int{0}, Out: [][]int{{0}}}
	if _, err := Inclusion(counting, cycle, cycle, Budget{1, 10000}); !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-search cancellation = %v (checks %d)", err, counting.calls)
	}
	if r, err := Inclusion(t.Context(), Graph{}, good, Budget{1, 10000}); err != nil || r.Status != "included" || r.Explored != 0 {
		t.Fatalf("empty source language = %+v, %v", r, err)
	}
}

type cancelContext struct {
	context.Context
	after, calls int
}

func (c *cancelContext) Err() error {
	c.calls++
	if c.calls >= c.after {
		return context.Canceled
	}
	return nil
}

// A shortest missing word has no repeated (source state, target subset)
// before rejection. Thus it has at most |source|*2^|target| observations.
// Enumerate ALL words up to that bound independently, rather than using a
// second implementation of the subset product as the oracle.
func TestInclusionAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(2047, 9182))
	for i := range 500 {
		a, b := randomGraph(rng), randomGraph(rng)
		bound := len(a.Observations) * (1 << len(b.Observations))
		aw, bw := words(a, bound), words(b, bound)
		shortest := 0
		for word := range aw {
			if !bw[word] && (shortest == 0 || len(word) < shortest) {
				shortest = len(word)
			}
		}
		r, err := Inclusion(t.Context(), a, b, Budget{bound, 10000})
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if shortest == 0 {
			if r.Status != "included" || r.Path != nil {
				t.Fatalf("case %d: included by enumeration, got %+v; source %+v target %+v", i, r, a, b)
			}
			continue
		}
		if r.Status != "witness" || len(r.Path) != shortest {
			t.Fatalf("case %d: shortest missing word length %d, got %+v; source %+v target %+v", i, shortest, r, a, b)
		}
		word := ""
		for j, id := range r.Path {
			if j == 0 && !slices.Contains(a.Starts, id) || j > 0 && !slices.Contains(a.Out[r.Path[j-1]], id) {
				t.Fatalf("case %d: non-source path %v", i, r.Path)
			}
			word += a.Observations[id]
		}
		if !aw[word] || bw[word] {
			t.Fatalf("case %d: reported witness word %q is not a difference", i, word)
		}
	}
}

func randomGraph(r *rand.Rand) Graph {
	n := 1 + r.IntN(2)
	g := Graph{Observations: make([]string, n), Out: make([][]int, n)}
	for i := range n {
		g.Observations[i] = string(rune('a' + r.IntN(2)))
		if r.IntN(2) != 0 {
			g.Starts = append(g.Starts, i)
		}
		for j := range n {
			if r.IntN(2) != 0 {
				g.Out[i] = append(g.Out[i], j)
			}
		}
	}
	return g
}

func words(g Graph, bound int) map[string]bool {
	out := map[string]bool{}
	var visit func(int, string)
	visit = func(id int, prefix string) {
		word := prefix + g.Observations[id]
		out[word] = true
		if len(word) == bound {
			return
		}
		for _, next := range g.Out[id] {
			visit(next, word)
		}
	}
	for _, init := range g.Starts {
		visit(init, "")
	}
	return out
}

// Thousands of same-observation initial states must share one target subset,
// rather than copying and encoding that subset once per source initial state.
func TestInclusionSharesLargeSubsetsAndBoundsWork(t *testing.T) {
	const n = 4096
	g := Graph{Observations: make([]string, n), Starts: make([]int, n), Out: make([][]int, n)}
	for i := range n {
		g.Observations[i] = "same"
		g.Starts[i] = i
		g.Out[i] = []int{i}
	}
	r, err := Inclusion(t.Context(), g, g, Budget{Pairs: n, Work: 2 * n})
	if err != nil || r.Status != "included" || r.Explored != n || r.Work != 2*n {
		t.Fatalf("shared subsets = %+v, %v", r, err)
	}
	// Stop while grouping target initials, before retaining a large subset.
	r, err = Inclusion(t.Context(), g, g, Budget{Pairs: n, Work: 128})
	if err != nil || !reflect.DeepEqual(r, Result{Status: "bounded", Work: 128, Cut: "work"}) {
		t.Fatalf("initial grouping budget = %+v, %v", r, err)
	}
	// Stop while intersecting successor observations, without reporting the
	// partial intersection as a witness or an inclusion verdict.
	r, err = Inclusion(t.Context(), g, g, Budget{Pairs: n, Work: n + 128})
	if err != nil || !reflect.DeepEqual(r, Result{Status: "bounded", Explored: n, Work: n + 128, Cut: "work"}) {
		t.Fatalf("successor budget = %+v, %v", r, err)
	}
}

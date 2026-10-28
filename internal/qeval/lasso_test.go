package qeval

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

// randomGraph is a graph of n states with random edges (labels 0 to
// labels-1, targets a state or -1, a state the search did not add), each
// state expanded but some; and random hot states.
func randomGraph(r *rand.Rand, n, labels int) (*Graph, []bool) {
	g := &Graph{Out: make([][]Edge, n), Expanded: make([]bool, n)}
	hot := make([]bool, n)
	for s := range n {
		g.Expanded[s] = r.IntN(8) > 0
		hot[s] = r.IntN(5) > 0
		if !g.Expanded[s] {
			continue
		}
		g.Out[s] = []Edge{}
		for range r.IntN(4) {
			e := Edge{Label: r.IntN(labels), To: r.IntN(n+1) - 1}
			if !slices.Contains(g.Out[s], e) {
				g.Out[s] = append(g.Out[s], e)
			}
		}
	}
	g.Labels = make([][]Event, labels)
	return g, hot
}

// HotComponents against brute force, on random graphs small enough to
// enumerate every closed walk's support: each set of edges between hot,
// expanded states (none cut by the state bound, an edge to -1 its last;
// no family of them stopping) that is strongly
// connected is the edges of some closed walk, fair when every weakly
// fair family enabled (by an edge to another state) in each of its
// states is taken by one of its edges to another state; a single hot
// state with no weakly fair family enabled stutters fairly. The states
// on some fair walk are exactly those of the fair components; each
// component is a strongly connected component of the hot subgraph, its
// Missed exactly the families its walks cannot take, and each lasso a
// closed walk from its nearest state, inside it and fair by the same
// definition. Under every fairness at random, all weakly fair, and all
// unfair (no fairness: every hot cycle reported).
func TestHotComponentsAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(6, 6))
	for iter := range 3000 {
		n := 1 + r.IntN(5)
		labels := 1 + r.IntN(4)
		g, hot := randomGraph(r, n, labels)
		families := 1 + r.IntN(3)
		of := make([]int, labels)
		for i := range of {
			of[i] = r.IntN(families)
		}
		random := make([]Fairness, families)
		for i := range random {
			random[i] = Fairness(r.IntN(3))
		}
		for fi, fair := range [][]Fairness{random, make([]Fairness, families), slices.Repeat([]Fairness{Unfair}, families)} {
			fam := Families{Of: of, Fair: fair}
			name := fmt.Sprintf("graph %d, fairness %d (%v): out %v, expanded %v, hot %v, families %v", iter, fi, fair, g.Out, g.Expanded, hot, of)
			checkHot(t, name, g, hot, fam)
		}
	}
}

func checkHot(t *testing.T, name string, g *Graph, hot []bool, fam Families) {
	t.Helper()
	n := len(g.Out)
	// a cut state's edges are not all known: an edge to -1 is its last
	in := func(s int) bool {
		return s >= 0 && hot[s] && g.Expanded[s] && !slices.ContainsFunc(g.Out[s], func(e Edge) bool { return e.To < 0 })
	}
	enabled := func(s int) map[int]bool {
		m := map[int]bool{}
		for _, e := range g.Out[s] {
			if e.To != s && fam.Fair[fam.Of[e.Label]] == WeakFair {
				m[fam.Of[e.Label]] = true
			}
		}
		return m
	}
	type arc struct{ from, to, family int }
	var arcs []arc
	for s := range n {
		if !in(s) {
			continue
		}
		for _, e := range g.Out[s] {
			if in(e.To) && fam.Fair[fam.Of[e.Label]] != Stops {
				arcs = append(arcs, arc{s, e.To, fam.Of[e.Label]})
			}
		}
	}
	// fairWalk: is a closed walk over exactly these states and arcs fair
	fairWalk := func(states map[int]bool, taken map[int]bool) bool {
		var obliged map[int]bool
		for s := range states {
			en := enabled(s)
			if obliged == nil {
				obliged = en
				continue
			}
			for f := range obliged {
				if !en[f] {
					delete(obliged, f)
				}
			}
		}
		for f := range obliged {
			if !taken[f] {
				return false
			}
		}
		return true
	}
	// reach over a set of arcs
	reaches := func(as []arc, a, b int) bool {
		seen := map[int]bool{a: true}
		for q := []int{a}; len(q) > 0; q = q[1:] {
			for _, x := range as {
				if x.from == q[0] && !seen[x.to] {
					seen[x.to] = true
					q = append(q, x.to)
				}
			}
		}
		return seen[b]
	}
	want := map[int]bool{}
	for mask := 1; mask < 1<<len(arcs); mask++ {
		var sub []arc
		states, taken := map[int]bool{}, map[int]bool{}
		for i, x := range arcs {
			if mask&(1<<i) != 0 {
				sub = append(sub, x)
				states[x.from], states[x.to] = true, true
				if x.from != x.to {
					taken[x.family] = true
				}
			}
		}
		strong := true
		for a := range states {
			for b := range states {
				if !reaches(sub, a, b) {
					strong = false
				}
			}
		}
		if strong && fairWalk(states, taken) {
			for s := range states {
				want[s] = true
			}
		}
	}
	for s := range n {
		if in(s) && len(enabled(s)) == 0 {
			want[s] = true // a fair stutter
		}
	}
	got := map[int]bool{}
	comps := g.HotComponents(hot, fam)
	for _, c := range comps {
		// a component of the hot subgraph: its states reach each other, and
		// no state outside it both reaches and is reached by them
		for _, a := range c.States {
			if !in(a) {
				t.Fatalf("%s: component %v holds state %d, not hot and expanded", name, c.States, a)
			}
			for _, b := range c.States {
				if !reaches(arcs, a, b) {
					t.Fatalf("%s: component %v: %d does not reach %d", name, c.States, a, b)
				}
			}
			for s := range n {
				if in(s) && !slices.Contains(c.States, s) && reaches(arcs, a, s) && reaches(arcs, s, a) {
					t.Fatalf("%s: component %v leaves out %d", name, c.States, s)
				}
			}
		}
		// Missed: the families enabled throughout no inner arc takes
		states, taken := map[int]bool{}, map[int]bool{}
		for _, s := range c.States {
			states[s] = true
		}
		for _, x := range arcs {
			if states[x.from] && states[x.to] && x.from != x.to {
				taken[x.family] = true
			}
		}
		if c.Fair() != fairWalk(states, taken) {
			t.Fatalf("%s: component %v fair %v (missed %v), brute force %v", name, c.States, c.Fair(), c.Missed, !c.Fair())
		}
		if !c.Fair() {
			if c.Lasso != nil {
				t.Fatalf("%s: unfair component %v has a lasso", name, c.States)
			}
			continue
		}
		for _, s := range c.States {
			got[s] = true
		}
		l := c.Lasso
		if l == nil || l.Entry != c.States[0] {
			t.Fatalf("%s: fair component %v: lasso %+v", name, c.States, l)
		}
		if l.Cycle == nil {
			if len(enabled(l.Entry)) > 0 {
				t.Fatalf("%s: component %v stutters at %d, where %v is enabled", name, c.States, l.Entry, enabled(l.Entry))
			}
			continue
		}
		// a closed walk from the entry, inside, fair
		visited, walkTaken := map[int]bool{l.Entry: true}, map[int]bool{}
		at := l.Entry
		for _, e := range l.Cycle {
			if !slices.Contains(g.Out[at], e) || !states[e.To] || fam.Fair[fam.Of[e.Label]] == Stops {
				t.Fatalf("%s: component %v: lasso %v takes %v from %d", name, c.States, l.Cycle, e, at)
			}
			if e.To != at {
				walkTaken[fam.Of[e.Label]] = true
			}
			at = e.To
			visited[at] = true
		}
		if at != l.Entry {
			t.Fatalf("%s: lasso %v ends at %d, not %d", name, l.Cycle, at, l.Entry)
		}
		if !fairWalk(visited, walkTaken) {
			t.Fatalf("%s: lasso %v of %v is unfair", name, l.Cycle, c.States)
		}
	}
	for s := range n {
		if want[s] != got[s] {
			t.Fatalf("%s: state %d on a fair hot walk: %v, brute force %v (components %+v)", name, s, got[s], want[s], comps)
		}
	}
}

// Fairness by hand, on a queue: from s0 (empty, not hot) submit leads to
// s1 (hot), where a read loops (a stutter), retry goes to s2 and back
// (retry), and process leads back to s0. Weakly fair process enabled in
// s1 and s2 rules the retry loop out; with process disabled in s2 (a
// retry storm: weak fairness never obliges it) the loop is fair; with
// fairness off it is fair either way; a stopping retry is in no cycle.
func TestHotComponentsFairness(t *testing.T) {
	const submit, read, retry, process = 0, 1, 2, 3
	g := &Graph{Out: [][]Edge{
		{{submit, 1}},
		{{read, 1}, {retry, 2}, {process, 0}},
		{{retry, 1}, {process, 0}},
	}, Expanded: []bool{true, true, true}, Labels: make([][]Event, 4)}
	hot := []bool{false, true, true}
	fair := Families{Of: []int{0, 1, 2, 3}, Fair: []Fairness{Unfair, WeakFair, WeakFair, WeakFair}}
	if cs := g.HotComponents(hot, fair); len(cs) != 1 || cs[0].Fair() || !slices.Equal(cs[0].Missed, []int{process}) {
		t.Fatalf("process enabled throughout: %+v", cs)
	}
	storm := &Graph{Out: [][]Edge{g.Out[0], g.Out[1], {{retry, 1}}}, Expanded: g.Expanded, Labels: g.Labels}
	cs := storm.HotComponents(hot, fair)
	if len(cs) != 1 || !cs[0].Fair() || cs[0].Lasso == nil || cs[0].Lasso.Entry != 1 || len(cs[0].Lasso.Cycle) != 2 {
		t.Fatalf("process disabled in s2: %+v", cs)
	}
	none := Families{Of: fair.Of, Fair: slices.Repeat([]Fairness{Unfair}, 4)}
	if cs := g.HotComponents(hot, none); len(cs) != 1 || !cs[0].Fair() {
		t.Fatalf("no fairness: %+v", cs)
	}
	stops := Families{Of: fair.Of, Fair: []Fairness{Unfair, WeakFair, Stops, WeakFair}}
	// (and s2, where only retry is enabled, stutters once retries stop)
	if cs := storm.HotComponents(hot, stops); len(cs) != 1 || !slices.Equal(cs[0].States, []int{2}) || cs[0].Lasso.Cycle != nil {
		t.Fatalf("retry stops: %+v", cs)
	}
	// a hot state where only a stutter and an unfair family are enabled
	// stutters fairly
	stuck := &Graph{Out: [][]Edge{{{submit, 1}}, {{read, 1}, {submit, 0}}}, Expanded: []bool{true, true}, Labels: g.Labels}
	if cs := stuck.HotComponents([]bool{false, true}, fair); len(cs) != 1 || cs[0].Lasso == nil || cs[0].Lasso.Cycle != nil {
		t.Fatalf("stuck: %+v", cs)
	}
}

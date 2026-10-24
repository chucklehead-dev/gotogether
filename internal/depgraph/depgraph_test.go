package depgraph

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// kindOf classifies one cycle by the definitions.
func kindOf(c []Hop) string {
	rws, ww := 0, true
	adjacent := false
	for i, h := range c {
		if h.Kind == "rw" {
			rws++
			if c[(i+1)%len(c)].Kind == "rw" {
				adjacent = true
			}
		}
		if h.Kind != "ww" {
			ww = false
		}
	}
	switch {
	case ww:
		return G0
	case rws == 0:
		return G1c
	case rws == 1:
		return GSingle
	case !adjacent:
		return GNonadjacent
	}
	return G2
}

var strength = map[string]int{G0: 0, G1c: 1, GSingle: 2, GNonadjacent: 3, G2: 4}

// simpleCycles enumerates every simple cycle through scc's least node
// first, then the rest (each cycle from its least node, so each once).
func simpleCycles(g *Graph, scc []int64) [][]Hop {
	in := map[int64]bool{}
	for _, n := range scc {
		in[n] = true
	}
	var all [][]Hop
	for _, s := range scc {
		var path []Hop
		on := map[int64]bool{s: true}
		var dfs func(v int64)
		dfs = func(v int64) {
			for _, e := range g.out[v] {
				if !in[e.To] || e.To < s {
					continue
				}
				if e.To == s {
					all = append(all, append(slices.Clone(path), Hop{v, e}))
					continue
				}
				if !on[e.To] {
					on[e.To] = true
					path = append(path, Hop{v, e})
					dfs(e.To)
					path = path[:len(path)-1]
					on[e.To] = false
				}
			}
		}
		dfs(s)
	}
	return all
}

// On small random graphs, Classify names each component's strongest
// cycle as an enumeration of every simple cycle does, and the cycle it
// returns is one, of that kind.
func TestClassifyAgainstBruteForce(t *testing.T) {
	kinds := []string{"ww", "wr", "rw"}
	r := rand.New(rand.NewPCG(1, 2))
	seen := map[string]int{}
	for trial := range 20000 {
		g := New()
		nodes := 2 + r.IntN(6)
		for range nodes + r.IntN(nodes) {
			g.Add(int64(r.IntN(nodes)), int64(r.IntN(nodes)), kinds[r.IntN(3)], "k")
		}
		sccs := g.Components()
		if !slices.IsSortedFunc(sccs, func(a, b []int64) int { return int(a[0] - b[0]) }) {
			t.Fatalf("trial %d: components %v not in order of their least transaction", trial, sccs)
		}
		for _, scc := range sccs {
			if !slices.IsSorted(scc) {
				t.Fatalf("trial %d: component %v not sorted", trial, scc)
			}
			best := ""
			for _, c := range simpleCycles(g, scc) {
				if k := kindOf(c); best == "" || strength[k] < strength[best] {
					best = k
				}
			}
			kind, cyc := g.Classify(scc)
			seen[kind]++
			if kind != best {
				t.Fatalf("trial %d: component %v: Classify says %s, the strongest cycle is %s", trial, scc, kind, best)
			}
			if got := kindOf(cyc); got != kind {
				t.Fatalf("trial %d: Classify's %s cycle %v is %s", trial, kind, cyc, got)
			}
			for i, h := range cyc {
				if next := cyc[(i+1)%len(cyc)].From; h.To != next || !slices.Contains(g.out[h.From], h.Edge) {
					t.Fatalf("trial %d: %v is not a cycle of the graph", trial, cyc)
				}
			}
			// through each node: never weaker than the strongest simple
			// cycle through it, never stronger than the component's, a
			// closed walk through it of the kind it names
			for _, n := range scc {
				through := ""
				for _, c := range simpleCycles(g, scc) {
					if slices.ContainsFunc(c, func(h Hop) bool { return h.From == n }) {
						if k := kindOf(c); through == "" || strength[k] < strength[through] {
							through = k
						}
					}
				}
				k, walk := g.ClassifyThrough(scc, n, G2)
				if k0, _ := g.ClassifyThrough(nil, n, G2); k0 != k {
					t.Fatalf("trial %d: through %d: %s in the component, %s in the whole graph", trial, n, k, k0)
				}
				if through != "" && (k == "" || strength[k] > strength[through] || strength[k] < strength[best]) {
					t.Fatalf("trial %d: through %d: %s, the strongest simple cycle through it %s, the component's %s", trial, n, k, through, best)
				}
				if k != "" && (kindOf(walk) != k || !slices.ContainsFunc(walk, func(h Hop) bool { return h.From == n })) {
					t.Fatalf("trial %d: through %d: %s walk %v", trial, n, k, walk)
				}
				for i, h := range walk {
					if next := walk[(i+1)%len(walk)].From; h.To != next || !slices.Contains(g.out[h.From], h.Edge) {
						t.Fatalf("trial %d: %v is not a closed walk of the graph", trial, walk)
					}
				}
				// most stops the search: at G1c, a G-single or weaker through n is no answer
				if k2, _ := g.ClassifyThrough(scc, n, G1c); (strength[k] <= strength[G1c]) != (k2 == k) || strength[k] > strength[G1c] && k2 != "" {
					t.Fatalf("trial %d: through %d up to G1c: %q, unbounded %q", trial, n, k2, k)
				}
			}
			if c := g.Component(scc[0]); !slices.Equal(c, scc) {
				t.Fatalf("trial %d: Component(%d) = %v, Tarjan's %v", trial, scc[0], c, scc)
			}
		}
	}
	t.Log(seen)
	for k := range strength {
		if seen[k] == 0 {
			t.Errorf("no trial was %s: the property never compared it", k)
		}
	}
}

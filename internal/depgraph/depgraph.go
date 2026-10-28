// Package depgraph is the dependency graph of a transactional history and
// Adya's classification of its cycles, shared by qhistory (a real
// database's history, from its clients' reads) and qenv's store checker
// (its own commits): one classifier, so the two name a cycle alike.
//
// Edges are ww (the second installed the version after the first's), wr
// (the second read the first's version) and rw (an anti-dependency: the
// second installed the version after the one the first read). A cycle is
// G0 if every edge is ww, G1c if none is rw, G-single with exactly one
// rw, G-nonadjacent with two or more never two in a row (the last and the
// first are in a row too), and G2 otherwise: write skew, whose rw edges
// are adjacent.
package depgraph

import (
	"maps"
	"slices"
	"sort"
)

// Edge is a dependency to To.
type Edge struct {
	To   int64
	Kind string // "ww", "wr" or "rw"
	Key  string
}

// Hop is an edge with its source: one step of a cycle.
type Hop struct {
	From int64
	Edge
}

// The anomalies a cycle can be, strongest (forbidden at the weakest
// level) first.
const (
	G0           = "G0"
	G1c          = "G1c"
	GSingle      = "G-single"
	GNonadjacent = "G-nonadjacent"
	G2           = "G2"
)

// Graph is a dependency graph, built by Add.
type Graph struct {
	out, in map[int64][]Edge // in's edges name their source as To
}

// New returns an empty graph.
func New() *Graph { return &Graph{out: map[int64][]Edge{}, in: map[int64][]Edge{}} }

// Add adds an edge; one from a transaction to itself is no dependency.
func (g *Graph) Add(from, to int64, kind, key string) {
	if from == to {
		return
	}
	g.out[from] = append(g.out[from], Edge{to, kind, key})
	g.in[to] = append(g.in[to], Edge{from, kind, key})
}

// Components returns the graph's strongly connected components of more
// than one transaction (Tarjan), each sorted, in order of their least id.
func (g *Graph) Components() [][]int64 {
	nodes := slices.Sorted(maps.Keys(g.out))
	index, low, on := map[int64]int{}, map[int64]int{}, map[int64]bool{}
	var stack []int64
	var sccs [][]int64
	var visit func(v int64)
	visit = func(v int64) {
		index[v], low[v] = len(index), len(index)
		stack, on[v] = append(stack, v), true
		for _, e := range g.out[v] {
			if _, ok := index[e.To]; !ok {
				visit(e.To)
				low[v] = min(low[v], low[e.To])
			} else if on[e.To] {
				low[v] = min(low[v], index[e.To])
			}
		}
		if low[v] == index[v] {
			var c []int64
			for {
				w := stack[len(stack)-1]
				stack, on[w] = stack[:len(stack)-1], false
				c = append(c, w)
				if w == v {
					break
				}
			}
			if len(c) > 1 {
				slices.Sort(c)
				sccs = append(sccs, c)
			}
		}
	}
	for _, n := range nodes {
		if _, ok := index[n]; !ok {
			visit(n)
		}
	}
	sort.Slice(sccs, func(i, j int) bool { return sccs[i][0] < sccs[j][0] })
	return sccs
}

// Component returns the strongly connected component holding n, sorted,
// or nil if n is on no cycle: what reaches n of what n reaches. A
// checker adding one transaction at a time classifies only its
// component, where every new cycle is.
func (g *Graph) Component(n int64) []int64 {
	reach := func(adj map[int64][]Edge, within map[int64]bool) map[int64]bool {
		seen := map[int64]bool{n: true}
		queue := []int64{n}
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			for _, e := range adj[v] {
				if !seen[e.To] && (within == nil || within[e.To]) {
					seen[e.To] = true
					queue = append(queue, e.To)
				}
			}
		}
		return seen
	}
	both := reach(g.in, reach(g.out, nil))
	if len(both) < 2 {
		return nil
	}
	return slices.Sorted(maps.Keys(both))
}

// Classify finds the component's cycle of the strongest anomaly: ww
// edges only (G0), ww and wr (G1c), exactly one rw (G-single), two or
// more never adjacent (G-nonadjacent), else any (G2).
func (g *Graph) Classify(scc []int64) (string, []Hop) {
	in := map[int64]bool{}
	for _, n := range scc {
		in[n] = true
	}
	// path is a shortest path from a to b over the component's edges of the
	// kinds allowed, or nil
	path := func(a, b int64, allowed func(string) bool) []Hop {
		prev := map[int64]Hop{}
		queue, visited := []int64{a}, map[int64]bool{a: true}
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			for _, e := range g.out[v] {
				if !in[e.To] || !allowed(e.Kind) || visited[e.To] {
					continue
				}
				visited[e.To], prev[e.To] = true, Hop{v, e}
				if e.To == b {
					var p []Hop
					for n := b; n != a; n = prev[n].From {
						p = append(p, prev[n])
					}
					slices.Reverse(p)
					return p
				}
				queue = append(queue, e.To)
			}
		}
		return nil
	}
	cycle := func(allowed func(string) bool) []Hop {
		for _, n := range scc {
			for _, e := range g.out[n] {
				if in[e.To] && allowed(e.Kind) {
					if back := path(e.To, n, allowed); back != nil {
						return append([]Hop{{n, e}}, back...)
					}
				}
			}
		}
		return nil
	}
	notRW := func(k string) bool { return k != "rw" }
	if c := cycle(func(k string) bool { return k == "ww" }); c != nil {
		return G0, c
	}
	if c := cycle(notRW); c != nil {
		return G1c, c
	}
	for _, n := range scc {
		for _, e := range g.out[n] {
			if in[e.To] && e.Kind == "rw" {
				if back := path(e.To, n, notRW); back != nil {
					return GSingle, append([]Hop{{n, e}}, back...)
				}
			}
		}
	}
	if c := g.nonadjacent(scc, everyNode{set: in}); c != nil {
		return GNonadjacent, c
	}
	return G2, cycle(func(string) bool { return true })
}

// ClassifyThrough finds the strongest cycle through n of n's component
// (Component; nil: the whole graph, whose cycles through n are all in
// it, without the cost of finding it), searching from n alone: what a checker adding one
// transaction at a time needs, since every new cycle goes through the
// transaction just added, at a cost linear in the component (Classify's
// is quadratic, and a component of cycles the level allows grows with
// the history). Up to the kinds of most: "" when none of those is
// through n (G2 if every kind may be).
func (g *Graph) ClassifyThrough(scc []int64, n int64, most string) (string, []Hop) {
	in := everyNode{}
	if scc != nil {
		in = everyNode{set: map[int64]bool{}}
		for _, v := range scc {
			in.set[v] = true
		}
	}
	// cycle is a shortest cycle through n over edges of the kinds allowed
	cycle := func(allowed func(string) bool) []Hop {
		prev := map[int64]Hop{}
		queue, seen := []int64{n}, map[int64]bool{n: true}
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			for _, e := range g.out[v] {
				if !in.has(e.To) || !allowed(e.Kind) {
					continue
				}
				if e.To == n {
					p := []Hop{{v, e}}
					for at := v; at != n; at = prev[at].From {
						p = append(p, prev[at])
					}
					slices.Reverse(p)
					return p
				}
				if !seen[e.To] {
					seen[e.To], prev[e.To] = true, Hop{v, e}
					queue = append(queue, e.To)
				}
			}
		}
		return nil
	}
	notRW := func(k string) bool { return k != "rw" }
	order := []string{G0, G1c, GSingle, GNonadjacent, G2}
	for _, kind := range order {
		var c []Hop
		switch kind {
		case G0:
			c = cycle(func(k string) bool { return k == "ww" })
		case G1c:
			c = cycle(notRW)
		case GSingle:
			c = g.singleThrough(n, in)
		case GNonadjacent:
			c = g.nonadjacent([]int64{n}, in)
		case G2:
			c = cycle(func(string) bool { return true })
		}
		if c != nil {
			return kind, c
		}
		if kind == most {
			break
		}
	}
	return "", nil
}

// singleThrough finds a cycle through n with exactly one rw edge, u -rw->
// v: n reaches u and v reaches n over the component's other edges.
func (g *Graph) singleThrough(n int64, in everyNode) []Hop {
	// fwd: n's reach over non-rw edges, with the hop into each; back: the
	// nodes that reach n so, with the hop out of each
	fwd, back := map[int64]Hop{n: {}}, map[int64]Hop{n: {}}
	for queue := []int64{n}; len(queue) > 0; queue = queue[1:] {
		for _, e := range g.out[queue[0]] {
			if _, ok := fwd[e.To]; in.has(e.To) && e.Kind != "rw" && !ok {
				fwd[e.To] = Hop{queue[0], e}
				queue = append(queue, e.To)
			}
		}
	}
	// no rw edge out of n's reach: no such cycle, whatever reaches n (the
	// backward search was over every earlier transaction that reaches the
	// newest commit, at every commit of a store's GOTOGETHER_CHECK)
	rw := false
	for u := range fwd {
		for _, e := range g.out[u] {
			rw = rw || e.Kind == "rw" && in.has(e.To)
		}
	}
	if !rw {
		return nil
	}
	for queue := []int64{n}; len(queue) > 0; queue = queue[1:] {
		for _, e := range g.in[queue[0]] { // e.To is the edge's source
			if _, ok := back[e.To]; in.has(e.To) && e.Kind != "rw" && !ok {
				back[e.To] = Hop{e.To, Edge{queue[0], e.Kind, e.Key}}
				queue = append(queue, e.To)
			}
		}
	}
	for u := range fwd {
		for _, e := range g.out[u] {
			if _, ok := back[e.To]; !ok || e.Kind != "rw" || !in.has(e.To) {
				continue
			}
			var c []Hop
			for at := u; at != n; at = fwd[at].From {
				c = append(c, fwd[at])
			}
			slices.Reverse(c)
			c = append(c, Hop{u, e})
			for at := e.To; at != n; at = back[at].To {
				c = append(c, back[at])
			}
			return c
		}
	}
	return nil
}

// nonadjacent finds a cycle through one of starts, within in, with no two
// rw edges in a row: a breadth-first search over (transaction, whether the
// edge into it was rw) from each first edge. Called after the G0, G1c and
// G-single searches over the same cycles, so any it finds has two rw edges
// or more.
func (g *Graph) nonadjacent(starts []int64, in everyNode) []Hop {
	type state struct {
		node  int64
		lastR bool // the edge into node was rw
	}
	type step struct {
		from state
		Hop
	}
	for _, n := range starts {
		for _, first := range g.out[n] {
			if !in.has(first.To) {
				continue
			}
			firstR := first.Kind == "rw"
			start := state{first.To, firstR}
			prev := map[state]step{}
			seen := map[state]bool{start: true}
			queue := []state{start}
			for len(queue) > 0 {
				st := queue[0]
				queue = queue[1:]
				for _, e := range g.out[st.node] {
					r := e.Kind == "rw"
					if !in.has(e.To) || r && st.lastR {
						continue
					}
					if e.To == n {
						if r && firstR {
							continue
						}
						var path []Hop
						for at := st; at != start; at = prev[at].from {
							path = append(path, prev[at].Hop)
						}
						slices.Reverse(path)
						return append(append([]Hop{{n, first}}, path...), Hop{st.node, e})
					}
					next := state{e.To, r}
					if !seen[next] {
						seen[next], prev[next] = true, step{st, Hop{st.node, e}}
						queue = append(queue, next)
					}
				}
			}
		}
	}
	return nil
}

// everyNode is a set of transactions, every one when set is nil.
type everyNode struct{ set map[int64]bool }

func (e everyNode) has(n int64) bool { return e.set == nil || e.set[n] }

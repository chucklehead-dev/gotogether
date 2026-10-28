package qeval

import (
	"slices"
)

// Liveness as cycles (P6): on a state graph Explore found, the states
// where an eventual val is false ("hot"), the strongly connected
// components of the graph restricted to them, and in each a fair cycle if
// one exists: a lasso, the path to the component's nearest state and a
// cycle through it, is a behaviour of the model that stays hot forever.
//
// Fairness is weak and per family (an action, Families says which label is
// which), with TLA's stuttering: an edge back to its own state is no step
// of its family, neither enabling it nor taking it, so a read that loops
// everywhere obliges nothing and satisfies nothing. A family is enabled
// in a state with an edge of it to another state (a state the search did
// not add, To -1, included); a weakly fair family enabled in every state
// of a cycle must be taken by one of its edges. Weak fairness needs no
// refinement of a component: a cycle through a subset of its states has
// at least the families enabled throughout it and takes at most its
// edges, so a component whose own families fail has no fair cycle in it,
// and one whose families pass has the cycle through all of it. Unfair
// families may be taken in a cycle and oblige nothing; families that
// stop (a settle turns them off) are never taken in one.

// Fairness is how the liveness search treats a family of edges.
type Fairness uint8

const (
	// WeakFair: taken in a cycle where it is enabled throughout
	WeakFair Fairness = iota
	// Unfair: never obliged, and may be taken forever (an input a client
	// may send or not)
	Unfair
	// Stops: taken only finitely often, so never in a cycle (a fault the
	// environment stops making at a settle)
	Stops
)

// Families name each label's family for the liveness search: Of is the
// family of each of the graph's labels (an index into Fair), Fair each
// family's fairness.
type Families struct {
	Of   []int
	Fair []Fairness
}

// A Lasso is a behaviour that stays in a component forever: the path to
// Entry (Graph.Path), then Cycle, edges from Entry back to it, forever.
// A nil Cycle stutters at Entry: no weakly fair family is enabled there.
type Lasso struct {
	Entry int
	Cycle []Edge
}

// A Component is a strongly connected component of the graph restricted
// to the hot states (and to the edges of families that do not stop):
// its States in id order, the weakly fair families enabled in every one
// of them (Obliged), those of them no edge inside it takes (Missed). It
// is fair when none is missed, and then Lasso is its fair cycle; an
// unfair one shows what fairness rules out (its Missed family is enabled
// throughout and never taken).
type Component struct {
	States  []int
	Obliged []int
	Missed  []int
	Lasso   *Lasso
}

// Fair says the component has a fair cycle.
func (c Component) Fair() bool { return len(c.Missed) == 0 }

// HotComponents are the components of the graph restricted to the
// expanded states where hot holds (a state not expanded has no known
// edges, and one the state bound cut while expanding, its edge to -1,
// not all of them, so no cycle goes through either), in order of their nearest state:
// every component with two states or more, and every single state where
// no weakly fair family is enabled (a fair stutter). A single state with
// one enabled is no component of the result (its only cycle, a
// stutter, is unfair). Each fair one carries its lasso.
func (g *Graph) HotComponents(hot []bool, fam Families) []Component {
	n := len(g.Out)
	in := func(s int) bool { return s >= 0 && s < n && s < len(hot) && hot[s] && g.whole(s) }
	inner := func(e Edge) bool { return fam.Fair[fam.Of[e.Label]] != Stops }
	// each state's weakly fair families enabled (by an edge to another
	// state), sorted
	enabled := func(s int) []int {
		var out []int
		for _, e := range g.Out[s] {
			if e.To == s {
				continue
			}
			if f := fam.Of[e.Label]; fam.Fair[f] == WeakFair && !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
		slices.Sort(out)
		return out
	}
	comp := g.sccs(in, inner)
	var out []Component
	for _, states := range comp {
		c := Component{States: states}
		members := map[int]bool{}
		for _, s := range states {
			members[s] = true
		}
		c.Obliged = enabled(states[0])
		for _, s := range states[1:] {
			c.Obliged = intersect(c.Obliged, enabled(s))
		}
		taken := map[int]bool{}
		for _, s := range states {
			for _, e := range g.Out[s] {
				if e.To != s && members[e.To] && inner(e) {
					taken[fam.Of[e.Label]] = true
				}
			}
		}
		for _, f := range c.Obliged {
			if !taken[f] {
				c.Missed = append(c.Missed, f)
			}
		}
		if len(states) == 1 && len(c.Obliged) > 0 {
			continue // a state's stutter, unfair: no component
		}
		if c.Fair() {
			c.Lasso = g.fairCycle(states, members, fam, enabled, inner)
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Component) int { return a.States[0] - b.States[0] })
	return out
}

// intersect is the sorted lists' common elements.
func intersect(a, b []int) []int {
	var out []int
	for _, x := range a {
		if _, ok := slices.BinarySearch(b, x); ok {
			out = append(out, x)
		}
	}
	return out
}

// sccs are the strongly connected components of the subgraph of the
// states in admits (in) and the edges inner admits between them (Tarjan's,
// iterative: a graph's paths may be 10^5 long), each sorted.
func (g *Graph) sccs(in func(int) bool, inner func(Edge) bool) [][]int {
	n := len(g.Out)
	index, low := make([]int, n), make([]int, n)
	for i := range index {
		index[i] = -1
	}
	onStack := make([]bool, n)
	var stack []int
	var out [][]int
	next := 0
	type frame struct{ s, e int }
	for root := range n {
		if !in(root) || index[root] >= 0 {
			continue
		}
		calls := []frame{{root, 0}}
		index[root], low[root] = next, next
		next++
		stack = append(stack, root)
		onStack[root] = true
		for len(calls) > 0 {
			f := &calls[len(calls)-1]
			s := f.s
			if f.e < len(g.Out[s]) {
				e := g.Out[s][f.e]
				f.e++
				t := e.To
				if !in(t) || !inner(e) {
					continue
				}
				if index[t] < 0 {
					index[t], low[t] = next, next
					next++
					stack = append(stack, t)
					onStack[t] = true
					calls = append(calls, frame{t, 0})
				} else if onStack[t] {
					low[s] = min(low[s], index[t])
				}
				continue
			}
			calls = calls[:len(calls)-1]
			if len(calls) > 0 {
				p := calls[len(calls)-1].s
				low[p] = min(low[p], low[s])
			}
			if low[s] == index[s] {
				var c []int
				for {
					t := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[t] = false
					c = append(c, t)
					if t == s {
						break
					}
				}
				slices.Sort(c)
				out = append(out, c)
			}
		}
	}
	return out
}

// fairCycle is a fair cycle of a fair component through its nearest
// state: a stutter there if no weakly fair family is enabled in it, else
// a closed walk through waypoints, each added for a family the walk so
// far leaves enabled throughout and untaken: an edge of it inside the
// component, or, where none is, a state of it where the family is not
// enabled (one exists, the component being fair). Each waypoint settles
// its family for good, so there are at most as many as families.
func (g *Graph) fairCycle(states []int, members map[int]bool, fam Families, enabled func(int) []int, inner func(Edge) bool) *Lasso {
	entry := states[0]
	if len(enabled(entry)) == 0 {
		return &Lasso{Entry: entry}
	}
	type waypoint struct {
		edge  *Edge // take this edge (from from)
		from  int
		state int // or visit this state
	}
	var wps []waypoint
	// path is a shortest path inside the component from a to b (none if
	// a == b), as edges
	path := func(a, b int) []Edge {
		if a == b {
			return nil
		}
		prev := map[int]Edge{}
		from := map[int]int{a: a}
		for q := []int{a}; len(q) > 0; {
			s := q[0]
			q = q[1:]
			for _, e := range g.Out[s] {
				if _, seen := from[e.To]; seen || !members[e.To] || !inner(e) {
					continue
				}
				from[e.To], prev[e.To] = s, e
				q = append(q, e.To)
			}
			if _, ok := from[b]; ok {
				break
			}
		}
		var p []Edge
		for x := b; x != a; x = from[x] {
			p = append(p, prev[x])
		}
		slices.Reverse(p)
		return p
	}
	build := func() []Edge {
		var cyc []Edge
		cur := entry
		for _, w := range wps {
			if w.edge != nil {
				cyc = append(cyc, path(cur, w.from)...)
				cyc = append(cyc, *w.edge)
				cur = w.edge.To
			} else {
				cyc = append(cyc, path(cur, w.state)...)
				cur = w.state
			}
		}
		return append(cyc, path(cur, entry)...)
	}
	var cyc []Edge
	for {
		cyc = build()
		visited := []int{entry}
		taken := map[int]bool{}
		from := entry
		for _, e := range cyc {
			if e.To != from {
				taken[fam.Of[e.Label]] = true
			}
			from = e.To
			if !slices.Contains(visited, e.To) {
				visited = append(visited, e.To)
			}
		}
		obliged := enabled(entry)
		for _, s := range visited[1:] {
			obliged = intersect(obliged, enabled(s))
		}
		missed := -1
		for _, f := range obliged {
			if !taken[f] {
				missed = f
				break
			}
		}
		if missed < 0 && len(cyc) > 0 {
			return &Lasso{Entry: entry, Cycle: cyc}
		}
		// an edge of the missed family inside, else a state where it is not
		// enabled; with nothing missed (an empty walk), any edge out of the
		// entry inside the component
		added := false
		for _, s := range states {
			for _, e := range g.Out[s] {
				if e.To != s && members[e.To] && inner(e) && (missed < 0 || fam.Of[e.Label] == missed) {
					wps = append(wps, waypoint{edge: &e, from: s})
					added = true
					break
				}
			}
			if added {
				break
			}
		}
		if !added {
			for _, s := range states {
				if !slices.Contains(enabled(s), missed) {
					wps = append(wps, waypoint{state: s})
					added = true
					break
				}
			}
		}
		if !added {
			return nil // not fair after all: the caller's check says it is
		}
	}
}

// whole says state s was expanded and every one of its edges is known:
// the state bound did not cut the search while it expanded s (an edge
// to -1, its last).
func (g *Graph) whole(s int) bool {
	return g.Expanded[s] && !slices.ContainsFunc(g.Out[s], func(e Edge) bool { return e.To < 0 })
}

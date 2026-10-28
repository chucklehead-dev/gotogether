package qeval

import (
	"container/heap"
	"fmt"
	"slices"
	"strconv"
)

// The reachability search (P3's possibility checks, the seed of P2's
// state-graph explorer): from a start, the model's step taken breadth
// first, distinct states by Key, a parent tree for the path to what it
// finds, bounded by states and depth, and saying when the bound cut it
// short. A goal not found by a search the bound did not cut is
// impossible (every reachable state up to the depth seen); one the bound
// cut is only not found.

// A Bound bounds a search (Reach): at most States search nodes (a state,
// with a sequence goal's progress), and paths of at most Depth
// transitions from the start. 0 is no bound.
type Bound struct {
	States, Depth int
}

// A Goal is what a search looks for: a state where Holds is true, or a
// path whose transitions take each of Steps in turn (any others between
// them). Holds is checked in the start state too.
type Goal struct {
	Holds func(State) (bool, error)
	Steps []func(Transition) bool
}

// A Reached is a search's answer to one goal (Reach).
type Reached struct {
	// Found says the goal was met: Path are the transitions from the
	// start that meet it (the last one reaching its state, or taking its
	// last step), the shortest for a state goal
	Found bool
	Path  []Transition
	// Nodes are the search nodes it visited; Depth the longest path it
	// expanded. Complete: no bound cut it, so a goal not found is
	// unreachable within Bound.Depth (or at all, with no depth bound).
	Nodes, Depth int
	Complete     bool
	// Cut says which bound cut the search ("states", "depth"), "" for none
	Cut string
}

// Reach searches from start by action step's transitions for each goal:
// the state goals in one breadth-first search (each path the shortest),
// each sequence goal in its own, best first (the most of its steps taken,
// then the shortest), so a scenario the model can follow directly is
// found in as many expansions as it has steps. The transitions of a
// state are computed once for every search.
func (m *Model) Reach(consts map[string]Value, start State, step string, goals []Goal, b Bound) ([]Reached, error) {
	memo := map[string][]Transition{}
	succ := func(s State, k string) ([]Transition, error) {
		if ts, ok := memo[k]; ok {
			return ts, nil
		}
		ts, err := m.Transitions(consts, s, step)
		if err != nil {
			return nil, err
		}
		memo[k] = ts
		return ts, nil
	}
	out := make([]Reached, len(goals))
	// the state goals and the one-step sequences share one breadth-first
	// search (a step is a property of a transition); longer sequences
	// have their own, their progress part of a node
	var shared []int
	var holds []func(State) (bool, error)
	var edges []func(Transition) bool
	for i, g := range goals {
		if (g.Holds == nil) == (len(g.Steps) == 0) {
			return nil, fmt.Errorf("qeval: goal %d is a state predicate or a sequence of steps, not both nor neither", i)
		}
		switch {
		case g.Holds != nil:
			shared, holds, edges = append(shared, i), append(holds, g.Holds), append(edges, nil)
		case len(g.Steps) == 1:
			shared, holds, edges = append(shared, i), append(holds, nil), append(edges, g.Steps[0])
		default:
			r, err := m.reach(start, g.Steps, nil, nil, b, succ)
			if err != nil {
				return nil, err
			}
			out[i] = r[0]
		}
	}
	if len(shared) > 0 {
		rs, err := m.reach(start, nil, holds, edges, b, succ)
		if err != nil {
			return nil, err
		}
		for j, i := range shared {
			out[i] = rs[j]
		}
	}
	return out, nil
}

// a node of a search: a state, the goal's steps taken so far, its depth,
// its parent's index and the transition from it (-1: the start)
type node struct {
	s        State
	key      string
	progress int
	depth    int
	parent   int
	via      Transition
}

// reach is one search: for a sequence of steps (its nodes a state and
// the steps taken), or for several goals at once (its nodes states), each
// a state predicate (holds[g]) or a transition's (edges[g]), until every
// goal is met or a bound cuts it. Its answers are one per goal, or one
// for the steps.
func (m *Model) reach(start State, steps []func(Transition) bool, holds []func(State) (bool, error), edges []func(Transition) bool, b Bound,
	succ func(State, string) ([]Transition, error)) ([]Reached, error) {
	n := max(len(holds), 1)
	out := make([]Reached, n)
	left := n
	nodes := []node{{s: start, key: m.Key(start), parent: -1}}
	seen := map[string]bool{nodes[0].key + "#0": true}
	pathTo := func(i int) []Transition {
		var path []Transition
		for j := i; nodes[j].parent >= 0; j = nodes[j].parent {
			path = append(path, nodes[j].via)
		}
		slices.Reverse(path)
		return path
	}
	// check meets the unmet predicates at node i
	check := func(i int) error {
		for g, h := range holds {
			if out[g].Found || h == nil {
				continue
			}
			ok, err := h(nodes[i].s)
			if err != nil {
				return err
			}
			if ok {
				out[g].Found, out[g].Path = true, pathTo(i)
				left--
			}
		}
		return nil
	}
	if err := check(0); err != nil {
		return nil, err
	}
	complete, cut, depth := true, "", 0
	q := &frontier{nodes: &nodes}
	heap.Push(q, 0)
search:
	for left > 0 && q.Len() > 0 {
		i := heap.Pop(q).(int)
		nd := nodes[i]
		if b.Depth > 0 && nd.depth >= b.Depth {
			complete, cut = false, "depth"
			continue
		}
		ts, err := succ(nd.s, nd.key)
		if err != nil {
			return nil, err
		}
		depth = max(depth, nd.depth+1)
		for _, t := range ts {
			// a transition goal, met by this transition whatever state it
			// reaches (one seen already too)
			for g, e := range edges {
				if e != nil && !out[g].Found && e(t) {
					out[g].Found, out[g].Path = true, append(pathTo(i), t)
					left--
				}
			}
			if left == 0 {
				break search
			}
			p := nd.progress
			if p < len(steps) && steps[p](t) {
				p++
			}
			k := m.Key(t.State)
			id := k + "#" + strconv.Itoa(p)
			if seen[id] {
				continue
			}
			if b.States > 0 && len(nodes) >= b.States {
				complete, cut = false, "states"
				break search
			}
			seen[id] = true
			nodes = append(nodes, node{s: t.State, key: k, progress: p, depth: nd.depth + 1, parent: i, via: t})
			j := len(nodes) - 1
			if len(steps) > 0 && p == len(steps) {
				out[0].Found, out[0].Path = true, pathTo(j)
				left = 0
				break search
			}
			if err := check(j); err != nil {
				return nil, err
			}
			if left == 0 {
				break search
			}
			heap.Push(q, j)
		}
	}
	for g := range out {
		out[g].Nodes, out[g].Depth = len(nodes), depth
		if !out[g].Found {
			out[g].Complete, out[g].Cut = complete, cut
		}
	}
	return out, nil
}

// frontier orders a search's open nodes: the most steps taken first,
// then the shallowest, then the oldest (breadth first among equals).
type frontier struct {
	nodes *[]node
	open  []int
}

func (f *frontier) Len() int { return len(f.open) }
func (f *frontier) Less(a, b int) bool {
	x, y := (*f.nodes)[f.open[a]], (*f.nodes)[f.open[b]]
	if x.progress != y.progress {
		return x.progress > y.progress
	}
	if x.depth != y.depth {
		return x.depth < y.depth
	}
	return f.open[a] < f.open[b]
}
func (f *frontier) Swap(a, b int) { f.open[a], f.open[b] = f.open[b], f.open[a] }
func (f *frontier) Push(x any)    { f.open = append(f.open, x.(int)) }
func (f *frontier) Pop() any {
	x := f.open[len(f.open)-1]
	f.open = f.open[:len(f.open)-1]
	return x
}

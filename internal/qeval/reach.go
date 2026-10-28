package qeval

import "fmt"

// The reachability search (P3's possibility checks, on the search core
// it shares with P2's state-graph explorer: graph.go): from a start, the
// model's step taken breadth first, distinct states by Key, a parent tree for the path to what it
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
	s := &search{m: m, b: b, succ: succ, steps: steps}
	// a node meets the unmet predicates, or ends the sequence
	s.added = func(j int) (bool, error) {
		if len(steps) > 0 && s.nodes[j].progress == len(steps) {
			out[0].Found, out[0].Path = true, s.pathTo(j)
			return true, nil
		}
		for g, h := range holds {
			if out[g].Found || h == nil {
				continue
			}
			ok, err := h(s.nodes[j].s)
			if err != nil {
				return true, err
			}
			if ok {
				out[g].Found, out[g].Path = true, s.pathTo(j)
				left--
			}
		}
		return left == 0, nil
	}
	// a transition goal, met by a transition whatever state it reaches (one
	// seen already too)
	s.edge = func(i int, t Transition, _ int) bool {
		for g, e := range edges {
			if e != nil && !out[g].Found && e(t) {
				out[g].Found, out[g].Path = true, append(s.pathTo(i), t)
				left--
			}
		}
		return left == 0
	}
	if err := s.run([]State{start}); err != nil {
		return nil, err
	}
	for g := range out {
		out[g].Nodes, out[g].Depth = len(s.nodes), s.depth
		if !out[g].Found {
			out[g].Complete, out[g].Cut = s.complete, s.cut
		}
	}
	return out, nil
}

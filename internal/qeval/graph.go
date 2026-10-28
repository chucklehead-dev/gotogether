package qeval

import (
	"container/heap"
	"errors"
	"slices"
	"strconv"
	"strings"
)

// The search core (P2's state-graph explorer and P3's Reach): from its
// starts, the model's step taken in order of the frontier (breadth first,
// or best first for a sequence goal), distinct nodes by Key (and a
// sequence's progress), dense ids in the order nodes are found, a parent
// tree for the path to each, bounded by states and depth and saying which
// bound cut it. Explore keeps every node's out-edges as well (the graph);
// Reach keeps only what its goals need.

// a node of a search: a state, the goal's steps taken so far, its depth,
// its parent's index and the transition from it (-1: a start)
type node struct {
	s        State
	key      string
	progress int
	depth    int
	parent   int
	via      []Event
}

// search is one run of the core. Its hooks may stop it: added for each
// node found (the starts too), edge for each transition of an expanded
// node, with the node it reaches (-1: the state bound cut the search
// there), expanded for each node whose transitions were computed.
type search struct {
	m     *Model
	b     Bound
	succ  func(s State, key string) ([]Transition, error)
	steps []func(Transition) bool

	nodes []node
	// the node of each (state, progress), the shallowest found: under a
	// depth bound, one reached again by a shorter path is a node again,
	// since best first may reach it first by a longer one (more steps
	// taken sooner) and the bound would then cut what the shorter one
	// reaches. Breadth first never does: its first path is the shortest.
	seen     map[string]int
	complete bool
	cut      string
	depth    int

	added    func(j int) (stop bool, err error)
	edge     func(i int, t Transition, j int) (stop bool)
	expanded func(i int, ts []Transition)
}

// pathTo is the transitions from node i's start to it.
func (s *search) pathTo(i int) []Transition {
	var path []Transition
	for j := i; s.nodes[j].parent >= 0; j = s.nodes[j].parent {
		path = append(path, Transition{Events: s.nodes[j].via, State: s.nodes[j].s})
	}
	slices.Reverse(path)
	return path
}

// run searches from starts until a hook stops it, its frontier is empty,
// or the state bound cuts it. An error is the evaluator's (Unsupported,
// EvalError) or a hook's: the nodes found before it stay.
func (s *search) run(starts []State) error {
	s.seen, s.complete = map[string]int{}, true
	q := &frontier{nodes: &s.nodes}
	for _, st := range starts {
		k := s.m.Key(st)
		if _, ok := s.seen[k+"#0"]; ok {
			continue
		}
		if s.b.States > 0 && len(s.nodes) >= s.b.States {
			s.complete, s.cut = false, "states"
			return nil
		}
		s.seen[k+"#0"] = len(s.nodes)
		s.nodes = append(s.nodes, node{s: st, key: k, parent: -1})
		if s.added != nil {
			if stop, err := s.added(len(s.nodes) - 1); err != nil || stop {
				return err
			}
		}
		heap.Push(q, len(s.nodes)-1)
	}
	for q.Len() > 0 {
		i := heap.Pop(q).(int)
		nd := s.nodes[i]
		if s.b.Depth > 0 && nd.depth >= s.b.Depth {
			s.complete, s.cut = false, "depth"
			continue
		}
		ts, err := s.succ(nd.s, nd.key)
		if err != nil {
			return err
		}
		if s.expanded != nil {
			s.expanded(i, ts)
		}
		s.depth = max(s.depth, nd.depth+1)
		for _, t := range ts {
			p := nd.progress
			if p < len(s.steps) && s.steps[p](t) {
				p++
			}
			k := t.key
			if k == "" {
				k = s.m.Key(t.State)
			}
			id := k + "#" + strconv.Itoa(p)
			if j, ok := s.seen[id]; ok && (s.b.Depth == 0 || s.nodes[j].depth <= nd.depth+1) {
				if s.edge != nil && s.edge(i, t, j) {
					return nil
				}
				continue
			}
			if s.b.States > 0 && len(s.nodes) >= s.b.States {
				s.complete, s.cut = false, "states"
				if s.edge != nil {
					s.edge(i, t, -1)
				}
				return nil
			}
			// the edge first, with the id the node will have: a goal it meets
			// stops the search before the node is added
			j := len(s.nodes)
			if s.edge != nil && s.edge(i, t, j) {
				return nil
			}
			s.seen[id] = j
			s.nodes = append(s.nodes, node{s: t.State, key: k, progress: p, depth: nd.depth + 1, parent: i, via: t.Events})
			if s.added != nil {
				if stop, err := s.added(j); err != nil || stop {
					return err
				}
			}
			heap.Push(q, j)
		}
	}
	return nil
}

// frontier orders a search's open nodes: the most steps taken first,
// then the shallowest, then the oldest (breadth first among equals, and
// so breadth first when there are no steps).
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

// A Graph is a model's state graph as Explore found it: its states by
// dense id (in the order found, so by depth: an id's path from its start
// is a shortest one, and the first id where a predicate holds is a
// nearest such state), a parent tree, and each expanded state's
// out-edges, labelled by what led to them (interned: Labels).
type Graph struct {
	// States are the states found, by id; Starts the number of them that
	// are initial states (ids 0 to Starts-1)
	States []State
	Starts int
	// Parent is each state's parent in the tree of shortest paths (-1 for
	// a start), Via the label of the edge from it; Depth its distance from
	// its start
	Parent, Via, Depth []int
	// Out are each state's edges, nil for a state not expanded (the depth
	// bound reached it, or the search stopped first): an expanded state
	// with no edge is a deadlock. Expanded says which were.
	Out      [][]Edge
	Expanded []bool
	// Labels are the edges' labels: the events (actions applied, choices
	// made) of a transition, each distinct list once
	Labels [][]Event
	// Complete: no bound cut the search and the evaluator evaluated every
	// state it reached, so every state reachable from the starts (within
	// Bound.Depth, if one is set) is in States. Else Cut says what cut it:
	// "states", "depth", or "unsupported" (Explore's error says why).
	Complete bool
	Cut      string
	// Transitions are the transitions the evaluator computed: the edges
	// before labels merged them (Edges counts the edges kept)
	Transitions int

	ids    map[string]int
	labels map[string]int
	m      *Model
}

// An Edge is a transition of the graph: its label (Graph.Labels) and the
// id of the state it reaches (-1: the state bound cut the search before
// that state was added).
type Edge struct {
	Label, To int
}

// Explore is the state graph of step from init's states (every successor
// of action init from no state), breadth first, bounded by b, every
// edge kept: one per distinct label and state reached, the label each
// transition's events, or what label makes of them (nil: all of them).
// A label that drops events (a step's choices no branch uses: bank's
// step picks a source and destination for a deposit too) merges the
// edges that differ only in them. An error is the evaluator's (an *Unsupported construct, an
// *EvalError) or init's: the graph found before it is returned with it,
// not Complete, cut "unsupported" for an Unsupported one, and holds only
// real states and edges (a path in it is a trace of the model).
func (m *Model) Explore(consts map[string]Value, init, step string, b Bound, label func([]Event) []Event) (*Graph, error) {
	g := &Graph{ids: map[string]int{}, labels: map[string]int{}, m: m}
	if init == "" {
		init = "init"
	}
	starts, err := m.Successors(consts, nil, init)
	if err != nil {
		g.Cut = cutOf(err)
		return g, err
	}
	s := &search{m: m, b: b, succ: func(st State, _ string) ([]Transition, error) { return m.Transitions(consts, st, step) }}
	s.added = func(j int) (bool, error) {
		nd := s.nodes[j]
		g.States = append(g.States, nd.s)
		g.Parent = append(g.Parent, nd.parent)
		g.Depth = append(g.Depth, nd.depth)
		g.Via = append(g.Via, -1)
		if nd.parent >= 0 {
			evs := nd.via
			if label != nil {
				evs = label(evs)
			}
			g.Via[j] = g.label(evs)
		}
		g.Out = append(g.Out, nil)
		g.Expanded = append(g.Expanded, false)
		g.ids[nd.key] = j
		return false, nil
	}
	var kept map[Edge]bool // the expanded state's edges so far
	s.expanded = func(i int, ts []Transition) {
		g.Expanded[i] = true
		g.Out[i] = []Edge{}
		g.Transitions += len(ts)
		kept = map[Edge]bool{}
	}
	s.edge = func(i int, t Transition, j int) bool {
		evs := t.Events
		if label != nil {
			evs = label(evs)
		}
		e := Edge{Label: g.label(evs), To: j}
		if !kept[e] || j < 0 {
			kept[e] = true
			g.Out[i] = append(g.Out[i], e)
		}
		return false
	}
	err = s.run(starts)
	g.Complete, g.Cut = s.complete && err == nil, s.cut
	if err != nil {
		g.Cut = cutOf(err)
	}
	// the starts are the first nodes: those distinct, within the bound
	g.Starts = 0
	for g.Starts < len(g.Parent) && g.Parent[g.Starts] < 0 {
		g.Starts++
	}
	return g, err
}

// cutOf is the Cut an error leaves: "unsupported" for the evaluator's
// refusal, "error" for any other.
func cutOf(err error) string {
	var u *Unsupported
	if errors.As(err, &u) {
		return "unsupported"
	}
	return "error"
}

// label is the id of a transition's events, interned.
func (g *Graph) label(evs []Event) int {
	var b strings.Builder
	eventsKey(&b, evs)
	k := b.String()
	if id, ok := g.labels[k]; ok {
		return id
	}
	g.labels[k] = len(g.Labels)
	g.Labels = append(g.Labels, evs)
	return len(g.Labels) - 1
}

// ID is the id of state s in the graph, if it was found.
func (g *Graph) ID(s State) (int, bool) {
	id, ok := g.ids[g.m.Key(s)]
	return id, ok
}

// Path is the transitions from state id's start to it along the parent
// tree: a shortest path, each transition with the state it reaches.
func (g *Graph) Path(id int) []Transition {
	var path []Transition
	for j := id; g.Parent[j] >= 0; j = g.Parent[j] {
		path = append(path, Transition{Events: g.Labels[g.Via[j]], State: g.States[j]})
	}
	slices.Reverse(path)
	return path
}

// Start is the start of state id's path: the initial state it is
// reached from.
func (g *Graph) Start(id int) State {
	for g.Parent[id] >= 0 {
		id = g.Parent[id]
	}
	return g.States[id]
}

// Edges are the graph's edges, every expanded state's.
func (g *Graph) Edges() int {
	n := 0
	for _, out := range g.Out {
		n += len(out)
	}
	return n
}

// Deadlocks are the ids of the expanded states with no edge: states the
// step cannot leave, in id order (the nearest first).
func (g *Graph) Deadlocks() []int {
	var out []int
	for i, x := range g.Expanded {
		if x && len(g.Out[i]) == 0 {
			out = append(out, i)
		}
	}
	return out
}

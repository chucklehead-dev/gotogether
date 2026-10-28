package qeval

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// actionsOnly is a label of a transition's actions alone, its choices
// dropped: edges differing only in a choice merge.
func actionsOnly(evs []Event) []Event {
	var out []Event
	for _, e := range evs {
		if e.Action != "" {
			out = append(out, e)
		}
	}
	return out
}

// labelText is a transition's label as one string, as brute force and
// the graph compare them.
func labelText(evs []Event) string {
	var b strings.Builder
	for _, e := range evs {
		b.WriteString(e.String() + ";")
	}
	return b.String()
}

// Explore against brute force, on graph.qnt (two initial states, a
// deadlock, an action never enabled, a loop) and vending: every path of
// up to each depth enumerated by Transitions from every initial state.
// The graph's states are exactly those the paths reach, each at its
// least depth with a path of that length that the model takes; an
// expanded state's edges are exactly its transitions, to the states
// they reach; its deadlocks are the expanded states with none; it is
// complete exactly when no state lies at the depth bound; under a state
// bound it holds the first states breadth first. A depth-first frontier
// (the order ablated) fails the depths; dropping edges to seen states
// fails the edges. Each depth is explored twice: with every event in a
// label, and with the actions' alone (vending's choices merged).
func TestExploreAgainstBruteForce(t *testing.T) {
	gm := compile(t, "../../../validate/testdata/graph.qnt", "graph", "init", "step")
	vm, vconsts, _ := vending(t)
	for _, c := range []struct {
		name   string
		m      *Model
		consts map[string]Value
		depths []int
	}{
		{"graph", gm, map[string]Value{}, []int{1, 2, 3, 4, 6, 9}},
		{"vending", vm, vconsts, []int{1, 3, 5, 8}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, consts := c.m, c.consts
			inits, err := m.Successors(consts, nil, "init")
			if err != nil {
				t.Fatal(err)
			}
			for _, depth := range c.depths {
				// brute force: the least depth of each state any path of up to
				// depth transitions reaches, and each state's transitions
				least := map[string]int{}
				trans := map[string][]Transition{}
				var walk func(s State, d int)
				walk = func(s State, d int) {
					k := m.Key(s)
					if old, ok := least[k]; !ok || d < old {
						least[k] = d
					}
					if d == depth {
						return
					}
					ts, ok := trans[k]
					if !ok {
						if ts, err = m.Transitions(consts, s, "step"); err != nil {
							t.Fatal(err)
						}
						trans[k] = ts
					}
					for _, tr := range ts {
						walk(tr.State, d+1)
					}
				}
				for _, s := range inits {
					walk(s, 0)
				}
				for li, lab := range []func([]Event) []Event{nil, actionsOnly} {
					relabel := func(evs []Event) string {
						if lab != nil {
							evs = lab(evs)
						}
						return labelText(evs)
					}
					g, err := m.Explore(consts, "init", "step", Bound{Depth: depth}, lab)
					if err != nil {
						t.Fatal(err)
					}
					if len(g.States) != len(least) {
						t.Fatalf("depth %d: %d states, brute force %d", depth, len(g.States), len(least))
					}
					maxDepth := 0
					for id, s := range g.States {
						k := m.Key(s)
						d, ok := least[k]
						if !ok {
							t.Fatalf("depth %d: state %d (%s) is reached by no path", depth, id, k)
						}
						if g.Depth[id] != d {
							t.Fatalf("depth %d: state %d at depth %d, its least is %d", depth, id, g.Depth[id], d)
						}
						maxDepth = max(maxDepth, d)
						// its path: d transitions the model takes from a start
						path := g.Path(id)
						if len(path) != d {
							t.Fatalf("depth %d: state %d's path has %d transitions, its depth is %d", depth, id, len(path), d)
						}
						cur := g.Start(id)
						if !slices.ContainsFunc(inits, func(s State) bool { return m.Key(s) == m.Key(cur) }) {
							t.Fatalf("depth %d: state %d's start is no initial state", depth, id)
						}
						for _, tr := range path {
							ts, _ := m.Transitions(consts, cur, "step")
							if !slices.ContainsFunc(ts, func(x Transition) bool {
								return relabel(x.Events) == labelText(tr.Events) && m.Key(x.State) == m.Key(tr.State)
							}) {
								t.Fatalf("depth %d: state %d's path takes %s, no transition of %s", depth, id, labelText(tr.Events), m.Key(cur))
							}
							cur = tr.State
						}
						if m.Key(cur) != k {
							t.Fatalf("depth %d: state %d's path ends elsewhere", depth, id)
						}
						// its edges, if expanded (every state short of the bound is)
						if g.Expanded[id] != (d < depth) {
							t.Fatalf("depth %d: state %d at depth %d expanded %v", depth, id, d, g.Expanded[id])
						}
						if !g.Expanded[id] {
							continue
						}
						var want, got []string
						for _, tr := range trans[k] {
							if w := relabel(tr.Events) + "->" + m.Key(tr.State); !slices.Contains(want, w) {
								want = append(want, w)
							}
						}
						for _, e := range g.Out[id] {
							got = append(got, labelText(g.Labels[e.Label])+"->"+m.Key(g.States[e.To]))
						}
						slices.Sort(want)
						slices.Sort(got)
						if !slices.Equal(want, got) {
							t.Fatalf("depth %d, labels %d: state %d's edges\n%v\nbrute force\n%v", depth, li, id, got, want)
						}
						if len(want) == 0 && !slices.Contains(g.Deadlocks(), id) {
							t.Fatalf("depth %d: state %d has no transition and is no deadlock", depth, id)
						}
					}
					for _, id := range g.Deadlocks() {
						if len(trans[m.Key(g.States[id])]) != 0 {
							t.Fatalf("depth %d: deadlock %d has transitions", depth, id)
						}
					}
					if want := maxDepth < depth; g.Complete != want {
						t.Fatalf("depth %d: complete %v (cut %q), the deepest state at %d", depth, g.Complete, g.Cut, maxDepth)
					}
					if !g.Complete && g.Cut != "depth" {
						t.Fatalf("depth %d: cut %q", depth, g.Cut)
					}
				}
			}
			// unbounded: the whole graph, complete; under a state bound, its
			// first states breadth first, cut by states
			whole, err := m.Explore(consts, "init", "step", Bound{}, nil)
			if err != nil || !whole.Complete || whole.Cut != "" {
				t.Fatalf("unbounded: complete %v, cut %q, %v", whole.Complete, whole.Cut, err)
			}
			n := len(whole.States) / 2
			half, err := m.Explore(consts, "init", "step", Bound{States: n}, nil)
			if err != nil || half.Complete || half.Cut != "states" || len(half.States) != n {
				t.Fatalf("half: %d states of %d, complete %v, cut %q, %v", len(half.States), n, half.Complete, half.Cut, err)
			}
			for id := range half.States {
				if m.Key(half.States[id]) != m.Key(whole.States[id]) {
					t.Fatalf("half: state %d differs from the whole graph's", id)
				}
			}
		})
	}
}

// Explore on graph.qnt: its facts by hand. Both initial states, the
// deadlock at (3, 2), `never` on no edge, and the shortest path to the
// deadlock from the start nearer to it.
func TestExploreGraph(t *testing.T) {
	m := compile(t, "../../../validate/testdata/graph.qnt", "graph", "init", "step")
	g, err := m.Explore(map[string]Value{}, "init", "step", Bound{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// x in 0..3, y in 0..2: twelve states, all reachable
	if len(g.States) != 12 || g.Starts != 2 || !g.Complete {
		t.Fatalf("%d states, %d starts, complete %v", len(g.States), g.Starts, g.Complete)
	}
	dl := g.Deadlocks()
	if len(dl) != 1 || g.States[dl[0]]["x"] != Int(3) || g.States[dl[0]]["y"] != Int(2) {
		t.Fatalf("deadlocks %v", dl)
	}
	// from x = 1: inc, inc and bump(2) (three transitions)
	path := g.Path(dl[0])
	if len(path) != 3 || g.Start(dl[0])["x"] != Int(1) {
		t.Fatalf("path to the deadlock: %d transitions from %s", len(path), m.Key(g.Start(dl[0])))
	}
	actions := map[string]bool{}
	for _, l := range g.Labels {
		for _, e := range l {
			actions[e.Action] = true
		}
	}
	if actions["never"] || !actions["reset"] || !actions["bump"] {
		t.Fatalf("labels' actions %v", actions)
	}
	// reset leads back to (0, 0): an edge to a start, kept
	back := false
	for i, out := range g.Out {
		for _, e := range out {
			if e.To == 0 && i != 0 {
				back = true
			}
		}
	}
	if !back {
		t.Fatal("no edge back to the start (0, 0)")
	}
	if id, ok := g.ID(State{"x": Int(2), "y": Int(1)}); !ok || g.Depth[id] != 2 {
		t.Fatalf("ID of (2, 1): %d %v", id, ok)
	}
}

// An unsupported construct (grow.qnt's integer past 64 bits after five
// squarings) stops the graph: the states found before it stay, it is not
// complete, and its cut and error say why.
func TestExploreUnsupported(t *testing.T) {
	m := compile(t, "grow.qnt", "grow", "init", "step")
	g, err := m.Explore(map[string]Value{}, "init", "step", Bound{}, nil)
	var u *Unsupported
	if !errors.As(err, &u) {
		t.Fatalf("err %v, want Unsupported", err)
	}
	// 2, 4, 16, 256, 65536, 2^32; 2^64 is past
	if g == nil || g.Complete || g.Cut != "unsupported" || len(g.States) != 6 {
		t.Fatalf("graph after %v: %d states, complete %v, cut %q", err, len(g.States), g.Complete, g.Cut)
	}
}

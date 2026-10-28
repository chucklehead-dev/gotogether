package qeval

import (
	"fmt"
	"testing"
)

// vending is validate's possibility model, its constants as the module's.
func vending(t *testing.T) (*Model, map[string]Value, State) {
	m := compile(t, "../../../validate/testdata/vending.qnt", "vending", "init", "step")
	consts := map[string]Value{"PRICE": Int(2), "STOCK": Int(2)}
	s, err := m.Init(consts, "init")
	if err != nil {
		t.Fatal(err)
	}
	return m, consts, s
}

// Reach against brute force: every path of the vending model up to a
// depth enumerated by Transitions, each goal found exactly when some
// path meets it, a state goal's path as short as the shortest, a
// sequence goal's path meeting it; a complete search's "not found" is
// that no path does. Ablating the progress of a sequence goal (a step
// matched anywhere counting once) or the shortest-first order fails it.
func TestReachAgainstBruteForce(t *testing.T) {
	m, consts, init := vending(t)
	val := func(name string) func(State) (bool, error) {
		return func(s State) (bool, error) {
			switch name {
			case "soldOut":
				return s["stock"].(Int) == 0, nil
			case "sold3":
				return s["sold"].(Int) > 2, nil
			case "credit3":
				return s["credit"].(Int) == 3, nil
			}
			return false, fmt.Errorf("no %s", name)
		}
	}
	act := func(name, result string) func(Transition) bool {
		return func(t Transition) bool {
			for _, e := range t.Actions() {
				if e.Action == name && (result == "" || len(e.Args) == 1 && Compare(e.Args[0], Str(result)) == 0) {
					return true
				}
			}
			return false
		}
	}
	goals := map[string]Goal{
		"soldOut":        {Holds: val("soldOut")},
		"sold3":          {Holds: val("sold3")},
		"credit3":        {Holds: val("credit3")},
		"refund":         {Steps: []func(Transition) bool{act("refund", "")}},
		"repair":         {Steps: []func(Transition) bool{act("repair", "")}},
		"short,ok,empty": {Steps: []func(Transition) bool{act("buy", "short"), act("buy", "ok"), act("buy", "empty")}},
		"ok,ok,ok":       {Steps: []func(Transition) bool{act("buy", "ok"), act("buy", "ok"), act("buy", "ok")}},
		"empty,refund":   {Steps: []func(Transition) bool{act("buy", "empty"), act("refund", "")}},
	}
	const depth = 7
	// brute force: every path of up to depth transitions
	var paths [][]Transition
	var walk func(s State, path []Transition)
	walk = func(s State, path []Transition) {
		paths = append(paths, path)
		if len(path) == depth {
			return
		}
		ts, err := m.Transitions(consts, s, "step")
		if err != nil {
			t.Fatal(err)
		}
		for _, tr := range ts {
			walk(tr.State, append(append([]Transition(nil), path...), tr))
		}
	}
	walk(init, nil)
	meets := func(g Goal, path []Transition) bool {
		if g.Holds != nil {
			s := init
			if len(path) > 0 {
				s = path[len(path)-1].State
			}
			ok, _ := g.Holds(s)
			return ok
		}
		k := 0
		for _, tr := range path {
			if k < len(g.Steps) && g.Steps[k](tr) {
				k++
			}
		}
		return k == len(g.Steps)
	}
	for name, g := range goals {
		shortest := -1
		for _, p := range paths {
			if meets(g, p) && (shortest < 0 || len(p) < shortest) {
				shortest = len(p)
			}
		}
		for _, b := range []Bound{{Depth: depth}, {}} {
			rs, err := m.Reach(consts, init, "step", []Goal{g}, b)
			if err != nil {
				t.Fatal(err)
			}
			r := rs[0]
			switch {
			case b.Depth > 0 && r.Found != (shortest >= 0):
				t.Errorf("%s (depth %d): found %v, brute force %v", name, depth, r.Found, shortest >= 0)
			case r.Found && !meets(g, r.Path):
				t.Errorf("%s: its path does not meet it: %v", name, r.Path)
			case r.Found && g.Holds != nil && shortest >= 0 && len(r.Path) != shortest:
				t.Errorf("%s: a path of %d, the shortest %d", name, len(r.Path), shortest)
			case !r.Found && b.Depth == 0 && !r.Complete:
				t.Errorf("%s: not found by an unbounded search that is not complete (cut by %q)", name, r.Cut)
			case !r.Found && b.Depth == 0 && shortest >= 0:
				t.Errorf("%s: a complete search found nothing, brute force a path of %d", name, shortest)
			}
		}
	}
	// a state bound cuts the search, which then says so, never "complete"
	rs, err := m.Reach(consts, init, "step", []Goal{goals["sold3"]}, Bound{States: 5})
	if err != nil {
		t.Fatal(err)
	}
	if r := rs[0]; r.Found || r.Complete || r.Cut != "states" {
		t.Errorf("a search bound to 5 states: %+v, want cut by states", r)
	}
}

// A depth bound cuts a sequence's search only where no path within it
// meets the sequence: for every sequence of three of vending's steps
// that some path meets, a search bound to the shortest such path's
// length finds it (brute force: breadth first over states and steps
// taken). Best first may reach a state with some steps taken by a
// longer path before a shorter one; without its re-expansion at the
// shorter depth, the bound cut two of them (insert, insert, then a buy
// refused empty: a path of 5; insert, a short buy, an empty one: 6).
func TestReachDepthBoundAgainstBruteForce(t *testing.T) {
	m, consts, init := vending(t)
	type sym struct{ action, result string }
	syms := []sym{{"insert", ""}, {"buy", "ok"}, {"buy", "short"}, {"buy", "empty"}, {"refund", ""}}
	takes := func(s sym) func(Transition) bool {
		return func(tr Transition) bool {
			for _, e := range tr.Actions() {
				if e.Action == s.action && (s.result == "" || len(e.Args) == 1 && Compare(e.Args[0], Str(s.result)) == 0) {
					return true
				}
			}
			return false
		}
	}
	// shortest is the length of the shortest path meeting steps (-1: none
	// within 9)
	shortest := func(steps []func(Transition) bool) int {
		type nd struct {
			s State
			p int
		}
		cur := []nd{{init, 0}}
		seen := map[string]bool{m.Key(init) + "#0": true}
		for d := 1; d <= 9; d++ {
			var next []nd
			for _, n := range cur {
				ts, err := m.Transitions(consts, n.s, "step")
				if err != nil {
					t.Fatal(err)
				}
				for _, tr := range ts {
					p := n.p
					if steps[p](tr) {
						p++
					}
					if p == len(steps) {
						return d
					}
					if k := fmt.Sprintf("%s#%d", m.Key(tr.State), p); !seen[k] {
						seen[k] = true
						next = append(next, nd{tr.State, p})
					}
				}
			}
			cur = next
		}
		return -1
	}
	checked := 0
	for _, a := range syms {
		for _, b := range syms {
			for _, c := range syms {
				steps := []func(Transition) bool{takes(a), takes(b), takes(c)}
				d := shortest(steps)
				if d < 0 {
					continue
				}
				rs, err := m.Reach(consts, init, "step", []Goal{{Steps: steps}}, Bound{Depth: d})
				if err != nil {
					t.Fatal(err)
				}
				if !rs[0].Found {
					t.Errorf("%v %v %v: a path of %d meets it, and a search bound to depth %d did not find it (cut by %q)", a, b, c, d, d, rs[0].Cut)
				}
				checked++
			}
		}
	}
	if checked < 50 {
		t.Fatalf("only %d sequences reachable", checked)
	}
}

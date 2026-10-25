package validate

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// gossipStep is a recorded write (seen nil) or read of the gossip model.
func gossipStep(seq int, node string, value int, seen []int) qtrace.Step {
	s := qtrace.Step{Recorder: "r", Seq: uint64(seq), Actor: "g", Args: map[string]any{"node": node}, Obs: map[string]any{}}
	if seen == nil {
		s.Action, s.Args["value"] = "write", int64(value)
	} else {
		b, _ := json.Marshal(seen) // a list, as a step log carries one
		s.Action, s.Obs["seen"] = "read", string(b)
	}
	return s
}

// A read may see what unrecorded gossip brought it: with the gossip
// hidden, a read of a value written elsewhere conforms, one of a value
// never written does not, and one two hops away needs a depth of two. The
// report says the evaluator judged it.
func TestHiddenActions(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	c, err := NewChecker("testdata/gossip.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	two := []qtrace.Step{gossipStep(1, "a", 1, nil), gossipStep(2, "b", 2, nil), gossipStep(3, "c", 0, []int{1, 2})}
	for _, x := range []struct {
		name  string
		depth int
		steps []qtrace.Step
		want  Verdict
	}{
		{"gossiped", 0, []qtrace.Step{gossipStep(1, "a", 1, nil), gossipStep(2, "b", 0, []int{1})}, Pass},
		{"never written", 0, []qtrace.Step{gossipStep(1, "a", 1, nil), gossipStep(2, "b", 0, []int{9})}, Fail},
		{"two rounds", 0, two, Pass},
		// a to c is two hops: two rounds in one gap
		{"two hops", 2, []qtrace.Step{gossipStep(1, "a", 1, nil), gossipStep(2, "c", 0, []int{1})}, Pass},
		{"two hops, depth one", 1, []qtrace.Step{gossipStep(1, "a", 1, nil), gossipStep(2, "c", 0, []int{1})}, Fail},
	} {
		cc := *c
		b := *c.Binding
		b.HiddenDepth = x.depth
		cc.Binding = &b
		rep, err := cc.Check(context.Background(), x.steps)
		if err != nil {
			t.Fatalf("%s: %v", x.name, err)
		}
		if got := rep.Actors[0].Conforms; got != x.want {
			t.Errorf("%s: %s, want %s\n%s", x.name, got, x.want, rep.Summary())
		}
		if !strings.Contains(fmt.Sprint(rep.Actors[0].Issues), "judged by the Go evaluator's search over hidden actions") {
			t.Errorf("%s: the report does not say who judged it: %s", x.name, rep.Summary())
		}
	}
	// an invariant every path breaks: three values on a, conforming
	ic, err := c.WithInvariants("small")
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		steps []qtrace.Step
		want  Verdict
	}{
		{[]qtrace.Step{gossipStep(1, "a", 1, nil), gossipStep(2, "a", 2, nil), gossipStep(3, "a", 3, nil)}, Fail},
		{[]qtrace.Step{gossipStep(1, "a", 1, nil), gossipStep(2, "c", 2, nil), gossipStep(3, "b", 0, []int{})}, Pass},
	} {
		rep, err := ic.Check(context.Background(), x.steps)
		if err != nil {
			t.Fatal(err)
		}
		a := rep.Actors[0]
		if a.Conforms != Pass || len(a.Invariants) != 1 || a.Invariants[0].Verdict != x.want {
			t.Errorf("small: %s", rep.Summary())
		}
	}
	if _, err := c.Search(two, SearchOptions{Linearize: true}); err == nil || !strings.Contains(err.Error(), "with hidden actions") {
		t.Errorf("a linearization with hidden actions: %v", err)
	}
}

// The search over hidden actions against brute force: the same model in
// Go, every sequence of gossip rounds up to the depth before each step,
// on random histories of writes and reads (some reads seeing what gossip
// may have brought, some what it could not).
func TestHiddenAgainstBruteForce(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	if testing.Short() {
		t.Skip("checks 60 histories with the evaluator's search")
	}
	c, err := NewChecker("testdata/gossip.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(13, 14))
	nodes := []string{"a", "b", "c"}
	counts := map[bool]int{}
	for trial := range 60 {
		var steps []qtrace.Step
		written := []int{}
		for i := range 2 + rng.IntN(3) {
			n := nodes[rng.IntN(3)]
			if rng.IntN(2) == 0 && len(written) < 3 {
				v := len(written) + 1
				written = append(written, v)
				steps = append(steps, gossipStep(i+1, n, v, nil))
			} else {
				var seen []int
				for _, v := range written {
					if rng.IntN(2) == 0 {
						seen = append(seen, v)
					}
				}
				steps = append(steps, gossipStep(i+1, n, 0, append([]int{}, seen...)))
			}
		}
		depth := 1 + rng.IntN(2)
		cc := *c
		b := *c.Binding
		b.HiddenDepth = depth
		cc.Binding = &b
		rep, err := cc.Check(context.Background(), steps)
		if err != nil {
			t.Fatal(err)
		}
		want := bruteGossip(steps, depth)
		if got := rep.Actors[0].Conforms == Pass; got != want {
			t.Fatalf("trial %d (depth %d): the search %v, brute force %v, for %v\n%s", trial, depth, got, want, steps, rep.Summary())
		}
		counts[want]++
	}
	if counts[true] == 0 || counts[false] == 0 {
		t.Errorf("verdicts %v: the property compared too little", counts)
	}
}

// bruteGossip replays steps on every path of at most depth gossip rounds
// before each, reporting whether one takes every step.
func bruteGossip(steps []qtrace.Step, depth int) bool {
	type state map[string][]int
	key := func(s state) string { return fmt.Sprint(s["a"], s["b"], s["c"]) }
	clone := func(s state) state {
		o := state{}
		for k, v := range s {
			o[k] = slices.Clone(v)
		}
		return o
	}
	union := func(a, b []int) []int {
		out := slices.Clone(a)
		for _, v := range b {
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
		sort.Ints(out)
		return out
	}
	frontier := []state{{"a": nil, "b": nil, "c": nil}}
	for _, st := range steps {
		// every state at most depth rounds away
		seen := map[string]bool{}
		var reach []state
		var walk func(s state, d int)
		walk = func(s state, d int) {
			if !seen[key(s)] {
				seen[key(s)] = true
				reach = append(reach, s)
			}
			if d == 0 {
				return
			}
			for _, l := range [][2]string{{"a", "b"}, {"b", "a"}, {"b", "c"}, {"c", "b"}} { // the line's links
				n := clone(s)
				n[l[1]] = union(n[l[1]], n[l[0]])
				walk(n, d-1)
			}
		}
		for _, s := range frontier {
			walk(s, depth)
		}
		var next []state
		for _, s := range reach {
			node := st.Args["node"].(string)
			if st.Action == "write" {
				n := clone(s)
				n[node] = union(n[node], []int{int(st.Args["value"].(int64))})
				next = append(next, n)
				continue
			}
			var obs []int
			_ = json.Unmarshal([]byte(st.Obs["seen"].(string)), &obs)
			sort.Ints(obs)
			if fmt.Sprint(s[node]) == fmt.Sprint(obs) || len(s[node]) == 0 && len(obs) == 0 {
				next = append(next, s)
			}
		}
		if len(next) == 0 {
			return false
		}
		frontier = next
	}
	return true
}

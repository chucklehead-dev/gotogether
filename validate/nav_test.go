package validate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// The navigator's branches are the model's enabled actions, against
// brute force: in every state within three steps of init, an action
// applied to arguments of its domain has successors exactly when a
// branch takes it with them, and an action no branch takes is among the
// disabled, explained. The limiter's and the account's.
func TestNavBranchesAgainstBruteForce(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	for _, tc := range []struct {
		binding string
		domain  map[string][]qeval.Value // each action's argument values (one parameter)
	}{
		{"testdata/limiter.yaml", map[string][]qeval.Value{"admit": strs("a", "b", "c"), "release": strs("a", "b", "c")}},
		{"testdata/account.yaml", map[string][]qeval.Value{"deposit": ints(1, 100), "withdraw": ints(1, 100)}},
	} {
		t.Run(tc.binding, func(t *testing.T) {
			c, err := NewChecker(tc.binding, Options{Dir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			n, err := c.Nav(NavStart{})
			if err != nil {
				t.Fatal(err)
			}
			e := evaluatorFor(c)
			paths := [][]string{nil}
			seen := 0
			for depth := 0; depth <= 3; depth++ {
				var next [][]string
				for _, p := range paths {
					nd, err := n.Node(p...)
					if err != nil {
						t.Fatal(err)
					}
					s := stateAt(t, n, p)
					takes := map[string]bool{}
					for _, b := range nd.Branches {
						takes[b.Take] = true
					}
					disabled := map[string]bool{}
					for _, d := range nd.Disabled {
						disabled[d.Action] = d.Why != nil && len(d.Why.Conditions) > 0
					}
					for a, vs := range tc.domain {
						any := false
						for _, v := range vs {
							succ, err := e.m.Successors(n.consts, s, e.em.Actions[a], v)
							if err != nil {
								t.Fatal(err)
							}
							take := fmt.Sprintf("%s(%s)", a, qeval.String(v))
							if (len(succ) > 0) != takes[take] {
								t.Errorf("node %q: %s has %d successors, a branch: %v", nd.ID, take, len(succ), takes[take])
							}
							any = any || len(succ) > 0
						}
						if !any && !disabled[a] {
							t.Errorf("node %q: %s is enabled for no argument, and not disabled with why: %+v", nd.ID, a, nd.Disabled)
						}
						if any && slices.ContainsFunc(nd.Disabled, func(d Disabled) bool { return d.Action == a }) {
							t.Errorf("node %q: %s is enabled, and disabled", nd.ID, a)
						}
					}
					seen++
					// every branch of a small node, the first and the last of a
					// large one (the account's hundred amounts)
					bs := nd.Branches
					if len(bs) > 8 {
						bs = []Edge{bs[0], bs[len(bs)-1]}
					}
					for _, b := range bs {
						next = append(next, append(slices.Clone(p), b.Take))
					}
				}
				paths = next
			}
			if seen < 10 {
				t.Errorf("%d nodes seen", seen)
			}
		})
	}
}

func strs(ss ...string) []qeval.Value {
	out := make([]qeval.Value, len(ss))
	for i, s := range ss {
		out[i] = qeval.Str(s)
	}
	return out
}

func ints(lo, hi int64) []qeval.Value {
	var out []qeval.Value
	for i := lo; i <= hi; i++ {
		out = append(out, qeval.Int(i))
	}
	return out
}

// stateAt is the state path reaches from n's start, by the edges' order.
func stateAt(t *testing.T, n *Navigator, path []string) qeval.State {
	t.Helper()
	s := n.start
	for _, p := range path {
		edges, ts, err := n.edges(s)
		if err != nil {
			t.Fatal(err)
		}
		k, err := pick(edges, p)
		if err != nil {
			t.Fatal(err)
		}
		s = ts[k].State
	}
	return s
}

// A node's JSON is the agents' interface (gotogether nav -json): its
// shape is fixed by a golden, the limiter's init, a node two admits
// later (admit disabled, with why), and a recorded run's state after
// its first call.
func TestNavNodeJSON(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/limiter.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	n, err := c.Nav(NavStart{})
	if err != nil {
		t.Fatal(err)
	}
	var nodes []*Node
	for _, p := range []string{"", `admit("a"),admit("b")`, `#1,release`} {
		nd, err := n.Node(SplitPath(p)...)
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, nd)
	}
	run, err := c.Nav(NavStart{Steps: limiterRun("a", "b", "c"), At: 1})
	if err != nil {
		t.Fatal(err)
	}
	nd, err := run.Node()
	if err != nil {
		t.Fatal(err)
	}
	nodes = append(nodes, nd)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(nodes); err != nil {
		t.Fatal(err)
	}
	got := buf.Bytes()
	const golden = "testdata/nav_limiter.golden.json"
	if os.Getenv("GOTOGETHER_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := os.ReadFile(golden)
	if string(got) != string(want) {
		t.Errorf("nav's JSON changed (GOTOGETHER_UPDATE_GOLDEN=1 rewrites %s, if it should):\n%s", golden, got)
	}
	// a run whose calls the model rejects opens no further than the call
	// before, and says why
	_, err = c.Nav(NavStart{Steps: limiterRun("a", "b", "c"), At: -1})
	if err == nil || !strings.Contains(err.Error(), "call 3") || !strings.Contains(err.Error(), "admitted.size() < LIMIT") {
		t.Errorf("a rejected call: %v", err)
	}
}

// Fold takes a linear stretch in one move: the ticks model's five ticks
// from init (tick its only branch until fire is enabled), Changed
// against the state it folded from; a node with several branches folds
// nothing, and a cycle of single branches (fire at the end, which keeps
// the state) stops before the state repeats.
func TestNavFold(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/ticks.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	n, err := c.Nav(NavStart{})
	if err != nil {
		t.Fatal(err)
	}
	nd, err := n.Fold()
	if err != nil {
		t.Fatal(err)
	}
	if nd.Folded != 5 || nd.ID != "tick,tick,tick,tick,tick" || len(nd.Branches) != 2 || !slices.Equal(nd.Changed, []string{"c"}) {
		t.Fatalf("folded from init: %+v", nd)
	}
	// the same node by its path, unfolded
	byPath, err := n.Node(SplitPath(nd.ID)...)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(byPath.State) != fmt.Sprint(nd.State) || byPath.Folded != 0 {
		t.Errorf("by its path: %+v", byPath)
	}
	again, err := n.Fold(SplitPath(nd.ID)...)
	if err != nil {
		t.Fatal(err)
	}
	if again.Folded != 0 || again.ID != nd.ID || len(again.Changed) != 1 {
		t.Errorf("a node of two branches folds nothing: %+v", again)
	}
	// ten ticks: only fire, which keeps the state
	end, err := n.Fold(SplitPath(strings.Repeat("tick,", 9) + "tick")...)
	if err != nil {
		t.Fatal(err)
	}
	if end.Folded != 0 || len(end.Branches) != 1 || end.Branches[0].Action != "fire" {
		t.Errorf("a cycle of single branches: %+v", end)
	}
}

package qoracle

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/ddmin"
)

func TestSplitDecls(t *testing.T) {
	text := `// a comment: val no = 1
module a {
  /// doc
  pure def f(x: int): int = { val y = x; y }
  var v: int
  import b.* from "./b"
  action step = all { v' = f(v), }
  temporal t = always(v >= 0)
  val s = "val in a string"
}
module c { const N: int nondet n = Set(1).oneOf() }
`
	d := SplitDecls(text)
	want := []string{
		"pure def f(x: int): int = { val y = x; y }",
		"var v: int",
		`import b.* from "./b"`,
		"action step = all { v' = f(v), }",
		"temporal t = always(v >= 0)",
		`val s = "val in a string"`,
		"const N: int",
		"nondet n = Set(1).oneOf()",
	}
	if d.N() != len(want) {
		t.Fatalf("%d declarations, want %d: %v", d.N(), len(want), d.spans)
	}
	for i, w := range want {
		if got := text[d.spans[i][0]:d.spans[i][1]]; got != w {
			t.Errorf("declaration %d is %q, want %q", i, got, w)
		}
	}
	// a kept declaration keeps its position
	kept := d.Keep([]int{3})
	if len(kept) != len(text) || strings.Index(kept, "action step") != strings.Index(text, "action step") {
		t.Fatalf("Keep moved the declaration:\n%s", kept)
	}
	if strings.Contains(kept, "var v") || !strings.Contains(kept, "module c {") {
		t.Fatalf("Keep kept the wrong text:\n%s", kept)
	}
}

// ShrinkDecls' search on a test that fails when the text holds every
// declaration of some cause (one or two of them, monotone), checked
// against brute force over every subset: its result fails, and is a
// smallest cause, since a monotone failure with one cause has one
// 1-minimal set.
func TestMinimizeDeclsAgainstBruteForce(t *testing.T) {
	for seed := range uint64(60) {
		r := rand.New(rand.NewPCG(seed, 3))
		n := 2 + r.IntN(7)
		var decls []string
		for i := range n {
			decls = append(decls, fmt.Sprintf("val d%d = %d", i, i))
		}
		text := "module m {\n  " + strings.Join(decls, "\n  ") + "\n}\n"
		cause := map[int]bool{r.IntN(n): true}
		if r.IntN(2) == 0 {
			cause[r.IntN(n)] = true
		}
		holds := func(text string) bool {
			for i := range cause {
				if !strings.Contains(text, fmt.Sprintf("val d%d =", i)) {
					return false
				}
			}
			return true
		}
		d := SplitDecls(text)
		got, res := MinimizeDecls(d, func(text string) ddmin.Outcome {
			if holds(text) {
				return ddmin.Fails
			}
			return ddmin.Passes
		}, nil)
		if !holds(got) || !res.Minimal {
			t.Fatalf("seed %d: the result does not fail, or is not 1-minimal: %v", seed, res)
		}
		// brute force: the smallest failing subsets
		best := n + 1
		for mask := range 1 << n {
			var set []int
			for i := range n {
				if mask>>i&1 == 1 {
					set = append(set, i)
				}
			}
			if holds(d.Keep(set)) {
				best = min(best, len(set))
			}
		}
		if len(res.Set) != best {
			t.Fatalf("seed %d: shrank to %d declarations, the smallest failing set has %d", seed, len(res.Set), best)
		}
	}
}

package qtest

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// tree is a deterministic system as an exhaustive search sees it: which
// choice comes next (its kind and options) depends on the values taken
// before it, as a run's does, and the run ends at some depth.
type tree struct {
	salt  uint64
	depth int
}

func (tr tree) next(prefix []int) (kind string, n int, end bool) {
	h := fnv.New64a()
	fmt.Fprint(h, tr.salt, prefix)
	x := h.Sum64()
	if len(prefix) >= tr.depth || len(prefix) > 0 && x%5 == 0 {
		return "", 0, true
	}
	kind = "sched.task"
	if x/5%3 == 0 {
		kind = "fault.rpc.x"
	}
	return kind, 1 + int(x/15%3), false
}

// run is the tree's run under a forcing chooser.
func (tr tree) run(ds []deviation) *Run {
	f := &forcing{ds: ds}
	var vals []int
	var cs []qenv.Choice
	for {
		kind, n, end := tr.next(vals)
		if end {
			break
		}
		v := f.Choose(kind, n)
		vals = append(vals, v)
		cs = append(cs, qenv.Choice{Kind: kind, N: n, Value: v})
	}
	return &Run{Choices: cs, Err: f.Err()}
}

// On random choice trees, an exhaustive search runs each path with at most
// Delays schedule choices and Faults fault choices made otherwise exactly
// once, and no other, fewest deviations first; a MaxRuns that cuts it says
// some were left.
func TestSchedulesAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))
	for trial := range 500 {
		tr := tree{salt: r.Uint64(), depth: 1 + r.IntN(5)}
		b := Bound{Delays: r.IntN(3), Faults: r.IntN(3), MaxRuns: 1 << 20, Parallel: 2}
		// every path, and its deviations by kind
		type path struct {
			vals           []int
			delays, faults int
		}
		var all []path
		var walk func(p path)
		walk = func(p path) {
			kind, n, end := tr.next(p.vals)
			if end {
				all = append(all, p)
				return
			}
			for v := range n {
				q := path{append(slices.Clone(p.vals), v), p.delays, p.faults}
				if v > 0 && qenv.IsFault(kind) {
					q.faults++
				} else if v > 0 {
					q.delays++
				}
				walk(q)
			}
		}
		walk(path{})
		want := map[string]bool{}
		for _, p := range all {
			if p.delays <= b.Delays && p.faults <= b.Faults {
				want[fmt.Sprint(p.vals)] = true
			}
		}
		runs, left := schedules(b, qenv.Rates{Kinds: map[string]float64{}}, tr.run)
		got := map[string]bool{}
		prev := 0
		for _, run := range runs {
			if run.Err != nil {
				t.Fatalf("trial %d: %v", trial, run.Err)
			}
			var vals []int
			devs := 0
			for _, c := range run.Choices {
				vals = append(vals, c.Value)
				if c.Value > 0 {
					devs++
				}
			}
			k := fmt.Sprint(vals)
			if got[k] {
				t.Fatalf("trial %d: %s run twice", trial, k)
			}
			if !want[k] {
				t.Fatalf("trial %d: %s is outside the bound %+v", trial, k, b)
			}
			if devs < prev {
				t.Fatalf("trial %d: %s, of %d deviations, after a run of %d", trial, k, devs, prev)
			}
			got[k], prev = true, devs
		}
		if left != 0 || len(got) != len(want) {
			t.Fatalf("trial %d: %d runs of the %d within %+v, %d left", trial, len(got), len(want), b, left)
		}
		// cut short: as many runs as MaxRuns, and some left, at most those
		// not run (a schedule not run has children no one knows of yet)
		if len(want) > 2 {
			b.MaxRuns = len(want) - 2
			runs, left := schedules(b, qenv.Rates{Kinds: map[string]float64{}}, tr.run)
			if len(runs) != b.MaxRuns || left < 1 || left > 2 {
				t.Fatalf("trial %d: MaxRuns %d of %d: %d runs, %d left", trial, b.MaxRuns, len(want), len(runs), left)
			}
		}
	}
}

package qtest

import (
	"errors"
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
	switch x / 5 % 6 {
	case 0, 1:
		kind = "fault.rpc.x"
	case 2:
		kind = "fault.rpc.off" // a kind the bound's Without turns off
	case 3:
		kind = "entropy" // never varied
	default:
		kind = "sched.task"
	}
	return kind, 1 + int(x/30%3), false
}

// fails reports whether the run of vals ends in an error (a scenario's
// failure): the search runs it, and goes no further from it.
func (tr tree) fails(vals []int) bool {
	h := fnv.New64a()
	fmt.Fprint(h, tr.salt, "fail", vals)
	return h.Sum64()%7 == 0
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
	err := f.Err()
	if err == nil && tr.fails(vals) {
		err = errors.New("the scenario failed")
	}
	return &Run{Choices: cs, Err: err}
}

// On random choice trees, an exhaustive search runs each path with at most
// Delays schedule choices and Faults fault choices made otherwise exactly
// once, and no other, fewest deviations first: never an entropy choice or
// a kind Without turns off made otherwise, nor a schedule beyond a run
// that failed. A MaxRuns that cuts it says some were left.
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
			opts := n
			if kind == "entropy" || kind == "fault.rpc.off" {
				opts = 1
			}
			for v := range opts {
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
		// a path is reached if no schedule it extends (its deviations
		// but its last) failed
		reached := func(vals []int) bool {
			var ds []deviation
			for at, v := range vals {
				if v > 0 {
					ds = append(ds, deviation{at, v})
				}
			}
			for k := range ds {
				if tr.run(ds[:k]).Err != nil {
					return false
				}
			}
			return true
		}
		want := map[string]bool{}
		for _, p := range all {
			if p.delays <= b.Delays && p.faults <= b.Faults && reached(p.vals) {
				want[fmt.Sprint(p.vals)] = true
			}
		}
		off := qenv.Rates{Kinds: map[string]float64{"rpc.off": 1}}
		runs, left := schedules(b, off, tr.run)
		got := map[string]bool{}
		prev := 0
		for _, run := range runs {
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
			runs, left := schedules(b, off, tr.run)
			if len(runs) != b.MaxRuns || left < 1 || left > 2 {
				t.Fatalf("trial %d: MaxRuns %d of %d: %d runs, %d left", trial, b.MaxRuns, len(want), len(runs), left)
			}
		}
	}
}

// schedules runs scheduleSource's schedules within b with run, one after
// another as the engine would batch them, and returns the runs in order
// and how many within b were left when MaxRuns cut the search short.
func schedules(b Bound, off qenv.Rates, run func([]deviation) *Run) (out []*Run, left int) {
	s := &scheduleSource{b: b, off: off, search: &search{}}
	for batch := s.next(out); len(batch) > 0; batch = s.next(out) {
		for i := range batch {
			r := run(s.level[i])
			out = append(out, r)
			if r == nil {
				return out, s.search.left
			}
		}
	}
	return out, s.search.left
}

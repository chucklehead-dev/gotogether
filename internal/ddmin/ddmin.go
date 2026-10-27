// Package ddmin is Zeller and Hildebrandt's delta debugging (ddmin) over
// index sets, with a three-valued test: a set fails (it shows the
// failure), passes, or is unknown (a rerun that could not run it, a
// budget spent). It is qshrink's search (qtest.ShrinkActions' and
// gotogether shrink's): the sets are the positions of a driver's actions
// kept, in their order.
//
// The result is 1-minimal for the test as it answered: it fails, and
// every set with one index fewer passes. For a monotone failure (every
// superset of a failing set fails) that is inclusion-minimal, one of the
// smallest failing sets by inclusion, not necessarily the smallest by
// size. An unknown answer is never taken as a pass: a set whose
// one-removals are not all known to pass is not reported minimal.
package ddmin

import (
	"slices"
	"strconv"
	"strings"
)

// Outcome is a test's answer for one set.
type Outcome int8

const (
	// Passes: the set does not show the failure.
	Passes Outcome = iota
	// Fails: the set shows the failure.
	Fails
	// Unknown: the test could not tell (every rerun diverged, the budget
	// ran out). The search does not reduce to such a set, and a result
	// with one among its one-removals is not minimal.
	Unknown
)

// String names the outcome as a report prints it.
func (o Outcome) String() string {
	switch o {
	case Passes:
		return "passes"
	case Fails:
		return "fails"
	}
	return "unknown"
}

// Result is a search's outcome.
type Result struct {
	// Set is the smallest failing set the search reached: indices in
	// 0..n-1, ascending. The full set when nothing smaller failed.
	Set []int
	// Minimal: every set of Set with one index fewer passed (1-minimal).
	// False when the search stopped early or a one-removal was unknown.
	Minimal bool
	// Stopped: stop asked the search to end before it finished.
	Stopped bool
	// Tests is how many distinct sets were tested (each at most once).
	Tests int
}

// Minimize searches for a 1-minimal failing subset of 0..n-1, the full
// set taken to fail (the caller checked it). test answers for a set
// (ascending, never the full set); each distinct set is asked once. stop,
// if not nil, is asked before each test: true ends the search with the
// smallest failing set so far.
func Minimize(n int, test func(set []int) Outcome, stop func() bool) Result {
	memo := map[string]Outcome{}
	var r Result
	ask := func(set []int) Outcome {
		k := key(set)
		if o, ok := memo[k]; ok {
			return o
		}
		if stop != nil && stop() {
			r.Stopped = true
			return Unknown
		}
		o := test(set)
		memo[k] = o
		r.Tests++
		return o
	}
	cur := make([]int, n)
	for i := range cur {
		cur[i] = i
	}
	gran := 2
	for len(cur) > 0 && !r.Stopped {
		gran = min(gran, len(cur))
		parts := split(cur, gran)
		reduced := false
		// a part alone: the failure needs nothing outside it (a part is
		// the whole set when the granularity is 1)
		if gran > 1 {
			for _, p := range parts {
				if ask(p) == Fails {
					cur, gran, reduced = p, 2, true
					break
				}
			}
		}
		// a part removed: at granularity 2 the complements are the parts,
		// asked already; at the finest, they are the one-removals that
		// make the result 1-minimal
		if !reduced && !r.Stopped {
			for _, p := range parts {
				c := without(cur, p)
				if ask(c) == Fails {
					cur, gran, reduced = c, max(gran-1, 2), true
					break
				}
			}
		}
		if !reduced {
			if gran >= len(cur) {
				break
			}
			gran = min(2*gran, len(cur))
		}
	}
	r.Set = cur
	if !r.Stopped {
		r.Minimal = true
		for i := range cur {
			// asked by the last round, at the finest granularity: an
			// unknown one, or one asked never (a stop), is no pass
			o, ok := memo[key(without(cur, cur[i:i+1]))]
			if !ok || o != Passes {
				r.Minimal = false
			}
		}
	}
	return r
}

// split cuts set into k parts of nearly equal size, in order.
func split(set []int, k int) [][]int {
	out := make([][]int, 0, k)
	start := 0
	for i := range k {
		end := start + (len(set)-start)/(k-i)
		out = append(out, set[start:end:end])
		start = end
	}
	return out
}

// without is set less the indices of part, both ascending.
func without(set, part []int) []int {
	out := make([]int, 0, len(set))
	for _, x := range set {
		if !slices.Contains(part, x) {
			out = append(out, x)
		}
	}
	return out
}

func key(set []int) string {
	var b strings.Builder
	for _, x := range set {
		b.WriteString(strconv.Itoa(x))
		b.WriteByte(',')
	}
	return b.String()
}

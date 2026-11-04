// Package tracecompare compares finite sequences of state observations in two
// finite graphs. Each transition contributes one observation, including a
// stuttering transition. All states accept: the languages contain finite prefixes,
// not fairness, termination, action labels, or infinite-run properties.
package tracecompare

import (
	"context"
	"encoding/binary"
	"fmt"
	"slices"
)

// Graph is a finite graph whose state identities are indices into Observations.
// Equal observation strings must mean equal projected values, including types.
// Starts names the initial states; Out names each state's successor states.
// Callers must supply complete graphs and not mutate them during Inclusion.
type Graph struct {
	Observations []string
	Starts       []int
	Out          [][]int
}

// Budget bounds both admitted product states (Pairs) and examined target initial
// states and successor candidates (Work). Both must be positive. Work is charged
// before examining a candidate, including duplicates or mismatching observations.
// It also bounds the total memberships generated for target subsets, preventing
// a small product-state count from hiding arbitrarily large subset storage.
type Budget struct {
	Pairs, Work int
}

// Result is one directional inclusion search. Status is "included" when every
// source observation sequence occurs in target, "witness" when Path is a shortest
// source path absent from target, or "bounded" when Cut ("pairs" or "work") stops
// the search. Explored counts unique product states admitted to the queue,
// including initial states and the empty-target state of a witness. Work counts
// examined target candidates. A bounded result has no verdict or witness.
type Result struct {
	Status   string
	Explored int
	Work     int
	Cut      string
	Path     []int
}

type product struct {
	source, targets int // targets is an interned subset ID
	parent          int
}

type productID struct{ source, targets int }
type advanceID struct {
	targets     int
	observation string
}

// Inclusion checks source's finite observation sequences against target's. Its
// product tracks a concrete source state and EVERY target state compatible with
// the entire observed prefix: merging target states solely by their observation
// would lose hidden-state correlations and admit impossible traces. Target subsets
// are interned and transitions cached, so repeated source states do not copy the
// same large subset. Traversal is breadth first with state IDs ordered: a witness
// is shortest and deterministic. Bounds concern search, not input validation.
func Inclusion(ctx context.Context, source, target Graph, budget Budget) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if budget.Pairs <= 0 || budget.Work <= 0 {
		return Result{}, fmt.Errorf("trace comparison: product-state and work limits must be positive")
	}
	if err := valid(ctx, "source", source); err != nil {
		return Result{}, err
	}
	if err := valid(ctx, "target", target); err != nil {
		return Result{}, err
	}
	if len(source.Starts) == 0 {
		return Result{Status: "included"}, nil
	}
	var queue []product
	work := 0
	bounded := func(cut string) Result {
		return Result{Status: "bounded", Explored: len(queue), Work: work, Cut: cut}
	}
	seen := map[productID]bool{}
	// ID 0 is the empty subset: reaching it is a difference witness.
	subsets := [][]int{nil}
	subsetIDs := map[string]int{"": 0}
	intern := func(ids []int) int {
		ids = ordered(ids)
		key := subsetKey(ids)
		if id, ok := subsetIDs[key]; ok {
			return id
		}
		id := len(subsets)
		subsets = append(subsets, ids)
		subsetIDs[key] = id
		return id
	}
	// Build each observation's compatible initial subset once, instead of
	// scanning and copying all target starts for every source initial state.
	initialGroups := map[string][]int{}
	for _, t := range target.Starts {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if work == budget.Work {
			return bounded("work"), nil
		}
		work++
		o := target.Observations[t]
		initialGroups[o] = append(initialGroups[o], t)
	}
	initial := map[string]int{}
	for observation, ids := range initialGroups {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		initial[observation] = intern(ids)
	}
	// Initial group map iteration cannot change subset contents or product
	// traversal order; subset IDs remain internal and never enter a result.
	initialGroups = nil
	add := func(state, compatible, parent int) (Result, bool) {
		key := productID{state, compatible}
		if seen[key] {
			return Result{}, false
		}
		if len(queue) == budget.Pairs {
			return bounded("pairs"), true
		}
		seen[key] = true
		queue = append(queue, product{state, compatible, parent})
		if compatible == 0 {
			return Result{Status: "witness", Explored: len(queue), Work: work, Path: path(queue, len(queue)-1)}, true
		}
		return Result{}, false
	}
	for _, s := range ordered(source.Starts) {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if result, done := add(s, initial[source.Observations[s]], -1); done {
			return result, nil
		}
	}
	advances := map[advanceID]int{}
	advance := func(subset int, observation string) (id int, cut bool, err error) {
		key := advanceID{subset, observation}
		if id, ok := advances[key]; ok {
			return id, false, nil
		}
		compatible := map[int]bool{}
		for _, t := range subsets[subset] {
			for _, next := range target.Out[t] {
				if err := ctx.Err(); err != nil {
					return 0, false, err
				}
				if work == budget.Work {
					return 0, true, nil
				}
				work++
				if target.Observations[next] == observation {
					compatible[next] = true
				}
			}
		}
		ids := make([]int, 0, len(compatible))
		for next := range compatible {
			ids = append(ids, next)
		}
		id = intern(ids)
		advances[key] = id
		return id, false, nil
	}
	for head := 0; head < len(queue); head++ {
		p := queue[head]
		for _, next := range ordered(source.Out[p.source]) {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			compatible, cut, err := advance(p.targets, source.Observations[next])
			if err != nil {
				return Result{}, err
			}
			if cut {
				return bounded("work"), nil
			}
			if result, done := add(next, compatible, head); done {
				return result, nil
			}
		}
	}
	return Result{Status: "included", Explored: len(queue), Work: work}, nil
}

func valid(ctx context.Context, side string, g Graph) error {
	if len(g.Observations) != len(g.Out) {
		return fmt.Errorf("trace comparison: %s has %d states but %d successor lists", side, len(g.Observations), len(g.Out))
	}
	check := func(id int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if id < 0 || id >= len(g.Observations) {
			return fmt.Errorf("trace comparison: %s state %d is outside [0,%d)", side, id, len(g.Observations))
		}
		return nil
	}
	for _, s := range g.Starts {
		if err := check(s); err != nil {
			return err
		}
	}
	for _, out := range g.Out {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, t := range out {
			if err := check(t); err != nil {
				return err
			}
		}
	}
	return nil
}

func ordered(ids []int) []int {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	return slices.Compact(ids)
}

func subsetKey(targets []int) string {
	var b []byte
	for _, t := range targets {
		b = binary.AppendUvarint(b, uint64(t))
	}
	return string(b)
}

func path(queue []product, id int) []int {
	var ids []int
	for id >= 0 {
		ids = append(ids, queue[id].source)
		id = queue[id].parent
	}
	slices.Reverse(ids)
	return ids
}

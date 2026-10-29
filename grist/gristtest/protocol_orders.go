package gristtest

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// ProtocolSearch reports a universal check over causal command orderings.
// A counterexample disproves the supplied check. Without one, only Exhaustive
// means every ordering passed; hitting the limit is inconclusive.
type ProtocolSearch struct {
	Checked        int           // orderings whose check returned a verdict, across all tenants
	Exhaustive     bool          // every ordering of every tenant was checked and passed
	Counterexample []qtrace.Step // first failing ordering, with native identities
}

// CheckProtocolOrders checks up to limit distinct command orderings consistent
// with the native store sequences and recorded peer causes used by ProtocolSteps.
// It stops at the first false verdict, error, cancellation, or exhausted limit.
// Errors are tool/evidence failures, never counterexamples. limit must be positive.
// check must judge the supplied order as given and must not modify its steps;
// for example it may call validate.Checker.Check and inspect the report.
//
// Each ordering is one tenant's projected commands, with fresh dense sequences
// and its settle, if recorded. Tenants are independent model instances: they
// are checked separately, in sorted order, without enumerating their cross
// product. Processor commits remain causal barriers but do not create duplicate
// orderings. Ordering is deterministic and the first ordering of each tenant
// matches ProtocolSteps. Original recorder/seq observations survive in every
// candidate and the counterexample, including after a JSONL round trip.
//
// This is a universal robustness check, not an existential linearizability
// search. A failure means some ordering allowed by the recorded evidence fails;
// missing cross-service dependencies may require better evidence or a model
// that permits concurrency. Exhaustive covers this history only, not other runs.
func CheckProtocolOrders(ctx context.Context, steps []qtrace.Step, limit int,
	check func(context.Context, []qtrace.Step) (bool, error), apps ...*grist.App) (ProtocolSearch, error) {
	var result ProtocolSearch
	if limit < 1 || check == nil {
		return result, fmt.Errorf("gristtest.CheckProtocolOrders: give a positive ordering limit and a non-nil check")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	graph := &protocolGraph{}
	if _, err := protocolSteps(steps, apps, graph); err != nil {
		return result, err
	}
	byActor := map[string][]int{}
	for i, n := range graph.commands {
		byActor[n.step.Actor] = append(byActor[n.step.Actor], i)
	}
	next := make([][]int, len(graph.commands))
	degree := make([]int, len(graph.commands))
	for i, n := range graph.commands {
		degree[i] = len(n.before)
		for _, before := range n.before {
			next[before] = append(next[before], i)
		}
	}
	for _, actor := range slices.Sorted(maps.Keys(byActor)) {
		indices := byActor[actor]
		var ready []int
		for _, i := range indices {
			if degree[i] == 0 {
				ready = append(ready, i)
			}
		}
		var order []int
		var walk func([]int) (bool, error)
		walk = func(ready []int) (bool, error) {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if len(order) == len(indices) {
				// Look for one more leaf before calling the limit exhausted:
				// exactly limit orders can still be an exhaustive result.
				if result.Checked == limit {
					return false, nil
				}
				candidate := make([]qtrace.Step, 0, len(order)+1)
				for j, i := range order {
					s := graph.commands[i].step
					s.Seq = uint64(j + 1)
					s.Args, s.Obs = maps.Clone(s.Args), maps.Clone(s.Obs)
					candidate = append(candidate, s)
				}
				if s, ok := graph.settles[actor]; ok {
					candidate = append(candidate, s)
				}
				passed, err := check(ctx, candidate)
				if err != nil {
					return false, err
				}
				result.Checked++
				if !passed {
					result.Counterexample = candidate
					return false, nil
				}
				if err := ctx.Err(); err != nil {
					return false, err
				}
				return true, nil
			}
			for j, i := range ready {
				child := append(slices.Clone(ready[:j]), ready[j+1:]...)
				for _, after := range next[i] {
					degree[after]--
					if degree[after] == 0 {
						child = append(child, after)
					}
				}
				slices.Sort(child)
				order = append(order, i)
				complete, err := walk(child)
				order = order[:len(order)-1]
				for _, after := range next[i] {
					degree[after]++
				}
				if err != nil || !complete {
					return false, err
				}
			}
			return true, nil
		}
		complete, err := walk(ready)
		if err != nil || !complete {
			return result, err
		}
	}
	result.Exhaustive = true
	return result, nil
}

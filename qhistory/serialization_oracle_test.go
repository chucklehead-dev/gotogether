package qhistory_test

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qhistory"
)

// The oracle executes every serial transaction order, independently of the
// dependency graph. This bounded universe has two committed writer/readers on
// different keys, optionally a third observer, or two same-key writers and an
// observer. Every read is an empty or complete candidate list of those writes.
// Timings include absent bounds, full overlap, each serial order, and a partial
// real-time order. In particular, no final observer is required to see writes.
func TestUnobservedAppendsAgainstBruteForce(t *testing.T) {
	cases := 0
	check := func(name string, history []qhistory.Txn) {
		t.Helper()
		for timing, timed := range serialOracleTimings(history) {
			for _, level := range []qhistory.Level{qhistory.Serializable, qhistory.StrictSerializable} {
				want := serialExplanationExists(timed, level == qhistory.StrictSerializable)
				rep, err := qhistory.Check(level, timed)
				if err != nil {
					t.Fatalf("%s, timing %d, %s: %v", name, timing, level, err)
				}
				if rep.OK() != want {
					t.Fatalf("%s, timing %d, %s: OK %v, serial explanation %v\nhistory: %+v\n%s", name, timing, level, rep.OK(), want, timed, rep)
				}
				cases++
			}
		}
	}

	// Each writer can read before or after its own append, and may or may
	// not see the other transaction's write. The two reads missing each
	// other's writes is write skew even if nobody ever reads either append.
	for order := range 4 {
		for seen := range 4 {
			x, y := []int{}, []int{}
			if seen&1 != 0 {
				y = []int{2}
			}
			if seen&2 != 0 {
				x = []int{1}
			}
			a, b := []qhistory.Op{app("x", 1), rd("y", y...)}, []qhistory.Op{app("y", 2), rd("x", x...)}
			if order&1 != 0 {
				slices.Reverse(a)
			}
			if order&2 != 0 {
				slices.Reverse(b)
			}
			base := []qhistory.Txn{txn(1, a...), txn(2, b...)}
			check(fmt.Sprintf("cross-key/order%d/seen%d", order, seen), base)
			for observed := range 4 {
				x, y := []int{}, []int{}
				if observed&1 != 0 {
					x = []int{1}
				}
				if observed&2 != 0 {
					y = []int{2}
				}
				withObserver := append(slices.Clone(base), txn(3, rd("x", x...), rd("y", y...)))
				check(fmt.Sprintf("cross-key/order%d/seen%d/observer%d", order, seen, observed), withObserver)
			}
		}
	}

	// Empty/partial reads leave one or both committed writers wholly unseen.
	// Their relative append order cannot be invented: either [1,2] or [2,1]
	// is a legal complete read when no real-time edge chooses between them.
	for _, list := range [][]int{{}, {1}, {2}, {1, 2}, {2, 1}} {
		check(fmt.Sprintf("same-key/read%v", list), []qhistory.Txn{txn(1, app("x", 1)), txn(2, app("x", 2)), txn(3, rd("x", list...))})
	}
	t.Logf("checked %d level/history combinations against every serial order", cases)
}

// serialExplanationExists applies whole transactions to lists, accepting a
// history exactly when some order gives every read its recorded answer. It does
// not construct dependencies or infer a maximal observed prefix.
func serialExplanationExists(history []qhistory.Txn, strict bool) bool {
	var visit func(uint64, map[string][]int) bool
	visit = func(done uint64, lists map[string][]int) bool {
		if done == 1<<len(history)-1 {
			return true
		}
		for i, tx := range history {
			if done&(1<<i) != 0 {
				continue
			}
			if tx.Outcome != qhistory.Committed {
				panic("serial oracle only admits the committed bounded universe")
			}
			blocked := false
			if strict {
				for j, prior := range history {
					if i != j && done&(1<<j) == 0 && !prior.Completed.IsZero() && !tx.Invoked.IsZero() && prior.Completed.Before(tx.Invoked) {
						blocked = true
					}
				}
			}
			if blocked {
				continue
			}
			next := maps.Clone(lists)
			matches := true
			for _, op := range tx.Ops {
				switch op.Kind {
				case qhistory.Append:
					next[op.Key] = append(slices.Clone(next[op.Key]), op.Value)
				case qhistory.Read:
					matches = matches && slices.Equal(next[op.Key], op.List)
				default:
					panic("operation outside list-append oracle")
				}
			}
			if matches && visit(done|1<<i, next) {
				return true
			}
		}
		return false
	}
	return visit(0, map[string][]int{})
}

func serialOracleTimings(history []qhistory.Txn) [][]qhistory.Txn {
	variants := [][]qhistory.Txn{slices.Clone(history)} // no known time order
	at := func(ms int) time.Time { return time.Unix(0, int64(ms)*int64(time.Millisecond)) }
	overlap := slices.Clone(history)
	for i := range overlap {
		overlap[i].Invoked, overlap[i].Completed = at(1), at(20)
	}
	variants = append(variants, overlap)

	var permutations func([]int, uint64)
	permutations = func(order []int, used uint64) {
		if len(order) == len(history) {
			serial := slices.Clone(history)
			for position, index := range order {
				serial[index].Invoked, serial[index].Completed = at(2*position+1), at(2*position+2)
			}
			variants = append(variants, serial)
			return
		}
		for i := range history {
			if used&(1<<i) == 0 {
				permutations(append(slices.Clone(order), i), used|1<<i)
			}
		}
	}
	permutations(nil, 0)

	partial := slices.Clone(history)
	partial[0].Invoked, partial[0].Completed = at(1), at(2)
	partial[len(partial)-1].Invoked = at(3)
	variants = append(variants, partial)
	return variants
}

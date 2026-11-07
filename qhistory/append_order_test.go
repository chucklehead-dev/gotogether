package qhistory_test

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qhistory"
)

func TestRepeatedAppendOperationOrder(t *testing.T) {
	for _, outcome := range []qhistory.Outcome{qhistory.Committed, qhistory.Unknown, qhistory.Aborted} {
		writer := txn(1, app("x", 2), app("x", 3))
		writer.Outcome = outcome
		for _, observed := range []struct {
			list    []int
			anomaly string
		}{
			{[]int{2, 3, 1}, ""}, {[]int{1, 2, 3}, ""},
			{[]int{3, 2, 1}, "incompatible-order"},
			{[]int{3, 1}, "G1b"},
			{[]int{2, 1, 3}, "incompatible-order"},
		} {
			list := observed.list
			history := []qhistory.Txn{writer, txn(2, app("x", 1)), txn(3, rd("x", list...))}
			for _, level := range []qhistory.Level{qhistory.ReadCommitted, qhistory.SnapshotIsolation, qhistory.Serializable, qhistory.StrongSessionSerializable, qhistory.StrictSerializable} {
				for i := range history {
					history[i].Session = "caller" + string(rune('a'+i))
					history[i].SessionOrder = 1
				}
				rep, err := qhistory.Check(level, history)
				if err != nil {
					t.Fatal(err)
				}
				kind := observed.anomaly
				if outcome == qhistory.Aborted {
					kind = "G1a"
				}
				valid := kind == ""
				if !valid && !rep.Has(kind) {
					t.Fatalf("outcome %v list %v %v: want %s: %s", outcome, list, level, kind, rep)
				}
				if rep.OK() != valid {
					t.Fatalf("outcome %v list %v %v: OK %v, want %v: %s", outcome, list, level, rep.OK(), valid, rep)
				}
			}
		}
	}
	// Writes that nobody observed establish no append version order; an unknown
	// or aborted writer's unobserved operations do not become observed evidence.
	for _, outcome := range []qhistory.Outcome{qhistory.Committed, qhistory.Unknown, qhistory.Aborted} {
		writer := txn(1, app("x", 2), app("x", 3))
		writer.Outcome = outcome
		rep, err := qhistory.Check(qhistory.Serializable, []qhistory.Txn{writer, txn(2, app("x", 1)), txn(3, rd("x"))})
		if err != nil || !rep.OK() {
			t.Fatalf("unobserved %v: %v %s", outcome, err, rep)
		}
	}
}

// All ordered subsets include empty/partial observations, reversals within a
// transaction, and interleaved whole blocks. The reference executes operations
// rather than consulting any inferred dependency or transaction block.
func TestRepeatedAppendsAgainstSerializationOracle(t *testing.T) {
	var lists [][]int
	var generate func([]int, uint)
	generate = func(prefix []int, used uint) {
		lists = append(lists, append([]int{}, prefix...))
		for i, v := range []int{1, 2, 3, 4} {
			if used&(1<<i) == 0 {
				generate(append(append([]int{}, prefix...), v), used|1<<i)
			}
		}
	}
	generate(nil, 0)
	cases := 0
	check := func(history []qhistory.Txn) {
		t.Helper()
		for _, timed := range serialOracleTimings(history) {
			for _, level := range []qhistory.Level{qhistory.Serializable, qhistory.StrictSerializable} {
				want := serialExplanationExists(timed, level)
				rep, err := qhistory.Check(level, timed)
				if err != nil || rep.OK() != want {
					t.Fatalf("%v: history %+v, want %v: %v %s", level, timed, want, err, rep)
				}
				cases++
			}
		}
	}
	for _, list := range lists {
		check([]qhistory.Txn{txn(1, app("x", 2), app("x", 3)), txn(2, app("x", 1), app("x", 4)), txn(3, rd("x", list...))})
		// Exercise own reads before, between and after repeated same-key appends.
		for at := range 3 {
			ops := []qhistory.Op{app("x", 2), app("x", 3)}
			ops = append(ops[:at:at], append([]qhistory.Op{rd("x", list...)}, ops[at:]...)...)
			check([]qhistory.Txn{txn(1, ops...), txn(2, app("x", 1), app("x", 4))})
		}
	}
	t.Logf("checked %d repeated-append history/level/timing combinations", cases)
}

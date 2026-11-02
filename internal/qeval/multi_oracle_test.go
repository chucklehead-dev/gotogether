package qeval

import "testing"

// These cases were authored independently of the generated-action successor
// translation. Their reference sets are analytical joint outcomes, rather than
// coordinate projections or a second rendering of the action tree.
func multiControls() []multiCase {
	return []multiCase{
		{
			Name: "shared-choice-correlation", From: [2]int{1, 2},
			Action: `{ nondet n = Set(1, 2).oneOf() all { x' = n, y' = 10 * n } }`,
			Oracle: `Set((1, 10), (2, 20))`,
		},
		{
			Name: "alternative-branch-correlation", From: [2]int{1, 2},
			Action: `any { all { x' = 1, y' = 10 }, all { x' = 2, y' = 20 } }`,
			Oracle: `Set((1, 10), (2, 20))`,
		},
		{
			Name: "independent-choices-product", From: [2]int{1, 2},
			Action: `{ nondet a = Set(1, 2).oneOf() nondet b = Set(3, 4).oneOf() all { x' = a, y' = b } }`,
			Oracle: `Set((1, 3), (1, 4), (2, 3), (2, 4))`,
		},
		{
			Name: "simultaneous-swap", From: [2]int{1, 2},
			Action: `all { x' = y, y' = x }`,
			Oracle: `Set((2, 1))`,
		},
		{
			Name: "simultaneous-swap-reversed-order", From: [2]int{1, 2},
			Action: `all { y' = x, x' = y }`,
			Oracle: `Set((2, 1))`,
		},
		{
			Name: "then-uses-intermediate-state", From: [2]int{1, 2},
			Action: `all { x' = x + 10, y' = y + 100 }`,
			Then:   `all { x' = y, y' = x }`,
			Oracle: `Set((102, 11))`,
		},
		{
			Name: "then-preserves-choice-correlation", From: [2]int{1, 2},
			Action: `{ nondet n = Set(1, 2).oneOf() all { x' = n, y' = 10 * n } }`,
			Then:   `all { x' = y, y' = x }`,
			Oracle: `Set((10, 1), (20, 2))`,
		},
		{
			Name: "then-disabled-second-stage", From: [2]int{1, 2},
			Action: `all { x' = 7, y' = 8 }`,
			Then:   `all { false, x' = 1, y' = 2 }`,
			Oracle: `Set()`,
		},
		{
			Name: "then-disabled-first-stage", From: [2]int{1, 2},
			Action: `all { false, x' = x, y' = y }`,
			Then:   `all { x' = 7, y' = 8 }`,
			Oracle: `Set()`,
		},
		{
			Name: "guard-filters-joint-choice", From: [2]int{1, 2},
			Action:           `{ nondet n = Set(-1, 0, 1).oneOf() all { n > 0, x' = n, y' = -n } }`,
			Oracle:           `Set((1, -1))`,
			SampleMayDisable: true,
		},
	}
}

func TestMultiVariableOracleControls(t *testing.T) {
	multiCheck(t, multiControls(), 1)
}

func oracleJointSet(pairs ...[2]int) *Set {
	values := make([]Value, len(pairs))
	for i, p := range pairs {
		values[i] = Tup{Int(p[0]), Int(p[1])}
	}
	return NewSet(values)
}

func oracleCoordinate(s *Set, at int) *Set {
	values := make([]Value, len(s.Elems))
	for i, p := range s.Elems {
		values[i] = p.(Tup)[at]
	}
	return NewSet(values)
}

// Each fabricated result represents a concrete oracle failure mode. The first
// three retain both coordinate projections; the third even retains cardinality.
// Thus independent per-variable checks or a count check cannot replace the
// joint comparison used by the differential harness.
func TestMultiVariableJointComparatorRejectsWrongSuccessors(t *testing.T) {
	for _, tc := range []struct {
		name            string
		want, wrong     *Set
		sameProjections bool
	}{
		{"spurious-cross-product", oracleJointSet([2]int{1, 10}, [2]int{2, 20}), oracleJointSet([2]int{1, 10}, [2]int{1, 20}, [2]int{2, 10}, [2]int{2, 20}), true},
		{"lost-independent-combinations", oracleJointSet([2]int{1, 3}, [2]int{1, 4}, [2]int{2, 3}, [2]int{2, 4}), oracleJointSet([2]int{1, 3}, [2]int{2, 4}), true},
		{"wrong-correlation-same-size", oracleJointSet([2]int{1, 2}, [2]int{2, 1}), oracleJointSet([2]int{1, 1}, [2]int{2, 2}), true},
		{"sequential-swap-first-order", oracleJointSet([2]int{2, 1}), oracleJointSet([2]int{2, 2}), false},
		{"sequential-swap-reverse-order", oracleJointSet([2]int{2, 1}), oracleJointSet([2]int{1, 1}), false},
		{"then-read-stale-prestate", oracleJointSet([2]int{102, 11}), oracleJointSet([2]int{2, 1}), false},
		{"disabled-became-enabled", oracleJointSet(), oracleJointSet([2]int{0, 0}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !multiSame(tc.want, tc.want) || multiSame(tc.want, tc.wrong) {
				t.Fatalf("joint comparison accepted fabricated successors: want %s, fabricated %s", String(tc.want), String(tc.wrong))
			}
			if tc.sameProjections {
				for at := range 2 {
					if Compare(oracleCoordinate(tc.want, at), oracleCoordinate(tc.wrong, at)) != 0 {
						t.Fatal("control no longer demonstrates equal coordinate projections")
					}
				}
			}
		})
	}
}

package qeval

import (
	"errors"
	"math"
	"math/big"
	"math/rand/v2"
	"testing"
)

// The shortcut's interval proof must agree with independently checked actual
// operations in every permutation, including intermediate cancellation.
func TestFoldDefinednessAgainstAllOrders(t *testing.T) {
	m := compile(t, "fold_overflow.qnt", "foldOverflow", "init", "step")
	rng := rand.New(rand.NewPCG(71, 29))
	elements := []int64{-math.MaxInt64, -2, -1, 0, 1, 2, math.MaxInt64}
	initials := []int64{math.MinInt64, math.MinInt64 + 1, -2, 0, 2, math.MaxInt64 - 1, math.MaxInt64}
	names := []string{"sumOf", "subtractOf", "addCancelOf", "subtractCancelOf", "conditionalOf", "conditionalLowerOf"}
	answered, refused := 0, 0
	for range 300 {
		initial := initials[rng.IntN(len(initials))]
		values := make([]Value, rng.IntN(5))
		for i := range values {
			values[i] = Int(elements[rng.IntN(len(elements))])
		}
		set := NewSet(values)
		for _, name := range names {
			want, safe := foldAllOrders(name, initial, set)
			got, err := m.Eval(nil, nil, name, Int(initial), set)
			if safe {
				answered++
				if err != nil || got != Int(want) {
					t.Fatalf("%s(%d, %s): every order gives %d; got %s, %v", name, initial, String(set), want, String(got), err)
				}
			} else {
				refused++
				var unsupported *Unsupported
				if !errors.As(err, &unsupported) {
					t.Fatalf("%s(%d, %s): an order overflows; got %s, %v", name, initial, String(set), String(got), err)
				}
			}
		}
	}
	if answered == 0 || refused == 0 {
		t.Fatalf("oracle did not exercise both outcomes: %d answered, %d refused", answered, refused)
	}
}

func foldAllOrders(name string, initial int64, set *Set) (int64, bool) {
	checked := func(a, b int64, subtract bool) (int64, bool) {
		x, y := big.NewInt(a), big.NewInt(b)
		if subtract {
			x.Sub(x, y)
		} else {
			x.Add(x, y)
		}
		return x.Int64(), x.IsInt64()
	}
	step := func(a, x int64) (int64, bool) {
		switch name {
		case "sumOf":
			return checked(a, x, false)
		case "subtractOf":
			return checked(a, x, true)
		case "conditionalOf":
			if x >= 0 {
				return checked(a, x, false)
			}
			x = 1
		case "conditionalLowerOf":
			if x >= 0 {
				return checked(a, x, true)
			}
			x = 1
		}
		subtractFirst := name == "subtractCancelOf" || name == "conditionalLowerOf"
		next, ok := checked(a, x, subtractFirst)
		if !ok {
			return 0, false
		}
		return checked(next, x, !subtractFirst)
	}
	var result int64
	var visit func(int64, uint) bool
	visit = func(a int64, used uint) bool {
		if used == 1<<len(set.Elems)-1 {
			result = a
			return true
		}
		for i, v := range set.Elems {
			if used&(1<<i) != 0 {
				continue
			}
			next, ok := step(a, int64(v.(Int)))
			if !ok || !visit(next, used|1<<i) {
				return false
			}
		}
		return true
	}
	safe := visit(initial, 0)
	return result, safe
}

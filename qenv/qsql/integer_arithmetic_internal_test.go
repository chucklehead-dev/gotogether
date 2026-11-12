package qsql

import (
	"maps"
	"math"
	"math/big"
	"slices"
	"testing"
)

// The oracle performs unbounded arithmetic and asks big.Int whether its
// mathematical result fits, independently of the fixed-width overflow checks.
func TestCheckedIntegerArithmeticAgainstBigInteger(t *testing.T) {
	values := map[int64]bool{math.MinInt64: true, math.MinInt64 + 1: true, math.MinInt64 + 2: true, math.MaxInt64: true, math.MaxInt64 - 1: true, math.MaxInt64 - 2: true}
	for n := int64(-16); n <= 16; n++ {
		values[n] = true
	}
	for bit := range 63 {
		p := int64(1) << bit
		for _, base := range []int64{p, -p} {
			for offset := int64(-1); offset <= 1; offset++ {
				values[base+offset] = true
			}
		}
	}
	operands := slices.Sorted(maps.Keys(values))
	for _, op := range []struct {
		name      string
		checked   func(int64, int64) (int64, bool)
		reference func(*big.Int, *big.Int, *big.Int) *big.Int
	}{
		{"add", addInt64, (*big.Int).Add},
		{"subtract", subtractInt64, (*big.Int).Sub},
	} {
		t.Run(op.name, func(t *testing.T) {
			for _, a := range operands {
				for _, b := range operands {
					want := op.reference(new(big.Int), big.NewInt(a), big.NewInt(b))
					got, ok := op.checked(a, b)
					if ok != want.IsInt64() || ok && got != want.Int64() {
						t.Fatalf("%s(%d,%d)=(%d,%t); mathematical result%s, fits=%t", op.name, a, b, got, ok, want, want.IsInt64())
					}
				}
			}
		})
	}
}

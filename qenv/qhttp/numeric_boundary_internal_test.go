package qhttp

import (
	"maps"
	"math"
	"math/big"
	"slices"
	"testing"
	"time"
)

// Enumerate the mathematical preferred points with arbitrary precision,
// independently of ceilGrid's decade search and fixed-width arithmetic.
func TestCeilGridAgainstBigIntegerGrid(t *testing.T) {
	var points []*big.Int
	scale := big.NewInt(1)
	for decade := 0; decade <= 18; decade++ {
		for _, coefficient := range []int64{10, 15, 20, 25, 30, 40, 50, 60, 80, 100} {
			points = append(points, new(big.Int).Mul(big.NewInt(coefficient), scale))
		}
		scale.Mul(scale, big.NewInt(10))
	}
	maximum := big.NewInt(math.MaxInt64)
	oracle := func(left time.Duration) time.Duration {
		if left < 10 {
			return left // the contract leaves sub-10ns and nonpositive inputs alone
		}
		input := big.NewInt(int64(left))
		var next *big.Int
		for _, candidate := range points {
			if candidate.Cmp(input) >= 0 && (next == nil || candidate.Cmp(next) < 0) {
				next = candidate
			}
		}
		if next == nil {
			t.Fatal("reference grid does not extend beyond the duration domain")
		}
		if next.Cmp(maximum) > 0 {
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(next.Int64())
	}
	inputs := map[time.Duration]bool{
		time.Duration(math.MinInt64):     true,
		time.Duration(math.MaxInt64):     true,
		time.Duration(math.MaxInt64 - 1): true,
	}
	for left := time.Duration(-20); left <= 1000; left++ {
		inputs[left] = true
	}
	for _, point := range points {
		if point.IsInt64() {
			value := time.Duration(point.Int64())
			inputs[value-1], inputs[value] = true, true
			if value < time.Duration(math.MaxInt64) {
				inputs[value+1] = true
			}
		}
	}
	for _, left := range slices.Sorted(maps.Keys(inputs)) {
		if got, want := ceilGrid(left), oracle(left); got != want {
			t.Errorf("ceilGrid(%d ns)=%d ns, want %d ns", left, got, want)
		}
	}
}

// The HTTP body prohibition is exactly the set of 1xx statuses, 204 and 304,
// including when the helper is called with any other int value.
func TestBodyAllowedAgainstStatusSet(t *testing.T) {
	blocked := map[int]bool{204: true, 304: true}
	for code := 100; code != 200; code++ {
		blocked[code] = true
	}
	for code := -1000; code <= 2000; code++ {
		if got, want := bodyAllowed(code), !blocked[code]; got != want {
			t.Errorf("bodyAllowed(%d)=%v, want %v", code, got, want)
		}
	}
	for _, code := range []int{math.MinInt, math.MaxInt} {
		if !bodyAllowed(code) {
			t.Errorf("bodyAllowed(%d)=false, want true", code)
		}
	}
}

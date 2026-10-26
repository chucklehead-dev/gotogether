package ddmin

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// mask is a set of indices below 8 as a bit mask.
func mask(set []int) uint {
	var m uint
	for _, x := range set {
		m |= 1 << x
	}
	return m
}

// TestMinimizeAgainstBruteForce checks the search against every subset of
// up to 8 indices: for a monotone failure (the supersets of a few random
// sets) the result is one of brute force's inclusion-minimal failing
// sets, the minimum when there is one; for any failure it fails and
// every one-removal passes.
func TestMinimizeAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for trial := range 600 {
		n := 1 + trial%8
		full := uint(1)<<n - 1
		monotone := trial%2 == 0
		var fails func(m uint) bool
		if monotone {
			var cores []uint
			for range 1 + rng.IntN(3) {
				cores = append(cores, rng.UintN(full+1)&full)
			}
			fails = func(m uint) bool {
				for _, c := range cores {
					if m&c == c {
						return true
					}
				}
				return false
			}
		} else {
			table := map[uint]bool{full: true}
			for m := range full {
				table[m] = rng.IntN(4) == 0
			}
			fails = func(m uint) bool { return table[m] }
		}
		if !fails(full) {
			continue
		}
		asked := map[uint]bool{}
		r := Minimize(n, func(set []int) Outcome {
			m := mask(set)
			if m == full {
				t.Fatalf("trial %d: the full set was asked", trial)
			}
			if asked[m] {
				t.Fatalf("trial %d: %v asked twice", trial, set)
			}
			asked[m] = true
			if fails(m) {
				return Fails
			}
			return Passes
		}, nil)
		got := mask(r.Set)
		if !slices.IsSorted(r.Set) || !fails(got) || !r.Minimal || r.Stopped {
			t.Fatalf("trial %d: %v (minimal %v, stopped %v): not a failing sorted set", trial, r.Set, r.Minimal, r.Stopped)
		}
		for _, x := range r.Set {
			if fails(got &^ (1 << x)) {
				t.Fatalf("trial %d: %v is not 1-minimal: without %d it still fails", trial, r.Set, x)
			}
		}
		if !monotone {
			continue
		}
		// brute force: the failing sets no failing proper subset of which exists
		var minimal []uint
		for m := uint(0); m <= full; m++ {
			if !fails(m) {
				continue
			}
			least := true
			for s := (m - 1) & m; ; s = (s - 1) & m {
				if s != m && fails(s) {
					least = false
					break
				}
				if s == 0 {
					break
				}
			}
			if least {
				minimal = append(minimal, m)
			}
		}
		if !slices.Contains(minimal, got) {
			t.Fatalf("trial %d: %b is none of brute force's inclusion-minimal failing sets %b", trial, got, minimal)
		}
	}
}

// TestMinimizeUnknown: an unknown answer is never a pass. The search does
// not reduce to it, and a result one of whose one-removals is unknown is
// not minimal.
func TestMinimizeUnknown(t *testing.T) {
	// fails iff it holds 0 and 2; without 1 the test cannot tell
	r := Minimize(4, func(set []int) Outcome {
		m := mask(set)
		switch {
		case m&0b10 == 0 && m&0b101 == 0b101:
			return Unknown
		case m&0b101 == 0b101:
			return Fails
		}
		return Passes
	}, nil)
	if mask(r.Set) != 0b111 || r.Minimal {
		t.Fatalf("got %v minimal %v, want [0 1 2] not minimal (its removal of 1 is unknown)", r.Set, r.Minimal)
	}
}

// TestMinimizeStop: a stop ends the search with the smallest failing set
// so far, not minimal.
func TestMinimizeStop(t *testing.T) {
	tests := 0
	r := Minimize(8, func(set []int) Outcome {
		tests++
		if mask(set)&1 == 1 {
			return Fails
		}
		return Passes
	}, func() bool { return tests >= 2 })
	if !r.Stopped || r.Minimal || r.Tests != 2 || mask(r.Set)&1 == 0 || len(r.Set) == 8 {
		t.Fatalf("got %+v, want stopped after 2 tests at a smaller failing set", r)
	}
}

// TestMinimizeEmpty: a failure that needs nothing shrinks to no indices.
func TestMinimizeEmpty(t *testing.T) {
	r := Minimize(3, func([]int) Outcome { return Fails }, nil)
	if len(r.Set) != 0 || !r.Minimal {
		t.Fatalf("got %+v, want the empty set, minimal", r)
	}
}

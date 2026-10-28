package choice

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// ofByScan is Rates.Of as it was: the longest key covering the kind, by
// ranging over every key.
func ofByScan(r Rates, kind string) float64 {
	kind = strings.TrimPrefix(kind, "fault.")
	best, rate := -1, r.Default
	for k, v := range r.Kinds {
		if covers(k, kind) && len(k) > best {
			best, rate = len(k), v
		}
	}
	return rate
}

// Of finds a kind's rate by its dot-prefixes: the same rate as the scan
// of every key, on keys and kinds of shared segments, prefixes that are
// not a segment's ("rpc" and "rpcx"), the empty key and empty segments.
func TestRatesOfAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 1))
	segs := []string{"rpc", "rpcx", "pay", "pay.req", "", "nemesis", "crash", "a", "a.b"}
	name := func() string {
		parts := make([]string, 1+r.IntN(4))
		for i := range parts {
			parts[i] = segs[r.IntN(len(segs))]
		}
		return strings.Join(parts, ".")
	}
	for range 20_000 {
		rates := Rates{Default: r.Float64(), Kinds: map[string]float64{}}
		for range r.IntN(6) {
			rates.Kinds[name()] = r.Float64()
		}
		kind := name()
		if r.IntN(2) == 0 {
			kind = "fault." + kind
		}
		if got, want := rates.of(kind), ofByScan(rates, kind); got != want {
			t.Fatalf("Of(%q) = %v, the scan's %v, of %v", kind, got, want, rates)
		}
	}
}

// A chooser checks its rates once, and again when a key is added or the
// map replaced: a prefixed key still panics.
func TestSeededChecksItsRates(t *testing.T) {
	panics := func(f func()) (p bool) {
		defer func() { p = recover() != nil }()
		f()
		return false
	}
	s := NewSeeded(1)
	s.Rates = Rates{Default: 0.5, Kinds: map[string]float64{"rpc": 1}}
	if panics(func() { s.Choose("fault.rpc.x", 2) }) {
		t.Fatal("a bare key panicked")
	}
	s.Rates.Kinds["fault.rpc"] = 1 // added in place
	if !panics(func() { s.Choose("fault.rpc.x", 2) }) {
		t.Fatal("a prefixed key added to the map did not panic")
	}
	s.Rates = Rates{Kinds: map[string]float64{"fault.q": 1}}
	if !panics(func() { s.Choose("fault.rpc.x", 2) }) {
		t.Fatal("a prefixed key of new rates did not panic")
	}
	b := &Bytes{Next: func(int) []byte { return []byte{0} }, Rates: Rates{Kinds: map[string]float64{"fault.q": 1}}}
	if !panics(func() { b.Choose("fault.rpc.x", 2) }) {
		t.Fatal("Bytes did not check its rates")
	}
}

package qeval

import (
	"runtime/debug"
	"testing"
)

// gcPercent reads the GC percent (setting it back).
func gcPercent() int {
	p := debug.SetGCPercent(100)
	debug.SetGCPercent(p)
	return p
}

// Searches at once share one raise of the GC percent, the last putting
// back what was there; a percent off or higher is left alone. Not
// parallel: the percent is the process's.
func TestRaiseGC(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(100))
	r1 := raiseGC()
	r2 := raiseGC()
	if p := gcPercent(); p != searchGCPercent {
		t.Fatalf("during two searches the percent is %d", p)
	}
	r1()
	if p := gcPercent(); p != searchGCPercent {
		t.Fatalf("one search on, the percent is %d", p)
	}
	r2()
	if p := gcPercent(); p != 100 {
		t.Fatalf("after the searches the percent is %d, not 100", p)
	}
	for _, p := range []int{-1, 800} {
		debug.SetGCPercent(p)
		r := raiseGC()
		if q := gcPercent(); q != p {
			t.Fatalf("a percent of %d raised to %d", p, q)
		}
		r()
		if q := gcPercent(); q != p {
			t.Fatalf("a percent of %d put back as %d", p, q)
		}
	}
}

package qshrink

import (
	"strings"
	"testing"
)

// The candidates' reruns follow the original's rate: n = ⌈ln α / ln(1−p̂)⌉
// for one reproduction, and the fewest n with a chance below α of
// fewer than k reproductions for k.
func TestRerunsFor(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		p     float64
		k     int
		alpha float64
		want  int
	}{
		{1, 1, 0.05, 1}, {1, 3, 0.05, 3}, {0.5, 1, 0.05, 5}, {0.1, 1, 0.05, 29}, {0.9, 1, 0.05, 2}, {0.5, 1, 0.01, 7},
		{0.5, 2, 0.05, 8}, // P(<2 in 8) = 9/256 < 0.05 < P(<2 in 7) = 8/128
		{0, 1, 0.05, maxReruns},
	} {
		if got := rerunsFor(c.p, c.k, c.alpha); got != c.want {
			t.Errorf("rerunsFor(%v, %d, %v) = %d, want %d", c.p, c.k, c.alpha, got, c.want)
		}
	}
	// the rate's lower bound at 1−α: a half-rate failure measured in 10
	// reruns is rerun 10 times, one never missed in 10 twice
	for _, c := range []struct {
		r    Rate
		want int
	}{{Rate{5, 10}, 10}, {Rate{10, 10}, 2}, {Rate{1, 10}, 131}} {
		if got := rerunsFor(rateBound(c.r, 0.05), 1, 0.05); got != c.want {
			t.Errorf("rate %s: %d reruns (bound %.3f), want %d", c.r, got, rateBound(c.r, 0.05), c.want)
		}
	}
	if _, err := (Calibration{Alpha: 2}).withDefaults(); err == nil || !strings.Contains(err.Error(), "between 0 and 1") {
		t.Errorf("Alpha 2: %v", err)
	}
}

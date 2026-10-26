package qhttp

import (
	"testing"
	"time"
)

// Every point of the grid comes back as itself under any jitter less than
// the gap to the point below (at least a sixth of it), 1-3 ms around it
// included, and the next point down just past that gap: the rounding
// takes back what time passed before the send, and nothing more.
func TestCeilGridStableUnderJitter(t *testing.T) {
	prev := 800 * time.Microsecond // the point below 1 ms
	for p := time.Millisecond; p <= 100*time.Second; p *= 10 {
		for _, m := range grid[:len(grid)-1] {
			d := m * p / 10
			gap := d - prev
			if prev > 0 && gap*6 < d {
				t.Errorf("the gap below %v is %v, less than a sixth of it", d, gap)
			}
			for _, j := range []time.Duration{0, time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, d / 7, gap / 2, gap - 1} {
				if j >= gap {
					continue
				}
				if got := ceilGrid(d - j); got != d {
					t.Errorf("ceilGrid(%v - %v) = %v, want %v", d, j, got, d)
				}
			}
			if prev > 0 {
				if got := ceilGrid(d - gap); got != prev {
					t.Errorf("ceilGrid(%v) = %v, want the point below, %v", d-gap, got, prev)
				}
			}
			prev = d
		}
	}
	for _, c := range []struct{ in, want time.Duration }{
		{700 * time.Millisecond, 800 * time.Millisecond},
		{45 * time.Second, 50 * time.Second},
		{90 * time.Millisecond, 100 * time.Millisecond},
		{100*time.Millisecond + 1, 150 * time.Millisecond},
		{7, 7},
		{0, 0},
		{-time.Second, -time.Second},
	} {
		if got := ceilGrid(c.in); got != c.want {
			t.Errorf("ceilGrid(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

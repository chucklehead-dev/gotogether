package qhttp

import (
	"testing"
	"time"
)

// A converted deadline is rounded up to two significant digits, so the
// real jitter before sending does not reach the run, and a timeout just
// under a power of ten keeps the coarser digit.
func TestRoundUp(t *testing.T) {
	for _, c := range []struct{ in, want time.Duration }{
		{148700 * time.Microsecond, 150 * time.Millisecond},
		{1999300 * time.Microsecond, 2 * time.Second},
		{9700 * time.Microsecond, 10 * time.Millisecond},
		{300 * time.Millisecond, 300 * time.Millisecond},
		{29990 * time.Millisecond, 30 * time.Second},
		{104 * time.Millisecond, 110 * time.Millisecond},
		{7, 7},
		{0, 0},
	} {
		if got := roundUp(c.in); got != c.want {
			t.Errorf("roundUp(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

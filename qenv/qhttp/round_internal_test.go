package qhttp

import (
	"testing"
	"time"
)

// A deadline's remaining time at first sight is rounded to the nearest two
// significant digits, so jitter of either sign below half the last digit,
// near every boundary, converts a round timeout to itself.
func TestRoundNearStableUnderJitter(t *testing.T) {
	for _, d := range []time.Duration{
		10 * time.Millisecond, 95 * time.Millisecond, 99 * time.Millisecond, 100 * time.Millisecond,
		150 * time.Millisecond, 910 * time.Millisecond, time.Second, 2 * time.Second, 30 * time.Second,
	} {
		// jitter that shortens d is judged by the digit just below it (a
		// power of ten's finer one), jitter that lengthens it by d's own
		short, long := lastDigit(d-1)*49/100, lastDigit(d)*49/100
		for _, e := range []time.Duration{0, short / 2, -long / 2, short, -long} {
			if got := roundNear(d - e); got != d {
				t.Errorf("roundNear(%v - %v) = %v, want %v", d, e, got, d)
			}
		}
	}
	for _, c := range []struct{ in, want time.Duration }{
		{148700 * time.Microsecond, 150 * time.Millisecond},
		{1999300 * time.Microsecond, 2 * time.Second},
		{9970 * time.Microsecond, 10 * time.Millisecond},
		{90800 * time.Microsecond, 91 * time.Millisecond},
		{104 * time.Millisecond, 100 * time.Millisecond},
		{7, 7},
		{0, 0},
		{-time.Second, -time.Second},
	} {
		if got := roundNear(c.in); got != c.want {
			t.Errorf("roundNear(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// lastDigit is the unit of the second significant digit of d.
func lastDigit(d time.Duration) time.Duration {
	u := time.Duration(1)
	for u*100 <= d {
		u *= 10
	}
	return u
}

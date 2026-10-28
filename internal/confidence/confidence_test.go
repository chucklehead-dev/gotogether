package confidence

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestBounds(t *testing.T) {
	for _, c := range []struct{ n, oneIn int }{{30, 10}, {50, 17}, {1000, 334}} {
		if got := OneIn(c.n); got != c.oneIn {
			t.Errorf("OneIn(%d) = %d, want %d", c.n, got, c.oneIn)
		}
		// at the bound, n samples miss it with probability 1-Level
		if m := Missed(c.n, Rate(c.n)); math.Abs(m-(1-Level)) > 1e-9 {
			t.Errorf("Missed(%d, Rate) = %g, want %g", c.n, m, 1-Level)
		}
		// one in OneIn is found at least Level of the time
		if m := Missed(c.n, 1/float64(c.oneIn)); m > 1-Level {
			t.Errorf("Missed(%d, 1/%d) = %g, more than %g", c.n, c.oneIn, m, 1-Level)
		}
	}
	if Rate(0) != 1 || OneIn(0) != 1 || Missed(0, 0.5) != 1 {
		t.Errorf("no samples bound nothing: Rate(0) %g, OneIn(0) %d, Missed(0, .5) %g", Rate(0), OneIn(0), Missed(0, 0.5))
	}
	if got, want := Sentence(30, "run", "a failure"), "a failure that 1 run in 10 shows is missed at most 5% of the time by 30 runs, a rarer one more often"; got != want {
		t.Errorf("Sentence = %q, want %q", got, want)
	}
	if got, want := Brief(30, "run", "a failure"), "a failure 1 run in 10 shows is missed at most 5% of the time, a rarer one more often"; got != want {
		t.Errorf("Brief = %q, want %q", got, want)
	}
	if got, want := Sentence(3, "run", "a failure"), "a failure that 64% of runs show is missed at most 5% of the time by 3 runs, a rarer one more often"; got != want {
		t.Errorf("Sentence = %q, want %q", got, want)
	}
}

// Short is the binomial tail: checked against counting outcomes of
// coins, and against its edges.
func TestShortAgainstSimulation(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, c := range []struct {
		n, need int
		p       float64
	}{{30, 15, 0.6}, {20, 3, 0.1}, {10, 10, 0.9}} {
		short := 0
		const trials = 200000
		for range trials {
			k := 0
			for range c.n {
				if r.Float64() < c.p {
					k++
				}
			}
			if k < c.need {
				short++
			}
		}
		if got, want := Short(c.n, c.need, c.p), float64(short)/trials; math.Abs(got-want) > 0.005 {
			t.Errorf("Short(%d, %d, %g) = %g, simulated %g", c.n, c.need, c.p, got, want)
		}
	}
	if Short(10, 0, 0.5) != 0 || Short(10, 11, 0.5) != 1 || Short(10, 3, 0) != 1 || Short(10, 3, 1) != 0 {
		t.Errorf("Short's edges: %g %g %g %g", Short(10, 0, 0.5), Short(10, 11, 0.5), Short(10, 3, 0), Short(10, 3, 1))
	}
}

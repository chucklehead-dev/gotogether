package qtest

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// ErrDiverged is what an ActionShrink's Rerun returns (wrapped or not)
// when it could not do the actions it was given: a click on a button the
// page does not show, a cancel of a job that was never submitted, a
// driver's reproduction that went elsewhere. Such a rerun says nothing of
// the sequence: ShrinkActions counts it (ShrunkActions.Diverged), never
// as a pass nor as a failure, and does not count it among a candidate's
// reruns.
var ErrDiverged = errors.New("gotogether: the rerun did not do its actions")

// Calibration sets how ShrinkActions reruns a sequence whose failure
// may not show every time. The original is rerun Baseline times first:
// its rate of reproduction sets how often each candidate is rerun
// before it counts as passing, n = ⌈ln α / ln(1−p)⌉ for one
// reproduction needed (the fewest reruns that miss a failure of rate p
// with chance at most α), stopping at the reproduction. p is the rate's
// lower bound at confidence 1−α (Wilson's), not the rate measured: ten
// reruns of a failure shown half the time measure 0.7 or more one time
// in six, and n from the rate measured reached the true 1-minimal
// sequence in 933 shrinks of 1000, the bound in 987. A failure shown in each of 10 reruns
// reruns each candidate twice; set Reruns to 1 for a driver known to be
// deterministic.
type Calibration struct {
	// Baseline is how many times the original is rerun to measure its
	// rate, and the minimal sequence for ShrunkActions.Rate (default 10).
	Baseline int
	// Reruns, if set, is each candidate's reruns, whatever the rate.
	Reruns int
	// Need is how many reproductions make a candidate fail (default 1).
	Need int
	// Alpha is the chance a candidate whose failure shows at the
	// original's rate is taken for one without it (default 0.05).
	Alpha float64
	// Budget is the most reruns the whole shrink makes (0: no limit), and
	// Time the longest it runs (0: no limit). Either reached, the shrink
	// ends with the smallest sequence so far, not minimal. It ends so too
	// before the test's deadline. The search keeps Baseline+1 reruns of
	// the Budget back, for the shrunk sequence's own rate and a last
	// rerun of no actions, without which a shrink is not minimal.
	Budget int
	Time   time.Duration
}

// withDefaults is c with its zero fields defaulted; an invalid field is
// an error naming the fix.
func (c Calibration) withDefaults() (Calibration, error) {
	if c.Baseline == 0 {
		c.Baseline = 10
	}
	if c.Need == 0 {
		c.Need = 1
	}
	if c.Alpha == 0 {
		c.Alpha = 0.05
	}
	switch {
	case c.Baseline < 0 || c.Reruns < 0 || c.Need < 0 || c.Budget < 0 || c.Time < 0:
		return c, fmt.Errorf("a negative Calibration field (%+v): leave a field 0 for its default", c)
	case c.Alpha <= 0 || c.Alpha >= 1:
		return c, fmt.Errorf("Calibration.Alpha %v: it is a chance, between 0 and 1 (default 0.05)", c.Alpha)
	case c.Reruns > 0 && c.Reruns < c.Need:
		return c, fmt.Errorf("Calibration.Reruns %d is below Need %d: no candidate could fail", c.Reruns, c.Need)
	}
	return c, nil
}

// maxReruns caps a candidate's reruns: a failure rarer than this many
// reruns would make the shrink rerun forever.
const maxReruns = 1000

// rateBound is the lower end of the one-sided (1−alpha) Wilson score
// interval of a rate measured as r: what the rate is at least, but with
// chance alpha.
func rateBound(r Rate, alpha float64) float64 {
	low, _ := rateBounds(r, alpha)
	return low
}

// rateBounds are the ends of a rate's one-sided (1−alpha) Wilson score
// intervals: at least low, and at most high, each but with chance alpha
// (0 and 1 with no reruns).
func rateBounds(r Rate, alpha float64) (low, high float64) {
	if r.Of == 0 {
		return 0, 1
	}
	z := math.Sqrt2 * math.Erfinv(1-2*alpha)
	n, p := float64(r.Of), r.p()
	z2 := z * z / n
	mid, half := p+z2/2, z*math.Sqrt(p*(1-p)/n+z2/(4*n))
	return max(0, (mid-half)/(1+z2)), min(1, (mid+half)/(1+z2))
}

// rerunsFor is how many times a candidate is rerun, for a failure that
// reproduces at rate p and needs k reproductions: the fewest n with a
// chance of fewer than k in n at most alpha (for k = 1, ⌈ln α / ln(1−p)⌉).
// It is capped at maxReruns.
func rerunsFor(p float64, k int, alpha float64) int {
	if p >= 1 {
		return k
	}
	if p <= 0 {
		return maxReruns
	}
	for n := k; n < maxReruns; n++ {
		if binomialBelow(n, k, p) <= alpha {
			return n
		}
	}
	return maxReruns
}

// binomialBelow is the chance of fewer than k successes in n trials of
// chance p.
func binomialBelow(n, k int, p float64) float64 {
	sum := 0.0
	for i := range k {
		// C(n,i) p^i (1-p)^(n-i), in logs to stay finite at large n
		lc, _ := math.Lgamma(float64(n + 1))
		li, _ := math.Lgamma(float64(i + 1))
		lr, _ := math.Lgamma(float64(n - i + 1))
		sum += math.Exp(lc - li - lr + float64(i)*math.Log(p) + float64(n-i)*math.Log1p(-p))
	}
	return sum
}

// A Rate is how often a sequence reproduced its failure: Reproduced of
// Of reruns that ran it (a diverged or failed rerun is in neither).
type Rate struct {
	Reproduced, Of int
}

// String is the rate as "k of m".
func (r Rate) String() string { return fmt.Sprintf("%d of %d", r.Reproduced, r.Of) }

// p is the rate as a fraction (0 with no reruns).
func (r Rate) p() float64 {
	if r.Of == 0 {
		return 0
	}
	return float64(r.Reproduced) / float64(r.Of)
}

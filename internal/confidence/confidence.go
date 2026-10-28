// Package confidence says what a sample of runs or traces bounds: how
// likely as many independent samples are to miss a failure of a given
// rate, the rate below which one none of them showed lies at 95%
// confidence, and the chance that a share a claim wants falls short at
// the rate measured. A sample's "none of N" is no proof: these say how
// much it is worth, and of what it says nothing (a failure the samples
// cannot reach, whatever their number).
package confidence

import (
	"fmt"
	"math"
)

// Level is the confidence the bounds are stated at.
const Level = 0.95

// Missed is the probability that n independent samples all miss a
// failure each shows with probability p: (1-p)^n.
func Missed(n int, p float64) float64 {
	if n <= 0 {
		return 1
	}
	return math.Pow(1-p, float64(n))
}

// Rate is the rate of a failure none of n samples showed, at the bound of
// Level: with that confidence, a failure the samples could show is rarer
// than this (1 - 0.05^(1/n), about 3/n: the rule of three). 1 for no
// samples, which bound nothing.
func Rate(n int) float64 {
	if n <= 0 {
		return 1
	}
	return 1 - math.Pow(1-Level, 1/float64(n))
}

// OneIn is 1/Rate(n), rounded down: a failure that shows in at least 1
// sample of OneIn(n) is found by n samples with probability Level or
// more; a rarer one may be missed.
func OneIn(n int) int {
	r := Rate(n)
	if r >= 1 {
		return 1
	}
	return max(1, int(math.Floor(1/r)))
}

// Sentence is a sample's bound in words, for a report: "a failure that
// 1 run in 10 shows is missed at most 5% of the time by 30 runs, a
// rarer one more often" (what "a failure", "a violation"; unit "run",
// "trace").
// With too few samples for 1 in 2, the rate is a share: "a failure
// that 64% of runs show".
func Sentence(n int, unit, what string) string {
	rate := rateWords(n, unit)
	return fmt.Sprintf("%s that %s is missed at most %.0f%% of the time by %s, a rarer one more often",
		what, rate, 100*(1-Level), plural(n, unit))
}

// Brief is Sentence without the sample's size, for a line that states
// it already: "a failure 1 run in 10 shows is missed at most 5% of the
// time, a rarer one more often".
func Brief(n int, unit, what string) string {
	rate := rateWords(n, unit)
	return fmt.Sprintf("%s %s is missed at most %.0f%% of the time, a rarer one more often", what, rate, 100*(1-Level))
}

// rateWords is Rate(n) as a sentence says it: "1 run in 10 shows", or
// a share where that would be 1 in 1 ("64% of runs show").
func rateWords(n int, unit string) string {
	if OneIn(n) < 2 {
		return fmt.Sprintf("%.0f%% of %ss show", math.Ceil(100*Rate(n)), unit)
	}
	return fmt.Sprintf("1 %s in %d shows", unit, OneIn(n))
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// Short falls below need with probability Short(n, need, p): fewer than
// need of n samples show what each shows with probability p (the
// binomial tail), how likely a share claim that held is to fail another
// day at the rate measured.
func Short(n, need int, p float64) float64 {
	if need <= 0 {
		return 0
	}
	if need > n || p <= 0 {
		return 1
	}
	if p >= 1 {
		return 0
	}
	sum := 0.0
	for k := 0; k < need; k++ {
		sum += math.Exp(logChoose(n, k) + float64(k)*math.Log(p) + float64(n-k)*math.Log1p(-p))
	}
	return math.Min(sum, 1)
}

func logChoose(n, k int) float64 {
	a, _ := math.Lgamma(float64(n + 1))
	b, _ := math.Lgamma(float64(k + 1))
	c, _ := math.Lgamma(float64(n - k + 1))
	return a - b - c
}

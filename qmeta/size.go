package qmeta

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// A Burst is a transient stressor a setting is sized against: new
// requests at Rate per second for Duration seconds, from an empty system,
// after which they return to the Params' Lambda. A peak alone does not
// say which states the system reaches: held long enough, any rate above
// capacity reaches every state of the chain.
type Burst struct {
	Rate     float64
	Duration float64
}

// RecoveryAfter is the probability that the system of p, empty at first,
// its arrivals at b.Rate for b.Duration seconds and at p.Lambda after, has
// been healthy within `within` seconds of the burst's end: the chain's
// counterpart of the fraction of seeds qload.Recovers says recover. It
// weighs the states the burst ends in by their probability (Distribution),
// then the chance of each to recover (RecoveryWithin); a minimum over the
// states the burst can reach would be 0 for nearly every setting, since
// the orbit's bound is reachable with some small probability.
func RecoveryAfter(p Params, b Burst, healthy func(State) bool, within float64) float64 {
	peak := p
	peak.Lambda = b.Rate
	during, after := New(peak), New(p)
	pi := make([]float64, during.States())
	pi[during.Index(State{})] = 1
	if b.Duration > 0 {
		pi = during.Distribution(pi, b.Duration)
	}
	r := after.RecoveryWithin(healthy, within)
	sum := 0.0
	for i, q := range pi {
		sum += q * r[i]
	}
	return math.Min(1, sum)
}

// Sized is Size's answer.
type Sized struct {
	// Workers is the fewest workers whose RecoveryAfter is at least the
	// probability asked, 0 if none up to the most tried.
	Workers int
	// Recovery is RecoveryAfter at each worker count Size evaluated: the
	// evidence a report prints beside the count (the count below the
	// answer, which fails, is its margin).
	Recovery map[int]float64
}

// Size is the fewest workers (p.Workers ignored, from 1 to most) with
// which the system of p recovers from b with probability at least prob:
// RecoveryAfter(healthy, within) ≥ prob. It assumes recovery does not get
// worse with more workers, and bisects; TestSizeAgainstBruteForce checks
// it against every count on small chains. A hop whose workers no setting
// changes (one tenant's commits, one processor on one tenant: one at a
// time whatever the replicas) is not sized by it: its answer is a limit
// on its arrivals (LimitFor).
func Size(p Params, b Burst, healthy func(State) bool, within, prob float64, most int) Sized {
	out := Sized{Recovery: map[int]float64{}}
	ok := func(w int) bool {
		if _, done := out.Recovery[w]; !done {
			q := p
			q.Workers = w
			out.Recovery[w] = RecoveryAfter(q, b, healthy, within)
		}
		return out.Recovery[w] >= prob
	}
	if most < 1 {
		return out
	}
	// double to a count that recovers, then bisect below it
	lo, hi := 0, 1 // lo fails (0 workers: nothing served), hi is tried
	for !ok(hi) {
		if hi == most {
			return out
		}
		lo, hi = hi, min(2*hi, most)
	}
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if ok(mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	out.Workers = hi
	return out
}

// LimitFor is the highest arrival rate, up to p.Lambda's hi (the most to
// try), at which the system of p (its Lambda replaced) recovers from a
// burst of factor times that rate for duration seconds with probability
// at least prob: a rate limit for a hop no replica count speeds up. It
// bisects to within 1% of hi, assuming recovery does not improve with
// more load (TestLimitForAgainstBruteForce).
func LimitFor(p Params, factor, duration float64, healthy func(State) bool, within, prob, hi float64) float64 {
	ok := func(lambda float64) bool {
		q := p
		q.Lambda = lambda
		return RecoveryAfter(q, Burst{Rate: factor * lambda, Duration: duration}, healthy, within) >= prob
	}
	if ok(hi) {
		return hi
	}
	lo := 0.0
	for hi-lo > hi/100 {
		mid := (lo + hi) / 2
		if ok(mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// Timeout is TimeoutFor's answer: a per-attempt timeout, in seconds, and
// how it was found.
type Timeout struct {
	// Value is the timeout to set: the shortest candidate at or above
	// Floor and Drain whose RecoveryAfter is at least the probability
	// asked; 0 if none up to the longest tried (the retries, not the
	// timeout, keep the storm: fix them first).
	Value float64
	// Floor is the latency bound: the service's 99.9th percentile plus the
	// queue's 99.9th-percentile wait at p.Lambda (Erlang C, scaled by
	// (1+CV²)/2 for a service that is not exponential): a shorter timeout
	// times out healthy requests.
	Floor float64
	// Drain is, for a service that is not exponential, the time the
	// burst's backlog takes to drain at full capacity: a timeout below it
	// leaves a queue longer than μ × timeout, where regular service times
	// out every request in it (absorbing), which the exponential chain
	// does not see. 0 for exponential service.
	Drain float64
	// Scale is the calibration: the chain's timeout per real second
	// (CalibrateTimeout's fit over the nominal timeout), 1 uncalibrated.
	Scale float64
	// Recovery is RecoveryAfter at each candidate tried (real seconds).
	Recovery map[float64]float64
	// Why is the formula with its numbers, for a report.
	Why string
}

// TimeoutFor is the per-attempt timeout for the system of p (p.Timeout:
// the nominal one data were simulated at), s its measured service: the
// shortest of a geometric ladder of candidates from the latency floor
// (Timeout.Floor, raised to Timeout.Drain) whose recovery from b is at
// least prob. With data (trajectories simulated at p.Timeout,
// qload.SimulateTrajectory), the chain is first calibrated with λ held
// (CalibrateTimeout), and each candidate is given to it scaled by the
// fit. It assumes recovery does not get worse with a longer timeout, and
// bisects the ladder (TestTimeoutForAgainstBruteForce).
func TimeoutFor(p Params, data []Trajectory, s Service, b Burst, healthy func(State) bool, within, prob float64) Timeout {
	w := max(p.Workers, 1)
	t := Timeout{Scale: 1, Recovery: map[float64]float64{}}
	if len(data) > 0 && p.Timeout > 0 {
		fit, _ := CalibrateTimeout(p, data)
		t.Scale = fit.Timeout / p.Timeout
	}
	cv2 := s.CV * s.CV
	if s.Exponential() {
		cv2 = 1
	}
	wait := waitQuantile(p.Lambda, p.Mu, w, 0.999) * (1 + cv2) / 2
	svc := s.Quantile(0.999).Seconds()
	if len(s.Samples) == 0 && p.Mu > 0 {
		svc = math.Log(1000) / p.Mu // exponential service's 99.9th percentile
	}
	t.Floor = svc + wait
	if !s.Exponential() && len(s.Samples) > 0 {
		capacity := float64(w) * p.Mu
		t.Drain = math.Max(0, b.Rate-capacity) * b.Duration / capacity
	}
	start := math.Max(t.Floor, t.Drain)
	if start <= 0 {
		start = 1 / math.Max(p.Mu, 1e-9)
	}
	ladder := timeoutLadder(start)
	ok := func(i int) bool {
		c := ladder[i]
		if _, done := t.Recovery[c]; !done {
			q := p
			q.Timeout = c * t.Scale
			t.Recovery[c] = RecoveryAfter(q, b, healthy, within)
		}
		return t.Recovery[c] >= prob
	}
	lo, hi := -1, len(ladder)-1
	if ok(hi) {
		for hi-lo > 1 {
			mid := (lo + hi) / 2
			if ok(mid) {
				hi = mid
			} else {
				lo = mid
			}
		}
		t.Value = ladder[hi]
	}
	t.Why = t.explain(p, s, svc, wait, prob)
	return t
}

// timeoutLadder is the candidates TimeoutFor tries: start times 2^(k/2),
// k = 0..12 (start to 64 × start).
func timeoutLadder(start float64) []float64 {
	var out []float64
	for k := 0; k <= 12; k++ {
		out = append(out, start*math.Pow(2, float64(k)/2))
	}
	return out
}

func (t Timeout) explain(p Params, s Service, svc, wait, prob float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "floor %s = p99.9 service %s + p99.9 wait %s at λ=%.3g/s", seconds(t.Floor), seconds(svc), seconds(wait), p.Lambda)
	if t.Drain > 0 {
		fmt.Fprintf(&b, "; raised to %s, the burst's backlog drain (service CV %.2f, not exponential: a longer queue is absorbing)", seconds(t.Drain), s.CV)
	}
	tried := make([]float64, 0, len(t.Recovery))
	for c := range t.Recovery {
		tried = append(tried, c)
	}
	slices.Sort(tried)
	var rs []string
	for _, c := range tried {
		rs = append(rs, fmt.Sprintf("%.3g at %s", t.Recovery[c], seconds(c)))
	}
	fmt.Fprintf(&b, "; recovery after the burst %s", strings.Join(rs, ", "))
	if t.Scale != 1 {
		fmt.Fprintf(&b, " (chain calibrated: timeout × %.2f)", t.Scale)
	}
	if t.Value == 0 {
		fmt.Fprintf(&b, "; none reaches %.3g: the retries keep the storm, not the timeout", prob)
	}
	return b.String()
}

// waitQuantile is the q-quantile of an M/M/w queue's waiting time at
// arrival rate lambda and service rate mu per worker: P(W > t) =
// C(w, a) e^{-(wμ-λ)t} (Erlang C), so t_q = ln(C/(1-q)) / (wμ - λ), 0
// when fewer than 1-q of arrivals wait at all; +Inf at or above capacity.
func waitQuantile(lambda, mu float64, w int, q float64) float64 {
	if lambda <= 0 || mu <= 0 {
		return 0
	}
	capacity := float64(w) * mu
	if lambda >= capacity {
		return math.Inf(1)
	}
	c := erlangC(w, lambda/mu)
	if c <= 1-q {
		return 0
	}
	return math.Log(c/(1-q)) / (capacity - lambda)
}

// erlangC is the probability that an arrival waits in an M/M/w queue
// with offered load a = λ/μ (< w), by the Erlang B recursion.
func erlangC(w int, a float64) float64 {
	b := 1.0
	for k := 1; k <= w; k++ {
		b = a * b / (float64(k) + a*b)
	}
	rho := a / float64(w)
	return b / (1 - rho + rho*b)
}

// inUnits is p counted in units of p.Unit requests (Params.Unit), Unit
// then 1.
func (p Params) inUnits() Params {
	g := p.Unit
	if g <= 1 {
		return p
	}
	w := max(p.Workers, 1)
	f := float64(g)
	q := p
	q.Unit = 1
	q.Workers = (w + g - 1) / g
	q.Mu = p.Mu * float64(w) / (f * float64(q.Workers))
	q.Lambda = p.Lambda / f
	q.RetryRate = p.RetryRate / f
	q.Queue = max(1, (p.Queue+g-1)/g)
	if p.Orbit > 0 {
		q.Orbit = max(1, (p.Orbit+g-1)/g)
	}
	return q
}

package qmeta_test

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qmeta"
)

// small is a random chain small enough to enumerate: a few workers'
// worth of queue, a short timeout, a burst above capacity.
func small(rnd *rand.Rand) (qmeta.Params, qmeta.Burst, func(qmeta.State) bool) {
	mu := 1 + 3*rnd.Float64()
	p := qmeta.Params{
		Lambda:  mu * (0.3 + rnd.Float64()), // up to 1.3 × one worker
		Mu:      mu,
		Workers: 1,
		Timeout: 1 + 4*rnd.Float64(),
		Retries: rnd.IntN(4),
		Queue:   8 + rnd.IntN(8),
		Orbit:   16 + rnd.IntN(12),
	}
	if rnd.IntN(3) == 0 {
		p.Backoff = 2 * rnd.Float64()
	}
	if rnd.IntN(3) == 0 {
		p.RetryBudget = 0.2
	}
	b := qmeta.Burst{Rate: p.Lambda * (2 + 2*rnd.Float64()), Duration: 2 + 8*rnd.Float64()}
	maxU := 1 + rnd.IntN(3)
	return p, b, func(s qmeta.State) bool { return s.U <= maxU }
}

// Size's bisection finds the fewest workers that every count, tried in
// turn, finds, and recovery is monotone in the workers on every chain
// tried: the assumption the bisection rests on.
func TestSizeAgainstBruteForce(t *testing.T) {
	rnd := rand.New(rand.NewPCG(7, 11))
	found := 0
	for i := range 40 {
		p, b, healthy := small(rnd)
		within, prob, most := 5+10*rnd.Float64(), 0.5+0.45*rnd.Float64(), 6
		got := qmeta.Size(p, b, healthy, within, prob, most)
		want, last := 0, -1.0
		for w := 1; w <= most; w++ {
			q := p
			q.Workers = w
			r := qmeta.RecoveryAfter(q, b, healthy, within)
			if r < last-1e-9 {
				t.Fatalf("chain %d: recovery %.4f at %d workers < %.4f at %d: not monotone (%+v)", i, r, w, last, w-1, p)
			}
			last = r
			if want == 0 && r >= prob {
				want = w
			}
		}
		if got.Workers != want {
			t.Fatalf("chain %d: Size = %d, brute force %d (%+v, %+v, prob %.2f; tried %v)", i, got.Workers, want, p, b, prob, got.Recovery)
		}
		if want > 1 {
			found++
		}
		if len(got.Recovery) > most {
			t.Fatalf("Size tried %d counts of %d", len(got.Recovery), most)
		}
	}
	if found < 5 {
		t.Fatalf("only %d of 40 chains needed more than one worker: the test does not exercise the search", found)
	}
}

// LimitFor's bisection is within its 1% of the highest rate a scan of
// rates finds recovering, and that is a threshold (recovery does not
// come back above it).
func TestLimitForAgainstBruteForce(t *testing.T) {
	rnd := rand.New(rand.NewPCG(3, 5))
	for i := range 6 {
		p, _, healthy := small(rnd)
		factor, duration, within, prob := 2+rnd.Float64(), 3+5*rnd.Float64(), 10.0, 0.8
		hi := 2 * p.Mu
		got := qmeta.LimitFor(p, factor, duration, healthy, within, prob, hi)
		want, broke := 0.0, false
		for k := 1; k <= 100; k++ {
			lambda := hi * float64(k) / 100
			q := p
			q.Lambda = lambda
			ok := qmeta.RecoveryAfter(q, qmeta.Burst{Rate: factor * lambda, Duration: duration}, healthy, within) >= prob
			if ok && broke {
				t.Fatalf("chain %d: recovers at %.3f above a rate that fails: no threshold", i, lambda)
			}
			if ok {
				want = lambda
			} else {
				broke = true
			}
		}
		if math.Abs(got-want) > hi/100+hi/100 {
			t.Fatalf("chain %d: LimitFor = %.3f, scan %.3f (hi %.3f)", i, got, want, hi)
		}
	}
}

// TimeoutFor's bisection of its ladder finds what trying every rung
// finds; its floor is the measured p99.9 plus the queue's wait.
func TestTimeoutForAgainstBruteForce(t *testing.T) {
	rnd := rand.New(rand.NewPCG(5, 9))
	nonzero := 0
	for i := range 12 {
		mu := 4 + 4*rnd.Float64()
		p := qmeta.Params{Lambda: mu * (0.5 + 0.35*rnd.Float64()), Mu: mu, Workers: 1, Timeout: 1,
			Retries: 2 + rnd.IntN(3), Queue: 20 + rnd.IntN(10), Orbit: 40 + rnd.IntN(20)}
		b := qmeta.Burst{Rate: p.Lambda * (2 + rnd.Float64()), Duration: 4 + 6*rnd.Float64()}
		var samples []time.Duration
		for range 400 {
			samples = append(samples, time.Duration(rnd.ExpFloat64()/p.Mu*float64(time.Second)))
		}
		s := qmeta.Measure(samples)
		within, prob := 15.0, 0.5+0.45*rnd.Float64()
		healthy := func(s qmeta.State) bool { return s.U <= 2 }
		got := qmeta.TimeoutFor(p, nil, s, b, healthy, within, prob)
		if got.Floor < s.Quantile(0.999).Seconds() {
			t.Fatalf("floor %.3f below the service's p99.9 %v", got.Floor, s.Quantile(0.999))
		}
		want := 0.0
		start := math.Max(got.Floor, got.Drain)
		last := -1.0
		for k := 0; k <= 12; k++ {
			c := start * math.Pow(2, float64(k)/2)
			q := p
			q.Timeout = c
			r := qmeta.RecoveryAfter(q, b, healthy, within)
			if r < last-1e-6 {
				t.Logf("chain %d: recovery %.4f at %.3fs below %.4f at the rung before", i, r, c, last)
			}
			last = r
			if want == 0 && r >= prob {
				want = c
			}
		}
		if math.Abs(got.Value-want) > 1e-9 {
			t.Fatalf("chain %d: TimeoutFor = %.4f, brute force %.4f (%+v; %s)", i, got.Value, want, p, got.Why)
		}
		if want > start {
			nonzero++
		}
	}
	if nonzero < 3 {
		t.Fatalf("only %d of 12 chains needed more than the floor: the test does not exercise the search", nonzero)
	}
}

// The burst matters: the same system recovers from a short burst and not
// from a long one, and RecoveryAfter without a burst is the empty
// system's (healthy at once).
func TestRecoveryAfter(t *testing.T) {
	p := qmeta.Params{Lambda: 9, Mu: 10, Timeout: 9, Retries: 3, Queue: 100, Orbit: 120}
	healthy := qmeta.Healthy{MaxU: 10}.Contains
	none := qmeta.RecoveryAfter(p, qmeta.Burst{}, healthy, 60)
	short := qmeta.RecoveryAfter(p, qmeta.Burst{Rate: 15, Duration: 5}, healthy, 60)
	long := qmeta.RecoveryAfter(p, qmeta.Burst{Rate: 15, Duration: 60}, healthy, 60)
	t.Logf("recovery within 60 s: no burst %.3f, 5 s at 15/s %.3f, 60 s %.3f", none, short, long)
	if none < 0.999 || short < long+0.3 {
		t.Fatalf("no burst %.3f, short %.3f, long %.3f: want 1, and short well above long", none, short, long)
	}
}

// A chain in units (Params.Unit) keeps the fluid drift: at the same state,
// its drift in units of g is the fine chain's divided by g, within the
// noise the coarser count loses near the queue's timeout threshold.
func TestUnitsKeepTheDrift(t *testing.T) {
	p := qmeta.Params{Lambda: 40, Mu: 70, Timeout: 1, Retries: 2, Queue: 210, Orbit: 420}
	g := 5
	fine := qmeta.New(p)
	pu := p
	pu.Unit = g
	coarse := qmeta.New(pu)
	drift := func(c *qmeta.Chain, s qmeta.State) (du, dv float64) {
		for _, tr := range c.Out(s) {
			du += tr.Rate * float64(tr.To.U-s.U)
			dv += tr.Rate * float64(tr.To.V-s.V)
		}
		return
	}
	for _, s := range []qmeta.State{{U: 10, V: 10}, {U: 30, V: 40}, {U: 150, V: 100}, {U: 200, V: 300}} {
		fu, fv := drift(fine, s)
		cu, cv := drift(coarse, qmeta.State{U: s.U / g, V: s.V / g})
		t.Logf("%v: fine drift (%.1f, %.1f)/s, coarse ×g (%.1f, %.1f)/s", s, fu, fv, cu*float64(g), cv*float64(g))
		if math.Abs(cu*float64(g)-fu) > 0.25*math.Max(math.Abs(fu), 10) || math.Abs(cv*float64(g)-fv) > 0.25*math.Max(math.Abs(fv), 10) {
			t.Fatalf("%v: coarse drift ×g (%.1f, %.1f) is not the fine (%.1f, %.1f)", s, cu*float64(g), cv*float64(g), fu, fv)
		}
	}
}

func TestErlangFloor(t *testing.T) {
	// M/M/1 at ρ = 0.5: P(wait > t) = ρ e^{-(μ-λ)t}, so the 99.9th
	// percentile wait is ln(500)/(μ-λ)
	p := qmeta.Params{Lambda: 5, Mu: 10, Workers: 1, Timeout: 1, Queue: 30, Orbit: 60}
	got := qmeta.TimeoutFor(p, nil, qmeta.Service{Mean: 100 * time.Millisecond, CV: 1}, qmeta.Burst{}, qmeta.Healthy{MaxU: 3}.Contains, 10, 0.9)
	want := math.Log(1000)/10 + math.Log(500)/5
	if math.Abs(got.Floor-want) > 1e-6 {
		t.Fatalf("floor %.4f, want %.4f (%s)", got.Floor, want, got.Why)
	}
}

// A region of a chain in units alerts in requests: its rule's
// thresholds and healthy condition are the units' times the unit, and
// its derivation says so.
func TestRulesInUnits(t *testing.T) {
	p := qmeta.Params{Lambda: 9.5, Mu: 10, Timeout: 9, Retries: 3, Queue: 100, Orbit: 120, Unit: 4}
	r := qmeta.New(p).HealthyRegion(qmeta.Healthy{MaxU: 3}, 120)
	rules := r.Rules(qmeta.RuleConfig{Server: "api", Service: "api"}, "")
	bands := r.Bands(10)
	if len(bands) == 0 {
		t.Fatal("no region")
	}
	want := fmt.Sprintf(`quint_serve_inflight{name="api"} >= %d`, bands[0].ULo*4)
	if !strings.Contains(rules, want) || !strings.Contains(rules, `<= 12"`) || !strings.Contains(rules, "units of 4 requests") {
		t.Fatalf("rules in units of 4 lack %q, the healthy u <= 12, or the note:\n%s", want, rules)
	}
}

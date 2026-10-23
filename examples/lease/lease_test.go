package lease

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

const binding = "lease.binding.yaml"

// system is the lock, the store and the holders with mutation m, with
// clock faults if clocks.
func system(m Mutation, clocks bool) qtest.System { return drifting(m, Clocks{Faults: clocks}) }

// drifting is system with clocks' faults.
func drifting(m Mutation, clocks Clocks) qtest.System {
	return qtest.System{Binding: binding, Run: func(c qenv.Chooser) error {
		_, err := Run(c, m, clocks)
		return err
	}}
}

// Only the clocks' faults, at the default rates, or only skews at half
// the processes: each mutant's own fault kind, without the other.
var (
	jumps = qenv.DefaultRates.Set("clock.skew", 0)
	skews = qenv.DefaultRates.Set("clock.jump", 0).Set("clock.skew", 0.5)
	// every holder's clock fast or slow, never skewed or jumping
	drifts = qenv.DefaultRates.Set("clock.jump", 0).Set("clock.skew", 0).Set("clock.drift", 1)
)

// The fenced store keeps the requirement whatever the clocks do: skewed,
// jumping, lost requests and responses. Every model action is taken and
// every fault kind reached is faulted in some run.
func TestFencedConformsUnderClockFaults(t *testing.T) {
	qtest.RequireQuint(t)
	system(Faithful, true).Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, qtest.Conforms(), qtest.Exercised())
}

// Without fencing, the holders' margin is enough while clocks are true:
// no run writes stale, so a test without clock faults passes the bug.
func TestNoFencingSafeWithTrueClocks(t *testing.T) {
	qtest.RequireQuint(t)
	system(NoFencing, false).Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, qtest.Conforms())
}

// A clock that jumps (the holder's back, the lock's forward) lets an old
// holder write after its successor. Measured: 37% of runs (200 seeds).
func TestNoFencingBrokenByAClockJump(t *testing.T) {
	qtest.RequireQuint(t)
	system(NoFencing, true).Explore(t, qtest.ExploreConfig{Seeds: 30, Rates: jumps}).
		Expect(t, qtest.Violates("noStaleWrite"), qtest.Some(qtest.Conforms()))
}

// Comparing the lock's expiry time with the holder's own clock is wrong
// as soon as the two clocks differ: a skew alone breaks it (36% of runs at
// skews', 200 seeds), where timing the lease on one clock (NoFencing)
// is immune to skew. True clocks hide it.
func TestAbsoluteExpiryBrokenBySkew(t *testing.T) {
	qtest.RequireQuint(t)
	system(AbsoluteExpiry, false).Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, qtest.Conforms())
	system(AbsoluteExpiry, true).Explore(t, qtest.ExploreConfig{Seeds: 30, Rates: skews}).Expect(t, qtest.Violates("noStaleWrite"))
	system(NoFencing, true).Explore(t, qtest.ExploreConfig{Seeds: 30, Rates: skews}).Expect(t, qtest.Conforms())
}

// Every run with one clock jump, not a sample (209 of them): one jump is
// enough without fencing, and the fenced store survives every one.
func TestOneClockJumpIsEnough(t *testing.T) {
	qtest.RequireQuint(t)
	one := qtest.Bound{Faults: 1, Without: []string{"rpc", "clock.skew"}}
	system(NoFencing, true).Exhaust(t, one).Expect(t, qtest.Violates("noStaleWrite"))
	system(Faithful, true).Exhaust(t, one).Expect(t, qtest.Conforms())
}

// Drift breaks what skew cannot: a lease timed on the holder's own clock.
// A slow clock under-measures it, so a holder writes past its lease's end
// once the drift eats what is left of its margin after a pause (300 ms
// less 200, over the 1.7 s it times: about 5%). Measured, of 100 runs: 0
// at 2%, 1 at 5%, 12 at 10%, 43 at 25%. The fenced store conforms at any
// drift.
func TestNoFencingBrokenByDrift(t *testing.T) {
	qtest.RequireQuint(t)
	x := qtest.ExploreConfig{Seeds: 30, Rates: drifts}
	drifting(NoFencing, Clocks{Faults: true, Drift: 0.02}).Explore(t, x).Expect(t, qtest.Conforms())
	drifting(NoFencing, Clocks{Faults: true, Drift: 0.25}).Explore(t, x).Expect(t, qtest.Violates("noStaleWrite"))
	drifting(Faithful, Clocks{Faults: true, Drift: 0.25}).Explore(t, x).Expect(t, qtest.Conforms())
}

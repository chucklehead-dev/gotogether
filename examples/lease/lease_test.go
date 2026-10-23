package lease

import (
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

const binding = "lease.binding.yaml"

// system is the lock, the store and the holders with mutation m, the
// processes' clocks faulted as clocks says.
func system(m Mutation, clocks qenv.Clocks) qtest.System {
	return qtest.System{Binding: binding, Run: func(c qenv.Chooser) error {
		_, err := Run(c, m, clocks)
		return err
	}}
}

// The clock faults, one kind at a time, and the rates that bring them
// about: a skew at half the processes, jumps at one scheduling step in
// twenty, every process drifting.
var (
	trueClocks = qenv.Clocks{}
	skew       = qenv.Clocks{Skew: time.Second}
	jump       = qenv.Clocks{Jump: 5 * time.Second}
	all        = qenv.Clocks{Skew: time.Second, Jump: 5 * time.Second, Drift: 0.25}

	skews  = qenv.DefaultRates.Set("clock.skew", 0.5)
	jumps  = qenv.DefaultRates.Set("nemesis", 0.05)
	drifts = qenv.DefaultRates.Set("clock.drift", 1)
)

// The fenced store keeps the requirement whatever the clocks do: skewed,
// jumping, drifting, and lost requests and responses. Every model action
// is taken and every fault kind reached is faulted in some run.
func TestFencedConformsUnderClockFaults(t *testing.T) {
	qtest.RequireQuint(t)
	rates := qenv.DefaultRates.Set("clock.skew", 0.5).Set("clock.drift", 0.5).Set("nemesis", 0.05)
	system(Faithful, all).Explore(t, qtest.ExploreConfig{Seeds: 30, Rates: rates}).Expect(t, qtest.Conforms(), qtest.Exercised())
}

// Without fencing, the holders' margin is enough while clocks are true:
// no run writes stale, so a test without clock faults passes the bug.
func TestNoFencingSafeWithTrueClocks(t *testing.T) {
	qtest.RequireQuint(t)
	system(NoFencing, trueClocks).Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, qtest.Conforms())
}

// A lease timed on the monotonic clocks (the holder's interval, the lock's
// expiry: what time.Since and Time.Before of two time.Now readings use in
// Go) does not see a skew or a jump: without fencing it still conforms,
// in every run with one jump (3209 of them) as in a sample with skews and
// jumps.
func TestNoFencingImmuneToSkewAndJumps(t *testing.T) {
	qtest.RequireQuint(t)
	both := qenv.Clocks{Skew: time.Second, Jump: 5 * time.Second}
	system(NoFencing, both).Explore(t, qtest.ExploreConfig{Seeds: 30, Rates: skews.Set("nemesis", 0.05)}).Expect(t, qtest.Conforms())
	system(NoFencing, jump).Exhaust(t, qtest.Bound{Faults: 1, Without: []string{"rpc"}, MaxRuns: 5000}).Expect(t, qtest.Conforms())
}

// Timing the lease on the wall clock instead (Now().Sub, as code does
// whose times lost their monotonic reading: a Round(0), a time read back
// from storage) is broken by one jump: the holder's clock goes back, and
// it writes after its successor. Measured: half the runs at jumps' rate
// (100 seeds); 306 of the 3209 runs with one jump. The fenced store
// survives every one of them.
func TestWallIntervalBrokenByAClockJump(t *testing.T) {
	qtest.RequireQuint(t)
	system(WallInterval, jump).Explore(t, qtest.ExploreConfig{Seeds: 30, Rates: jumps}).
		Expect(t, qtest.Violates("noStaleWrite"), qtest.Some(qtest.Conforms()))
	one := qtest.Bound{Faults: 1, Without: []string{"rpc"}, MaxRuns: 5000}
	system(WallInterval, jump).Exhaust(t, one).Expect(t, qtest.Violates("noStaleWrite"))
	system(Faithful, jump).Exhaust(t, one).Expect(t, qtest.Conforms())
}

// Comparing the lock's expiry time with the holder's own clock is wrong
// as soon as the two clocks differ: a skew alone breaks it (36 of 100 runs
// at skews'), where timing the lease on one clock, even its wall clock
// (WallInterval), is immune to skew. True clocks hide it.
func TestAbsoluteExpiryBrokenBySkew(t *testing.T) {
	qtest.RequireQuint(t)
	system(AbsoluteExpiry, trueClocks).Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, qtest.Conforms())
	system(AbsoluteExpiry, skew).Explore(t, qtest.ExploreConfig{Seeds: 30, Rates: skews}).Expect(t, qtest.Violates("noStaleWrite"))
	system(WallInterval, skew).Explore(t, qtest.ExploreConfig{Seeds: 30, Rates: skews}).Expect(t, qtest.Conforms())
}

// Drift breaks what skew and jumps cannot: a lease timed on a monotonic
// clock. A slow clock under-measures it, so a holder writes past its
// lease's end once the drift eats what is left of its margin after a
// pause (300 ms less 200, over the 1.7 s it times: about 5%). Measured, of
// 100 runs: 0 at 2%, 30 at 25%. The fenced store conforms at any drift.
func TestNoFencingBrokenByDrift(t *testing.T) {
	qtest.RequireQuint(t)
	x := qtest.ExploreConfig{Seeds: 30, Rates: drifts}
	system(NoFencing, qenv.Clocks{Drift: 0.02}).Explore(t, x).Expect(t, qtest.Conforms())
	system(NoFencing, qenv.Clocks{Drift: 0.25}).Explore(t, x).Expect(t, qtest.Violates("noStaleWrite"))
	system(Faithful, qenv.Clocks{Drift: 0.25}).Explore(t, x).Expect(t, qtest.Conforms())
}

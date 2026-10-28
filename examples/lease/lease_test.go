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

	skews  = punctual(qenv.DefaultRates.Set("clock.skew", 0.5))
	jumps  = punctual(qenv.DefaultRates.Set("nemesis", 0.05))
	drifts = punctual(qenv.DefaultRates.Set("clock.drift", 1))
)

// oneJump is every run with one jump (3209 runs). Under -short it is the
// first 100 of them (a search cut short, breadth first: the nominal run,
// then each jump at the first dozen of the 401 scheduling steps), which
// hold WallInterval's first stale writes (9 runs, from run 30, all after
// holder a's jump back; holder b's begin at run 696).
func oneJump() qtest.Bound {
	if testing.Short() {
		return qtest.Bound{Faults: 1, Without: []string{"rpc"}, MaxRuns: 100, Partial: true}
	}
	return qtest.Bound{Faults: 1, Without: []string{"rpc"}, MaxRuns: 5000}
}

// punctual is r with no request delivered late: the clock tests' runs
// fault the clocks, the lost requests and responses, and nothing else. A
// late write breaks a lock without fencing whatever the clocks do
// (TestNoFencingBrokenByALateWrite).
func punctual(r qenv.Rates) qenv.Rates {
	return r.Set("rpc.lock.delay", 0).Set("rpc.store.delay", 0)
}

// The fenced store keeps the requirement whatever the clocks do: skewed,
// jumping, drifting, and lost requests and responses. Every model action
// is taken and every fault kind reached is faulted in some run.
func TestFencedConformsUnderClockFaults(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	rates := qenv.DefaultRates.Set("clock.skew", 0.5).Set("clock.drift", 0.5).Set("nemesis", 0.05)
	system(Faithful, all).WithRates(rates).Explore(t, qtest.ExploreConfig{Seeds: 30, ShortSeeds: 15}).Expect(t, qtest.Conforms(), qtest.Exercised())
}

// Without fencing, the holders' margin is enough while clocks are true
// and every request arrives on time: no run writes stale, so a test
// without clock faults or late messages passes the bug.
func TestNoFencingSafeWithTrueClocks(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	system(NoFencing, trueClocks).WithRates(punctual(qenv.DefaultRates)).Explore(t, qtest.ExploreConfig{Seeds: 30, ShortSeeds: 10}).Expect(t, qtest.Conforms())
}

// A write delivered late (a delayed packet, after its writer's timeout)
// lands after the lease has passed to the next holder, who has written:
// without fencing it overwrites, whatever the clocks, while the fenced
// store refuses its old token. It is the case fencing exists for, which a
// network that is never late hid. Measured at the default rates: 25 of
// 90 runs write stale with true clocks; at a late write in three, every
// run of the fenced store had one, and conformed. -short runs 12 seeds
// of each (at 28%, 12 miss a stale write 2% of the time).
func TestNoFencingBrokenByALateWrite(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	x := qtest.ExploreConfig{Seeds: 30, ShortSeeds: 12}
	system(NoFencing, trueClocks).Explore(t, x).Expect(t, qtest.Some(qtest.Violates("noStaleWrite")), qtest.Some(qtest.Conforms()))
	system(Faithful, trueClocks).WithRates(qenv.DefaultRates.Set("rpc.store.delay", 0.3)).Explore(t, x).
		Expect(t, qtest.Conforms(), qtest.Reaches("rpc.store.delay").Faulted().InAtLeast(0.3))
}

// A holder paused longer than its lease (a GC, a SIGSTOP) finds its
// lease still valid by its own measure when it goes on, though the lock
// gave it to the next holder meanwhile: without fencing it writes stale,
// with true clocks and every request on time; the fenced store refuses
// its old token. Measured: 25 of 90 runs at a pause in fifty steps.
// -short runs 12 seeds of each.
func TestNoFencingBrokenByAPause(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	paused := func(m Mutation) qtest.System {
		return qtest.System{Binding: binding, Rates: punctual(qenv.DefaultRates.Set("nemesis.pause", 0.02)), Run: func(c qenv.Chooser) error {
			_, err := RunStalled(c, m, trueClocks, qenv.Stalls{Pause: 3 * time.Second})
			return err
		}}
	}
	x := qtest.ExploreConfig{Seeds: 30, ShortSeeds: 12}
	paused(NoFencing).Explore(t, x).Expect(t, qtest.Some(qtest.Violates("noStaleWrite")), qtest.Some(qtest.Conforms()))
	paused(Faithful).Explore(t, x).Expect(t, qtest.Conforms(), qtest.Reaches("nemesis.pause").Faulted().InAtLeast(0.3))
}

// A lease timed on the monotonic clocks (the holder's interval, the lock's
// expiry: what time.Since and Time.Before of two time.Now readings use in
// Go) does not see a skew or a jump: without fencing it still conforms,
// in every run with one jump (3209 of them; -short: the first 100) as in
// a sample with skews and jumps (-short: 10 seeds).
func TestNoFencingImmuneToSkewAndJumps(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	both := qenv.Clocks{Skew: time.Second, Jump: 5 * time.Second}
	system(NoFencing, both).WithRates(skews.Set("nemesis", 0.05)).Explore(t, qtest.ExploreConfig{Seeds: 30, ShortSeeds: 10}).Expect(t, qtest.Conforms())
	system(NoFencing, jump).Exhaust(t, oneJump()).Expect(t, qtest.Conforms())
}

// Timing the lease on the wall clock instead (Now().Sub, as code does
// whose times lost their monotonic reading: a Round(0), a time read back
// from storage) is broken by one jump: the holder's clock goes back, and
// it writes after its successor. Measured: half the runs at jumps' rate
// (100 seeds); 306 of the 3209 runs with one jump. The fenced store
// survives every one of them. -short runs 8 seeds and the first 100 runs
// with one jump (oneJump).
func TestWallIntervalBrokenByAClockJump(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	system(WallInterval, jump).WithRates(jumps).Explore(t, qtest.ExploreConfig{Seeds: 30, ShortSeeds: 8}).
		Expect(t, qtest.Some(qtest.Violates("noStaleWrite")), qtest.Some(qtest.Conforms()))
	one := oneJump()
	system(WallInterval, jump).Exhaust(t, one).Expect(t, qtest.Some(qtest.Violates("noStaleWrite")))
	system(Faithful, jump).Exhaust(t, one).Expect(t, qtest.Conforms())
}

// Comparing the lock's expiry time with the holder's own clock is wrong
// as soon as the two clocks differ: a skew alone breaks it (36 of 100 runs
// at skews'), where timing the lease on one clock, even its wall clock
// (WallInterval), is immune to skew. True clocks hide it. -short runs 10
// seeds of each (at 32%, 10 miss the stale write 2% of the time).
func TestAbsoluteExpiryBrokenBySkew(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	system(AbsoluteExpiry, trueClocks).WithRates(punctual(qenv.DefaultRates)).Explore(t, qtest.ExploreConfig{Seeds: 30, ShortSeeds: 10}).Expect(t, qtest.Conforms())
	system(AbsoluteExpiry, skew).WithRates(skews).Explore(t, qtest.ExploreConfig{Seeds: 30, ShortSeeds: 10}).Expect(t, qtest.Some(qtest.Violates("noStaleWrite")))
	system(WallInterval, skew).WithRates(skews).Explore(t, qtest.ExploreConfig{Seeds: 30, ShortSeeds: 10}).Expect(t, qtest.Conforms())
}

// Drift breaks what skew and jumps cannot: a lease timed on a monotonic
// clock. A slow clock under-measures it, so a holder writes past its
// lease's end once the drift eats what is left of its margin after a
// pause (300 ms less 200, over the 1.7 s it times: about 5%). Measured, of
// 100 runs: 0 at 2%, 30 at 25%. The fenced store conforms at any drift.
// -short runs 10 seeds, and 14 at 25% (which miss it 1% of the time).
func TestNoFencingBrokenByDrift(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	x := qtest.ExploreConfig{Seeds: 30, ShortSeeds: 10}
	system(NoFencing, qenv.Clocks{Drift: 0.02}).WithRates(drifts).Explore(t, x).Expect(t, qtest.Conforms())
	system(NoFencing, qenv.Clocks{Drift: 0.25}).WithRates(drifts).Explore(t, qtest.ExploreConfig{Seeds: 30, ShortSeeds: 14}).Expect(t, qtest.Some(qtest.Violates("noStaleWrite")))
	system(Faithful, qenv.Clocks{Drift: 0.25}).WithRates(drifts).Explore(t, x).Expect(t, qtest.Conforms())
}

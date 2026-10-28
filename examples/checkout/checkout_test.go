package checkout_test

import (
	"fmt"
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/checkout"
	"github.com/chucklehead-dev/gotogether/examples/checkout/shop"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qtest"
	"hegel.dev/go/hegel"
)

const binding = "checkout.binding.yaml"

// system is the shop as cfg deploys it, bound to the model.
func system(cfg checkout.Config) qtest.System {
	s := qtest.System{Binding: binding, Run: func(c qenv.Chooser) error {
		_, err := checkout.Run(c, cfg)
		return err
	}}
	if cfg.Mutation != shop.Faithful {
		s.Mutant = cfg.Mutation.String() // its runs are not the shop's: the suite's evidence leaves them out
	}
	return s
}

func explore(t *testing.T, cfg checkout.Config, rates qenv.Rates, x qtest.ExploreConfig) *qtest.Exploration {
	return system(cfg).WithRates(rates).Explore(t, x)
}

// crashy crashes a process at one scheduling step in twenty.
var crashy = qenv.Rates{Default: 0.05, Kinds: map[string]float64{"nemesis": 0.05}}

var levels = []qenv.Isolation{qenv.Serializable, qenv.SnapshotIsolation, qenv.ReadCommitted}

// The faithful shop is correct at every isolation level, through crashes,
// lost responses, duplicate requests and redeliveries: 60 seeds a level,
// which take every fault option. Under -short, 6 seeds a level conform,
// without the claim that they exercise every option (the rarest delays
// take more seeds).
func TestFaithfulConformsAtEveryLevel(t *testing.T) {
	qtest.RequireQuint(t)
	t.Parallel()
	claims := []qtest.Claim{qtest.Conforms(), qtest.NotVacuous()}
	if !testing.Short() {
		claims = append(claims, qtest.Exercised())
	}
	for _, iso := range levels {
		t.Run(iso.String(), func(t *testing.T) {
			t.Parallel()
			explore(t, checkout.Config{Isolation: iso}, crashy, qtest.ExploreConfig{Seeds: 60, ShortSeeds: 6}).Expect(t, claims...)
		})
	}
}

// What each bug looks like, and at which isolation levels it is a bug at
// all: missing row locks oversell only under read committed, a lock
// without a write only under snapshot isolation. Under snapshot isolation
// missing row locks also refuse an order from a stale snapshot (a
// cancellation's stock returned after it), which the model, judging the
// refusal where it committed, rejects; a crash after a commit (qenv.Service's
// window) makes that interleaving reachable.
//
// Under -short each case runs the seeds that keep its claim's miss chance
// at most 5%; the two caught in about one run in fifty need hundreds, and
// a mutant that conforms claims no catch: they are left to the full run.
func TestMutants(t *testing.T) {
	qtest.RequireQuint(t)
	t.Parallel()
	shipOrCancel := qtest.AnyOf(qtest.Rejects("ship"), qtest.Rejects("cancel"))
	cases := []struct {
		m     shop.Mutation
		iso   qenv.Isolation
		rates qenv.Rates
		want  []qtest.Claim
		seeds int // 0: 100
		short int // seeds under -short; 0: skipped there
	}{
		{shop.NoLocks, qenv.Serializable, crashy, []qtest.Claim{qtest.Conforms()}, 0, 0},
		// the stale refusal in about one run in fifty (2 of 100, 4 of 350):
		// 350 seeds miss it about once in a thousand, and the 260 that
		// would miss it one time in twenty are too many for -short
		{shop.NoLocks, qenv.SnapshotIsolation, crashy, []qtest.Claim{qtest.Rejects("refuse"), qtest.None(qtest.Rejects("place"))}, 350, 0},
		// lost updates: the stock oversold, or a shipment and a cancellation
		// (13 of 100 runs reject one; most others mismatch after place): 25
		// seeds miss it 3% of the time
		{shop.NoLocks, qenv.ReadCommitted, qenv.DefaultRates, []qtest.Claim{qtest.AnyOf(qtest.Rejects("place"), shipOrCancel)}, 0, 25},
		{shop.LockWithoutWrite, qenv.Serializable, crashy, []qtest.Claim{qtest.Conforms()}, 0, 0},
		{shop.LockWithoutWrite, qenv.ReadCommitted, crashy, []qtest.Claim{qtest.Conforms()}, 0, 0},
		// write skew: a shipment and a cancellation both commit (9 of 100,
		// though 3 of the first 50): 70 seeds miss it 0.1% to 1.4% of the
		// time
		{shop.LockWithoutWrite, qenv.SnapshotIsolation, qenv.DefaultRates, []qtest.Claim{shipOrCancel}, 0, 70},
		// a retry after a lost response charges again (15 of 100): 25 seeds
		// miss it 1.7% of the time
		{shop.NoIdempotencyKey, qenv.Serializable, qenv.DefaultRates, []qtest.Claim{qtest.Violates("chargedAtMostOnce")}, 0, 25},
		// a crash between commit and publish loses the order's payment: a
		// one-step window, found with crashes ten times as often, in about
		// one run in fifty (5 and 6 of 300, measured with and without late
		// requests): 420 seeds miss it about once in a thousand; too many
		// for -short
		{shop.DualWrite, qenv.Serializable, crashy, []qtest.Claim{qtest.Violates("settledCorrectly")}, 420, 0},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%d/%s", c.m, c.iso), func(t *testing.T) {
			t.Parallel()
			n := c.seeds
			if n == 0 {
				n = 100
			}
			if testing.Short() {
				if c.short == 0 {
					t.Skip("a rare catch, or a mutant that conforms: the full tests run it")
				}
				n = c.short
			}
			explore(t, checkout.Config{Isolation: c.iso, Mutation: c.m}, c.rates, qtest.ExploreConfig{Seeds: n}).Expect(t, c.want...)
		})
	}
}

// Every run with one fault, not a sample: the faithful shop conforms in
// all 2828, each fault at each point it can happen (a crash at every
// scheduling step among them). DualWrite's one-step window, which seeds
// find only at ten times the crash rate, is among them: the search needs
// no rate.
func TestExhaustiveOneFault(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("thousands of runs")
	}
	system(checkout.Config{}).Exhaust(t, qtest.Bound{Faults: 1, MaxRuns: 5000}).Expect(t, qtest.Conforms(), qtest.NotVacuous())
	system(checkout.Config{Mutation: shop.DualWrite}).Exhaust(t, qtest.Bound{Faults: 1, MaxRuns: 5000}).
		Expect(t, qtest.Violates("settledCorrectly"))
}

// Hegel makes every choice of the environment and shrinks a write skew to
// the interleaving and faults it needs. The shrunk run's choices replay it
// outside Hegel, step for step: a regression test needs no Hegel.
func TestHegelShrinksWriteSkew(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("shrinking against the model")
	}
	sys := system(checkout.Config{Isolation: qenv.SnapshotIsolation, Mutation: shop.LockWithoutWrite})
	skew := qtest.AnyOf(qtest.Rejects("ship"), qtest.Rejects("cancel"))
	// up to 200 cases: a case finds a skew about one time in 27 (at 50
	// cases, 2 of 13 fresh seeds found none, nightly 6's 1506461457257314357
	// among them; both find one within 100), and the search stops at the
	// first
	v := qhegel.Shrink(t, sys.WithRates(crashy), hegel.WithTestCases(200), hegel.WithSeed(1))
	v.Expect(t, skew)

	replay := sys.Explore(t, qtest.ExploreConfig{Seeds: 1, Chooser: func(uint64) qenv.Chooser { return qenv.Replay(v.Choices) }})
	if got, want := qhegel.Render(replay.Runs[0].Steps), qhegel.Render(v.Steps); got != want {
		t.Errorf("the replayed run differs:\n%s\nfrom the shrunk run:\n%s", got, want)
	}
	replay.Expect(t, skew)
}

// Hegel explores the faithful shop under snapshot isolation, in batches.
func TestHegelFaithful(t *testing.T) {
	qtest.RequireQuint(t)
	qhegel.Test(t, qhegel.Config{Binding: binding, Mode: qhegel.Batched}, func(ht *hegel.T, rec *qhegel.Case) {
		if _, err := checkout.Run(rec.Env(qhegel.Choices(ht)), checkout.Config{Isolation: qenv.SnapshotIsolation}); err != nil {
			ht.Fatalf("%v", err)
		}
	}, hegel.WithTestCases(30))
}

// Partitions between the broker, the relay and the consumers: a publish
// waits out a cut, a consumer cut off has its events redelivered
// (measured apart from this test, with every fault but the cuts off and
// crashes off, at nemesis rate 0.05: 20 redeliveries in 16 of 40 runs,
// none without the cuts), and the shop, which already survives
// redelivery, stays correct under every fault: 40 seeds. A conformance
// claim, not a catch: -short leaves it to the full tests, and the faithful
// shop conforms at every level there without the cuts.
func TestFaithfulConformsUnderPartitions(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("conforms under partitions: the full tests run it")
	}
	t.Parallel()
	explore(t, checkout.Config{Isolation: qenv.Serializable, Partitions: true}, crashy, qtest.ExploreConfig{Seeds: 40}).Expect(t, qtest.Conforms(), qtest.NotVacuous())
}

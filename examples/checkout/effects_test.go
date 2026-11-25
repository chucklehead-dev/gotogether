package checkout_test

import (
	"slices"
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/checkout"
	"github.com/chucklehead-dev/gotogether/examples/checkout/shop"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// noEffects is the binding with pay's effects ablated: the checks as they
// were before I/O specifications.
const noEffects = "testdata/noeffects.binding.yaml"

// pays reports whether a run completed a pay step: a run whose orders
// were all cancelled before payment has none, and one whose payments
// consumer crashed during its only payment has it pending, its effects
// attached to the pending call and not compared (DESIGN §112, left for
// P2: the pending-call rule).
func pays(r *qtest.Run) bool {
	return slices.ContainsFunc(r.Recorded, func(s qtrace.Step) bool { return s.Action == "pay" && s.Model() })
}

// failing counts an exploration's runs that fail, for a measure.
func failing(x *qtest.Exploration) (n int) {
	for _, r := range x.Runs {
		if !r.Report.OK() {
			n++
		}
	}
	return n
}

// A capture of the order's units, not its money, changes nothing the
// model's state keeps: without pay's effects compared it conforms in
// every run (the baseline, measured: 40 of 40 at crashy's rates), and
// with them it is Mismatch("pay") in every run that completes a payment.
// The faithful shop conforms under the same binding at every level
// (TestFaithfulConformsAtEveryLevel).
func TestCaptureUnits(t *testing.T) {
	qtest.RequireQuint(t)
	t.Parallel()
	seeds := 40
	if testing.Short() {
		seeds = 8
	}
	cfg := checkout.Config{Isolation: qenv.Serializable, Mutation: shop.CaptureUnits}
	base := system(cfg)
	base.Binding = noEffects
	base.WithRates(crashy).Explore(t, qtest.ExploreConfig{Seeds: seeds}).Expect(t, qtest.Conforms())

	x := explore(t, cfg, crashy, qtest.ExploreConfig{Seeds: seeds})
	paid := 0
	for _, r := range x.Runs {
		if pays(r) {
			paid++
			r.Expect(t, qtest.Mismatch("pay"))
		} else {
			r.Expect(t, qtest.Conforms())
		}
	}
	if paid == 0 {
		t.Fatal("no run paid: the mutant was never exercised")
	}
	t.Logf("captureUnits: %d of %d runs paid, each a mismatch after pay", paid, len(x.Runs))
}

// NoIdempotencyKey's fresh key per try is a payload difference at the
// first retry, whatever made it retry (a lost request, a timeout, a lost
// response), where before it was caught only when a retry charged twice
// (a lost response after the provider's commit). Measured on the same
// seeds with and without pay's effects compared, it is killed in strictly
// more runs (the README's table has the counts).
func TestNoIdempotencyKeyKilledByEffects(t *testing.T) {
	qtest.RequireQuint(t)
	t.Parallel()
	seeds := 100
	if testing.Short() {
		seeds = 25
	}
	cases := []struct {
		name  string
		rates qenv.Rates
	}{
		{"default", qenv.DefaultRates},
		// the prerequisite forced: a lost request, never processed, retried
		// under a fresh key, no crash and no cancellation (whose void would
		// miss the charge): charged once, it conformed before
		{"lostRequests", qenv.NoFaults.Set("rpc.provider.request", 0.3)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			forced := c.name != "default"
			if testing.Short() && !forced {
				t.Skip("the default rates' small difference (50 and 51 of 100) needs the full run's seeds")
			}
			cfg := checkout.Config{Isolation: qenv.Serializable, Mutation: shop.NoIdempotencyKey, NoCrashes: forced, NoCancels: forced}
			base := system(cfg)
			base.Binding = noEffects
			before := base.WithRates(c.rates).Explore(t, qtest.ExploreConfig{Seeds: seeds})
			after := explore(t, cfg, c.rates, qtest.ExploreConfig{Seeds: seeds})
			after.Expect(t, qtest.Some(qtest.Mismatch("pay")))
			b, a := failing(before), failing(after)
			t.Logf("NoIdempotencyKey (%s): killed in %d of %d runs without effects, %d with", c.name, b, seeds, a)
			if a <= b {
				t.Errorf("killed in %d runs with effects compared, %d without: want strictly more", a, b)
			}
		})
	}
}

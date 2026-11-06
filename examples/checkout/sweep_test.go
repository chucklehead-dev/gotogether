package checkout_test

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/checkout"
	"github.com/chucklehead-dev/gotogether/examples/checkout/shop"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qsweep"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// The shop's parameter space: isolation level, orders and stock, and which
// kinds of faults happen at all. 432 combinations in full.
var space = []qsweep.Factor{
	{Name: "iso", Values: []string{"ser", "si", "rc"}},
	qsweep.Ints("orders", 2, 3, 4),
	qsweep.Ints("stock", 1, 2, 3).Constant("STOCK"), // the model's instance follows the row's
	qsweep.Bool("crash"),
	qsweep.FaultKind("queue"), // both queues' lost acknowledgements
	qsweep.FaultKind("rpc.api"),
	qsweep.FaultKind("rpc.provider"),
}

// sweepRates fault each kind that is on three times as often as crashy:
// a pair of kinds on in a row must fault together in some run of it, and
// at crashy's rates a lost acknowledgement and an API fault met in 0 to 2
// runs of a sweep (seeds 1-20 met every pair, 1001-1020 missed three).
// At these, every pair meets in 4 to 7 runs at fresh seeds.
var sweepRates = qenv.Rates{Default: 0.15, Kinds: map[string]float64{"nemesis": 0.05}}

var isoOf = map[string]qenv.Isolation{"ser": qenv.Serializable, "si": qenv.SnapshotIsolation, "rc": qenv.ReadCommitted}

// sweepSystem is the shop with mutation m, deployed as each sweep row says
// (the row's fault kinds that are off are off in its chooser already).
func sweepSystem(m shop.Mutation) qtest.System {
	mutant := ""
	if m != shop.Faithful {
		mutant = m.String()
	}
	return qtest.System{Binding: binding, Mutant: mutant, Run: func(c qenv.Chooser) error {
		row, _ := qtest.RowOf(c)
		cfg := checkout.Config{Isolation: isoOf[row.Get("iso")], Mutation: m,
			Orders: row.Int("orders"), Stock: row.Int("stock"), NoCrashes: !row.On("crash")}
		_, err := checkout.Run(c, cfg)
		return err
	}}
}

// The faithful shop conforms on every pairwise row: every pair of factor
// values, every pair of fault kinds together and each kind alone, at
// every isolation level.
func TestFaithfulSweep(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("sweeps hundreds of runs")
	}
	sw := sweepSystem(shop.Faithful).WithRates(sweepRates).Sweep(t, qtest.SweepConfig{Factors: space, Explore: qtest.ExploreConfig{Seeds: 20}})
	t.Log(sw.Evidence())
	if u := sw.Evidence().Faults.Untried(); len(u) > 0 {
		t.Errorf("fault kinds never together: %v", u)
	}
}

// A fresh idempotency key per attempt is two bugs, and the sweep tells
// them apart. A cancellation's void cannot find the charge in every row,
// faults or none (settledCorrectly). A double charge needs the provider's
// faults: some run of every row with them charges twice, and no run of a
// row without them does.
func TestNoIdempotencyKeySweep(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("sweeps hundreds of runs")
	}
	// the provider's faults at a quarter: since a crash can fall after a
	// commit (qenv.Service's window), the double charge needs a lost
	// provider response at the right moment, in 2 runs of 100 at crashy's
	// rates and 13 of 100 at this one; the rarest rows show theirs in 4
	// runs of 30, which 50 seeds miss about once in a thousand
	sweepSystem(shop.NoIdempotencyKey).WithRates(crashy.Set("rpc.provider", 0.25)).Sweep(t, qtest.SweepConfig{Factors: space, Explore: qtest.ExploreConfig{Seeds: 50},
		Expect: func(row qsweep.Row) []qtest.Claim {
			if row.On("rpc.provider") {
				return []qtest.Claim{qtest.Some(qtest.Violates("chargedAtMostOnce"))}
			}
			return []qtest.Claim{qtest.Some(qtest.Violates("settledCorrectly")), qtest.None(qtest.Violates("chargedAtMostOnce"))}
		}})
}

// Request both directions of each fault pair and check every attempt against
// the model. Some phase orders may remain unreached within the seed budget;
// the evidence reports those bounds rather than calling them impossible.
func TestFaultOrders(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("sweeps fault-order attempts")
	}
	sys := qtest.System{Binding: binding, Run: func(c qenv.Chooser) error {
		_, err := checkout.Run(c, checkout.Config{Isolation: qenv.SnapshotIsolation})
		return err
	}}
	result := sys.FaultOrders(t, qtest.FaultOrderConfig{Seeds: 3, Faults: []qenv.Scripted{
		{Kind: "nemesis.crash.payments", Option: 1},
		{Kind: "queue.payments.ack", Option: 1},
		{Kind: "rpc.api.request", Option: 1},
		{Kind: "rpc.api.response", Option: 1},
		{Kind: "rpc.provider.request", Option: 1},
		{Kind: "rpc.provider.response", Option: 1},
	}})
	t.Log(result.Evidence())
}

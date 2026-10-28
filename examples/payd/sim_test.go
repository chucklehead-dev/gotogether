package harness

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// system is payd under cfg as one System.
func system(cfg Config, opts ...qtest.Option) qtest.System {
	return qtest.System{Binding: "payd.binding.yaml", Rates: faulty, Opts: opts, Mutant: qtest.Mutant(),
		Run: func(c qenv.Chooser) error { return Run(c, cfg) }}
}

// faulty loses, duplicates and delays one request or response in ten (to
// payd and to the processor), refuses connections, breaks database
// connections, loses commits' answers, and crashes payd.
var faulty = qenv.DefaultRates.Set("rpc", 0.1).Set("nemesis", 0.02)

// dups is faulty with a third of payd's requests duplicated or late:
// copies of one attempt that overlap.
var dups = faulty.Set("rpc.payd.request", 0.3).Set("rpc.payd.delay", 0.3)

// Through every fault, at each isolation level (20 seeds each, 2 under
// -short, and the duplicated runs 20, 4 under -short), with the scheduler's and
// the Store's own checks on, payd charges and captures each key once, and
// answers every key it charged with its charge; and the faults happened.
func TestFaithful(t *testing.T) {
	t.Parallel() // its explorations record into their own runs
	qtest.RequireQuint(t)
	for _, iso := range []qenv.Isolation{qenv.ReadCommitted, qenv.SnapshotIsolation, qenv.Serializable} {
		t.Run(iso.String(), func(t *testing.T) {
			system(Config{Isolation: iso, Check: true}).Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 2}).
				Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
		})
	}
	// duplicated and late requests, the faults payd's dedupe is for
	system(Config{Check: true}).WithRates(dups).Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 4}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()),
			qtest.Reaches("rpc.payd.request").Faulted().InAtLeast(0.5),
			qtest.Reaches("nemesis.crash").Faulted().InAtLeast(0.5))
}

// Every run of one charge within one fault: a lost capture response, a
// crash between the claim and the completion, a commit whose answer is
// lost; payd charges it once in each.
func TestOneFault(t *testing.T) {
	t.Parallel() // its explorations record into their own runs
	qtest.RequireQuint(t)
	system(Config{Workload: One}).Exhaust(t, qtest.Bound{Faults: 1}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
}

// twoTabs is Shared without faults: k1 and k3 each sent by two clients
// at once, nothing else.
func twoTabs() qtest.System { return system(Config{}).WithRates(qenv.NoFaults) }

// An operator's edit of a charge after the run (a refund written over
// it) reaches the model through the change feed: a charge changes.
func TestChargeEdited(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("one run, but a violation's report costs a quint run of its own")
	}
	system(Config{Workload: One, EditCharge: true}).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1}).
		Expect(t, qtest.All(qtest.Violates("chargesImmutable")))
}

// TestMutant states what catches the mutant this binary was built with;
// TestMutants builds and runs it once per mutant. Each claim's rate was
// measured on 2026-10-26 (README); its seeds give a miss chance under 5%.
// Under -short each mutant states its cheapest claim (the others run in
// the full tests and nightly).
func TestMutant(t *testing.T) {
	mutant := qtest.Mutant()
	if mutant == "" {
		t.Skip("payd as it is: TestMutants runs this test built with each mutant's overlay")
	}
	qtest.RequireQuint(t)
	switch mutant {
	case "precheck":
		// two tabs sending one key at once, no faults at all: both find no
		// charge and both charge (40 of 40 runs); the processor, given the
		// key, captures once
		twoTabs().Explore(t, qtest.ExploreConfig{Seeds: 6, ShortSeeds: 1}).
			Expect(t, qtest.Some(qtest.Violates("noDoubleCharge")), qtest.None(qtest.Violates("noDoubleCapture")))
		// one client, half its requests duplicated: the two copies overlap
		// (22 of 60 runs; 20 seeds miss with chance 0.01%)
		if !testing.Short() {
			system(Config{Workload: One}).WithRates(qenv.NoFaults.Set("rpc.payd.request", 0.5)).
				Explore(t, qtest.ExploreConfig{Seeds: 20}).
				Expect(t, qtest.Some(qtest.Violates("noDoubleCharge")))
		}
		// and for real, on SQLite, three tabs at once (15 of 15 runs)
		if !testing.Short() {
			realMutant(t, 16, "noDoubleCharge")
		}
	case "noForwardKey":
		// one fault is enough: a capture's answer lost, payd's retry
		// (its client's) captures again (30 of 76 runs)
		system(Config{Workload: One}).Exhaust(t, qtest.Bound{Faults: 1}).
			Expect(t, qtest.Some(qtest.Violates("noDoubleCapture")), qtest.None(qtest.Violates("noDoubleCharge")))
		// for real, without a fault: two tabs' requests for one key both
		// drive it, and both capture (15 of 15 runs)
		if !testing.Short() {
			realMutant(t, 16, "noDoubleCapture")
		}
	case "pendingConflict":
		// one fault is enough: a capture's answer lost, payd answers 502,
		// its client's retry finds the key pending and is refused; the
		// processor captured: 29 of 78 runs report the refused key
		// captured, 5 more that after the settle it never had a success
		if !testing.Short() {
			system(Config{Workload: One}).Exhaust(t, qtest.Bound{Faults: 1}).
				Expect(t, qtest.Some(qtest.Violates("noRefusalOfCharged")), qtest.Some(qtest.Violates(validate.EventuallyName("allAnswered"))),
					qtest.None(qtest.Violates("noDoubleCharge")), qtest.None(qtest.Violates("noDoubleCapture")))
		}
		// two tabs: the second is told no while the first charges (40 of 40)
		twoTabs().Explore(t, qtest.ExploreConfig{Seeds: 6, ShortSeeds: 1}).Expect(t, qtest.Some(qtest.Violates("noRefusalOfCharged")))
		if !testing.Short() {
			realMutant(t, 16, "noRefusalOfCharged") // 15 of 15
		}
	case "debitFirst":
		// two tabs: the attempt that loses the compare-and-set has debited
		// already (40 of 40); the balance shows it after the settle
		twoTabs().Explore(t, qtest.ExploreConfig{Seeds: 6, ShortSeeds: 1}).
			Expect(t, qtest.Some(qtest.Violates("noDebitWithoutCharge")), qtest.Some(qtest.Violates("balanceMatchesCharges")),
				qtest.None(qtest.Violates("noDoubleCharge")))
		// for real, where no debit is seen, the balances read after the
		// run show it (15 of 15)
		if !testing.Short() {
			realMutant(t, 16, "balanceMatchesCharges")
		}
	case "lostRace":
		// two tabs: the attempt that loses the compare-and-set answers 409
		// while its key is charged (40 of 40)
		twoTabs().Explore(t, qtest.ExploreConfig{Seeds: 6, ShortSeeds: 1}).
			Expect(t, qtest.Some(qtest.Violates("noRefusalOfCharged")), qtest.None(qtest.Violates("noDoubleCharge")))
		if !testing.Short() {
			realMutant(t, 16, "noRefusalOfCharged") // 15 of 15
		}
	case "centsInBody":
		// any successful charge: its response names the key's charge by
		// id, with another amount (every run)
		system(Config{Workload: One}).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1}).
			Expect(t, qtest.All(qtest.Violates("responseMatchesCharge")))
	default:
		t.Fatalf("mutant %s: say what catches it (a case of TestMutant)", mutant)
	}
}

package harness

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// mutant is the mutant this test binary was built with: set by the file a
// mutant's overlay adds (TestMutants), "" for payd as it is.
var mutant string

// system is payd under cfg as one System.
func system(cfg Config, opts ...qtest.Option) qtest.System {
	return qtest.System{Binding: "payd.binding.yaml", Rates: faulty, Opts: opts, Mutant: mutant,
		Run: func(c qenv.Chooser) error { return Run(c, cfg) }}
}

// seeds is n, or short under -short.
func seeds(n, short int) int {
	if testing.Short() {
		return short
	}
	return n
}

// faulty loses, duplicates and delays one request or response in ten (to
// payd and to the processor), refuses connections, breaks database
// connections, loses commits' answers, and crashes payd.
var faulty = qenv.DefaultRates.Set("rpc", 0.1).Set("nemesis", 0.02)

// dups is faulty with a third of payd's requests duplicated or late:
// copies of one attempt that overlap.
var dups = faulty.Set("rpc.payd.request", 0.3).Set("rpc.payd.delay", 0.3)

// Through every fault, at each isolation level, with the scheduler's and
// the Store's own checks on, payd charges and captures each key once, and
// answers every key it charged with its charge; and the faults happened.
func TestFaithful(t *testing.T) {
	t.Parallel() // its explorations record into their own runs
	qtest.RequireQuint(t)
	for _, iso := range []qenv.Isolation{qenv.ReadCommitted, qenv.SnapshotIsolation, qenv.Serializable} {
		t.Run(iso.String(), func(t *testing.T) {
			system(Config{Isolation: iso, Check: true}).Explore(t, qtest.ExploreConfig{Seeds: seeds(20, 4)}).
				Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
		})
	}
	// duplicated and late requests, the faults payd's dedupe is for
	system(Config{Check: true}).WithRates(dups).Explore(t, qtest.ExploreConfig{Seeds: seeds(20, 4)}).
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

// TestMutant states what catches the mutant this binary was built with;
// TestMutants builds and runs it once per mutant.
func TestMutant(t *testing.T) {
	switch mutant {
	case "":
		t.Skip("payd as it is: TestMutants runs this test built with each mutant's overlay")
	case "precheck":
		qtest.RequireQuint(t)
		// one charge within one fault: a duplicated request's two copies
		// overlap, both find no charge, both charge (the processor, given
		// the key, captures once)
		system(Config{Workload: One}).Exhaust(t, qtest.Bound{Faults: 1}).
			Expect(t, qtest.Some(qtest.Violates("noDoubleCharge")), qtest.None(qtest.Violates("noDoubleCapture")))
		// two tabs sending one key at once, no faults at all: every run
		system(Config{}).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(10, 2)}).
			Expect(t, qtest.All(qtest.Violates("noDoubleCharge")))
		// and for real, on SQLite under concurrent load
		TestRealPrecheck(t)
	case "noForwardKey":
		qtest.RequireQuint(t)
		// one fault is enough: a capture's response lost, payd's retry
		// (its client's) captures again
		system(Config{Workload: One}).Exhaust(t, qtest.Bound{Faults: 1}).
			Expect(t, qtest.Some(qtest.Violates("noDoubleCapture")), qtest.None(qtest.Violates("noDoubleCharge")))
		// and for real, without a fault: two tabs' requests for one key
		// both drive it, and both capture
		run, _ := realRun(t, 10, 3)
		run.Expect(t, qtest.Violates("noDoubleCapture"))
	case "pendingConflict":
		qtest.RequireQuint(t)
		// one fault is enough: a capture's answer lost, payd answers 502,
		// its client's retry finds the key pending and is refused; the
		// processor captured, and after the settle the key has no charge
		system(Config{Workload: One}).Exhaust(t, qtest.Bound{Faults: 1}).
			Expect(t, qtest.Some(qtest.Violates(validate.EventuallyName("capturedCharged"))),
				qtest.None(qtest.Violates("noDoubleCharge")), qtest.None(qtest.Violates("noDoubleCapture")))
	default:
		t.Fatalf("unknown mutant %q", mutant)
	}
}

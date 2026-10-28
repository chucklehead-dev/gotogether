package hello

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qtest"
	"hegel.dev/go/hegel"
)

// system is the counter and its client as one System: explored by seeds
// here, and by Hegel, sweeps or the fuzzer the same way. In a mutant's
// binary it names the mutant: its runs are not the counter's, and the
// suite's evidence leaves them out.
func system() qtest.System {
	return qtest.System{Binding: "hello.binding.yaml", Run: Run, Mutant: qtest.Mutant()}
}

// lossy loses or duplicates one message in five.
var lossy = qenv.Rates{Default: 0.2}

// retried is the acceptance criterion in the model's actions: request 1
// applied, then seen again (its retry after a lost response, or a
// duplicate) and recognised as a replay, then the client done.
var retried = qtest.NamedScenario("a retry recognised",
	qtest.Takes("apply").With("id", "1"), qtest.Takes("replay").With("id", "1"), qtest.Takes("done"))

// The model on its own can follow the scenario, before any run: a model
// whose replay needed something no run does would fail here, with the
// search's extent, where every run would still conform to it.
func TestRetryPossible(t *testing.T) {
	qtest.Possible(t, "hello.binding.yaml", retried)
}

// Through lost requests, lost responses and duplicates, every request
// counts once.
func TestFaithfulConforms(t *testing.T) {
	qtest.RequireQuint(t)
	system().WithRates(lossy).Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, qtest.Conforms(),
		// and the faults happened: a pass with no duplicates says nothing of
		// dedupe. Measured, 56% of runs fault a request (2000 seeds); the bar
		// is well under it, so a window of fresh seeds (a nightly run) does
		// not fail it by chance, and a change that stops the faults does
		qtest.Reaches("rpc.counter.request").Faulted().InAtLeast(0.3),
		// and every model action was taken (a replay of a seen request),
		// every fault kind faulted: nothing the model allows went untried
		qtest.Exercised(),
		// and some run met a retry of its first request and recognised it
		qtest.Some(retried))
}

// Every run within two delays and two faults, not a sample: the faithful
// counter conforms in all 4430, late requests among them (the Go
// evaluator judges them, quint a sample).
func TestExhaustive(t *testing.T) {
	qtest.RequireQuint(t)
	system().Exhaust(t, qtest.Bound{Delays: 2, Faults: 2, MaxRuns: 5000}).Expect(t, qtest.Conforms())
}

// Hegel makes every choice of the same System; in the faithful counter,
// at lossy's rates too, it finds nothing (noDedupe's shrink is
// TestMutant's).
func TestHegelFindsNothing(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("shrinks against the model")
	}
	qhegel.Shrink(t, system().WithRates(lossy), hegel.WithTestCases(20)).Expect(t, qtest.Conforms())
}

// TestMutant states what catches the mutant this binary was built with
// (testdata/mutants/<name>: files replacing the package's of the same
// name, the way to plant a bug by hand); TestMutants builds and runs it
// once per mutant.
func TestMutant(t *testing.T) {
	switch qtest.Mutant() {
	case "":
		t.Skip("the counter as it is: TestMutants runs this test in each mutant's binary")
	case "noDedupe":
		// Without dedupe a retried request is counted again: the model has
		// no apply of a request already applied, so some run's apply is
		// rejected. With no faults there is no retry, and the bug is
		// invisible.
		qtest.RequireQuint(t)
		system().WithRates(lossy).Explore(t, qtest.ExploreConfig{Seeds: 30}).
			Expect(t, qtest.Some(qtest.Rejects("apply")), qtest.Some(qtest.Conforms()),
				// it never recognises a retry: no run follows the scenario
				qtest.None(retried))
		system().WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 5}).Expect(t, qtest.Conforms())
		// the bug needs one fault, which the search of every single fault
		// finds
		system().Exhaust(t, qtest.Bound{Faults: 1}).Expect(t, qtest.Some(qtest.Rejects("apply")))
		if testing.Short() {
			return
		}
		// Hegel makes every choice and shrinks the failure to the one fault
		// it needs (a duplicated request, or a lost response and its
		// retry). It draws at the default rates. Hegel's shrinking alone
		// stopped at two faults for 3 seeds of 10 (a lost response and a
		// dropped request, where the response is enough); Shrink's own pass
		// then removes each fault the failure does not need, and every seed
		// tried ends at one.
		qhegel.Shrink(t, system(), hegel.WithTestCases(50), hegel.WithSeed(1)).
			Expect(t, qtest.Rejects("apply"), qtest.ShrunkTo(1))
	default:
		t.Fatalf("mutant %s: say what catches it", qtest.Mutant())
	}
}

// Each mutant of testdata/mutants, built with its overlay, is caught as
// TestMutant says.
func TestMutants(t *testing.T) {
	t.Parallel() // each mutant's binary is a process of its own
	qtest.Mutants{}.Run(t)
}

package hello

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qtest"
	"hegel.dev/go/hegel"
)

// system is the counter and its client, with mutation m, as one System:
// explored by seeds here, and by Hegel, sweeps or the fuzzer the same way.
func system(m Mutation) qtest.System {
	return qtest.System{Binding: "hello.binding.yaml", Run: func(c qenv.Chooser) error { return Run(c, m) }}
}

// lossy loses or duplicates one message in five.
var lossy = qenv.Rates{Default: 0.2}

// Through lost requests, lost responses and duplicates, every request
// counts once.
func TestFaithfulConforms(t *testing.T) {
	qtest.RequireQuint(t)
	system(Faithful).Explore(t, qtest.ExploreConfig{Seeds: 30, Rates: lossy}).Expect(t, qtest.Conforms(),
		// and the faults happened: a pass with no duplicates says nothing of
		// dedupe. Measured, 56% of runs fault a request (2000 seeds); the bar
		// is well under it, so a window of fresh seeds (a nightly run) does
		// not fail it by chance, and a change that stops the faults does
		qtest.Reaches("rpc.counter.request").Faulted().InAtLeast(0.3),
		// and every model action was taken (a replay of a seen request),
		// every fault kind faulted: nothing the model allows went untried
		qtest.Exercised())
}

// Without dedupe a retried request is counted again: the model has no
// apply of a request already applied, so some run's apply is rejected.
// With no faults there is no retry, and the bug is invisible.
func TestNoDedupeCountsRetries(t *testing.T) {
	qtest.RequireQuint(t)
	system(NoDedupe).Explore(t, qtest.ExploreConfig{Seeds: 30, Rates: lossy}).
		Expect(t, qtest.Some(qtest.Rejects("apply")), qtest.Some(qtest.Conforms()))
	system(NoDedupe).Explore(t, qtest.ExploreConfig{Seeds: 5, Rates: qenv.NoFaults}).Expect(t, qtest.Conforms())
}

// Every run within two delays and two faults, not a sample: the faithful
// counter conforms in all 921 (quint checks 21: the rest repeat their
// steps), and NoDedupe's bug needs one fault, which the search of every
// single fault finds.
func TestExhaustive(t *testing.T) {
	qtest.RequireQuint(t)
	system(Faithful).Exhaust(t, qtest.Bound{Delays: 2, Faults: 2}).Expect(t, qtest.Conforms(), qtest.Complete())
	system(NoDedupe).Exhaust(t, qtest.Bound{Faults: 1}).Expect(t, qtest.Rejects("apply"), qtest.Complete())
}

// Hegel makes every choice of the same System and shrinks NoDedupe's
// failure to the one fault it needs (a duplicated request, or a lost
// response and its retry); in the faithful counter it finds nothing. It
// draws at the default rates. Hegel's shrinking alone stopped at two
// faults for 3 seeds of 10 (a lost response and a dropped request, where
// the response is enough); Shrink's own pass then removes each fault the
// failure does not need, and every seed tried ends at one.
func TestHegelShrinks(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("shrinks against the model")
	}
	qhegel.Shrink(t, system(NoDedupe), qenv.DefaultRates, hegel.WithTestCases(50), hegel.WithSeed(1)).
		Expect(t, qtest.Rejects("apply"), qtest.ShrunkTo(1))
	qhegel.Shrink(t, system(Faithful), lossy, hegel.WithTestCases(20)).Expect(t, qtest.Conforms()) // nothing to find, at lossy's rates too
}

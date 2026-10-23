package checkout_test

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/checkout"
	"github.com/chucklehead-dev/gotogether/examples/checkout/shop"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qtest"
	"hegel.dev/go/hegel"
)

// The faithful shop under Go's fuzzer: go test runs its corpus (every
// choice nominal, and what testdata/fuzz holds); go test -fuzz
// FuzzFaithful explores schedules and faults guided by coverage, the model
// judging every run.
func FuzzFaithful(f *testing.F) {
	qtest.RequireQuint(f)
	system(checkout.Config{Isolation: qenv.SnapshotIsolation}).WithRates(crashy).Fuzz(f)
}

// A shrunk Hegel failure becomes a fuzz corpus entry: its choices encode to
// bytes from which the fuzzer's chooser makes the same run, rejected the
// same way.
func TestShrunkFailureAsCorpus(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("shrinking against the model")
	}
	sys := system(checkout.Config{Isolation: qenv.SnapshotIsolation, Mutation: shop.LockWithoutWrite})
	v := qhegel.Shrink(t, sys, hegel.WithTestCases(50), hegel.WithSeed(1))
	data, err := qenv.EncodeChoices(v.Choices, qenv.DefaultRates)
	if err != nil {
		t.Fatal(err)
	}
	x := sys.Explore(t, qtest.ExploreConfig{Seeds: 1, Chooser: func(uint64) qenv.Chooser { return qenv.FromBytes(data, qenv.DefaultRates) }})
	if got, want := qhegel.Render(x.Runs[0].Steps), qhegel.Render(v.Steps); got != want {
		t.Errorf("the corpus entry runs differently:\n%s\nfrom the shrunk run:\n%s", got, want)
	}
	x.Expect(t, qtest.AnyOf(qtest.Rejects("ship"), qtest.Rejects("cancel")))
	t.Logf("corpus entry: %d bytes", len(data))
}

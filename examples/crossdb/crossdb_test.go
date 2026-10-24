package crossdb_test

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/crossdb"
	"github.com/chucklehead-dev/gotogether/examples/crossdb/provider"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qtest"
	"hegel.dev/go/hegel"
)

const binding = "crossdb.binding.yaml"

func explore(t *testing.T, cfg crossdb.Config, rates qenv.Rates, x qtest.ExploreConfig) *qtest.Exploration {
	return qtest.System{Binding: binding, Rates: rates, Run: func(c qenv.Chooser) error { return crossdb.Run(c, cfg) }}.Explore(t, x)
}

// crashy crashes the provider at one scheduling step in fifty.
var crashy = qenv.Rates{Default: 0.05, Kinds: map[string]float64{"nemesis": 0.02}}

// Through lost and duplicated requests, lost responses, throttling, an
// eventually consistent cloud and provider crashes, the faithful provider
// makes one database per resource, deletes them all, and writes secrets
// that open them.
func TestFaithfulConforms(t *testing.T) {
	qtest.RequireQuint(t)
	explore(t, crossdb.Config{}, crashy, qtest.ExploreConfig{Seeds: 60}).Expect(t, qtest.Conforms(), qtest.NotVacuous())
}

// A cloud that applies a request however late it arrives does not leak
// either: the provider waits out its creation grace (30 s from a create's
// start, whatever its result) before it believes a deleted resource's
// database absent, longer than a request is ever delayed here (a timeout
// or two). This test once claimed a leak, which was the simulated kube
// API's stamp translation (TestStampsSurviveConflictRetry): the stamps
// came out months old and the grace over.
func TestLateCreateCoveredByCreationGrace(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("300 runs")
	}
	explore(t, crossdb.Config{NoRequestExpiry: true}, crashy, qtest.ExploreConfig{Seeds: 300}).Expect(t, qtest.Conforms())
}

// The cloud's ID as the external name. Correct while nothing fails. A
// create whose response is lost is marked failed (by Crossplane's
// contract, an error from Create is a failure); if the resource is deleted
// before the retry, there is no name to look up, the finalizer comes off,
// and the database leaks. A crash between the create and saving the name
// is worse: Crossplane refuses to go on.
func TestGeneratedExternalName(t *testing.T) {
	qtest.RequireQuint(t)
	noFaults := qenv.NoFaults
	explore(t, crossdb.Config{Mutation: provider.GeneratedExternalName, NoCrashes: true}, noFaults, qtest.ExploreConfig{Seeds: 20}).
		Expect(t, qtest.Conforms())
	// in about one run in seven (6 of 40): 50 seeds miss it about once in
	// a thousand
	explore(t, crossdb.Config{Mutation: provider.GeneratedExternalName, NoCrashes: true}, qenv.DefaultRates, qtest.ExploreConfig{Seeds: 50}).
		Expect(t, qtest.Violates("settledCorrectly"))
	// crashes alone: an incomplete create, and a resource that never gets ready
	crashesOnly := qenv.Rates{Kinds: map[string]float64{"nemesis": 0.05}}
	explore(t, crossdb.Config{Mutation: provider.GeneratedExternalName}, crashesOnly, qtest.ExploreConfig{Seeds: 40}).
		Expect(t, qtest.Violates("settledCorrectly"))
}

// Without a client token, a create whose response was lost is retried as a
// failure: a second database.
func TestNoClientTokenDuplicates(t *testing.T) {
	qtest.RequireQuint(t)
	// in about one run in ten (4 of 40): 70 seeds miss it about once in a
	// thousand
	explore(t, crossdb.Config{Mutation: provider.NoClientToken, NoCrashes: true}, qenv.DefaultRates, qtest.ExploreConfig{Seeds: 70}).
		Expect(t, qtest.Violates("oneDatabasePerResource"))
}

// A password generated in Create and published only with the creation's
// result: a lost response (the retry's new password is ignored by the
// idempotent cloud) leaves a secret that does not open the database.
func TestPasswordFromCreate(t *testing.T) {
	qtest.RequireQuint(t)
	lossy := qenv.Rates{Default: 0.05, Kinds: map[string]float64{"rpc.dbaas.response": 0.3}}
	// in about one run in seven (6 of 40): 50 seeds
	explore(t, crossdb.Config{Mutation: provider.PasswordFromCreate, NoCrashes: true}, lossy, qtest.ExploreConfig{Seeds: 50}).
		Expect(t, qtest.Violates("settledCorrectly"))
}

// Hegel shrinks the duplicate to the fault it needs: one lost response.
func TestHegelShrinksDuplicate(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("shrinking against the model")
	}
	qhegel.FindViolation(t, qhegel.ViolationConfig{Binding: binding, Opts: []qtest.Option{qtest.Global()}}, func(tc hegel.TestCase, _ *qhegel.Case) {
		if err := crossdb.Run(qhegel.Choices(tc), crossdb.Config{Mutation: provider.NoClientToken, NoCrashes: true, Resources: 2}); err != nil {
			tc.Errorf("%v", err)
		}
	}, hegel.WithTestCases(50), hegel.WithSeed(1))
}

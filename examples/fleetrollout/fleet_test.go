package fleetrollout_test

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/fleetrollout"
	"github.com/chucklehead-dev/gotogether/examples/fleetrollout/rollout"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qtest"
	"hegel.dev/go/hegel"
)

const binding = "fleet.binding.yaml"

// system is the controller in a simulated fleet, configured as cfg.
func system(cfg fleetrollout.Config) qtest.System {
	return qtest.System{Binding: binding, Run: func(c qenv.Chooser) error {
		_, err := fleetrollout.Run(c, cfg)
		return err
	}}
}

func explore(t *testing.T, cfg fleetrollout.Config, runs qtest.ExploreConfig) *qtest.Exploration {
	return system(cfg).Explore(t, runs)
}

// The faithful controller keeps the budget and finishes the rollout,
// through lost requests and responses, status lag and its own crashes.
func TestFaithfulConforms(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	explore(t, fleetrollout.Config{}, qtest.ExploreConfig{Seeds: 30, ShortSeeds: 3}).Expect(t, qtest.Conforms(), qtest.NotVacuous())
}

// Trusting a status written for the previous generation: the next
// cluster is patched while the last one has barely started (17 of 20
// runs; -short runs 3).
func TestIgnoreObservedGenerationOverBudget(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	explore(t, fleetrollout.Config{Mutation: rollout.IgnoreObservedGeneration, NoCrashes: true}, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 3}).
		Expect(t, qtest.Violates("withinBudget"))
}

// Progress kept in memory: correct while nothing fails. A restart forgets
// it, and so does a patch whose response was lost (applied, but the
// controller never knew): either puts the rollout over budget. -short
// runs 2, 2 and 26 seeds.
func TestInMemoryProgress(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	noKubeFaults := qenv.Rates{Default: 0.05, Kinds: map[string]float64{"kube": 0}}
	system(fleetrollout.Config{Mutation: rollout.InMemoryProgress, NoCrashes: true}).WithRates(noKubeFaults).
		Explore(t, qtest.ExploreConfig{Seeds: 15, ShortSeeds: 2}).Expect(t, qtest.Conforms())
	system(fleetrollout.Config{Mutation: rollout.InMemoryProgress}).WithRates(noKubeFaults).
		Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 2}).Expect(t, qtest.Violates("withinBudget"))
	// a lost patch response alone puts it over budget in 6.5% of runs at
	// the default rates (20 seeds missed it at fresh seeds a quarter of
	// the time), 13.5% with the API's faults at 0.15: 40 seeds miss it
	// with probability 0.3%; 26 (-short) miss it 2.3% of the time at the
	// 13.6% of seeds 1 to 140, 4.8% at the 11% of seeds 41 to 140 alone
	system(fleetrollout.Config{Mutation: rollout.InMemoryProgress, NoCrashes: true}).WithRates(qenv.DefaultRates.Set("kube", 0.15)).
		Explore(t, qtest.ExploreConfig{Seeds: 40, ShortSeeds: 26}).Expect(t, qtest.Violates("withinBudget"))
}

// Hegel shrinks the restart bug to the faults it needs: a crash.
func TestHegelShrinksInMemoryProgress(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("shrinking against the model")
	}
	qhegel.FindViolation(t, qhegel.ViolationConfig{Binding: binding, Opts: []qtest.Option{qtest.Global()}}, func(tc hegel.TestCase, _ *qhegel.Case) {
		c := qhegel.Choices(tc)
		c.Rates.Kinds = map[string]float64{"nemesis": 0.05}
		if _, err := fleetrollout.Run(c, fleetrollout.Config{Mutation: rollout.InMemoryProgress}); err != nil {
			tc.Errorf("%v", err)
		}
	}, hegel.WithTestCases(50), hegel.WithSeed(1))
}

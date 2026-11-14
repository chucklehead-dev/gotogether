package qload_test

import (
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qload"
)

// Runs calls a Target for seeds 1..n in parallel; OfSimulation's runs
// must each be the seed's own simulation, the same as one run alone.
// OfSimulation once set the seed on the configuration all its calls
// share, so parallel runs took each other's seeds: a batch could run one
// seed twice and another never, and a trigger search (qhegel.FindTrigger)
// saw one trigger judged two ways (Hegel's "nondeterministic failure").
// Run it with -race too.
func TestOfSimulationRunsAreTheirSeeds(t *testing.T) {
	t.Parallel()
	cfg := example(9.5)
	cfg.Client.Rate = qload.Constant(9.5)
	cfg.Horizon = 400 * time.Second
	tr := qload.Trigger{Start: 100 * time.Second, Duration: 60 * time.Second, ExtraRate: 4}
	const n = 8
	within := 120 * time.Second
	rs, err := qload.Runs(qload.OfSimulation(cfg, 9.5, within), tr, n)
	if err != nil {
		t.Fatal(err)
	}
	distinct := map[float64]bool{}
	for i, r := range rs {
		alone := cfg
		alone.Seed = uint64(i + 1)
		want := qload.Recovers(alone, 9.5, tr, within)
		if r.Recovered != want.Recovered || r.Before != want.Before || r.After != want.After {
			t.Errorf("seed %d in the batch: recovered %v, goodput %.4f before, %.4f after; alone: %v, %.4f, %.4f",
				i+1, r.Recovered, r.Before, r.After, want.Recovered, want.Before, want.After)
		}
		distinct[r.Before] = true
	}
	// the seeds are different simulations: were they one seed's, every
	// run's goodput would be the same
	if len(distinct) < 2 {
		t.Fatalf("%d seeds, one goodput before the trigger: the runs are not their seeds' own", n)
	}
}

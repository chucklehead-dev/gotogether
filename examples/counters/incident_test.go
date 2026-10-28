package counters

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// callsPerKey is the most calls of action any one key had in a run of x
// (a call's completed step; its invocation record aside).
func callsPerKey(x *qtest.Exploration, action string) int {
	most := 0
	for _, r := range x.Runs {
		n := map[any]int{}
		for _, s := range r.Steps {
			if s.Action == action && !s.Invocation {
				n[s.Args["key"]]++
				most = max(most, n[s.Args["key"]])
			}
		}
	}
	return most
}

// rewound reports whether every run of x recorded a rewind
// (gristRewound), judged or not (a run whose validation stopped at an
// earlier rejection does not take it).
func rewound(x *qtest.Exploration) bool {
	for _, r := range x.Runs {
		seen := false
		for _, s := range r.Steps {
			seen = seen || s.Action == "gristRewound"
		}
		if !seen {
			return false
		}
	}
	return len(x.Runs) > 0
}

// TestRewindFailsClosed: GR10 slice 3's at-most-once acceptance. The
// store loses its last two commits right after its sixth, the payout's
// checkpoint among them, after the bank was paid: every run's node sees
// its log go back (gristRewound) and stops the tenant's processors
// (grist.ErrRewound), no operator resuming it here. The payout's trigger,
// back in the log after its checkpoint, is not paid again: one call of
// its key, and no run rejects the payout's call (its lost outcome,
// everyPayoutOutcomeLogged, is the tail's loss). With the fail-closed
// ablated (gristsim.Managed.FailOpen: the processors go on after the
// rewind, as a grist without ErrRewound would) the payout runs again
// unasked: two calls of one key (the bank, Keyed, replays the second),
// some run rejecting the second at its caller step (bankPay: a
// processor's step while the log is rewound; another, the welcome's).
func TestRewindFailsClosed(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	runs := qtest.ExploreConfig{Seeds: 3, ShortSeeds: 2}
	cfg := payOnce
	cfg.Store = &gristsim.Managed{LoseAfter: 6, Lose: 2}
	closed := system(exactly, cfg).WithRates(qenv.NoFaults).Explore(t, runs)
	closed.Expect(t, qtest.None(qtest.Rejects("bankPay")))
	if n := callsPerKey(closed, "bankPay"); n != 1 || !rewound(closed) {
		t.Errorf("fail closed: a payout key called %d times in a run (want once), every run rewound: %v", n, rewound(closed))
	}
	open := cfg
	open.Store = &gristsim.Managed{LoseAfter: 6, Lose: 2, FailOpen: true}
	ablated := system(exactly, open).WithRates(qenv.NoFaults).Explore(t, runs)
	ablated.Expect(t, qtest.Some(qtest.Rejects("bankPay")))
	if n := callsPerKey(ablated, "bankPay"); n != 2 {
		t.Errorf("fail open: a payout key called at most %d times in a run, want twice", n)
	}
}

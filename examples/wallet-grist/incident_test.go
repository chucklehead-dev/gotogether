package walletgrist

import (
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// lost is deposits whose store loses its last commit right after its
// k-th (gristsim.Managed.LoseAfter: a restore from a backup taken just
// before it, or a failover's lost tail), the operator resuming the
// tenant 5 s after the rewind (grist.Resume), the history checked.
func lost(k int) gristsim.Config {
	cfg := deposits
	cfg.Check = true
	cfg.Store = &gristsim.Managed{LoseAfter: k, Lose: 1, Resume: 5 * time.Second}
	return cfg
}

// TestRestoreReplays: GR10 slice 3's acceptance. A restore that loses a
// post's checkpoint commit, which the node's relay had recorded (the
// 4th and the 6th commit of every seed here): the relay sees the step log
// go back and records it (gristRewound), the processors stop (fail
// closed, grist.ErrRewound) until the operator resumes the tenant
// (gristResumed), and the post runs again for the move under its effect
// key, which the Keyed ledger answers from its record (ledgerPostReplayed)
// instead of posting twice. Both sides conform and settle. The
// ledgerNoDedupe mutant posts again there (TestMutant).
func TestRestoreReplays(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	runs := qtest.ExploreConfig{Seeds: 3, ShortSeeds: 1}
	for _, k := range []int{4, 6} {
		system(commits, lost(k)).WithRates(qenv.NoFaults).Explore(t, runs).
			Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Takes("gristRewound").InAtLeast(1),
				qtest.Takes("gristResumed").InAtLeast(1), qtest.Takes("ledgerPostReplayed").InAtLeast(1))
		system(calls, lost(k)).WithRates(qenv.NoFaults).Explore(t, runs).Expect(t, qtest.Conforms())
	}
}

// TestLostTail: a lost commit is named, never passed in silence. A
// deposit acknowledged and then lost before any node read it (the 2nd
// commit, every seed) leaves the store's side nothing to judge (its steps
// are the history that remains, and conform), and the clients' side a
// receipt with no commit: a later read of the balance misses it. (A
// commit a node had seen is named at the store's: gristRewound, or, for
// one its relay had not recorded yet, the first step that followed it,
// rejected.)
func TestLostTail(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	runs := qtest.ExploreConfig{Seeds: 3, ShortSeeds: 1}
	system(commits, lost(2)).WithRates(qenv.NoFaults).Explore(t, runs).Expect(t, qtest.Conforms())
	system(calls, lost(2)).WithRates(qenv.NoFaults).Explore(t, runs).Expect(t, qtest.All(qtest.Mismatch("read")))
}

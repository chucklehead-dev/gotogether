package counters

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// The SQL tier: claim 2 again with the app on grist/sqlstore over qsql
// (gristsim.Config.SQL), the statements the real tier runs on PostgreSQL
// and SQLite, interleaved at each isolation level, with commit faults and
// node crashes. One run is judged by both bindings: the store's steps
// (commits relayed from the step log) and the clients' calls, linearized.
// Under Serializable a lost race for the head lock is qsql's 40001,
// retried from the key's marker. Under -short (every push) ReadCommitted
// alone: the head lock's wait and both bindings over one run;
// Serializable's 40001 is full mode's (and the contract's, on qsql at
// every level, in grist/sqlstore).
func TestSQLTier(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	levels := []qenv.Isolation{qenv.Serializable, qenv.ReadCommitted}
	if testing.Short() {
		levels = levels[1:]
	}
	for _, iso := range levels {
		t.Run(iso.String(), func(t *testing.T) {
			cfg := contended
			cfg.Isolation, cfg.SQL = iso, true
			// a run commits less than on simstore (every statement is a
			// scheduling point, so the nemesis crashes the node more often:
			// about ten commits a run): their fault at 15 in 100, so 3
			// seeds miss it with chance under 1%
			sys := system(commits, cfg).WithRates(faulty.Set("store.counters.commit", 0.15))
			n := qtest.ExploreConfig{Seeds: seeds(20, 3)}
			sys.Explore(t, n).
				Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()),
					qtest.Reaches("store.counters.commit").Faulted(), qtest.Reaches("nemesis.crash").Faulted())
			sys.WithBinding(calls, qtest.Linearize()).Explore(t, n).
				Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
		})
	}
}

// Duplicated and late requests on the SQL tier: a key's first answer is
// final there too, at the store and at the clients. Without grist's
// markers 20 of 20 runs fail at the store, without refusal markers 16 of
// 20 (3 seeds miss with chance under 1%). Full mode: the markers'
// mechanism is the same code on every tier, checked per push by
// TestDuplicateRequests on simstore.
func TestSQLDuplicateRequests(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("full mode: TestDuplicateRequests checks the markers per push")
	}
	qtest.RequireQuint(t)
	rates := qenv.NoFaults.Set("rpc.api.delay", 0.3).Set("rpc.api.request", 0.3)
	cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 3, Ops: 4, Alphabet: []string{"a", "b"}, SQL: true,
		Send: []string{"create-counter", "increment-counter", "show-counter"}}
	sys := system(commits, cfg).WithRates(rates)
	n := qtest.ExploreConfig{Seeds: seeds(20, 3)}
	sys.Explore(t, n).Expect(t, qtest.Conforms(), qtest.Reaches("rpc.api.request").Faulted())
	sys.WithBinding(calls, qtest.Linearize()).Explore(t, n).Expect(t, qtest.Conforms())
}

// Claim 1's contrast on the SQL tier: the faithful app at claim 1's
// configuration never commits a create the model refuses, where the
// blind mutant does (TestMutant). With grist's condition taken out of
// the runtime it fails in 11 of 20 runs (6 seeds miss with chance 1%).
// Full mode: TestFaithfulAtClaim1 is the contrast per push, and the
// condition under the head lock is the contract's on qsql
// (ConcurrentAgainstBruteForce, grist/sqlstore).
func TestSQLFaithfulAtClaim1(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("full mode: TestFaithfulAtClaim1 is claim 1's contrast per push")
	}
	qtest.RequireQuint(t)
	system(commits, gristsim.Config{Isolation: qenv.ReadCommitted, Tenants: []string{"t1"}, Send: []string{"create-counter"}, SQL: true}).
		WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(40, 6)}).
		Expect(t, qtest.None(qtest.Rejects("createCounter")), qtest.Conforms())
}

package wallet

import (
	"fmt"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qhistory"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// system is the wallet under cfg as one System.
func system(cfg Config, opts ...qtest.Option) qtest.System {
	s := qtest.System{Binding: "wallet.binding.yaml", Rates: faulty, Opts: opts,
		Run: func(c qenv.Chooser) error { return Run(c, cfg) }}
	if cfg.Mutation != Faithful {
		s.Mutant = fmt.Sprint(cfg.Mutation)
	}
	return s
}

// faulty loses, duplicates and delays one request or response in ten,
// and crashes the bank and the ledger.
var faulty = qenv.DefaultRates.Set("rpc", 0.1)

// Through lost, late and duplicated messages and a crashing bank and
// ledger, concurrent transfers and reads are linearizable, every call
// answers, and after the settle every applied request is posted.
func TestFaithful(t *testing.T) {
	qtest.RequireQuint(t)
	system(Config{}, qtest.Linearize()).Explore(t, qtest.ExploreConfig{Seeds: 10}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.All(qtest.Completes()))
}

// A retried request applied again moves the money twice: a lost response
// is all it takes, so a search of every run within one fault finds it,
// and proves the faithful bank applies every request once there.
func TestNoDedupeWithinOneFault(t *testing.T) {
	qtest.RequireQuint(t)
	one := Config{Workload: [][]Op{{{Kind: "transfer", Src: "b", Dest: "c", Amount: 30}}}}
	system(one).Exhaust(t, qtest.Bound{Faults: 1}).Expect(t, qtest.Conforms())
	one.Mutation = NoDedupe
	system(one).Exhaust(t, qtest.Bound{Faults: 1}).Expect(t, qtest.Some(qtest.Rejects("observe")))
}

// Two transfers from one account, each reading it without its lock, both
// spend what one saw at read committed: a lost update. At snapshot
// isolation the first committer wins and the other retries; the faithful
// bank locks the account at every level.
func TestReadThenWrite(t *testing.T) {
	qtest.RequireQuint(t)
	rc := Config{Mutation: ReadThenWrite, Isolation: qenv.ReadCommitted, Workload: Contended}
	system(rc).Explore(t, qtest.ExploreConfig{Seeds: 20}).Expect(t, qtest.Some(qtest.Rejects("observe")))
	si := rc
	si.Isolation = qenv.SnapshotIsolation
	system(si).Explore(t, qtest.ExploreConfig{Seeds: 20}).Expect(t, qtest.Conforms())
	faithful := rc
	faithful.Mutation = Faithful
	system(faithful).Explore(t, qtest.ExploreConfig{Seeds: 20}).Expect(t, qtest.Conforms())
}

// quiet is no faults: two transfers that interleave, nothing else.
var quiet = qenv.DefaultRates.Set("rpc", 0).Set("nemesis", 0).Set("queue", 0).Set("store", 0)

// Two transfers, one from each account of a credit line, each reading the
// other account without its lock, both pass at snapshot isolation, which
// lets disjoint writes commit: the line goes negative. The transfers
// overlap, and which one the line refuses depends on which took effect
// first, not on which answered first: the history is linearized. The
// window is wide: uniform seeds interleave the two in 39 runs of 40 (PCT,
// which runs a task long before switching, in fewer, sized to the run: it
// is for a narrow window, as quorumkv's BlindRepair).
func TestWriteSkew(t *testing.T) {
	qtest.RequireQuint(t)
	skew := Config{Mutation: WriteSkew, Workload: Skew}
	system(skew, qtest.Linearize()).WithRates(quiet).Explore(t, qtest.ExploreConfig{Seeds: 10}).Expect(t, qtest.Some(qtest.Mismatch("transfer")))
	skew.Mutation = Faithful
	system(skew, qtest.Linearize()).WithRates(quiet).Explore(t, qtest.ExploreConfig{Seeds: 10}).Expect(t, qtest.Conforms())
}

// The bank's store's history, read as Elle reads a database's: at
// snapshot isolation the skewed transfers form a cycle of two
// anti-dependencies, G2, which snapshot isolation allows and
// serializability forbids; the faithful bank's history has none.
func TestWriteSkewHistory(t *testing.T) {
	found := false
	for seed := uint64(1); seed <= 10 && !found; seed++ {
		for _, m := range []Mutation{Faithful, WriteSkew} {
			c := qenv.NewSeeded(seed)
			c.Rates = quiet
			d, err := RunDeployment(c, Config{Mutation: m, Workload: Skew})
			if err != nil {
				t.Fatal(err)
			}
			ser, err := qhistory.Check(qhistory.Serializable, d.History.OnAccounts())
			if err != nil {
				t.Fatal(err)
			}
			si, _ := qhistory.Check(qhistory.SnapshotIsolation, d.History.OnAccounts())
			switch {
			case m == Faithful && !ser.OK():
				t.Fatalf("seed %d: the faithful bank's history is not serializable:\n%s", seed, ser)
			case m == WriteSkew && ser.Has("G2"):
				if !si.OK() {
					t.Errorf("seed %d: G2 is snapshot isolation's to allow, but:\n%s", seed, si)
				}
				t.Logf("seed %d, the skewed bank's history: %s", seed, ser)
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no seed of 10 skewed the credit line's transfers into G2")
	}
	// and through every fault, the faithful bank's transfers are
	// serializable, though its store is at snapshot isolation: it locks
	// what it reads
	for seed := uint64(1); seed <= 20; seed++ {
		c := qenv.NewSeeded(seed)
		c.Rates = faulty
		d, err := RunDeployment(c, Config{})
		if err != nil {
			t.Fatal(err)
		}
		if rep, err := qhistory.Check(qhistory.Serializable, d.History.OnAccounts()); err != nil || !rep.OK() {
			t.Fatalf("seed %d: %v\n%s", seed, err, rep)
		}
	}
}

// Under every fault family at once (clock skews and jumps, stalls,
// partitions between every pair of processes, as a nightly run turns them
// on), the faithful wallet still conforms and posts everything.
func TestAllFaults(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("explores 10 runs under every fault family, each linearized")
	}
	t.Setenv("GOTOGETHER_ALL_FAULTS", "1")
	system(Config{}, qtest.Linearize()).Explore(t, qtest.ExploreConfig{Seeds: 10}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.All(qtest.Completes()))
}

// Publishing after the commit instead of through the outbox loses a post
// when the bank crashes between them: one crash, so a search within one
// fault finds it. Every call still conforms (the transfer was applied
// and answered); only the eventual check, after the settle, sees the post
// never came.
func TestNoOutbox(t *testing.T) {
	qtest.RequireQuint(t)
	one := Config{Mutation: NoOutbox, Workload: [][]Op{{{Kind: "transfer", Src: "b", Dest: "c", Amount: 30}}}}
	system(one).Exhaust(t, qtest.Bound{Faults: 1}).
		Expect(t, qtest.Some(qtest.Violates(validate.EventuallyName("allPosted"))), qtest.None(qtest.Mismatch("transfer")))
}

// A request the bank must refuse (a transfer to its own source, to an
// account it does not hold, of a negative amount; a deposit of nothing) is
// answered false and moves nothing; a bank that applies them makes and
// destroys money.
func TestInvalidRequests(t *testing.T) {
	qtest.RequireQuint(t)
	system(Config{Workload: Invalid}).Explore(t, qtest.ExploreConfig{Seeds: 10}).Expect(t, qtest.Conforms())
	system(Config{Mutation: NoValidation, Workload: Invalid}).Explore(t, qtest.ExploreConfig{Seeds: 10}).
		Expect(t, qtest.All(qtest.Mismatch("transfer")))
}

// A duplicated request delivered while its first delivery is in flight
// finds no record unless the record is locked: at read committed both
// deliveries apply it (8 runs of 20 here). Duplicates are half of the
// requests, and the two deliveries must both read before either commits.
// The first faithful version of this bank did it.
func TestUnlockedRecord(t *testing.T) {
	qtest.RequireQuint(t)
	dups := qenv.DefaultRates.Set("rpc.wallet.request", 0.5)
	rc := Config{Isolation: qenv.ReadCommitted, Workload: Contended}
	system(rc).WithRates(dups).Explore(t, qtest.ExploreConfig{Seeds: 20}).Expect(t, qtest.Conforms())
	rc.Mutation = UnlockedRecord
	// the request applied twice: its answer is the second's (ok false, the
	// funds spent by the first) or the balances show it
	system(rc).WithRates(dups).Explore(t, qtest.ExploreConfig{Seeds: 20}).
		Expect(t, qtest.Some(qtest.AnyOf(qtest.Mismatch("transfer"), qtest.Rejects("observe"))))
}

// A balance answered from the ledger's copy misses a deposit the bank
// applied and the ledger has not posted: a read after the deposit
// returned shows the old balance. Each call is on one account, so the
// history is linearized account by account. (On a history this small that
// costs more than Linearize, 15 s to 9 s for five runs: each partition's
// search pays quint's fixed cost; it is for histories of many keys.)
func TestReplicaRead(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("explores 5 runs linearized per account")
	}
	byKey := qtest.LinearizeByKey("account")
	// some of these runs need the order (a read concurrent with a deposit
	// on its account)
	system(Config{Workload: Deposits}, byKey).Explore(t, qtest.ExploreConfig{Seeds: 10}).Expect(t, qtest.Conforms())
	system(Config{Mutation: ReplicaRead, Workload: Deposits}, byKey).Explore(t, qtest.ExploreConfig{Seeds: 2}).
		Expect(t, qtest.Some(qtest.Mismatch("read")))
}

// A client that crashes mid-call never learns whether its transfer took
// effect: the call stays pending. The probe's balances after the settle
// say whether it did, and ResolvePending finds the resolution they need;
// without it the runs that applied one look wrong.
func TestPendingTransfers(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("explores 24 runs whose pending calls are resolved by search")
	}
	crashing := qenv.DefaultRates.Set("nemesis.crash", 0.02)
	cfg := Config{Workload: Blind, ClientCrashes: true}
	system(cfg, qtest.ResolvePending()).WithRates(crashing).Explore(t, qtest.ExploreConfig{Seeds: 12}).
		Expect(t, qtest.Conforms(), qtest.Some(qtest.Pending("transfer")))
	system(cfg).WithRates(crashing).Explore(t, qtest.ExploreConfig{Seeds: 12}).Expect(t, qtest.Some(qtest.Rejects("observe")))
}

package walletgrist

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// The bindings: the commit side's, its test-only exactly-once variant,
// and the client side's.
const (
	commits = "wallet.binding.yaml"
	exactly = "wallet.exactly.binding.yaml"
	calls   = "wallet.calls.binding.yaml"
)

// system is the app under cfg, judged by binding, with faulty rates.
func system(binding string, cfg gristsim.Config, opts ...qtest.Option) qtest.System {
	cfg.Doubles, cfg.Setup = doubles, front
	s := gristsim.System(binding, App, cfg).WithRates(faulty)
	s.Opts = opts
	return s
}

// faulty crashes the node (2 in 100 scheduling steps) and makes every
// other fault at the default rate: requests and answers lost, duplicated
// and late (the API's and the ledger's), commits whose answer is lost
// (with CommitFaults), the post's crash points.
var faulty = qenv.DefaultRates.Set("nemesis", 0.02)

// of picks one of xs.
func of[T any](pick func(int) int, xs ...T) T { return xs[pick(len(xs))] }

// accounts are a generated request's accounts: the bank's, and with
// strangers one it does not hold.
var (
	held3    = []string{"a", "b", "c"}
	stranger = []string{"a", "b", "c", "z"}
)

// gen are the workload's generators: transfers and deposits of 1 to 4
// between the bank's accounts (a transfer to its own source among them,
// refused), reads of every account.
var gen = map[string]func(pick func(int) int) any{
	"transfer": func(p func(int) int) any {
		return Transfer{Src: of(p, held3...), Dest: of(p, held3...), Amount: 1 + p(4)}
	},
	"deposit": func(p func(int) int) any { return Deposit{Account: of(p, held3...), Amount: 1 + p(4)} },
	"read":    func(p func(int) int) any { return Read{Account: of(p, held3...)} },
}

// invalidGen adds what the bank must refuse: an account it does not
// hold, amounts that are not positive.
var invalidGen = map[string]func(pick func(int) int) any{
	"transfer": func(p func(int) int) any {
		return Transfer{Src: of(p, stranger...), Dest: of(p, stranger...), Amount: of(p, -1, 0, 1, 2)}
	},
	"deposit": func(p func(int) int) any { return Deposit{Account: of(p, stranger...), Amount: of(p, -1, 0, 2)} },
	"read":    gen["read"],
}

// contended is the faithful runs' shape: one tenant, three clients of
// four operations each, every command and the read.
var contended = gristsim.Config{Tenants: []string{"t1"}, Clients: 3, Ops: 4, CommitFaults: true, Check: true, Gen: gen}

// TestFaithful: through commit faults, node crashes, a lossy API and a
// lossy ledger, at Serializable and ReadCommitted, every run conforms
// at the store and settles (every move posted, every post a move's), and
// the faults happened. examples/wallet's TestFaithful.
func TestFaithful(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	for _, iso := range []qenv.Isolation{qenv.Serializable, qenv.ReadCommitted} {
		t.Run(iso.String(), func(t *testing.T) {
			cfg := contended
			cfg.Isolation = iso
			system(commits, cfg).
				Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 4}).
				Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()),
					qtest.Reaches("store.wallet.commit").Faulted(), qtest.Reaches("nemesis.crash").Faulted())
		})
	}
}

// TestFaithfulAtClients: the same runs judged where the clients saw them,
// linearized against the client-side model: a retry applied twice would
// show as a balance no order explains.
func TestFaithfulAtClients(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	cfg := contended
	cfg.Isolation = qenv.ReadCommitted
	system(calls, cfg, qtest.Linearize()).
		Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 3}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("store.wallet.commit").Faulted())
}

// TestDuplicateRequests: the API duplicating and delaying three requests
// in ten; a request's first answer is final (grist's marker is the
// request record examples/wallet's NoDedupe and UnlockedRecord mutants
// break), at the store and at the clients.
func TestDuplicateRequests(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	rates := qenv.NoFaults.Set("rpc.api.delay", 0.3).Set("rpc.api.request", 0.3)
	cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 3, Ops: 4, Isolation: qenv.ReadCommitted, Gen: gen,
		Send: []string{"transfer", "deposit", "read"}}
	system(commits, cfg).WithRates(rates).Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 4}).
		Expect(t, qtest.Conforms(), qtest.Reaches("rpc.api.request").Faulted())
	system(calls, cfg, qtest.Linearize()).WithRates(rates).Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 3}).
		Expect(t, qtest.Conforms(), qtest.Reaches("rpc.api.request").Faulted())
}

// invalid is TestInvalidRequests' shape (and the noValidation mutant's).
var invalid = gristsim.Config{Tenants: []string{"t1"}, Clients: 2, Ops: 4, Gen: invalidGen,
	Send: []string{"transfer", "deposit"}}

// TestInvalidRequests: a transfer to its own source, to or from an
// account the bank does not hold, of an amount that is not positive, a
// deposit of nothing: the faithful bank refuses each, and conforms.
func TestInvalidRequests(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	system(commits, invalid).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 10, ShortSeeds: 3}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
}

// race is two clients spending from one account, c (5 cents), three at a
// time: one of each pair must be refused. ReadThenWrite's shape.
var race = gristsim.Config{Tenants: []string{"t1"}, Clients: 2, Ops: 2, Isolation: qenv.ReadCommitted,
	Send: []string{"transfer"},
	Gen: map[string]func(pick func(int) int) any{"transfer": func(p func(int) int) any {
		return Transfer{Src: "c", Dest: of(p, "a", "b"), Amount: 3}
	}}}

// skew is one transfer from each account of the credit line (10 cents
// between them), 6 at a time: the line covers one. WriteSkew's shape.
var skew = gristsim.Config{Tenants: []string{"t1"}, Clients: 2, Ops: 1, Isolation: qenv.ReadCommitted,
	Send: []string{"transfer"},
	Gen: map[string]func(pick func(int) int) any{"transfer": func(p func(int) int) any {
		return Transfer{Src: of(p, "a", "b"), Dest: "c", Amount: 6}
	}}}

// TestRaces: the faithful bank at read committed, two transfers from one
// account and one from each account of the credit line: its transfer is
// conditioned on every account it read (the line's both), so the race's
// loser decides again and is refused. The contrast of the readThenWrite
// mutant (its transfer Unconditioned), caught at the same shapes.
func TestRaces(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	for name, cfg := range map[string]gristsim.Config{"race": race, "skew": skew} {
		t.Run(name, func(t *testing.T) {
			system(commits, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 6}).
				Expect(t, qtest.None(qtest.Rejects("transfer")), qtest.Conforms())
			system(calls, cfg, qtest.Linearize()).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 6}).
				Expect(t, qtest.Conforms())
		})
	}
}

// transfers is one client transferring from c to a or b, no faults: each
// transfer commits its two moves in one batch, each the trigger of a
// post.
var transfers = gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 3, Send: []string{"transfer"},
	Gen: map[string]func(pick func(int) int) any{"transfer": func(p func(int) int) any {
		return Transfer{Src: "c", Dest: of(p, "a", "b"), Amount: 1}
	}}}

// TestPostAfterTransfer: a post's record at the ledger follows the commit
// holding its move, even the first move of a transfer's two-event batch:
// grist places the ledger's record at its cause (the head the poller
// read), and the generated ledgerPostServed takes a record only once its
// trigger is in the log. Placed at its trigger's own position (2·T+1) the
// first move's post comes before the batch's commit (at 2·P, P the
// batch's end): every run rejects ledgerPostServed at its first post.
func TestPostAfterTransfer(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	system(commits, transfers).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 6, ShortSeeds: 2}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Takes("ledgerPostServed"))
}

// crashAfterPost is a script of one fault: the node crashes after the
// ledger's post and before its checkpoint, the rest of each run its
// seed's; the restarted node posts the move again under the same key.
func crashAfterPost(seed uint64) qenv.Chooser {
	base := qenv.NewSeeded(seed)
	base.Rates = qenv.NoFaults
	return &qenv.Script{Base: base, Faults: []qenv.Scripted{{Kind: "grist.post.effect", Name: "crashAfter"}}}
}

// deposits is one client depositing: the ledger's claims' shape.
var deposits = gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 3, Gen: gen, Send: []string{"deposit"}}

// TestPostedOnce: the post is at least once, and the ledger's dedupe by
// the effect key makes it once. A crash after the ledger's post and
// before the checkpoint posts the move again: the run conforms (the
// ledger records it once), and the test-only exactly-once eventual val
// holds; without faults too. The ledgerNoDedupe mutant is caught at the
// same script (TestMutant).
func TestPostedOnce(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	runs := qtest.ExploreConfig{Seeds: 10, ShortSeeds: 3}
	crashed := runs
	crashed.Chooser = crashAfterPost
	system(commits, deposits).Explore(t, crashed).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	system(exactly, deposits).Explore(t, crashed).
		Expect(t, qtest.Conforms())
	system(exactly, deposits).WithRates(qenv.NoFaults).Explore(t, runs).
		Expect(t, qtest.Conforms())
}

// TestOneRequestExhaust: every run of one transfer within one fault
// (examples/wallet's TestNoDedupeWithinOneFault, faithful half): every
// one conforms and settles. Full mode.
func TestOneRequestExhaust(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("full mode: an exhaustive search")
	}
	qtest.RequireQuint(t)
	one := gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 1, Gen: gen, Send: []string{"transfer"}}
	system(commits, one).Exhaust(t, qtest.Bound{Faults: 1}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
}

// TestVerify: the registry boots.
func TestVerify(t *testing.T) {
	if err := App.Verify(); err != nil {
		t.Fatal(err)
	}
}

// depositsAndReads is two clients depositing to and reading account a:
// the replicaRead mutant's shape, a read soon after a deposit.
var depositsAndReads = gristsim.Config{Tenants: []string{"t1"}, Clients: 2, Ops: 4, Send: []string{"deposit", "read"},
	Gen: map[string]func(pick func(int) int) any{
		"deposit": func(p func(int) int) any { return Deposit{Account: "a", Amount: 1 + p(3)} },
		"read":    func(p func(int) int) any { return Read{Account: "a"} },
	}}

// TestReplicaReadShape: the faithful bank at the replicaRead mutant's
// shape: every read is the log's balance at its position.
func TestReplicaReadShape(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	system(commits, depositsAndReads).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 10, ShortSeeds: 3}).
		Expect(t, qtest.None(qtest.Mismatch("read")), qtest.Conforms())
}

// TestSQLTier: TestFaithful's shape on the SQL tier (sqlstore over qsql,
// the real tier's statements) at ReadCommitted, both sides. Full mode.
func TestSQLTier(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("full mode: the SQL tier repeats TestFaithful's mechanisms")
	}
	qtest.RequireQuint(t)
	cfg := contended
	cfg.Isolation, cfg.SQL = qenv.ReadCommitted, true
	sys := system(commits, cfg).WithRates(faulty.Set("store.wallet.commit", 0.15))
	n := qtest.ExploreConfig{Seeds: 10}
	sys.Explore(t, n).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("store.wallet.commit").Faulted())
	sys.WithBinding(calls, qtest.Linearize()).Explore(t, n).Expect(t, qtest.Conforms())
}

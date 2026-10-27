package counters

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// setup gives every run a mailer of its own, and the store (for the
// dispatchDirect mutant's raw append).
func setup(s *qenv.Sim, st grist.Store) func(context.Context) context.Context {
	m, b := NewSimMailer(s), NewSimBank(s)
	return func(ctx context.Context) context.Context {
		return StoreDep.With(BankDep.With(MailerDep.With(ctx, m), b), st)
	}
}

// The bindings: the commit side's, its test-only variant judging one
// welcome per counter, the client side's, and those of runs that retire
// counters.
const (
	commits = "counters.binding.yaml"
	once    = "counters.once.binding.yaml"
	// retiring and exactly judge runs that retire counters (the payout's
	// steps): model/retiring.qnt, the commit side with retirements, and
	// its test-only exactly-once eventual val
	retiring = "counters.retiring.binding.yaml"
	exactly  = "counters.exactly.binding.yaml"
	calls    = "counters.calls.binding.yaml"
)

// system is the app under cfg, judged by binding, with faulty rates.
func system(binding string, cfg gristsim.Config, opts ...qtest.Option) qtest.System {
	cfg.Setup = setup
	return qtest.System{Binding: binding, Rates: faulty, Opts: opts, Mutant: gristtest.Mutant(),
		Run: func(c qenv.Chooser) error { return gristsim.Run(c, App, cfg) }}
}

// faulty crashes the node (2 in 100 scheduling steps) and makes every
// other fault at the default rate: requests and answers lost, duplicated
// and late, commits whose answer is lost (with CommitFaults), the
// welcome's crash points.
var faulty = qenv.DefaultRates.Set("nemesis", 0.02)

// seeds is n, or short under -short.
func seeds(n, short int) int {
	if testing.Short() {
		return short
	}
	return n
}

// contended is claim 2's shape: one tenant and names from two letters,
// so that creates and increments collide; no retirements (the payout's
// claims are TestPayoutAtMostOnce's, and TestFaithfulRetiring's in full
// mode).
var contended = gristsim.Config{Tenants: []string{"t1"}, Alphabet: []string{"a", "b"}, CommitFaults: true, Check: true,
	Send: []string{"create-counter", "increment-counter", "show-counter"}}

// Claim 2: the safe default passes, through every fault. At Serializable
// and ReadCommitted, with commit faults and node crashes, and the
// scheduler's and the Store's own checks on, every run conforms and
// settles, and the faults happened. Snapshot is left out: every append
// writes the head, so its runs are Serializable's, byte for byte (the
// verdicts are cached across the two).
func TestFaithful(t *testing.T) {
	t.Parallel() // its explorations record into their own runs
	qtest.RequireQuint(t)
	for _, iso := range []qenv.Isolation{qenv.Serializable, qenv.ReadCommitted} {
		t.Run(iso.String(), func(t *testing.T) {
			cfg := contended
			cfg.Isolation = iso
			system(commits, cfg).
				Explore(t, qtest.ExploreConfig{Seeds: seeds(20, 4)}).
				Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()),
					qtest.Reaches("store.counters.commit").Faulted(), qtest.Reaches("nemesis.crash").Faulted())
		})
	}
}

// Claim 2 with retirements: every command and query of the app, the
// at-most-once payout among the processors, through commit faults and
// node crashes. Full mode: the payout's claims are TestPayoutAtMostOnce's
// per push.
func TestFaithfulRetiring(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("full mode: TestPayoutAtMostOnce judges the payout per push")
	}
	qtest.RequireQuint(t)
	cfg := contended
	cfg.Send = nil // every command and query
	system(retiring, cfg).Explore(t, qtest.ExploreConfig{Seeds: 20}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("store.counters.commit").Faulted(), qtest.Reaches("nemesis.crash").Faulted())
}

// Claim 1's contrast: the faithful app at claim 1's configuration (two
// clients creating in one tenant, no faults) never commits a create the
// model refuses, where the blind mutant does in most runs. With grist's
// condition taken out of the runtime (the append unconditioned), this
// claim fails as the blind mutant's holds: 13 of 20 runs (6 seeds miss
// it with chance 0.2%).
func TestFaithfulAtClaim1(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	system(commits, gristsim.Config{Isolation: qenv.ReadCommitted, Tenants: []string{"t1"}, Send: []string{"create-counter"}}).
		WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(40, 6)}).
		Expect(t, qtest.None(qtest.Rejects("createCounter")), qtest.Conforms())
}

// A key's first answer is final, through duplicated and late requests:
// with the API duplicating and delaying three requests in ten, a refused
// command's late duplicate does not commit after its client was told
// no, nor a committed one's commit again. Both sides conform: the
// commits (every committed outcome needs its key fresh) and the clients,
// linearized. Without the marker (no key remembered) the duplicates
// commit twice: 19 of 20 runs fail at the commits; without refusal
// markers 18 of 20 (4 seeds miss either with chance under 0.1%).
func TestDuplicateRequests(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	rates := qenv.NoFaults.Set("rpc.api.delay", 0.3).Set("rpc.api.request", 0.3)
	cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 3, Ops: 4, Alphabet: []string{"a", "b"},
		Send: []string{"create-counter", "increment-counter", "show-counter"}}
	system(commits, cfg).WithRates(rates).Explore(t, qtest.ExploreConfig{Seeds: seeds(20, 4)}).
		Expect(t, qtest.Conforms(), qtest.Reaches("rpc.api.request").Faulted())
	system(calls, cfg, qtest.Linearize()).WithRates(rates).Explore(t, qtest.ExploreConfig{Seeds: seeds(40, 4)}).
		Expect(t, qtest.Conforms(), qtest.Reaches("rpc.api.request").Faulted())
}

// Claim 2, at the clients: the same runs recorded where the clients see
// them, linearized against the client-side model: a duplicate a retry
// created would show as a later read's mismatch. Full mode: the client
// side is judged per push by TestDuplicateRequests and TestSQLTier.
func TestFaithfulAtClients(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("full mode: TestDuplicateRequests and TestSQLTier judge the clients' side per push")
	}
	qtest.RequireQuint(t)
	cfg := contended
	system(calls, cfg, qtest.Linearize()).
		Explore(t, qtest.ExploreConfig{Seeds: seeds(20, 4)}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("store.counters.commit").Faulted())
}

// crashAfterWelcome is a script of one fault: the node crashes after the
// welcome's send and before its checkpoint, the rest of each run its
// seed's.
func crashAfterWelcome(seed uint64) qenv.Chooser {
	base := qenv.NewSeeded(seed)
	base.Rates = qenv.NoFaults
	return &qenv.Script{Base: base, Faults: []qenv.Scripted{{Kind: "grist.welcome.effect", Name: "crashAfter"}}}
}

// okThenFailed is crashAfterWelcome, then three lost sends after it.
func okThenFailed(seed uint64) qenv.Chooser {
	base := qenv.NewSeeded(seed)
	base.Rates = qenv.NoFaults
	return &qenv.Script{Base: base, Faults: []qenv.Scripted{
		{Kind: "grist.welcome.effect", Name: "crashAfter"},
		{Kind: "rpc.mailer.request", Name: "drop", Follows: []int{0}},
		{Kind: "rpc.mailer.request", Name: "drop", Follows: []int{0}},
		{Kind: "rpc.mailer.request", Name: "drop", Follows: []int{0}},
	}}
}

// Claim 3: the duplicated welcome is the declared guarantee's, seen and
// judged. A crash after the send and before the checkpoint: the restarted
// node sends the welcome again under the same effect key. The model
// admits it (at least once); the test-only invariant welcomedOnce
// rejects exactly those runs. Exhaust within one fault reaches it too.
func TestWelcomeDuplicate(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 3, Send: []string{"create-counter"}}
	n := seeds(10, 3)
	system(commits, cfg).Explore(t, qtest.ExploreConfig{Seeds: n, Chooser: crashAfterWelcome}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	system(once, cfg).Explore(t, qtest.ExploreConfig{Seeds: n, Chooser: crashAfterWelcome}).
		Expect(t, qtest.All(qtest.Violates("welcomedOnce")))
	// without the crash, no duplicate
	system(once, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: n}).
		Expect(t, qtest.None(qtest.Violates("welcomedOnce")))
	// an ok send, a crash before the checkpoint, then every try of the
	// restarted node's send lost: the committed run checkpoints
	// grist.failed, which at least once admits (atLeastOnceDone)
	system(commits, cfg).Explore(t, qtest.ExploreConfig{Seeds: n, Chooser: okThenFailed}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("rpc.mailer.request").Faulted())
	if testing.Short() {
		return
	}
	one := gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 1}
	system(once, one).Exhaust(t, qtest.Bound{Faults: 1}).
		Expect(t, qtest.Some(qtest.Violates("welcomedOnce")))
	system(commits, one).Exhaust(t, qtest.Bound{Faults: 1}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
}

// retirements is the payout claims' shape: one client creating and
// retiring counter a, eight times, so a run retires a counter that
// exists unless no create comes before a retire (a counter is retired by
// its creator only: with two clients, one's retirements of the other's
// counter are refused, and a run could pay nothing).
var retirements = gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 8, Alphabet: []string{"a"},
	Send: []string{"create-counter", "retire-counter"}}

// contested is two clients creating and retiring counter a, six times
// each: one's retirement of a counter only the other created is refused
// (not-creator).
var contested = gristsim.Config{Tenants: []string{"t1"}, Clients: 2, Ops: 6, Alphabet: []string{"a"},
	Send: []string{"create-counter", "retire-counter"}}

// scripted is a script of faults over seed's choices, no other fault.
func scripted(faults ...qenv.Scripted) func(seed uint64) qenv.Chooser {
	return func(seed uint64) qenv.Chooser {
		base := qenv.NewSeeded(seed)
		base.Rates = qenv.NoFaults
		return &qenv.Script{Base: base, Faults: faults}
	}
}

// The payout is at most once, seen and judged. A crash after its
// checkpoint and before the payment (grist.payout.effect's crashBefore)
// loses the payment: the model admits it (the checkpoint first, no
// effect step, no outcome: 10 of 10 runs conform), and the test-only
// exactly-once eventual val (everyRetirementPaidOnce, by the bank's own
// record of its payments) rejects those runs (10 of 10), while every
// payment that was made has its outcome (everyPayoutOutcomeLogged
// holds). A crash after the payment (crashAfter) loses its paid-out
// event and pays nothing twice: everyRetirementPaidOnce holds and
// everyPayoutOutcomeLogged rejects every run (10 of 10). Without faults
// both hold. With grist's AtMostOnce run as at least once, 10 of 10 runs
// reject payoutEffect (a payment before its checkpoint); with grist
// returning before it appends an ok effect's outcome, every run violates
// everyPayoutOutcomeLogged (10 of 10 under crashBefore: the other
// retirements' payments), which the model's actions admit (the outcome
// is optional). Per push the admitted loss
// alone, at 2 seeds; the rest in full mode.
func TestPayoutAtMostOnce(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	n := qtest.ExploreConfig{Seeds: seeds(10, 2)}
	lose := qtest.ExploreConfig{Seeds: n.Seeds, Chooser: scripted(qenv.Scripted{Kind: "grist.payout.effect", Name: "crashBefore"})}
	system(retiring, retirements).Explore(t, lose).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	if testing.Short() {
		return // the judgments of exactly once: full mode (grist's TestEffectAtMostOnceCrashes covers both crashes per push)
	}
	paidOnce, logged := validate.EventuallyName("everyRetirementPaidOnce"), validate.EventuallyName("everyPayoutOutcomeLogged")
	system(exactly, retirements).Explore(t, lose).
		Expect(t, qtest.All(qtest.Violates(paidOnce)), qtest.None(qtest.Violates(logged)))
	after := qtest.ExploreConfig{Seeds: n.Seeds, Chooser: scripted(qenv.Scripted{Kind: "grist.payout.effect", Name: "crashAfter"})}
	system(exactly, retirements).Explore(t, after).
		Expect(t, qtest.All(qtest.Violates(logged)), qtest.None(qtest.Violates(paidOnce)))
	system(exactly, retirements).WithRates(qenv.NoFaults).Explore(t, n).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	// the bank's answer lost after it paid: the payment's outcome is
	// unknown (grist.unknown, with the error), not "failed"; the bank's own
	// record shows it paid once, so both vals hold
	lostAnswer := qtest.ExploreConfig{Seeds: n.Seeds, Chooser: scripted(qenv.Scripted{Kind: "rpc.bank.response", Name: "lost"})}
	system(exactly, retirements).Explore(t, lostAnswer).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("rpc.bank.response").Faulted())
}

// twoNodes is the payout's shape with two node processes on the one
// store (gristsim.Config.Nodes): two pollers of payout race for each
// checkpoint, as two replicas of a real tier do.
var twoNodes = func() gristsim.Config {
	cfg := retirements
	cfg.Nodes = 2
	return cfg
}()

// At most once with two pollers. Without faults, two nodes race for
// each payout checkpoint; the loser of a race whose append failed after
// the winner committed finds the checkpoint moved and leaves the payment
// to the winner (grist's TestEffectAtMostOnceLostCheckpoint): every run
// conforms to the retiring binding, every retirement is paid exactly
// once (counters.exactly.binding.yaml) and some run took that branch.
// With grist taking a moved checkpoint for its own after a failed append
// (the mutant in commitAt), the loser pays too: 10 of 10 runs reject
// payoutEffect (atMostOnceEffect refuses a second payment step). Then
// two nodes through commit faults and crashes: the per-push exploration
// of the payout under faults other than a scripted crash.
func TestPayoutTwoNodes(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	// the branch is no fault kind (qtest.Reaches names those): grist logs
	// it to the run's log, counted here
	var left atomic.Int64
	race := twoNodes
	race.Log = func(l string) {
		if strings.Contains(l, "grist: payout/") && strings.Contains(l, "another poller's") {
			left.Add(1)
		}
	}
	system(retiring, race).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(10, 4)}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	if left.Load() == 0 {
		t.Error("no payout checkpoint was left to another poller after a failed append: the race the claim is about never happened")
	}
	if !testing.Short() {
		system(exactly, twoNodes).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 10}).
			Expect(t, qtest.Conforms())
	}
	faults := twoNodes
	faults.CommitFaults = true
	system(retiring, faults).Explore(t, qtest.ExploreConfig{Seeds: seeds(10, 3)}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("store.counters.commit").Faulted())
}

// Only a counter's creator retires it: the decision reads the creator
// recorded in counter-created (Reads.Actor at its creation) and refuses
// another actor (not-creator, a committed refusal recorded at the
// store), and the model's retireCounter judges it by the step's
// principal: ok needs createdBy, not-creator its negation. Two clients
// creating and retiring counter a (contested), without faults: every run conforms,
// and some retire is refused as a non-creator's. Without the check (the
// anyoneRetires mutant, full mode) 6 of 6 runs reject retireCounter.
func TestRetireByCreatorOnly(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	x := system(retiring, contested).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(6, 2)})
	x.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	refused := 0
	for _, run := range x.Runs {
		for _, s := range run.Steps {
			if s.Action == "retireCounter" && s.Outcome == "not-creator" && !strings.HasSuffix(s.Actor, "/clients") {
				refused++
			}
		}
	}
	if refused == 0 {
		t.Error("no retirement was refused as a non-creator's at the store: the claim's case never happened")
	}
}

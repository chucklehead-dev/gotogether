package counters

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// setup gives every run the store (for the dispatchDirect mutant's raw
// append), on the node's context as the app's binary runs it
// (NodeContext). The mailer is gristsim's: its default double.
func setup(s *qenv.Sim, st grist.Store) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context { return StoreDep.With(NodeContext(ctx), st) }
}

// doubles are a run's bank, which gristsim serves as the qenv.Service
// bank: the team's own (NewBank), paying each key once and declining a
// payment over 100. grist records each payment it makes at its site
// (bankPayServed), so a payment made twice, or made while its answer was
// lost, is in the trace whatever the payout saw.
//
// The mailer is given none: gristsim serves its default double.
func doubles() gristsim.Doubles { return gristsim.Doubles{Bank: NewBank()} }

// freshBank is a bank that answers a retry of a key with a new receipt
// (its replay no longer its first answer).
func freshBank() gristsim.Doubles {
	paid := map[string]bool{}
	return gristsim.Doubles{Bank: grist.Handlers{
		Pay.Handler(func(ctx context.Context, r PayRequest) (PayReceipt, error) {
			key := grist.CallKey(ctx)
			if paid[key] {
				grist.Replayed(ctx)
				return PayReceipt{Ref: key + "/again"}, nil
			}
			paid[key] = true
			return PayReceipt{Ref: key}, nil
		}),
	}}
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
	// onReal judges the real tier (real_test.go): the default binding's
	// vals and the retiring ones but the mailer's site claim, the real
	// tier's mailer being a third party's
	onReal = "counters.real.binding.yaml"
)

// system is the app under cfg, judged by binding, with faulty rates.
func system(binding string, cfg gristsim.Config, opts ...qtest.Option) qtest.System {
	if cfg.Doubles == nil {
		cfg.Doubles = doubles
	}
	cfg.Setup = setup
	s := gristsim.System(binding, App, cfg).WithRates(faulty)
	s.Opts = opts
	return s
}

// faulty crashes the node (2 in 100 scheduling steps) and makes every
// other fault at the default rate: requests and answers lost, duplicated
// and late, commits whose answer is lost (with CommitFaults), the
// welcome's crash points.
var faulty = qenv.DefaultRates.Set("nemesis", 0.02)

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
				Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 4}).
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
		WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 40, ShortSeeds: 6}).
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
	rates := qenv.NoFaults.Set("rpc.counters.delay", 0.3).Set("rpc.counters.request", 0.3)
	cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 3, Ops: 4, Alphabet: []string{"a", "b"},
		Send: []string{"create-counter", "increment-counter", "show-counter"}}
	system(commits, cfg).WithRates(rates).Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 4}).
		Expect(t, qtest.Conforms(), qtest.Reaches("rpc.counters.request").Faulted())
	system(calls, cfg, qtest.Linearize()).WithRates(rates).Explore(t, qtest.ExploreConfig{Seeds: 40, ShortSeeds: 4}).
		Expect(t, qtest.Conforms(), qtest.Reaches("rpc.counters.request").Faulted())
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
		Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 4}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("store.counters.commit").Faulted(),
			qtest.Reaches("rpc.bank.request").Faulted(), qtest.Reaches("rpc.bank.response").Faulted(),
			qtest.Takes("bankPay").Outcome("unknown"), qtest.Takes("bankPayReplayed"))
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
	runs := qtest.ExploreConfig{Seeds: 10, ShortSeeds: 3}
	crashed := runs
	crashed.Chooser = crashAfterWelcome
	system(commits, cfg).Explore(t, crashed).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	system(once, cfg).Explore(t, crashed).
		Expect(t, qtest.All(qtest.Violates("welcomedOnce")))
	// without the crash, no duplicate
	system(once, cfg).WithRates(qenv.NoFaults).Explore(t, runs).
		Expect(t, qtest.None(qtest.Violates("welcomedOnce")))
	// an ok send, a crash before the checkpoint, then every try of the
	// restarted node's send lost: the committed run checkpoints
	// grist.failed, which at least once admits (atLeastOnceDone)
	okThen := runs
	okThen.Chooser = okThenFailed
	system(commits, cfg).Explore(t, okThen).
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

// paying is retirements after a required phase creating and retiring counter
// a: every run pays at least once, so a claim about the payment holds in
// every run, at one seed.
var paying = func() gristsim.Config {
	cfg := retirements
	// Payout faults are part of this scenario, including faults occurring
	// before the retirement's answer reaches its client. Keep them configured
	// while requiring both command outcomes under the same phase caller.
	cfg.Phases = []gristsim.Phase{{Name: "create and retire payout", Faults: gristsim.FaultsConfigured,
		Calls: []gristsim.Call{{Name: "create-counter", Args: CreateCounter{Counter: "a", Name: "a"}}, {Name: "retire-counter", Args: RetireCounter{Counter: "a"}}}}}
	return cfg
}()

// payOnce is paying's preparation alone (its client only creates): the run's
// one payment is that phase's retirement's, so a script's fault on the
// bank's call lands on it.
var payOnce = func() gristsim.Config {
	cfg := paying
	cfg.Ops = 1
	cfg.Send = []string{"create-counter"}
	return cfg
}()

// lostThenCrash is a script of the bank's answer lost, then the node
// crashed while the payout waits for it: the payout's call is left
// pending (at most once: never made again), the bank's served record the
// only record of the payment.
var lostThenCrash = scripted(qenv.Scripted{Kind: "rpc.bank.response", Name: "lost"}, qenv.Scripted{Kind: "nemesis.crash.node", Follows: []int{0}})

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

// The payout pays what the retirement says: grist records the bank's
// call where the payout makes it (bankPay: its request, the trigger, the
// key) and the payment where the bank makes it (bankPayServed), and the
// model's rule (payoutBankPay: the retired counter and its total) judges
// the request at both ends (the bank's record is the request's only one
// when the payout's node crashes during the call). Through the bank's own faults (its requests lost,
// duplicated and late at three in ten; its answers lost at that rate too,
// though no run here reaches one, 0 of 10: TestPayoutTwoNodes' faulty
// runs claim them per push, TestPayoutAtMostOnce scripts one): a duplicate
// the bank answers from its first answer is no second payment but a
// replay (bankPayReplayed: an earlier served record, answer alike), a
// lost answer an unknown outcome (grist.unknown). With the first request
// duplicated (paying: every run pays) the bank replays and every run
// conforms; a bank that answers the duplicate with a new receipt
// (freshBank) is caught at its replay in every run. Without
// faults at paying's shape every run conforms and pays; with the payout
// paying one more (the wrongAmount mutant, at that shape) every run
// rejects bankPayServed, where nothing the store records differs (and
// with its answer lost and the node crashed during the call, the payout's
// step left pending, every run still rejects the bank's record: the third
// contrast, lostThenCrash, conforms). The three contrasts are full mode:
// per push TestPayoutTwoNodes' runs take bankPay ok without faults and a
// replay under faults, and conform.
func TestPayoutAmount(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	bank := qenv.NoFaults.Set("rpc.bank.request", 0.3).Set("rpc.bank.delay", 0.3).Set("rpc.bank.response", 0.3)
	system(retiring, retirements).WithRates(bank).Explore(t, qtest.ExploreConfig{Seeds: 10, ShortSeeds: 3}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("rpc.bank.request").Faulted(),
			qtest.Reaches("rpc.bank.delay").Faulted(), qtest.Takes("bankPay").Outcome("unknown"))
	dup := qtest.ExploreConfig{Seeds: 6, ShortSeeds: 1, Chooser: scripted(qenv.Scripted{Kind: "rpc.bank.request", Name: "duplicate"})}
	fresh := paying
	fresh.Doubles = freshBank
	system(retiring, fresh).Explore(t, dup).Expect(t, qtest.All(qtest.Rejects("bankPayReplayed")))
	if testing.Short() {
		return
	}
	system(retiring, paying).Explore(t, dup).Expect(t, qtest.Conforms(), qtest.Takes("bankPayReplayed"))
	system(retiring, paying).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 6}).
		Expect(t, qtest.Conforms(), qtest.None(qtest.Rejects("bankPay")), qtest.All(qtest.Settles()),
			qtest.Takes("bankPay").Outcome("ok"), qtest.Takes("bankPayServed"))
	// the contrast of wrongAmount's pending payment: the bank's record
	// alone, its request the rule's
	system(retiring, payOnce).Explore(t, qtest.ExploreConfig{Seeds: 4, Chooser: lostThenCrash}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Takes("bankPayServed"))
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
	n := qtest.ExploreConfig{Seeds: 10, ShortSeeds: 2}
	lose := n
	lose.Chooser = scripted(qenv.Scripted{Kind: "grist.payout.effect", Name: "crashBefore"})
	system(retiring, retirements).Explore(t, lose).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	if testing.Short() {
		return // the judgments of exactly once: full mode (grist's TestEffectAtMostOnceCrashes covers both crashes per push)
	}
	paidOnce, logged := validate.EventuallyName("everyRetirementPaidOnce"), validate.EventuallyName("everyPayoutOutcomeLogged")
	system(exactly, retirements).Explore(t, lose).
		Expect(t, qtest.All(qtest.Violates(paidOnce)), qtest.None(qtest.Violates(logged)))
	after := n
	after.Chooser = scripted(qenv.Scripted{Kind: "grist.payout.effect", Name: "crashAfter"})
	system(exactly, retirements).Explore(t, after).
		Expect(t, qtest.All(qtest.Violates(logged)), qtest.None(qtest.Violates(paidOnce)))
	system(exactly, retirements).WithRates(qenv.NoFaults).Explore(t, n).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	// the bank's answer lost after it paid: the payment's outcome is
	// unknown (grist.unknown, with the error), not "failed"; the bank's own
	// record shows it paid once, so both vals hold
	lostAnswer := n
	lostAnswer.Chooser = scripted(qenv.Scripted{Kind: "rpc.bank.response", Name: "lost"})
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
// of the payout under faults other than a scripted crash, the bank's
// requests and answers lost among them (a call's unknown outcome, a
// duplicate replayed).
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
	system(retiring, race).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 10, ShortSeeds: 4}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Takes("bankPay").Outcome("ok"), qtest.Takes("bankPayServed"))
	if left.Load() == 0 {
		t.Error("no payout checkpoint was left to another poller after a failed append: the race the claim is about never happened")
	}
	if !testing.Short() {
		system(exactly, twoNodes).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 10}).
			Expect(t, qtest.Conforms())
	}
	faults := twoNodes
	faults.CommitFaults = true
	system(retiring, faults).Explore(t, qtest.ExploreConfig{Seeds: 30, ShortSeeds: 20}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("store.counters.commit").Faulted(),
			qtest.Reaches("rpc.bank.request").Faulted(), qtest.Reaches("rpc.bank.response").Faulted(),
			qtest.Takes("bankPay").Outcome("unknown"), qtest.Takes("bankPayReplayed"))
}

// Only a counter's creator retires it: the decision reads the creator
// recorded in counter-created (Reads.Caller at its creation) and refuses
// another actor (not-creator, a committed refusal recorded at the
// store), and the model's retireCounter judges it by the step's
// caller: ok needs createdBy, not-creator its negation. Two clients
// creating and retiring counter a (contested), without faults: every run conforms,
// and some retire is refused as a non-creator's. Without the check (the
// anyoneRetires mutant, full mode) 6 of 6 runs reject retireCounter.
func TestRetireByCreatorOnly(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	x := system(retiring, contested).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 6, ShortSeeds: 2})
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

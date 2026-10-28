package counters

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The mutants are grain's two bugs (blind, dispatchDirect), an effect
// checkpointed first, a payout without its outcome, a retirement by
// anyone and a payout of the wrong amount, a payout that calls the bank
// again after an unknown answer, and two pods recording as one node
// (pidIdentity), each a directory of testdata/mutants/ whose files
// replace the app's of the same name (a go build overlay, found there
// by qtest.Mutants): the app's own files stay as they are, and each
// mutant is a whole file to diff against them.

// fullOnly are the mutants built and run in full mode only: their claim
// repeats a mechanism a per-push test checks (checkpointFirst: the
// at-least-once model refusing a checkpoint before its effect, which
// grist's TestEffectAtMostOnce orders and TestPayoutAtMostOnce judges),
// and building a mutant's binary is most of its cost.
var fullOnly = []string{"checkpointFirst", "silentPayout", "anyoneRetires"}

// TestMutant states what catches the mutant this binary was built with;
// TestMutants builds and runs it once per mutant. Rates measured on
// 2026-10-27 (README); seeds give a miss chance under 5%. The faithful
// app passes every one of these configurations (TestFaithful's claims).
func TestMutant(t *testing.T) {
	mutant := qtest.Mutant()
	if mutant == "" {
		t.Skip("the app as it is: TestMutants runs this test built with each mutant's overlay")
	}
	qtest.RequireQuint(t)
	switch mutant {
	case "blind":
		// Claim 1: the race is found in the unsafe variant. Two clients
		// creating counters in one tenant, no faults, names from three
		// letters: a second create of a name commits where the model's
		// precondition is false. At ReadCommitted (PostgreSQL's default) the
		// loser of the race for the head waits for its lock and commits
		// blind: 25 of 40 runs (4 seeds miss with chance 2%). Under
		// Snapshot and Serializable a loser whose append overlaps the
		// winner's gets ErrConflict, a transient error whose retry decides
		// again on fresh reads, but one that read before the winner's
		// commit and began its append after it commits blind: no level
		// protects an Unconditioned decision (grist's
		// TestUnconditionedAtEveryLevel). The window is narrow at this
		// shape (0 of 120 runs in three configurations; 2 of 60 with four
		// clients creating three each), so the claim is ReadCommitted's.
		// TestFaithfulAtClaim1 is its contrast
		claim1 := gristsim.Config{Isolation: qenv.ReadCommitted, Tenants: []string{"t1"}, Send: []string{"create-counter"}}
		x := system(commits, claim1).
			WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 8, ShortSeeds: 4})
		x.Expect(t, qtest.Some(qtest.Rejects("createCounter")))
		// the rejection says why (H2), in grist's terms: the blind commit's
		// condition, which an append after its reads broke
		explained(t, x, "createCounter", "conditionHolds(log, readPos, reads_)", "a concurrent append")
		// the same on the SQL tier (sqlstore over qsql at read committed:
		// the loser of the race for the head lock waits for it, then
		// commits blind): 25 of 40 runs, 4 seeds miss with chance 2%.
		// Full mode: the race is the runtime's, on every tier
		if testing.Short() {
			return
		}
		claim1.SQL = true
		system(commits, claim1).
			WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 8, ShortSeeds: 4}).
			Expect(t, qtest.Some(qtest.Rejects("createCounter")))
	case "dispatchDirect":
		// Claim 4: grain's processor duplicate is caught. One tenant, creates
		// and increments: the average's step records no event where the
		// model's action appends one, and the raw append shifts every later
		// head. Without faults in 15 of 30 runs (5 seeds miss with chance
		// 3%), with commit faults and crashes in 22 of 30 (4 seeds: 0.5%)
		cfg := gristsim.Config{Tenants: []string{"t1"}, Send: []string{"create-counter", "increment-counter"}}
		system(commits, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 8, ShortSeeds: 5}).
			Expect(t, qtest.Some(qtest.Mismatch("average")))
		cfg.CommitFaults = true
		system(commits, cfg).Explore(t, qtest.ExploreConfig{Seeds: 8, ShortSeeds: 4}).
			Expect(t, qtest.Some(qtest.Mismatch("average")))
		// the same on the SQL tier, without faults: 18 of 30 runs (4 seeds
		// miss with chance 3%). Full mode: the raw append is the mutant's,
		// on every tier
		if testing.Short() {
			return
		}
		cfg.CommitFaults, cfg.SQL = false, true
		system(commits, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 8, ShortSeeds: 4}).
			Expect(t, qtest.Some(qtest.Mismatch("average")))
	case "checkpointFirst":
		// The welcome declared at most once while the model says at least
		// once: its checkpoint step comes before any effect step of its
		// trigger, which atLeastOnceDone refuses. One client creating in
		// one tenant, no faults: every run that creates a counter, 10 of 10.
		// What it shows is that declaration's contradiction of the model
		// (the faithful app's runs here reject nothing, TestWelcomeDuplicate's
		// without faults), not a lost mail: the at-most-once loss is the
		// payout's claim (TestPayoutAtMostOnce)
		cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 3, Send: []string{"create-counter"}}
		system(commits, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 10, ShortSeeds: 2}).
			Expect(t, qtest.Some(qtest.Rejects("welcome")))
	case "silentPayout":
		// The payout without its success event: a payment made leaves no
		// paid-out. The model's outcome is optional (a crash after the
		// payment loses it, by right), so the retiring binding admits every
		// run; the test-only everyPayoutOutcomeLogged rejects every run that
		// paid, without faults 6 of 6
		system(retiring, retirements).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 6, ShortSeeds: 2}).
			Expect(t, qtest.Conforms())
		system(exactly, retirements).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 6, ShortSeeds: 2}).
			Expect(t, qtest.Some(qtest.Violates(validate.EventuallyName("everyPayoutOutcomeLogged"))))
	case "anyoneRetires":
		// retire-counter without its creator check: a client retires the
		// other's counter, which the model's ok requires createdBy of
		// (the step's caller): Some(Rejects("retireCounter")), without
		// faults 6 of 6
		system(retiring, contested).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 6, ShortSeeds: 2}).
			Expect(t, qtest.Some(qtest.Rejects("retireCounter")))
	case "wrongAmount":
		// the payout paying one more than the retired total: a request the
		// model's rule (payoutBankPay) does not make, judged wherever it is
		// recorded. Without faults at paying's shape (every run retires) the
		// bank's served record comes first (it is recorded inside the call,
		// before the payout's completion): every run rejects bankPayServed.
		system(retiring, paying).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 4, ShortSeeds: 1}).
			Expect(t, qtest.All(qtest.Rejects("bankPayServed")))
		// the bank's answer lost and the node crashed while the payout
		// waits: the payout's call stays pending (at most once: never made
		// again), so the bank's record is the only one of the wrong payment.
		// Every run rejects bankPayServed; with the site judging no request
		// (as slice 2 built it) every run conformed
		system(retiring, payOnce).Explore(t, qtest.ExploreConfig{Seeds: 4, ShortSeeds: 1, Chooser: lostThenCrash}).
			Expect(t, qtest.All(qtest.Rejects("bankPayServed")))
		// the request lost: the bank applies nothing, and the payout's own
		// step (outcome unknown) is the only record of what it asked, as on
		// a tier where the dependency is a third party's (no site): every
		// run rejects bankPay
		system(retiring, payOnce).Explore(t, qtest.ExploreConfig{Seeds: 4, ShortSeeds: 1, Chooser: scripted(
			qenv.Scripted{Kind: "rpc.bank.request", Name: "drop"})}).
			Expect(t, qtest.All(qtest.Rejects("bankPay")))
		// the same shape over HTTP on the real tier (lostThenKilled): the
		// bank, behind grist.ServeCalls, makes the payment and its answer
		// is lost as the node is killed, so the bank's own step file holds
		// the payment's only record, judged against payoutBankPay there;
		// TestRealLostThenKilled is its contrast. Full mode: the site's
		// request is judged by the action lostThenCrash rejects above, and
		// that the bank's file joins the caller's trace is TestRealHTTP's
		// (its site claim compares each answer with the bank's record)
		if testing.Short() {
			return
		}
		tier := lostThenKilled(t)
		pendingPayment(t, tier)
		qtest.Validate(t, onReal, tier.Steps()).Expect(t, qtest.Rejects("bankPayServed"))
	case "payTwice":
		// the at-most-once payout calling the bank again after an unknown
		// answer: on the real tier (one counter created and retired, the
		// bank losing every answer after paying) the retry is the bank's
		// replay, nothing is paid twice, and the store's records are the
		// faithful app's; the payout's own steps, in the nodes' steps file,
		// hold two calls of one at-most-once key. TestRealHTTP's third
		// phase is its contrast
		tier := realTier(t, gristtest.TierConfig{DependencyLoseEvery: 1})
		tier.Start("node1")
		retireAll(tier, 1, 1)
		tier.Settle(nil)
		if tier.LostCalls() == 0 {
			t.Fatal("no bank answer lost: the retry was never made")
		}
		paidOnce(t, tier, 1)
		qtest.Validate(t, onReal, tier.Steps()).Expect(t, qtest.Rejects("bankPay"))
	case "pidIdentity":
		// GR10 slice 1: two pods recording as one node (as <app>-<pid> named
		// both, pid 1 in every container) number one recorder twice: the
		// merged files get Reconstruct's duplicate error, and the report
		// fails on it alone, no verdict of the model's to read
		// (TestRealTwoPods is the faithful app's contrast)
		steps := twoPods(t)
		d := duplicates(steps)
		if len(d) == 0 {
			t.Fatal("two pods as one node: no duplicate error in the merged files")
		}
		t.Logf("%d duplicate errors, the first: %s", len(d), d[0])
		qtest.Validate(t, commits, steps).Expect(t, qtest.HasIssue("duplicate"))
	case "stopNodeAtSignal":
		// GR10 slice 2: the node's work cancelled at SIGTERM (as grist's
		// Main once cancelled it): a payout whose bank call is in flight at
		// a node's signal is cancelled, its outcome unknown though the bank
		// was asked, in some run of the rolling restart, where the faithful
		// app's are none (TestRollingRestart): 6 of 20 runs, 2 of -short's
		// 10 (10 seeds at that rate miss with chance 4%)
		system(terminating, rolling).WithRates(lagging).Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 10}).
			Expect(t, qtest.Some(qtest.Violates("noPayoutUnknown")))
	case "noDrainDelay":
		// GR10 slice 2: no delay before the listener closes, while the
		// router still sends the node requests for its lag: their tries are
		// refused (each retried under its key, so the answers conform), in
		// some run of the rolling restart, where the faithful app's are
		// none. With the router's lag off nothing needs the delay: every
		// run is free of refusals, the mutant's too
		system(calls, rolling, qtest.Linearize()).WithRates(lagging).Explore(t, qtest.ExploreConfig{Seeds: 10, ShortSeeds: 4}).
			Expect(t, qtest.Conforms(), qtest.Some(gristtest.ClientErrors()))
		system(calls, rolling, qtest.Linearize()).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 10, ShortSeeds: 4}).
			Expect(t, qtest.Conforms(), gristtest.NoClientErrors())
		// the same on the real tier, three pods of the binary behind a
		// router polling /readyz every 200 ms: a pod's listener closes at
		// its signal, and the router sends it requests until its next poll.
		// Three rounds of the rolling restart (nine drains, each without
		// the delay a second or two): TestRealRollingRestart's one round
		// of the faithful app has no refused try. Full mode, as that is
		if testing.Short() {
			return
		}
		steps, _ := rollingRestart(t, 3)
		t.Logf("nine drains without the delay: %d calls refused a try", refusedCalls(steps))
		qtest.Validate(t, calls, steps, qtest.Linearize()).Expect(t, qtest.Conforms(), gristtest.ClientErrors())
	default:
		t.Fatalf("mutant %s: say what catches it (a case of TestMutant)", mutant)
	}
}

// Each mutant, built with its overlay, is caught as TestMutant says.
func TestMutants(t *testing.T) {
	t.Parallel() // its explorations record into their own runs
	qtest.Mutants{FullOnly: fullOnly, Show: []string{"calls refused a try"}}.Run(t)
}

// explained fails t unless a run of x rejects action with an explanation
// (validate.Failure.Why) naming cond among its false conditions and whose
// grist reading says reading: what the report of a rejection says, not
// its verdict (Expect's).
func explained(t *testing.T, x *qtest.Exploration, action, cond, reading string) {
	t.Helper()
	for _, r := range x.Runs {
		if r.Report == nil {
			continue
		}
		for _, a := range r.Report.Actors {
			f := a.Failure
			if f == nil || f.Kind != "disabled" || f.Step == nil || f.Step.Action != action || f.Why == nil {
				continue
			}
			for _, c := range f.Why.Conditions {
				if c.Expr == cond && strings.Contains(f.Why.Reading, reading) {
					return
				}
			}
		}
	}
	t.Errorf("no rejection of %s explained by %s (%q): the explanation of a rejected step (validate.Explanation) lost it", action, cond, reading)
}

package counters

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// mutants are grain's two bugs, an effect checkpointed first, a payout
// without its outcome, a retirement by anyone and a payout of the wrong
// amount, each a directory of testdata/mutants/
// whose files replace the app's of the same name (a go build overlay):
// the app's own files stay as they are, and each mutant is a whole file
// to diff against them.
var mutants = []string{"blind", "dispatchDirect", "checkpointFirst", "silentPayout", "anyoneRetires", "wrongAmount"}

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
	mutant := gristtest.Mutant()
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
		system(commits, claim1).
			WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(8, 4)}).
			Expect(t, qtest.Some(qtest.Rejects("createCounter")))
		// the same on the SQL tier (sqlstore over qsql at read committed:
		// the loser of the race for the head lock waits for it, then
		// commits blind): 25 of 40 runs, 4 seeds miss with chance 2%.
		// Full mode: the race is the runtime's, on every tier
		if testing.Short() {
			return
		}
		claim1.SQL = true
		system(commits, claim1).
			WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(8, 4)}).
			Expect(t, qtest.Some(qtest.Rejects("createCounter")))
	case "dispatchDirect":
		// Claim 4: grain's processor duplicate is caught. One tenant, creates
		// and increments: the average's step records no event where the
		// model's action appends one, and the raw append shifts every later
		// head. Without faults in 15 of 30 runs (5 seeds miss with chance
		// 3%), with commit faults and crashes in 22 of 30 (4 seeds: 0.5%)
		cfg := gristsim.Config{Tenants: []string{"t1"}, Send: []string{"create-counter", "increment-counter"}}
		system(commits, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(8, 5)}).
			Expect(t, qtest.Some(qtest.Mismatch("average")))
		cfg.CommitFaults = true
		system(commits, cfg).Explore(t, qtest.ExploreConfig{Seeds: seeds(8, 4)}).
			Expect(t, qtest.Some(qtest.Mismatch("average")))
		// the same on the SQL tier, without faults: 18 of 30 runs (4 seeds
		// miss with chance 3%). Full mode: the raw append is the mutant's,
		// on every tier
		if testing.Short() {
			return
		}
		cfg.CommitFaults, cfg.SQL = false, true
		system(commits, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(8, 4)}).
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
		system(commits, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(10, 2)}).
			Expect(t, qtest.Some(qtest.Rejects("welcome")))
	case "silentPayout":
		// The payout without its success event: a payment made leaves no
		// paid-out. The model's outcome is optional (a crash after the
		// payment loses it, by right), so the retiring binding admits every
		// run; the test-only everyPayoutOutcomeLogged rejects every run that
		// paid, without faults 6 of 6
		system(retiring, retirements).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(6, 2)}).
			Expect(t, qtest.Conforms())
		system(exactly, retirements).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(6, 2)}).
			Expect(t, qtest.Some(qtest.Violates(validate.EventuallyName("everyPayoutOutcomeLogged"))))
	case "anyoneRetires":
		// retire-counter without its creator check: a client retires the
		// other's counter, which the model's ok requires createdBy of
		// (the step's principal): Some(Rejects("retireCounter")), without
		// faults 6 of 6
		system(retiring, contested).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(6, 2)}).
			Expect(t, qtest.Some(qtest.Rejects("retireCounter")))
	case "wrongAmount":
		// the payout paying one more than the retired total: the bank's call,
		// recorded where the payout makes it, asks for an amount the model's
		// rule (payoutBankPay) does not, Some(Rejects("bankPay")), without
		// faults 4 of 4 runs (every run that retires); TestPayoutAmount is
		// its contrast
		system(retiring, retirements).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(4, 2)}).
			Expect(t, qtest.Some(qtest.Rejects("bankPay")))
	default:
		t.Fatalf("unknown mutant %q", mutant)
	}
}

// Each mutant, built with its overlay, is caught as TestMutant says.
func TestMutants(t *testing.T) {
	t.Parallel() // its explorations record into their own runs
	gristtest.Mutants{Names: mutants, FullOnly: fullOnly}.Run(t)
}

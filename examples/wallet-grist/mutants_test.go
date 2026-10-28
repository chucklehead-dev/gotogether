package walletgrist

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// mutants are examples/wallet's mutants that grist lets an application
// write, and two of the ledger's, each a directory of testdata/mutants/
// whose files replace the app's of the same name (a go build overlay).
// NoDedupe and UnlockedRecord are not among them: the request record is
// grist's marker, which no application code reads or writes. WriteSkew
// is readThenWrite's skew shape: a conditioned read cannot be left out of
// the condition, so the only skew an application can write is a whole
// command Unconditioned.
var mutants = []string{"noValidation", "readThenWrite", "noOutbox", "ledgerNoDedupe", "ledgerAcksUnposted", "replicaRead"}

// TestMutant states what catches the mutant this binary was built with;
// TestMutants builds and runs it once per mutant. The faithful app passes
// every one of these configurations (sim_test.go's claims).
func TestMutant(t *testing.T) {
	mutant := gristtest.Mutant()
	if mutant == "" {
		t.Skip("the app as it is: TestMutants runs this test built with each mutant's overlay")
	}
	qtest.RequireQuint(t)
	switch mutant {
	case "noValidation":
		// requests the bank must refuse, applied: an ok where the model's
		// precondition says invalid (TestInvalidRequests is the contrast)
		system(commits, invalid).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(6, 3)}).
			Expect(t, qtest.Some(qtest.Rejects("transfer")))
	case "readThenWrite":
		// at read committed both of a race's transfers spend what one saw:
		// a commit the funds do not cover (a lost update), and a credit
		// line overdrawn by one transfer from each of its accounts (the
		// write skew's effect); TestRaces is the contrast
		system(commits, race).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(20, 8)}).
			Expect(t, qtest.Some(qtest.Rejects("transfer")))
		system(commits, skew).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(20, 8)}).
			Expect(t, qtest.Some(qtest.Rejects("transfer")))
		// Under Serializable the race is narrower but open: a decision's
		// reads come before its append's transaction, so no isolation
		// level protects an Unconditioned one (grist's
		// TestUnconditionedAtEveryLevel). A loser whose append overlaps the
		// winner's gets a transient conflict and decides again; one that
		// read before the winner's commit and began its append after it
		// commits the overdraft (readPos 0, committed at head 4): 7 of 180
		// runs at the race shape, so 80 seeds miss with chance 4%. Full
		// mode: the cost is the seeds
		if testing.Short() {
			return
		}
		ser := race
		ser.Isolation = qenv.Serializable
		system(commits, ser).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 80}).
			Expect(t, qtest.Some(qtest.Rejects("transfer")))
	case "noOutbox":
		// the post at most once: its checkpoint commits before any effect
		// step, which the at-least-once model refuses (atLeastOnceDone)
		system(commits, deposits).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(6, 2)}).
			Expect(t, qtest.Some(qtest.Rejects("post")))
	case "ledgerNoDedupe":
		// a ledger double posting every try: after a crash between the post
		// and the checkpoint the restarted node posts again under the same
		// key, and the ledger posts twice, a second served record of the
		// key, which the generated ledgerPostAdmits refuses (PostEntry is
		// Keyed; no rule of the app's says so). Every run deposits and
		// crashes so: every run rejects, at one seed per push
		system(commits, deposits).Explore(t, qtest.ExploreConfig{Seeds: seeds(3, 1), Chooser: crashAfterPost}).
			Expect(t, qtest.All(qtest.Rejects("ledgerPostServed")))
	case "ledgerAcksUnposted":
		// a proxy answering every post booked without posting: every step
		// the model's actions judge is admitted (the call's answer is the
		// ledger's to give, the effect ok, the checkpoint after it), and
		// postedApplied holds of no posts at all. Only the generated
		// obligation ledgerPostEventuallyServed (every answered call of a
		// Keyed dependency has its served record) says the ledger must
		// have posted what it answered: no val of the default binding written
		// by hand catches it (the test-only everyMovePostedOnce would, in
		// runs without the ledger's faults)
		system(commits, deposits).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(3, 1)}).
			Expect(t, qtest.All(qtest.Settles()), qtest.All(qtest.Violates(validate.EventuallyName("ledgerPostEventuallyServed"))),
				qtest.None(qtest.Violates(validate.EventuallyName("postEventuallyHandled"))), qtest.None(qtest.Violates(validate.EventuallyName("postedApplied"))))
	case "replicaRead":
		// a balance read from the ledger's postings misses a deposit not
		// posted yet
		system(commits, depositsAndReads).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(10, 4)}).
			Expect(t, qtest.Some(qtest.Mismatch("read")))
	default:
		t.Fatalf("unknown mutant %q", mutant)
	}
}

// Each mutant, built with its overlay, is caught as TestMutant says.
func TestMutants(t *testing.T) {
	t.Parallel() // its explorations record into their own runs
	gristtest.Mutants{Names: mutants}.Run(t)
}

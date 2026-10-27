package library

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// TestMutant states what catches the mutant this binary was built with
// (testdata/mutants/<name>: files replacing the app's of the same name);
// TestMutants builds and runs it once per mutant.
func TestMutant(t *testing.T) {
	mutant := gristtest.Mutant()
	if mutant == "" {
		t.Skip("the app as it is: TestMutants runs this test built with each mutant's overlay")
	}
	qtest.RequireQuint(t)
	switch mutant {
	case "noCopyCheck":
		// the shelf never read: a borrow with no copy free commits where the
		// model's rule refuses it (TestScarce is the contrast)
		system(commits, scarce).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(6, 3)}).
			Expect(t, qtest.Some(qtest.Rejects("borrow")))
	case "staleLimit":
		// the borrow Unconditioned: two borrows of the member's last loan both
		// commit, the second where the model's rule says limit (TestGreedy is
		// the contrast)
		system(commits, greedy).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(10, 6)}).
			Expect(t, qtest.Some(qtest.Rejects("borrow")))
	case "noDedupe":
		// the mailer without its dedupe (a double sending every try): the
		// receipt mailed again after the scripted crash is a second served
		// record of its key, which the generated mailerSendAdmits refuses
		// (Send is Keyed; no rule of the app's says so; TestReceiptOnce is
		// the contrast)
		system(commits, lending).Explore(t, qtest.ExploreConfig{Seeds: seeds(6, 3), Chooser: crashAfterMail}).
			Expect(t, qtest.Some(qtest.Rejects("mailerSendServed")))
	case "wrongReceipt":
		// the receipt's member and book swapped: a receipt for no borrow,
		// refused where the effect asks for it (mailerSend: the request is
		// not receiptsMailerSend's for the trigger). A rejected run is not
		// one Violates(everyReceiptForABorrow) claims (a conforming run's
		// violation): grist records the mailer's record with the bytes its
		// caller sent, so the caller's rule refuses every such run (TestFaithful
		// is the contrast)
		system(commits, lending).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(6, 3)}).
			Expect(t, qtest.Some(qtest.Rejects("mailerSend")))
	default:
		t.Fatalf("unknown mutant %q", mutant)
	}
}

// Each mutant, built with its overlay, is caught as TestMutant says.
func TestMutants(t *testing.T) {
	t.Parallel()
	gristtest.Mutants{Names: []string{"noCopyCheck", "staleLimit", "noDedupe", "wrongReceipt"}}.Run(t)
}

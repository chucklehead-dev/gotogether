package payouts

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// TestMutant states what catches the mutant this binary was built with:
// noDuplicateCheck (testdata/mutants: request takes a payout's id twice)
// and freshKey (generated, gristtest.PeerMutants: each try of the bank's
// pay under a new command id, as a client minting a request id per
// request does). TestMutants builds and runs it once per mutant. The
// faithful pair passes every one of these configurations (TestFaithful).
func TestMutant(t *testing.T) {
	mutant := qtest.Mutant()
	if mutant == "" {
		t.Skip("the app as it is: TestMutants runs this test built with each mutant's overlay")
	}
	qtest.RequireQuint(t)
	cfg := qtest.ExploreConfig{Seeds: 8, ShortSeeds: 4}
	switch mutant {
	case "freshKey":
		// a try whose answer was lost, sent again under another id, is a
		// second payment at the bank: its model's invariant is violated,
		// and Links finds a bank commit whose id names payouts that no call
		// has. The payouts' own model sees nothing: its calls are as the
		// faithful's, each try under the trigger's key
		// Force the lost reply after a valid payment: mutant detection
		// must not depend on random workload/fault choices reaching it.
		lostBankReply(atBank).Explore(t, lostBankReplyConfig()).Expect(t,
			qtest.All(qtest.Violates("onePaymentPerPayout")), qtest.Reaches("rpc.bank.response").Faulted().InAtLeast(1))
		e := lostBankReply(commits).Explore(t, lostBankReplyConfig())
		e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("rpc.bank.response").Faulted().InAtLeast(1))
		if n := linked(t, e); n != len(e.Runs) {
			t.Errorf("freshKey: Links found %d of %d runs unlinked, want every lost-reply run", n, len(e.Runs))
		}
	case "noDuplicateCheck":
		// a payout's id requested twice is two requests, each sent: the
		// payouts' model rejects the second request (its rule refuses it
		// as duplicate), and the bank pays the payout twice, both under
		// keys of their own: its invariant violated, the two sides still
		// linked (every commit a call's). The bank's invariant needs its
		// callers' rule: Linked says the two sides agree, not that either
		// is right
		quiet := system(commits, pair).WithRates(qenv.NoFaults)
		quiet.Explore(t, cfg).Expect(t, qtest.Some(qtest.Rejects("request")))
		quiet.WithBinding(atBank).Explore(t, cfg).Expect(t, qtest.Some(qtest.Violates("onePaymentPerPayout")))
		if n := linked(t, quiet.Explore(t, cfg)); n != 0 {
			t.Errorf("noDuplicateCheck: Links found %d runs unlinked: want the two sides agreeing", n)
		}
	default:
		t.Fatalf("mutant %s: say what catches it (a case of TestMutant)", mutant)
	}
}

// Each mutant, built with its overlay (testdata/mutants) or generated
// (freshKey, gristtest.PeerMutants), is caught as TestMutant says.
func TestMutants(t *testing.T) {
	t.Parallel()
	qtest.Mutants{Generated: gristtest.PeerMutants(App)}.Run(t)
}

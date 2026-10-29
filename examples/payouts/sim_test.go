package payouts

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/payouts/bank"
	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// The bindings: each App's store side, generated from its catalog. One
// run records both Apps, each under its own actors (payouts/<tenant>,
// bank/<tenant>), so each binding judges its own side of the same runs.
const (
	commits = "payouts.binding.yaml"
	atBank  = "bank/bank.binding.yaml"
)

// faulty loses, duplicates and delays the bank's requests and answers
// (its API's, rpc.bank.*) at 1 in 5, crashes either App's node at 1 in
// 100 scheduling steps, and loses the answer of 1 in 10 commits at
// either store; every other fault at the default rate.
var faulty = qenv.DefaultRates.Set("rpc.bank", 0.2).Set("nemesis", 0.01).Set("store", 0.1)

// pair is the payouts service and its bank, one tenant, two clients
// requesting payouts a, b and z into accounts a, b and z (the bank holds
// a and b) of 0 to 2 cents.
var pair = gristsim.Config{Tenants: []string{"t1"}, Clients: 2, Ops: 4, CommitFaults: true, Send: []string{"request"},
	Alphabet: []string{"a", "b", "z"}, Ints: []int{0, 1, 2}, Peers: []*grist.App{bank.App}}

// system is the pair judged by binding at faulty rates (a
// gristsim.System: the mutant this binary was built with, if any).
func system(binding string, cfg gristsim.Config) qtest.System {
	return gristsim.System(binding, App, cfg).WithRates(faulty)
}

// linked is how many of e's runs Links finds a problem in, each logged.
func linked(t *testing.T, e *qtest.Exploration) int {
	t.Helper()
	n := 0
	for i, r := range e.Runs {
		if p := gristtest.Links(App, bank.App, r.Recorded); len(p) > 0 {
			n++
			t.Logf("run %d: %s", i+1, p[0])
		}
	}
	return n
}

// retried is how many of e's runs called the bank twice under one key:
// a try whose answer was lost, or whose node crashed, sent again.
func retried(e *qtest.Exploration) int {
	n := 0
	for _, r := range e.Runs {
		tries := map[any]int{}
		for _, s := range r.Recorded {
			if s.Action == "bankPay" && s.Invocation {
				tries[s.Args["key"]]++
			}
		}
		for _, c := range tries {
			if c > 1 {
				n++
				break
			}
		}
	}
	return n
}

// TestFaithful: through the bank's lost and late answers, both nodes'
// crashes and both stores' lost commit answers, the pair conforms on both
// sides, each judged by its own model: the payouts' (every payout sent
// once, each call's request its rule's, under its trigger's key) and the
// bank's (no payout paid twice, an unknown account refused). The runs
// settle (every request handled), reach the bank's lost answers, retry
// some call under its key (bankPay taken more than once in some run), and
// the two sides agree (gristtest.Linked: every answer the payouts saw is
// the bank's commit of its key, every bank commit a call's).
func TestFaithful(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	cfg := qtest.ExploreConfig{Seeds: 20, ShortSeeds: 4}
	e := system(commits, pair).Explore(t, cfg)
	e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("rpc.bank.response").Faulted(), qtest.Reaches("nemesis.crash").Faulted(),
		gristtest.Catalog(App).Exercised())
	for _, r := range e.Runs {
		gristtest.Linked(t, App, bank.App, r.Recorded)
	}
	if retried(e) == 0 {
		t.Errorf("no run called the bank twice under one key over %d runs: the marker's answer to a retry was never exercised", len(e.Runs))
	}
	system(atBank, pair).Explore(t, cfg).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), gristtest.Catalog(bank.App).Exercised())
}

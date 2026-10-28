package counters

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// terminating judges runs under graceful terminations: the retiring
// binding's actions, and the test-only noPayoutUnknown.
const terminating = "counters.terminating.binding.yaml"

// slowBank is the team's bank (it pays each key once, replays a retry,
// declines over 100) taking 900 ms of virtual time per payment, under
// gristsim.DependencyTimeout: a payout's call is in flight long enough
// for a node's termination to land in it.
func slowBank() gristsim.Doubles {
	type answer struct {
		rc  PayReceipt
		err error
	}
	var mu sync.Mutex
	first := map[string]answer{}
	return gristsim.Doubles{Bank: grist.Handlers{
		Pay.Handler(func(ctx context.Context, r PayRequest) (PayReceipt, error) {
			qenv.Sleep(ctx, 900*time.Millisecond)
			key := grist.CallKey(ctx)
			mu.Lock()
			defer mu.Unlock()
			if a, ok := first[key]; ok {
				grist.Replayed(ctx)
				return a.rc, a.err
			}
			a := answer{rc: PayReceipt{Ref: key}}
			if r.Amount > 100 {
				a = answer{err: ErrDeclined}
			}
			first[key] = a
			return a.rc, a.err
		}),
	}}
}

// rolling is a rolling restart of two nodes under load: two clients
// creating, incrementing, showing and retiring counters a and b, a
// second apart, while each node in turn is sent SIGTERM and drains as
// the app's binary does (Drain: 5 s of serving, not ready, before its
// listener closes), the router in front learning of each change up to
// 3 s late (route.api.lag), at most one node down at once (a
// PodDisruptionBudget of one). The bank is slow (slowBank), so payouts
// are in flight at the signals. Two nodes, not three: a signal lands on
// the node running a payout one time in two (with three, one in three,
// and the stopNodeAtSignal mutant's runs showed an unknown payout 5
// times in 40, where with two they did 12 in 40; 8 in 30 since the
// rollout stopped waiting for the router).
var rolling = gristsim.Config{Tenants: []string{"t1"}, Nodes: 2, Clients: 2, Ops: 14, Alphabet: []string{"a", "b"}, Doubles: slowBank,
	Send:    []string{"create-counter", "increment-counter", "retire-counter", "retire-counter", "show-counter"},
	Cluster: &gristsim.Cluster{Rolling: true, EndpointLag: 3 * time.Second, Disruptions: 1, Drain: Drain}}

// lagging is the rolling restart's faults: the router's view stale at
// half its changes, nothing else.
var lagging = qenv.NoFaults.Set("route.api.lag", 0.5)

// A rolling restart under load, the faithful app: every run conforms
// and settles; at the clients, every call is answered, linearizes, and
// none had a try refused (NoClientErrors: the drain's delay outlasts the
// router's lag); no payout's outcome is unknown (each in flight at a
// signal finishes: grist stops the node's work after its drain's
// settle). With the drain's delay off (the noDrainDelay mutant) the
// router sends tries to a node that closed its listener; with the node's
// work cancelled at the signal (stopNodeAtSignal) a payout in flight
// ends unknown (TestMutant). Without the router's lag (route.api.lag
// off) the delay is needed by nothing: noDrainDelay passes
// NoClientErrors too.
func TestRollingRestart(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	n := qtest.ExploreConfig{Seeds: 10, ShortSeeds: 3}
	system(retiring, rolling).WithRates(lagging).Explore(t, n).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("route.api.lag").Faulted(), qtest.Takes("bankPay").Outcome("ok"))
	system(calls, rolling, qtest.Linearize()).WithRates(lagging).Explore(t, n).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Completes()), gristtest.NoClientErrors())
	system(terminating, rolling).WithRates(lagging).Explore(t, n).
		Expect(t, qtest.None(qtest.Violates("noPayoutUnknown")))
}

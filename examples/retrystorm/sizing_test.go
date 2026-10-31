package retrystorm

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qmeta"
)

// Sizing from the chain (GR10 slice 6's qmeta.LimitFor and Size) on the
// papers' system: the highest load each policy recovers at from the
// spike's shape (half again the load for a minute) ranks them as the
// recovery times do (the budget highest, backoff alone no better than
// retrying at once), retrying at once needs more servers than the budget
// for the same spike, and the simulation ranks the ends alike where it
// tips (the budget recovering, backoff alone staying degraded). Orderings,
// not the chain's rates: its limits are below the simulation's.
func TestSizedFromTheChain(t *testing.T) {
	if testing.Short() {
		t.Skip("a dozen transient analyses and a dozen simulations")
	}
	healthy := qmeta.Healthy{MaxU: 5}.Contains // in the chain's units of 2
	within := 120.0
	limit := map[string]float64{}
	for _, p := range Policies[:3] {
		q := p.Params(0)
		q.Unit = 2
		limit[p.Name] = qmeta.LimitFor(q, 1.5, 60, healthy, within, 0.9, ServiceRate)
		t.Logf("%-9s recovers from 1.5x for 60 s up to λ=%.2f/s", p.Name, limit[p.Name])
	}
	if !(limit["budget"] > limit["immediate"] && limit["immediate"] >= limit["backoff"]) {
		t.Fatalf("limits %v: want budget > immediate >= backoff", limit)
	}
	workers := map[string]int{}
	for _, p := range []Policy{Policies[0], Policies[2]} {
		q := p.Params(8)
		q.Unit = 2
		workers[p.Name] = qmeta.Size(q, qmeta.Burst{Rate: 12, Duration: 60}, healthy, within, 0.9, 4).Workers
	}
	t.Logf("servers for 8/s and a spike to 12/s for a minute: %v", workers)
	if workers["budget"] == 0 || workers["immediate"] != 0 && workers["immediate"] <= workers["budget"] {
		t.Fatalf("servers %v: want the budget to need fewer than retrying at once", workers)
	}
	// the simulation ranks the ends alike where it tips: the budget
	// recovering, backoff alone staying degraded (TestStormAfterSpike's
	// spike). Its limits are not the chain's (the chain's rates and times
	// are its own, DESIGN §11.3): the claim is the order
	budget, err := Recoveries(Policies[2], 9, Spike, 6)
	if err != nil {
		t.Fatal(err)
	}
	backoff, err := Recoveries(Policies[1], 9, Spike, 6)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("simulated at 9/s after %v: budget %d of 6 recover, backoff alone %d of 6", Spike, budget, backoff)
	if budget < 4 || backoff > 2 {
		t.Fatalf("simulated at 9/s: budget %d of 6, backoff %d of 6: want most and few", budget, backoff)
	}
}

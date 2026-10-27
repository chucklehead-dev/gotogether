package counters

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// The HTTP tier: claim 2 again with the clients calling the node's HTTP
// API (grist.NewHandler on a qhttp.Server, grist/client over qhttp's
// Transport: gristsim.Config.HTTP), so what they see is the API's codes
// (a 503 with Retry-After for an unknown outcome, 422 and 409 for
// refusals) and the network's faults are qhttp's: requests dropped,
// duplicated and late, answers lost, the node crashing with requests in
// flight. One run is judged by both bindings: the store's steps and the
// clients' calls, linearized.
func TestHTTPTier(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	cfg := contended
	cfg.HTTP = true
	sys := system(commits, cfg)
	n := qtest.ExploreConfig{Seeds: seeds(20, 3)}
	sys.Explore(t, n).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()),
			qtest.Reaches("rpc.api.response").Faulted(), qtest.Reaches("nemesis.crash").Faulted())
	sys.WithBinding(calls, qtest.Linearize()).Explore(t, n).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
}

// Duplicated and late requests over HTTP: a key's first answer is final
// at the API too (its Idempotency-Key is the command id), at the store
// and at the clients. Full mode: the markers' mechanism is the runtime's,
// checked per push by TestDuplicateRequests.
func TestHTTPDuplicateRequests(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("full mode: TestDuplicateRequests checks the markers per push")
	}
	qtest.RequireQuint(t)
	rates := qenv.NoFaults.Set("rpc.api.delay", 0.3).Set("rpc.api.request", 0.3)
	cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 3, Ops: 4, Alphabet: []string{"a", "b"}, HTTP: true,
		Send: []string{"create-counter", "increment-counter", "show-counter"}}
	sys := system(commits, cfg).WithRates(rates)
	n := qtest.ExploreConfig{Seeds: seeds(20, 3)}
	sys.Explore(t, n).Expect(t, qtest.Conforms(), qtest.Reaches("rpc.api.request").Faulted())
	sys.WithBinding(calls, qtest.Linearize()).Explore(t, n).Expect(t, qtest.Conforms())
}

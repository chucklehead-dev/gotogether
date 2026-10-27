package counters

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// The cost of judging one long run against the log model (docs/grist.md
// §10, open question 4): COUNTERS_COST=n runs one tenant's workload of n
// operations and logs the steps and the time the run and its validation
// took. An opt-in measurement, not a check.
func TestValidationCost(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("COUNTERS_COST"))
	if n == 0 {
		t.Skip("a measurement: COUNTERS_COST=<operations> runs it")
	}
	qtest.RequireQuint(t)
	start := time.Now() //qenv:ok the measurement's own wall time, outside the simulation
	x := system(commits, gristsim.Config{Tenants: []string{"t1"}, Clients: 4, Ops: n / 4,
		Send: []string{"create-counter", "increment-counter", "increment-counter", "show-counter"}}).
		WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1})
	x.Expect(t, qtest.Conforms())
	t.Logf("%d operations: %d steps, run and judged in %v", n, len(x.Runs[0].Steps), time.Since(start).Round(time.Millisecond)) //qenv:ok the measurement's wall time
}

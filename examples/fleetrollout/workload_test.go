package fleetrollout_test

import (
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/fleetrollout"
	"github.com/chucklehead-dev/gotogether/examples/fleetrollout/rollout"
	"github.com/chucklehead-dev/gotogether/examples/sktrace"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// production is SimKube's scaling trace: a real cluster's Deployments
// scaled up and down over 19 minutes.
func production(t *testing.T) *sktrace.Trace {
	t.Helper()
	tr, err := sktrace.ReadFile("../sktrace/testdata/dsb-scaling.sktrace")
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// The rollout with a production workload as another writer: the trace's
// scalings land on the members' Deployments (60 times faster) while the
// rollout patches them, through the same faults. The model needed a step
// for another writer (scale); with it, the faithful controller keeps the
// budget and finishes, and trusting a stale status is still caught (in
// 33 of seeds 1 to 60). -short runs 2 and 6 seeds.
func TestRolloutUnderProductionWorkload(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	tr := production(t)
	x := explore(t, fleetrollout.Config{Workload: tr}, seeds(30, 2))
	x.Expect(t, qtest.Conforms())
	scales := 0
	for _, r := range x.Runs {
		for _, s := range r.Steps {
			if s.Action == "scale" {
				scales++
			}
		}
	}
	if scales < len(x.Runs) {
		t.Errorf("%d scale steps over %d runs: the workload did not reach the fleet", scales, len(x.Runs))
	}
	explore(t, fleetrollout.Config{Workload: tr, Mutation: rollout.IgnoreObservedGeneration, NoCrashes: true}, seeds(20, 6)).
		Expect(t, qtest.Violates("withinBudget"))
}

// Pods that take as long as a production trace's: the CronJob trace's
// lifetimes (30 s and 45 s), 60 times faster, as the options of a pod's
// readiness. Rollouts take longer and status lags further behind; the
// faithful controller still conforms, and trusting a stale status is
// still caught (19 of 20 runs). -short runs 2 and 2 seeds.
func TestRolloutWithMeasuredPodTimes(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	tr, err := sktrace.ReadFile("../sktrace/testdata/cronjob.sktrace")
	if err != nil {
		t.Fatal(err)
	}
	var pods []time.Duration
	for _, d := range sktrace.Quantiles(tr.Lifetimes(""), 3) {
		pods = append(pods, d/60)
	}
	explore(t, fleetrollout.Config{PodTimes: pods}, seeds(20, 2)).Expect(t, qtest.Conforms())
	explore(t, fleetrollout.Config{PodTimes: pods, Mutation: rollout.IgnoreObservedGeneration, NoCrashes: true}, seeds(20, 2)).
		Expect(t, qtest.Violates("withinBudget"))
}

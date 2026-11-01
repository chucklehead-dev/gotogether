package qtest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/evidence"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestRunOneRetainsActualSimulationLimits(t *testing.T) {
	r := runOne(settings{}, false, qenv.Nominal{}, func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		s.MaxSteps, s.Hang = 2, 0
		s.Spawn("busy", func(ctx context.Context) {
			for {
				qenv.Yield(ctx)
			}
		})
		return s.Run(time.Minute)
	})
	if r.Err == nil || len(r.simulations) != 1 {
		t.Fatalf("run: err %v, observations %+v", r.Err, r.simulations)
	}
	call := r.simulations[0]
	if call.Runtime != "deterministic" || call.Horizon != "1m0s" || call.Scheduler == nil || call.Scheduler.MaxSteps != 2 || call.Scheduler.Hang != "0s" || !call.Scheduler.StepLimitReached {
		t.Fatalf("effective limits: %+v", call)
	}
}

func TestNativeSimulationLimitsAreNotReportedAsEnforced(t *testing.T) {
	r := runOne(settings{}, false, qenv.Nominal{}, func(c qenv.Chooser) error {
		s := qenv.NewNative(c, nil)
		defer s.Close()
		return s.Run(0)
	})
	if r.Err != nil || len(r.simulations) != 1 || r.simulations[0].Runtime != "native" || r.simulations[0].Scheduler != nil {
		t.Fatalf("native observations: %+v, err %v", r.simulations, r.Err)
	}
}

// Exact completion must remain successful all the way from the scheduler hook
// through the execution evidence consumed by JSON clients and the navigator.
func TestExactStepBudgetCompletionEvidence(t *testing.T) {
	r := runOne(settings{}, false, qenv.Nominal{}, func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		s.MaxSteps, s.Hang = 1, 0
		s.Spawn("once", func(context.Context) {})
		return s.Run(time.Second)
	})
	if r.Err != nil {
		t.Fatalf("completed run failed: %v", r.Err)
	}
	// Model checking is separate from runOne; supply the judged empty report
	// when constructing the exploration evidence fixture.
	r.Report = &validate.Report{}
	x := &Exploration{test: "TestExactBudget", id: 1, Runs: []*Run{r}}
	data, err := json.Marshal(x.Evidence())
	if err != nil {
		t.Fatal(err)
	}
	var view evidence.View
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Executions) != 1 || len(view.Executions[0].Simulations) != 1 {
		t.Fatalf("missing execution evidence: %s", data)
	}
	scheduler := view.Executions[0].Simulations[0].Scheduler
	if scheduler == nil || scheduler.MaxSteps != 1 || scheduler.StepsBefore != 0 || scheduler.StepsAfter != 1 || scheduler.StepLimitReached {
		t.Fatalf("incorrect completion evidence: %+v", scheduler)
	}
}

package qtest

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
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

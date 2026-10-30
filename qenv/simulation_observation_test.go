package qenv_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qenv"
)

func TestSimulationObservationsCaptureActualChangedLimits(t *testing.T) {
	collector := &hooks.Simulations{}
	ctx := hooks.WithSimulations(context.Background(), collector)
	s := qenv.New(qenv.WithContext(qenv.Nominal{}, ctx))
	defer s.Close()
	s.Hang = 0
	s.MaxSteps = 3
	s.Spawn("busy", func(ctx context.Context) {
		for {
			qenv.Yield(ctx)
		}
	})
	if err := s.Run(time.Second); !errors.Is(err, qenv.ErrMaxSteps) {
		t.Fatalf("first run: %v", err)
	}
	s.MaxSteps = 6
	if err := s.Run(2 * time.Second); !errors.Is(err, qenv.ErrMaxSteps) {
		t.Fatalf("second run: %v", err)
	}
	other := qenv.New(qenv.WithContext(qenv.Nominal{}, ctx))
	defer other.Close()
	if err := other.Run(0); err != nil {
		t.Fatal(err)
	}
	calls := collector.Calls()
	if len(calls) != 3 {
		t.Fatalf("calls: %+v", calls)
	}
	first, second := calls[0], calls[1]
	if first.ID != 1 || second.ID != 1 || calls[2].ID != 2 || first.MaxSteps != 3 || second.MaxSteps != 6 || first.Horizon != time.Second || second.Horizon != 2*time.Second || first.Hang != 0 {
		t.Fatalf("effective policies: %+v", calls)
	}
	if first.StepsBefore != 0 || first.StepsAfter != 3 || second.StepsBefore != 3 || second.StepsAfter != 6 || !first.StepLimitReached || !second.StepLimitReached || calls[2].StepLimitReached {
		t.Fatalf("actual outcomes: %+v", calls)
	}
	calls[0].MaxSteps = 999
	if collector.Calls()[0].MaxSteps != 3 {
		t.Fatal("snapshot mutated the collector")
	}
}

func TestNativeSimulationObservationDoesNotClaimScheduler(t *testing.T) {
	collector := &hooks.Simulations{}
	ctx := hooks.WithSimulations(context.Background(), collector)
	s := qenv.NewNative(qenv.WithContext(qenv.Nominal{}, ctx), nil)
	defer s.Close()
	s.MaxSteps = 0 // native Run does not enforce this deterministic limit
	if err := s.Run(0); err != nil {
		t.Fatal(err)
	}
	calls := collector.Calls()
	if len(calls) != 1 || !calls[0].Native || calls[0].StepLimitReached {
		t.Fatalf("native calls: %+v", calls)
	}
}

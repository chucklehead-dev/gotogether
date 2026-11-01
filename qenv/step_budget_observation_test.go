package qenv_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Observation reports actual exhaustion, rather than treating a step counter
// equal to its bound as proof that another scheduling step was needed.
func TestStepBudgetObservationsDistinguishTermination(t *testing.T) {
	for _, settle := range []bool{false, true} {
		operation := "run"
		if settle {
			operation = "settle"
		}
		for _, kind := range []string{"complete", "runnable", "deadline", "blocked", "horizon"} {
			t.Run(operation+"/"+kind, func(t *testing.T) {
				collector := &hooks.Simulations{}
				sink := &qobs.MemorySink{}
				recorder := qobs.NewRecorder(sink)
				defer recorder.Close()
				ctx := hooks.WithSimulations(qobs.WithRecorder(context.Background(), recorder), collector)
				log := &qenv.ChoiceLog{C: qenv.Nominal{}}
				s := qenv.New(qenv.WithContext(log, ctx))
				defer s.Close()
				s.MaxSteps, s.Hang = 1, 0
				s.Spawn("work", func(ctx context.Context) {
					switch kind {
					case "runnable":
						qenv.Yield(ctx)
					case "deadline":
						qenv.Sleep(ctx, time.Millisecond)
					case "blocked":
						qenv.Wait(ctx, func() bool { return false })
					case "horizon":
						qenv.Sleep(ctx, time.Hour)
					}
				})
				var err error
				if settle {
					err = s.Settle(time.Second)
				} else {
					err = s.Run(time.Second)
				}
				limited := kind == "runnable" || kind == "deadline"
				if errors.Is(err, qenv.ErrMaxSteps) != limited {
					t.Fatalf("wrong exhaustion: %v", err)
				}
				switch kind {
				case "complete":
					if err != nil {
						t.Fatalf("completed: %v", err)
					}
				case "blocked":
					if !errors.Is(err, qenv.ErrStuck) {
						t.Fatalf("blocked: %v", err)
					}
				case "horizon":
					if settle {
						var unsettled *qenv.Unsettled
						if !errors.As(err, &unsettled) {
							t.Fatalf("horizon: %v", err)
						}
					} else if err != nil {
						t.Fatalf("horizon: %v", err)
					}
				}
				calls := collector.Calls()
				if len(calls) != 1 {
					t.Fatalf("observations: %+v", calls)
				}
				call := calls[0]
				if call.StepsBefore != 0 || call.StepsAfter != 1 || call.StepLimitReached != limited || call.Native {
					t.Fatalf("incorrect scheduling evidence: %+v", call)
				}
				// A settle marker is evidence for eventual properties: emit it
				// only when all work completed, even exactly at the budget.
				markers := 0
				for _, step := range sink.Steps() {
					if step.Settled {
						markers++
					}
				}
				wantMarkers := 0
				if settle && kind == "complete" {
					wantMarkers = 1
				}
				if markers != wantMarkers {
					t.Fatalf("settle markers %d, want %d (error %v)", markers, wantMarkers, err)
				}
				wantElapsed := time.Duration(0)
				if kind == "horizon" {
					wantElapsed = time.Second
				}
				if s.Elapsed() != wantElapsed || len(log.Choices) != 0 {
					t.Fatalf("boundary consumed time/choices: elapsed %v choices %+v", s.Elapsed(), log.Choices)
				}
			})
		}
	}
}

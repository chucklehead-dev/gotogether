package qenv_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

func TestStepBudgetQuietBoundary(t *testing.T) {
	for _, settle := range []bool{false, true} {
		t.Run(map[bool]string{false: "Run", true: "Settle"}[settle], func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			defer s.Close()
			call := s.Run
			if settle {
				call = s.Settle
			}
			s.MaxSteps = 0
			if err := call(time.Second); err != nil {
				t.Fatalf("empty zero-budget simulation: %v", err)
			}
			s.MaxSteps = 1
			completed := 0
			s.Spawn("one turn", func(context.Context) { completed++ })
			if err := call(time.Second); err != nil || completed != 1 {
				t.Fatalf("final allowed turn: completed=%d err=%v", completed, err)
			}
			if err := call(time.Second); err != nil {
				t.Fatalf("later quiet call at cumulative limit: %v", err)
			}
			s.Spawn("new work", func(context.Context) { completed++ })
			if err := call(time.Second); !errors.Is(err, qenv.ErrMaxSteps) || completed != 1 {
				t.Fatalf("new work exceeded cumulative limit: completed=%d err=%v", completed, err)
			}
		})
	}
}

func TestStepBudgetBlockedOutcome(t *testing.T) {
	for _, mode := range []string{"active", "idle", "daemon"} {
		for _, settle := range []bool{false, true} {
			name := mode + "/Run"
			if settle {
				name = mode + "/Settle"
			}
			t.Run(name, func(t *testing.T) {
				s := qenv.New(qenv.Nominal{})
				defer s.Close()
				s.MaxSteps = 1
				var sig qenv.Signal
				s.Spawn(mode, func(ctx context.Context) {
					if mode == "daemon" {
						qenv.Daemon(ctx)
					}
					if mode == "idle" {
						sig.WaitIdle(ctx, 0)
					} else {
						sig.Wait(ctx, 0)
					}
				})
				call := s.Run
				if settle {
					call = s.Settle
				}
				err := call(time.Second)
				if mode != "active" {
					if err != nil {
						t.Fatalf("quiet %s wait: %v", mode, err)
					}
				} else if settle {
					var unsettled *qenv.Unsettled
					if !errors.As(err, &unsettled) || !errors.Is(err, qenv.ErrUnsettled) || !errors.Is(err, qenv.ErrStuck) {
						t.Fatalf("active wait should be unsettled and stuck: %v", err)
					}
				} else if !errors.Is(err, qenv.ErrStuck) {
					t.Fatalf("active wait should be stuck: %v", err)
				}
			})
		}
	}
}

// Reaching a time horizon needs no scheduling turn. A deadline within that
// horizon does: refusing it must leave virtual time and user choices intact.
func TestStepBudgetDeadlineBoundary(t *testing.T) {
	for _, settle := range []bool{false, true} {
		for _, horizon := range []time.Duration{time.Second, 2 * time.Second, 3 * time.Second} {
			name := "Run/" + horizon.String()
			if settle {
				name = "Settle/" + horizon.String()
			}
			t.Run(name, func(t *testing.T) {
				chooser := &stepBudgetChoices{}
				s := qenv.New(chooser)
				defer s.Close()
				s.MaxSteps = 1
				woke := false
				s.Spawn("sleeper", func(ctx context.Context) { qenv.Sleep(ctx, 2*time.Second); woke = true })
				call := s.Run
				if settle {
					call = s.Settle
				}
				err := call(horizon)
				if horizon < 2*time.Second {
					if settle {
						if !errors.Is(err, qenv.ErrUnsettled) {
							t.Fatalf("active sleeper beyond horizon: %v", err)
						}
					} else if err != nil {
						t.Fatalf("deadline beyond horizon: %v", err)
					}
					if got := s.Now().Sub(qenv.Epoch); got != horizon {
						t.Fatalf("time=%v, want horizon %v", got, horizon)
					}
				} else {
					if !errors.Is(err, qenv.ErrMaxSteps) {
						t.Fatalf("deadline needs another turn: %v", err)
					}
					if got := s.Now().Sub(qenv.Epoch); got != 0 {
						t.Fatalf("exhausted budget advanced time to %v", got)
					}
				}
				if woke || chooser.calls != 0 {
					t.Fatalf("budget/horizon ran sleeper=%v or drew choices=%d", woke, chooser.calls)
				}
				// Retrying with another turn available must still deliver that deadline.
				s.MaxSteps = 2
				if err := call(2 * time.Second); err != nil || !woke {
					t.Fatalf("retry lost deadline: woke=%v err=%v", woke, err)
				}
			})
		}
	}
}

type stepBudgetChoices struct{ calls int }

func (c *stepBudgetChoices) Choose(string, int) int { c.calls++; return 0 }

func TestStepBudgetRunnableTurnAndRunUntil(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	s.MaxSteps = 1
	done := false
	s.Spawn("yield once", func(ctx context.Context) { qenv.Yield(ctx); done = true })
	if err := s.Run(time.Second); !errors.Is(err, qenv.ErrMaxSteps) || done {
		t.Fatalf("extra runnable turn escaped cap: done=%v err=%v", done, err)
	}
	s.MaxSteps = 2
	reached, err := s.RunUntil(time.Second, func() bool { return done })
	if err != nil || !reached {
		t.Fatalf("RunUntil completion at final allowed turn: reached=%v err=%v", reached, err)
	}
}

// Two ready tasks would require a schedule choice. An exhausted budget must
// refuse that choice as well as refusing to execute either task.
func TestStepBudgetZeroDoesNotChooseRunnableWork(t *testing.T) {
	chooser := &stepBudgetChoices{}
	s := qenv.New(chooser)
	defer s.Close()
	s.MaxSteps = 0
	ran := 0
	s.Spawn("first", func(context.Context) { ran++ })
	s.Spawn("second", func(context.Context) { ran++ })
	if err := s.Run(time.Second); !errors.Is(err, qenv.ErrMaxSteps) || ran != 0 || chooser.calls != 0 || s.Elapsed() != 0 {
		t.Fatalf("zero budget executed or chose work: ran=%d choices=%d elapsed=%v err=%v", ran, chooser.calls, s.Elapsed(), err)
	}
}

// SettleCore's completion obligations exclude other processes, but those
// processes still consume scheduling turns. They cannot bypass the step cap.
func TestStepBudgetSettleCoreDoesNotIgnoreRunnableOutsideWork(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	s.MaxSteps = 1
	coreDone, outsideRan := false, false
	core := s.Process("core", false, func(context.Context) { coreDone = true })
	s.Process("outside", false, func(ctx context.Context) {
		outsideRan = true
		for {
			qenv.Yield(ctx)
		}
	})
	if err := s.SettleCore(time.Second, core); !errors.Is(err, qenv.ErrMaxSteps) || !coreDone || outsideRan {
		t.Fatalf("core completion bypassed remaining scheduler work: core=%v outside=%v err=%v", coreDone, outsideRan, err)
	}
}

// A daemon is exempt from completion obligations, not scheduling limits. Its
// in-bound deadline still needs a turn; merely inspecting it must not move time.
func TestStepBudgetDaemonDeadlineStillNeedsATurn(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	s.MaxSteps = 1
	woke := false
	s.Spawn("daemon", func(ctx context.Context) {
		qenv.Daemon(ctx)
		qenv.Sleep(ctx, 2*time.Second)
		woke = true
	})
	if err := s.Settle(3 * time.Second); !errors.Is(err, qenv.ErrMaxSteps) || woke || s.Elapsed() != 0 {
		t.Fatalf("daemon deadline bypassed cap: woke=%v elapsed=%v err=%v", woke, s.Elapsed(), err)
	}
	s.MaxSteps = 2
	if err := s.Settle(3 * time.Second); err != nil || !woke || s.Elapsed() != 2*time.Second {
		t.Fatalf("retry lost daemon deadline: woke=%v elapsed=%v err=%v", woke, s.Elapsed(), err)
	}
}

// Predicate readiness is observed at the boundary. A worker whose no-work wait
// became ready on the final allowed turn is active work, not quiet completion.
func TestStepBudgetReadyIdlePredicateNeedsATurn(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	s.MaxSteps = 2
	ready, handled := false, false
	s.Spawn("worker", func(ctx context.Context) {
		qenv.WaitIdle(ctx, func() bool { return ready })
		handled = true
	})
	s.Spawn("make ready", func(context.Context) { ready = true })
	if err := s.Run(time.Second); !errors.Is(err, qenv.ErrMaxSteps) || !ready || handled {
		t.Fatalf("ready wait became quiet or executed past cap: ready=%v handled=%v err=%v", ready, handled, err)
	}
	if err := s.Settle(time.Second); !errors.Is(err, qenv.ErrMaxSteps) || handled {
		t.Fatalf("settle ignored ready worker: handled=%v err=%v", handled, err)
	}
	s.MaxSteps = 3
	if err := s.Settle(time.Second); err != nil || !handled {
		t.Fatalf("retry lost ready predicate: handled=%v err=%v", handled, err)
	}
}

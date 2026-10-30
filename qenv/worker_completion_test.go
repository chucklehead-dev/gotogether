package qenv_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

func TestSignalIdleWorkerCompletion(t *testing.T) {
	for _, work := range []string{"empty", "slow", "blocked", "finished"} {
		t.Run(work, func(t *testing.T) {
			sink := &qobs.MemorySink{}
			rec := qobs.NewRecorder(sink)
			s := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(t.Context(), rec)))
			defer s.Close()
			var available, dependency qenv.Signal
			waiting, handled := false, false
			s.Spawn("worker", func(ctx context.Context) {
				waiting = true
				available.WaitIdle(ctx, 0)
				waiting = false
				switch work {
				case "blocked":
					dependency.Wait(ctx, 0)
				default:
					qenv.Sleep(ctx, 2*time.Second)
				}
				handled = true
				available.WaitIdle(ctx, 0)
			})
			if err := s.Run(time.Millisecond); err != nil || !waiting {
				t.Fatalf("worker not waiting: %v", err)
			}
			if work != "empty" {
				available.Notify()
			}
			bound := time.Second
			if work == "finished" {
				bound = 3 * time.Second
			}
			err := s.Settle(bound)
			markers := 0
			for _, step := range sink.Steps() {
				if step.Settled {
					markers++
				}
			}
			if work == "slow" || work == "blocked" {
				if !errors.Is(err, qenv.ErrUnsettled) || !strings.Contains(err.Error(), "worker") || markers != 0 || handled {
					t.Fatalf("unfinished worker claimed completion: err=%v markers=%d handled=%t", err, markers, handled)
				}
			} else if err != nil || markers != 1 || (work == "finished" && !handled) {
				t.Fatalf("quiet worker: err=%v markers=%d handled=%t", err, markers, handled)
			}
		})
	}
}

func TestSignalIdleTimeoutResumesActiveWork(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	var signal, dependency qenv.Signal
	timedOut := false
	s.Spawn("poller", func(ctx context.Context) {
		timedOut = !signal.WaitIdle(ctx, time.Millisecond)
		dependency.Wait(ctx, 0)
	})
	if err := s.Settle(time.Second); !errors.Is(err, qenv.ErrUnsettled) || !timedOut {
		t.Fatalf("idle exemption survived timeout: %v timedOut=%t", err, timedOut)
	}
}

func TestSignalIdleOutsideSimulation(t *testing.T) {
	var signal qenv.Signal
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if signal.WaitIdle(ctx, 0) {
		t.Fatal("cancelled wait reported notification")
	}
	if signal.WaitIdle(t.Context(), -time.Second) {
		t.Fatal("expired wait reported notification")
	}
}

func TestNativeSignalIdleWaitGeneration(t *testing.T) {
	nativeModes(t, func(t *testing.T, newSim func() *qenv.Sim) {
		s := newSim()
		var idle, dependency qenv.Signal
		var notified, escaped bool
		var ended time.Duration
		s.Spawn("worker", func(ctx context.Context) {
			notified = idle.WaitIdle(ctx, 10*time.Millisecond)
			ended = s.Elapsed()
			dependency.Wait(ctx, 0)
			escaped = true
		})
		s.Spawn("notifier", func(ctx context.Context) { qenv.Sleep(ctx, 30*time.Millisecond); idle.Notify() })
		if err := s.Run(100 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if notified || ended != 10*time.Millisecond || escaped {
			t.Fatalf("idle wait changed timeout or woke later work: notified=%t ended=%v escaped=%t", notified, ended, escaped)
		}
	})
}

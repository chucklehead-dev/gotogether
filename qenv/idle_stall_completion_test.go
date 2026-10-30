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

// A notification or a true readiness predicate establishes work even when a
// process pause prevents the worker from reaching the code after its idle wait.
func TestIdleWorkerWakeDuringProcessPause(t *testing.T) {
	for _, mode := range []string{"signal", "predicate", "predicate-consumed"} {
		for _, work := range []bool{false, true} {
			if mode == "predicate-consumed" && !work {
				continue
			}
			name := mode + "/empty"
			if work {
				name = mode + "/work"
			}
			t.Run(name, func(t *testing.T) {
				waiting, ready, handled := false, false, false
				script := &qenv.Script{
					Faults: []qenv.Scripted{{Kind: "nemesis.pause.worker", After: 1}},
					Progress: func() int {
						if waiting {
							return 1
						}
						return 0
					},
				}
				sink := &qobs.MemorySink{}
				rec := qobs.NewRecorder(sink)
				sim := qenv.New(qenv.WithContext(script, qobs.WithRecorder(t.Context(), rec)))
				defer sim.Close()
				sim.Stalls = qenv.Stalls{Pause: 10 * time.Second}
				var available qenv.Signal
				sim.Process("worker", false, func(ctx context.Context) {
					waiting = true
					if mode == "signal" {
						available.WaitIdle(ctx, 0)
					} else {
						qenv.WaitIdle(ctx, func() bool { return ready })
					}
					handled = true
					ready = false
					if mode == "signal" {
						available.WaitIdle(ctx, 0)
					} else {
						qenv.WaitIdle(ctx, func() bool { return ready })
					}
				})
				sim.Spawn("source", func(context.Context) {
					if work {
						ready = true
						available.Notify()
					}
				})
				if err := sim.Run(time.Millisecond); err != nil {
					t.Fatal(err)
				}
				if len(script.Remaining()) != 0 || !waiting || handled {
					t.Fatalf("pause fixture did not hold waiting worker: remaining=%v waiting=%t handled=%t", script.Remaining(), waiting, handled)
				}
				err := sim.Settle(time.Second)
				markers := func() int {
					n := 0
					for _, step := range sink.Steps() {
						if step.Settled {
							n++
						}
					}
					return n
				}
				if !work {
					if err != nil || markers() != 1 || handled {
						t.Fatalf("empty paused worker prevented settlement: err=%v markers=%d handled=%t", err, markers(), handled)
					}
					return
				}
				if !errors.Is(err, qenv.ErrUnsettled) || !strings.Contains(err.Error(), "worker") || markers() != 0 || handled {
					t.Fatalf("paused worker with observed work claimed settlement: err=%v markers=%d handled=%t", err, markers(), handled)
				}
				if mode == "predicate-consumed" {
					// Readiness is not a decided wake. A different worker can
					// consume the item before this paused worker is selected;
					// an earlier pending-work scan must not make it active forever.
					consumed := false
					sim.Spawn("other worker", func(context.Context) {
						consumed = ready
						ready = false
					})
					if err := sim.Settle(time.Second); err != nil || !consumed || handled || markers() != 1 {
						t.Fatalf("consumed readiness left a sticky active worker: err=%v consumed=%t handled=%t markers=%d", err, consumed, handled, markers())
					}
					return
				}
				if err := sim.Settle(20 * time.Second); err != nil || !handled || markers() != 1 {
					t.Fatalf("resumed worker failed to finish: err=%v handled=%t markers=%d", err, handled, markers())
				}
			})
		}
	}
}

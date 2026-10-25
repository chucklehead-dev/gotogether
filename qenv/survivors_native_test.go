package qenv_test

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// The native runtime's seams, waits, crashes and Close, pinned on both of
// its modes: without a Platform (the lock the running task holds) and on a
// cooperative Platform (no lock; a condition broadcast at every seam). The
// tests run in testing/synctest bubbles, whose fake clock advances only
// when every goroutine is durably blocked: the real goroutines then take
// their turns as on a cooperative runtime, and the times they see are
// exact.

// coopPlatform is a Platform for tasks outside any process: the waits take
// the cooperative path (no lock), as under gosim. The bubble's fake clock
// stands in for gosim's one-goroutine-at-a-time scheduling: these tests'
// tasks hand over only through time and the runtime's broadcasts.
type coopPlatform struct{}

func (coopPlatform) Machine(name string, main func()) qenv.Machine {
	panic("coopPlatform runs no processes: " + name)
}

// raceBuild reports whether the test binary was built with -race. On a
// Platform the runtime takes no lock (the Platform runs one goroutine at a
// time), which real goroutines do not: the race detector would report
// what gosim never interleaves.
func raceBuild() bool {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, s := range bi.Settings {
		if s.Key == "-race" && s.Value == "true" {
			return true
		}
	}
	return false
}

// nativeModes runs f on the native runtime without a Platform and, unless
// built with -race, on a cooperative one, each in a synctest bubble.
func nativeModes(t *testing.T, f func(t *testing.T, newSim func() *qenv.Sim)) {
	t.Run("locked", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f(t, func() *qenv.Sim { return qenv.NewNative(qenv.Nominal{}, nil) })
		})
	})
	t.Run("cooperative", func(t *testing.T) {
		if raceBuild() {
			t.Skip("a cooperative Platform runs one goroutine at a time; real goroutines under -race do not")
		}
		synctest.Test(t, func(t *testing.T) {
			f(t, func() *qenv.Sim { return qenv.NewNative(qenv.Nominal{}, coopPlatform{}) })
			// On a Platform, Close does not wait for the tasks, and a sleep
			// ends only at its deadline (gosim's machines stop them): let
			// the fake clock pass every sleep here, so each task unwinds.
			time.Sleep(2 * time.Hour) //qenv:ok the bubble's fake clock
		})
	})
}

// Every seam of the task that made a waiter's predicate true wakes the
// waiter: the setter going on to sleep, yield, wait for something else or
// end. On a cooperative runtime nothing else wakes a parked wait, and
// without a Platform a predicate waiter sleeps on the lock's condition: a
// seam that forgot to broadcast leaves the waiter parked until Close, its
// state change never seen. The waiter returns only once its predicate
// holds, and the setter's own unbounded wait never returns.
func TestNativeEverySeamWakesWaiters(t *testing.T) {
	seams := map[string]func(ctx context.Context){
		"sleep": func(ctx context.Context) { qenv.Sleep(ctx, time.Millisecond) },
		"yield": func(ctx context.Context) { qenv.Yield(ctx); qenv.Sleep(ctx, time.Hour) },
		"wait": func(ctx context.Context) {
			var never qenv.Signal
			never.Wait(ctx, 0)
			panic("an unbounded wait on a signal never notified returned")
		},
		"end": func(context.Context) {},
	}
	for _, name := range []string{"sleep", "yield", "wait", "end"} {
		t.Run(name, func(t *testing.T) {
			nativeModes(t, func(t *testing.T, newSim func() *qenv.Sim) {
				s := newSim()
				x, woke, sawX := false, false, false
				var wokeAt time.Duration
				s.Spawn("waiter", func(ctx context.Context) {
					qenv.Wait(ctx, func() bool { return x })
					woke, sawX, wokeAt = true, x, s.Elapsed()
				})
				s.Spawn("setter", func(ctx context.Context) {
					qenv.Sleep(ctx, 10*time.Millisecond)
					x = true
					seams[name](ctx)
				})
				if err := s.Run(100 * time.Millisecond); err != nil {
					t.Fatal(err)
				}
				s.Close()
				if !woke || !sawX || wokeAt != 10*time.Millisecond {
					t.Fatalf("after the setter's %s: waiter woke=%v saw its predicate=%v at %v, want true, true, 10ms", name, woke, sawX, wokeAt)
				}
			})
		})
	}
}

// A timed Signal wait ends at its deadline, not at the next unrelated
// seam nor at a Notify that came later; and once it timed out it is over:
// that later Notify must not end the task's next wait (its stale entry in
// the signal's waiters names the old wait's generation). A sleep lasts its
// duration on both modes.
func TestNativeTimedSignalWait(t *testing.T) {
	nativeModes(t, func(t *testing.T, newSim func() *qenv.Sim) {
		s := newSim()
		var a, b qenv.Signal
		var firstOK, secondReturned bool
		var timedOutAt, sleptUntil time.Duration
		s.Spawn("waiter", func(ctx context.Context) {
			firstOK = a.Wait(ctx, 10*time.Millisecond)
			timedOutAt = s.Elapsed()
			b.Wait(ctx, 0) // nothing notifies b
			secondReturned = true
		})
		s.Spawn("notifier", func(ctx context.Context) {
			qenv.Sleep(ctx, 30*time.Millisecond)
			sleptUntil = s.Elapsed()
			a.Notify()
		})
		if err := s.Run(100 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if firstOK || timedOutAt != 10*time.Millisecond {
			t.Errorf("a.Wait(10ms) returned notified=%v at %v, want a timeout at 10ms", firstOK, timedOutAt)
		}
		if sleptUntil != 30*time.Millisecond {
			t.Errorf("Sleep(30ms) ended at %v", sleptUntil)
		}
		if secondReturned {
			t.Error("a Notify of a after its wait timed out ended the task's next wait, on b")
		}
	})
}

// Close unwinds every task still parked (a predicate wait, a Signal wait,
// a sleep of an hour), running its deferred code, and without a Platform
// it returns only once it has: no task's goroutine outlives the
// simulation (a synctest bubble fails if one does), and a sleep ends at
// Close rather than at its deadline. The deferred code here itself sleeps,
// which in the bubble finishes only once Close has blocked waiting for it.
// One parked task a simulation: an unwinding task holds the runtime's
// lock, and a bubble's clock does not move while another waits for it.
func TestNativeCloseUnwindsEveryTask(t *testing.T) {
	parks := map[string]func(ctx context.Context){
		"predicate": func(ctx context.Context) { qenv.Wait(ctx, func() bool { return false }) },
		"signal": func(ctx context.Context) {
			var never qenv.Signal
			never.Wait(ctx, 0)
		},
		"sleep": func(ctx context.Context) { qenv.Sleep(ctx, time.Hour) },
	}
	for _, park := range []string{"predicate", "signal", "sleep"} {
		t.Run(park, func(t *testing.T) {
			nativeModes(t, func(t *testing.T, newSim func() *qenv.Sim) {
				s := newSim()
				coop := strings.HasSuffix(t.Name(), "cooperative")
				unwound := false
				s.Spawn(park, func(ctx context.Context) {
					defer func() {
						time.Sleep(time.Millisecond) //qenv:ok deferred code that takes time: Close must wait for it
						unwound = true
					}()
					parks[park](ctx)
				})
				start := time.Now() //qenv:ok the bubble's fake clock: how long Close took
				if err := s.Run(10 * time.Millisecond); err != nil {
					t.Fatal(err)
				}
				s.Close()
				if coop {
					// a Platform's machines stop the tasks, and Close does not
					// wait, nor end a sleep: here the task unwinds on its own,
					// once the clock moves past its hour
					time.Sleep(2 * time.Hour) //qenv:ok the bubble's fake clock
				} else if took := time.Since(start); took >= time.Hour { //qenv:ok the bubble's fake clock
					t.Errorf("Close returned after %v: it waited the sleep out", took)
				}
				if !unwound {
					t.Errorf("after Close, the %s task's deferred code has not run", park)
				}
			})
		})
	}
}

// A task that panics fails the run: Run re-raises the panic with its own
// value, the Sim's Log says which task panicked, and PanicStack is that
// task's stack. A crash's unwinding (killed) is no panic.
func TestNativeTaskPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := qenv.NewNative(qenv.Nominal{}, nil)
		var logs []string
		s.Log = func(m string) { logs = append(logs, m) }
		s.Spawn("bomb", func(ctx context.Context) {
			qenv.Sleep(ctx, time.Millisecond)
			panicInNativeTask()
		})
		got := func() (r any) {
			defer func() { r = recover() }()
			_ = s.Run(10 * time.Millisecond)
			return nil
		}()
		s.Close()
		if got != "boom" {
			t.Fatalf("Run re-raised %v, want the task's panic boom", got)
		}
		if !strings.Contains(s.PanicStack(), "panicInNativeTask") {
			t.Errorf("PanicStack is not the task's:\n%s", s.PanicStack())
		}
		if len(logs) != 1 || !strings.HasPrefix(logs[0], "task bomb panicked: boom\n") {
			t.Errorf("log %q, want one line naming task bomb's panic", logs)
		}
	})
}

func panicInNativeTask() { panic("boom") }

// A task killed by a crash unwinds quietly: neither the crash nor Close
// makes its unwinding a panic of the run, and without a Log the runtime
// logs nothing (a nil Log is not called).
func TestNativeCrashIsNoPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := qenv.NewNative(qenv.Nominal{}, nil)
		p := s.Process("node", false, func(ctx context.Context) { qenv.Wait(ctx, func() bool { return false }) })
		s.Spawn("killer", func(ctx context.Context) {
			qenv.Sleep(ctx, time.Millisecond)
			p.Crash()
		})
		if err := s.Run(10 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if st := s.PanicStack(); st != "" {
			t.Errorf("a crashed task's unwinding was noted as a panic:\n%s", st)
		}
	})
}

// A task started in a process that crashes before the task first runs
// never runs: its parent starts it and crashes its own process at once,
// with no seam between, so the child's goroutine reaches the runtime only
// after the crash. Running it would let a dead incarnation act.
func TestNativeChildOfCrashedProcessNeverRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := qenv.NewNative(qenv.Nominal{}, nil)
		childRan, parentRanOn := false, false
		s.Process("node", false, func(ctx context.Context) {
			qenv.Go(ctx, "child", func(context.Context) { childRan = true })
			qenv.ProcessOf(ctx).Crash()
			parentRanOn = true
		})
		if err := s.Run(10 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if childRan || parentRanOn {
			t.Fatalf("after the crash: child ran=%v, parent ran on=%v", childRan, parentRanOn)
		}
	})
}

// Without NemesisEvery the native runtime considers a nemesis every
// 100ms: a crashable process under a nemesis rate of 1 is not crashed in
// its first 30ms, and is within 200ms. (Considered continuously, the
// nemesis would crash it at once and over and over.) Real time: the
// nemesis task's sleep is the runtime's.
func TestNativeNemesisEveryDefault(t *testing.T) {
	c := qenv.NewSeeded(1)
	c.Rates = qenv.NoFaults.Set("nemesis", 1)
	s := qenv.NewNative(c, nil)
	p := s.Process("node", true, func(ctx context.Context) { qenv.Wait(ctx, func() bool { return false }) })
	if err := s.Run(30 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	early := p.Incarnation
	if err := s.Run(170 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	later := p.Incarnation
	s.Close()
	if early != 1 || later < 2 {
		t.Fatalf("incarnation %d after 30ms and %d after 200ms: want 1, then at least 2", early, later)
	}
}

// Yield lets another task run: two tasks that each yield in a loop take
// turns, rather than one running all its iterations while the other waits
// for the lock. Pinned on one P, where the runtime's own scheduling is
// deterministic enough to tell a handover from none.
func TestNativeYieldTakesTurns(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	s := qenv.NewNative(qenv.Nominal{}, nil)
	var order []string
	for _, name := range []string{"a", "b"} {
		s.Spawn(name, func(ctx context.Context) {
			for range 20 {
				order = append(order, name)
				qenv.Yield(ctx)
			}
		})
	}
	if err := s.Run(20 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	s.Close()
	switches := 0
	for i := 1; i < len(order); i++ {
		if order[i] != order[i-1] {
			switches++
		}
	}
	if len(order) != 40 || switches < 10 {
		t.Fatalf("%d turns, %d switches between the tasks: %s", len(order), switches, fmt.Sprint(order))
	}
}

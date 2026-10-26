package qenv_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A task blocked on a real channel never reaches a seam, and the task that
// would unblock it never gets its turn: Run waited forever, and the test
// ended at go test's timeout naming nothing. The watchdog panics naming
// the task, what blocks it and its stack, and Close still returns.
func TestHangNamesTheBlockedTask(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	if s.Hang != 30*time.Second {
		t.Fatalf("New's Hang is %v, want 30s", s.Hang)
	}
	s.Hang = 100 * time.Millisecond
	never := make(chan struct{})
	s.Spawn("blocker", func(ctx context.Context) {
		qenv.Yield(ctx)
		blockOnChannel(never)
	})
	s.Spawn("other", func(ctx context.Context) { qenv.Yield(ctx) })
	msg := runPanics(t, s)
	for _, want := range []string{"task blocker ran 100ms of wall time without reaching a seam", "SetMaxOpenConns", "Sim.Hang", "blockOnChannel", "chan receive"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the panic does not say %q:\n%s", want, msg)
		}
	}
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }() //qenv:ok the test's own watch on Close, outside the simulation
	select {                                 //qenv:ok the test waits for Close against wall time
	case <-closed:
	case <-time.After(5 * time.Second): //qenv:ok as above
		t.Fatal("Close did not return after a hang: it waited for the hung task")
	}
	close(never) // what blocked it lets go: the task unwinds (TestMain checks no goroutine is left)
}

// A task held up briefly outside the simulation (a real call that returns)
// is not a hang: the watchdog waits per step, and a task's wait within it
// passes.
func TestHangAllowsAShortRealWait(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	s.Hang = 5 * time.Second
	for i := range 3 {
		s.Spawn(fmt.Sprint("t", i), func(ctx context.Context) {
			for range 3 {
				ch := make(chan struct{})
				go func() { time.Sleep(10 * time.Millisecond); close(ch) }() //qenv:ok a real call that returns, outside the simulation
				<-ch
				qenv.Yield(ctx)
			}
		})
	}
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
}

func blockOnChannel(ch chan struct{}) { <-ch }

// realWait holds the calling task up outside the simulation for d of wall
// time: a real call that returns.
func realWait(d time.Duration) {
	ch := make(chan struct{})
	go func() { time.Sleep(d); close(ch) }() //qenv:ok a real call that returns, outside the simulation
	<-ch
}

// The watchdog measures one wake from the tick that first saw it: a wait
// shorter than Hang that spans several of its ticks (it looks every Hang/4)
// is no hang, nor is the task's state changing between ticks (a new wake).
// Hang is 1s and the wait 600ms: at least two ticks see the same wake,
// 250ms apart, 400ms short of Hang.
func TestHangMeasuresOneWakeAcrossTicks(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	s.Hang = time.Second
	s.Spawn("slow", func(ctx context.Context) {
		qenv.Yield(ctx)
		realWait(600 * time.Millisecond)
		qenv.Yield(ctx)
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
}

// Hang 0 turns the watchdog off: a task held up in real time for many of
// the ticks a watchdog would make is let be.
func TestHangZeroIsOff(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	s.Hang = 0
	s.Spawn("slow", func(ctx context.Context) {
		qenv.Yield(ctx)
		realWait(50 * time.Millisecond)
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
}

// runPanics runs s and returns what Run panicked with.
func runPanics(t *testing.T, s *qenv.Sim) (msg string) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Run returned; want a panic")
		}
		msg = fmt.Sprint(r)
	}()
	_ = s.Run(time.Second)
	return ""
}

// A hung task let go while Close unwinds another task once ran alongside
// it, both in the scheduler's state (the run queue: -race flagged it).
// Abandoned, it unwinds at its next seam touching nothing of the
// simulation's, while Close unwinds the others.
func TestHangReleasedDuringClose(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	s.Hang = 50 * time.Millisecond
	release := make(chan struct{})
	reached := make(chan struct{})
	s.Spawn("blocker", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Millisecond) // the other first: its unwinding is what lets this one go
		blockOnChannel(release)
		qenv.Yield(ctx) // a seam after the hang: it once enqueued itself, under Close's unwinding
		close(reached)  // not reached: it unwinds at the seam
	})
	s.Spawn("other", func(ctx context.Context) {
		defer func() {
			close(release)                    // let the blocker go while this task is still unwinding
			time.Sleep(20 * time.Millisecond) //qenv:ok holds the unwinding open in real time, for the released task to run in
		}()
		qenv.Sleep(ctx, time.Hour)
	})
	runPanics(t, s)
	s.Close()
	select { //qenv:ok the test waits in wall time
	case <-reached:
		t.Fatal("the abandoned task ran on past its seam")
	case <-time.After(50 * time.Millisecond): //qenv:ok as above
	}
}

// BenchmarkBareSteps is the scheduler's cost per step with the watchdog
// off and on: tasks that only yield.
func BenchmarkBareSteps(b *testing.B) {
	for _, hang := range []time.Duration{0, 30 * time.Second} {
		b.Run(fmt.Sprint("Hang=", hang), func(b *testing.B) {
			s := qenv.New(qenv.Nominal{})
			defer s.Close()
			s.Hang = hang
			s.MaxSteps = b.N + 100
			for i := range 4 {
				s.Spawn(fmt.Sprint("t", i), func(ctx context.Context) {
					for range b.N / 4 {
						qenv.Yield(ctx)
					}
				})
			}
			b.ResetTimer()
			if err := s.Run(time.Hour); err != nil {
				b.Fatal(err)
			}
		})
	}
}

// A seam in a killed task's deferred code, as Close unwinds it, handed
// control back early: Close took that for the task's end, and the task
// stayed parked forever (TestMain's leak check). It now unwinds on.
func TestDeferredSeamAtClose(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	ran := false
	s.Spawn("sleeper", func(ctx context.Context) {
		defer func() { ran = true }()
		defer qenv.Sleep(ctx, time.Second) // a seam while it unwinds
		qenv.Sleep(ctx, time.Hour)
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if !ran {
		t.Fatal("the deferred code after the seam did not run")
	}
}

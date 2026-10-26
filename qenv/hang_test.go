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

package qenv_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Run(0) runs what can run at the current instant, a deadline on it
// included, and lets no virtual time pass.
func TestRunZeroTakesNoTime(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	start := s.Now()
	var began, slept, woke bool
	s.Spawn("a", func(ctx context.Context) {
		began = true
		qenv.Sleep(ctx, 0)
		slept = true
		qenv.Sleep(ctx, time.Millisecond)
		woke = true
	})
	if err := s.Run(0); err != nil {
		t.Fatal(err)
	}
	if !began || !slept || woke || !s.Now().Equal(start) {
		t.Fatalf("after Run(0): began %v, past a sleep of 0 %v, past a sleep of 1ms %v, %v passed", began, slept, woke, s.Now().Sub(start))
	}
	if err := s.Run(time.Millisecond); err != nil || !woke {
		t.Fatalf("after Run(1ms): woke %v, %v", woke, err)
	}
}

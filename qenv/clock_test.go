package qenv_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// With clock faults a process's clock is off true time by its skew and
// its jumps, and only its own reads see it: sleeps take true time, and a
// task of no process reads true time. Without them every clock is true.
func TestClockFaults(t *testing.T) {
	for _, faults := range []bool{false, true} {
		off := 0
		for seed := uint64(1); seed <= 40; seed++ {
			s := qenv.New(qenv.NewSeeded(seed))
			s.ClockFaults = faults
			var read, after time.Time
			var p *qenv.Process
			p = s.Process("node", false, func(ctx context.Context) {
				for range 10 {
					read = qenv.Now(ctx)
				}
				qenv.Sleep(ctx, time.Second)
				after = s.Now()
			})
			start := s.Now()
			var outside time.Time
			s.Spawn("observer", func(ctx context.Context) { outside = qenv.Now(ctx) })
			if err := s.Run(time.Minute); err != nil {
				t.Fatal(err)
			}
			s.Close()
			if got := read.Sub(start); got != p.ClockOffset() {
				t.Fatalf("seed %d: the process read %v from true time, its offset is %v", seed, got, p.ClockOffset())
			}
			if !outside.Equal(start) || after.Sub(start) != time.Second {
				t.Fatalf("seed %d: true time moved: outside %v, slept %v", seed, outside.Sub(start), after.Sub(start))
			}
			if p.ClockOffset() != 0 {
				off++
			}
		}
		if faults && off == 0 || !faults && off > 0 {
			t.Errorf("clock faults %v: %d of 40 processes' clocks off", faults, off)
		}
	}
}

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

// With ClockDrift a process's clock gains or loses its fraction of true
// time, and a sleep on it lasts a second by that clock: shorter in true
// time on a fast clock, longer on a slow one. Without it no drift is
// chosen.
func TestClockDrift(t *testing.T) {
	seen := map[string]bool{}
	for seed := uint64(1); seed <= 20; seed++ {
		c := qenv.NewSeeded(seed)
		c.Rates = qenv.DefaultRates.Set("clock.skew", 0).Set("clock.jump", 0).Set("clock.drift", 1)
		s := qenv.New(c)
		s.ClockFaults, s.ClockDrift = true, 0.25
		var local, real time.Duration
		s.Process("node", false, func(ctx context.Context) {
			start, trueStart := qenv.Now(ctx), s.Now()
			qenv.Sleep(ctx, time.Second)
			local, real = qenv.Now(ctx).Sub(start), s.Now().Sub(trueStart)
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		s.Close()
		near := func(a, b time.Duration) bool { return (a - b).Abs() < time.Microsecond }
		switch {
		case near(local, time.Second) && near(real, 800*time.Millisecond): // 1s / 1.25
			seen["fast"] = true
		case near(local, time.Second) && near(real, 1333333333): // 1s / 0.75
			seen["slow"] = true
		default:
			t.Fatalf("seed %d: slept %v on the process's clock, %v of true time", seed, local, real)
		}
	}
	if !seen["fast"] || !seen["slow"] {
		t.Errorf("drifts seen %v, want both fast and slow", seen)
	}

	log := &qenv.ChoiceLog{C: qenv.NewSeeded(1)}
	s := qenv.New(log)
	s.ClockFaults = true
	s.Process("node", false, func(context.Context) {})
	for _, ch := range log.Choices {
		if ch.Kind == "fault.clock.drift" {
			t.Error("a drift chosen without ClockDrift")
		}
	}
	s.Close()
}

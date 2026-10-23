package qenv_test

import (
	"context"
	"fmt"
	"strings"
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

// A drift of 1 or more would stop a slow clock or run it backwards (its
// sleeps would end at once): it panics with the field and its range.
func TestClockDriftRange(t *testing.T) {
	for _, d := range []float64{1, 1.5, -0.1} {
		func() {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "in [0, 1)") {
					t.Errorf("ClockDrift %v: %v, want a panic naming the range", d, r)
				}
			}()
			s := qenv.New(qenv.NewSeeded(1))
			defer s.Close()
			s.ClockFaults, s.ClockDrift = true, d
			s.Process("node", false, func(context.Context) {})
		}()
	}
}

// A Service call's timeout is the caller's timer: on a slow clock it lasts
// longer in true time.
func TestClockDriftTimesCalls(t *testing.T) {
	seen := map[time.Duration]bool{}
	for seed := uint64(1); seed <= 10; seed++ {
		c := qenv.NewSeeded(seed)
		c.Rates = qenv.NoFaults.Set("clock.drift", 1)
		s := qenv.New(c)
		s.ClockFaults, s.ClockDrift = true, 0.5
		svc := qenv.NewService(s, "slow", nil, func(ctx context.Context, n int) (int, error) {
			qenv.Sleep(ctx, 10*time.Second) // a server of no process: true time
			return n, nil
		})
		var took time.Duration
		s.Process("client", false, func(ctx context.Context) {
			start := s.Now()
			_, _ = svc.Call(ctx, 1, time.Second) // the server takes 10 s: a timeout
			took = s.Now().Sub(start)
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if took != 666666666 && took != 2*time.Second {
			t.Errorf("seed %d: a 1s timeout on a drifting clock took %v of true time, want 2/3 s (fast) or 2 s (slow)", seed, took)
		}
		seen[took] = true
	}
	if len(seen) != 2 {
		t.Errorf("timeouts seen %v, want a fast clock's and a slow one's", seen)
	}
}

// A process's ToVirtual is its clock's view of a real-clock time: off true
// virtual time by its offset, and ToReal undoes it.
func TestProcessClockTranslation(t *testing.T) {
	c := qenv.NewSeeded(1)
	c.Rates = qenv.NoFaults.Set("clock.skew", 1)
	s := qenv.New(c)
	defer s.Close()
	s.ClockFaults = true
	p := s.Process("node", false, func(context.Context) {})
	if p.ClockOffset() == 0 {
		t.Fatal("no skew chosen")
	}
	real := time.Now()
	v := p.ToVirtual(real)
	if got := v.Sub(s.ToVirtual(real)); (got - p.ClockOffset()).Abs() > time.Millisecond {
		t.Errorf("the process's view is %v off true virtual time, want its offset %v", got, p.ClockOffset())
	}
	if back := p.ToReal(v); back.Sub(real).Abs() > time.Millisecond {
		t.Errorf("ToReal(ToVirtual(t)) is %v off t", back.Sub(real))
	}
}

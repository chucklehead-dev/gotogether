package qenv_test

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// clocked is a simulation with clock faults c, every skew and drift
// chosen (rate 1) and nemeses (jumps) at rate nemesis.
func clocked(seed uint64, c qenv.Clocks, nemesis float64) *qenv.Sim {
	ch := qenv.NewSeeded(seed)
	ch.Rates = qenv.NoFaults.Set("clock.skew", 1).Set("clock.drift", 1).Set("nemesis", nemesis)
	s := qenv.New(ch)
	s.Clocks = c
	return s
}

// With clock faults a process's wall clock is off true time by its skew
// and its jumps, and only its own reads see it; its monotonic clock is
// not moved by either, sleeps take true time, and a task of no process
// reads true time. Without them every clock is true.
func TestClockFaults(t *testing.T) {
	for _, faults := range []bool{false, true} {
		off, jumped := 0, 0
		for seed := uint64(1); seed <= 40; seed++ {
			c := qenv.Clocks{}
			if faults {
				c = qenv.Clocks{Skew: time.Second, Jump: 5 * time.Second}
			}
			s := clocked(seed, c, 0.2)
			var read, after time.Time
			var mono, offAtRead time.Duration
			var p *qenv.Process
			p = s.Process("node", false, func(ctx context.Context) {
				m0 := qenv.Mono(ctx)
				for range 10 {
					read, offAtRead = qenv.Now(ctx), p.ClockOffset()
					qenv.Yield(ctx) // a step, where a jump may happen
				}
				qenv.Sleep(ctx, time.Second)
				after, mono = s.Now(), qenv.Since(ctx, m0)
			})
			start := s.Now()
			var outside time.Time
			s.Spawn("observer", func(ctx context.Context) { outside = qenv.Now(ctx) })
			if err := s.Run(time.Minute); err != nil {
				t.Fatal(err)
			}
			s.Close()
			if got := read.Sub(start); got != offAtRead {
				t.Fatalf("seed %d: the process read %v from true time, its offset then %v", seed, got, offAtRead)
			}
			if !outside.Equal(start) || after.Sub(start) != time.Second {
				t.Fatalf("seed %d: true time moved: outside %v, slept %v", seed, outside.Sub(start), after.Sub(start))
			}
			if mono != time.Second {
				t.Fatalf("seed %d: the monotonic clock measured %v across a 1s sleep, with skew %v and jumps", seed, mono, p.ClockOffset())
			}
			if p.ClockOffset() != 0 {
				off++
			}
			if o := p.ClockOffset(); o != 0 && o != time.Second && o != -time.Second {
				jumped++
			}
		}
		if faults && (off == 0 || jumped == 0) || !faults && off > 0 {
			t.Errorf("clock faults %v: %d of 40 processes' clocks off, %d jumped", faults, off, jumped)
		}
	}
}

// A jump is a nemesis, not a choice at every read: reading the clock more
// often makes no more choices.
func TestClockReadsMakeNoChoices(t *testing.T) {
	count := func(reads int) int {
		log := &qenv.ChoiceLog{C: qenv.NewSeeded(1)}
		s := qenv.New(log)
		defer s.Close()
		s.Clocks = qenv.Clocks{Skew: time.Second, Jump: time.Second}
		s.Process("node", false, func(ctx context.Context) {
			for range reads {
				qenv.Now(ctx)
			}
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		return len(log.Choices)
	}
	if one, many := count(1), count(100); one != many {
		t.Errorf("1 read made %d choices, 100 reads %d", one, many)
	}
}

// With Clocks.Drift a process's clocks gain or lose their fraction of true
// time, and a sleep on them lasts a second by them: shorter in true time
// on a fast clock, longer on a slow one, and the monotonic clock measures
// the second. Without it no drift is chosen.
func TestClockDrift(t *testing.T) {
	seen := map[string]bool{}
	near := func(a, b time.Duration) bool { return (a - b).Abs() < time.Microsecond }
	for seed := uint64(1); seed <= 20; seed++ {
		s := clocked(seed, qenv.Clocks{Drift: 0.25}, 0)
		var wall, mono, real time.Duration
		s.Process("node", false, func(ctx context.Context) {
			start, m0, trueStart := qenv.Now(ctx), qenv.Mono(ctx), s.Now()
			qenv.Sleep(ctx, time.Second)
			wall, mono, real = qenv.Now(ctx).Sub(start), qenv.Since(ctx, m0), s.Now().Sub(trueStart)
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if !near(wall, time.Second) || !near(mono, time.Second) {
			t.Fatalf("seed %d: slept %v on the wall clock, %v on the monotonic, want 1s on both", seed, wall, mono)
		}
		switch {
		case near(real, 800*time.Millisecond): // 1s / 1.25
			seen["fast"] = true
		case near(real, 1333333333): // 1s / 0.75
			seen["slow"] = true
		default:
			t.Fatalf("seed %d: a 1s sleep on a drifting clock took %v of true time", seed, real)
		}
	}
	if !seen["fast"] || !seen["slow"] {
		t.Errorf("drifts seen %v, want both fast and slow", seen)
	}

	log := &qenv.ChoiceLog{C: qenv.NewSeeded(1)}
	s := qenv.New(log)
	defer s.Close()
	s.Process("node", false, func(context.Context) {})
	for _, ch := range log.Choices {
		if strings.HasPrefix(ch.Kind, "fault.clock.") {
			t.Errorf("a clock fault chosen with no Clocks: %v", ch)
		}
	}
}

// Clocks that cannot be (a drift of 1 or more would stop a slow clock or
// run it backwards; a negative size) panic with the field; so do Clocks
// changed after the first process, which would give processes different
// faults.
func TestClocksChecked(t *testing.T) {
	panics := func(want string, f func(s *qenv.Sim)) {
		t.Helper()
		defer func() {
			if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), want) {
				t.Errorf("%v, want a panic saying %q", r, want)
			}
		}()
		s := qenv.New(qenv.NewSeeded(1))
		defer s.Close()
		f(s)
	}
	for _, d := range []float64{1, 1.5, -0.1, math.NaN()} {
		panics("in [0, 1)", func(s *qenv.Sim) {
			s.Clocks = qenv.Clocks{Drift: d}
			s.Process("node", false, func(context.Context) {})
		})
	}
	panics("not negative", func(s *qenv.Sim) {
		s.Clocks = qenv.Clocks{Skew: -time.Second}
		s.Process("node", false, func(context.Context) {})
	})
	panics("set them before creating processes", func(s *qenv.Sim) {
		s.Process("a", false, func(context.Context) {})
		s.Clocks = qenv.Clocks{Skew: time.Second}
		_ = s.Run(time.Second)
	})
}

// An interval a process times on its clock lasts longer in true time on a
// slow clock: a Service call's timeout, and WaitFor.
func TestClockDriftTimesCalls(t *testing.T) {
	seen := map[time.Duration]bool{}
	for seed := uint64(1); seed <= 10; seed++ {
		s := clocked(seed, qenv.Clocks{Drift: 0.5}, 0)
		svc := qenv.NewService(s, "slow", nil, func(ctx context.Context, n int) (int, error) {
			qenv.Sleep(ctx, 10*time.Second) // longer than the call's timeout, drifting or not
			return n, nil
		})
		var call, wait time.Duration
		s.Process("client", false, func(ctx context.Context) {
			start := s.Now()
			_, _ = svc.Call(ctx, 1, time.Second) // the server takes 10 s: a timeout
			call = s.Now().Sub(start)
			start = s.Now()
			qenv.WaitFor(ctx, func() bool { return false }, time.Second)
			wait = s.Now().Sub(start)
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if call != 666666666 && call != 2*time.Second || wait != call {
			t.Errorf("seed %d: a 1s timeout took %v, a 1s WaitFor %v of true time, want 2/3 s (fast) or 2 s (slow) for both", seed, call, wait)
		}
		seen[call] = true
	}
	if len(seen) != 2 {
		t.Errorf("timeouts seen %v, want a fast clock's and a slow one's", seen)
	}
}

// A process's ToVirtual is its wall clock's view of a real-clock time: off
// true virtual time by its offset, and ToReal undoes it.
func TestProcessClockTranslation(t *testing.T) {
	s := clocked(1, qenv.Clocks{Skew: time.Second}, 0)
	defer s.Close()
	p := s.Process("node", false, func(context.Context) {})
	if p.ClockOffset() == 0 {
		t.Fatal("no skew chosen")
	}
	real := time.Now() //qenv:ok a real timestamp, as a library would read one
	v := p.ToVirtual(real)
	if got := v.Sub(s.ToVirtual(real)); (got - p.ClockOffset()).Abs() > time.Millisecond {
		t.Errorf("the process's view is %v off true virtual time, want its offset %v", got, p.ClockOffset())
	}
	if back := p.ToReal(v); back.Sub(real).Abs() > time.Millisecond {
		t.Errorf("ToReal(ToVirtual(t)) is %v off t", back.Sub(real))
	}
}

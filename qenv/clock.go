package qenv

import (
	"context"
	"fmt"
	"time"
)

// Clock faults. A process has two clocks, as a Go program does:
//
//   - the wall clock, read by Now: what time the process believes it is.
//     With Sim.Clocks it starts off true time by a skew (fault kind
//     "clock.skew": ahead or behind by Clocks.Skew) and may jump, forward
//     or back by Clocks.Jump, at any step (the nemeses
//     "jump.forward.<process>" and "jump.back.<process>"), as an NTP step,
//     a VM migration or a leap does;
//   - the monotonic clock, read by Mono and measured by Since: what Go's
//     time.Since and Time.Sub of two time.Now readings use. A skew or a jump
//     does not move it; a drift does.
//
// With Clocks.Drift both run fast or slow (fault kind "clock.drift", chosen
// when the process is created): they gain or lose that fraction of true
// time, and the intervals the process times with the same oscillator (its
// sleeps, its Service calls' timeouts, WaitFor) last a correspondingly
// shorter or longer true time.
//
// The simulation's own time (Sim.Now, the limit of a
// Signal.Wait) stays true: a clock fault changes what a process believes,
// not when things happen. Code that compares wall-clock times from two
// processes (an expiry stamped by one, checked by another) is broken by a
// skew; code that times an interval on the wall clock (Now().Sub, a time
// that lost its monotonic reading) by a jump; code that times it on the
// monotonic clock (Mono, Since) only by a drift larger than its margin.
//
// Skew and drift are fault choices at their kinds' rates (Rates), like any
// fault: at DefaultRates about one process in twenty gets one. Raise the
// rate to have every process drift (Rates.Set("clock.drift", 1)). Jumps
// are nemeses of their own family, "nemesis.jump" (DefaultRates: one step
// in 200, as crashes), a choice apart from crashes: raise it alone with
// Rates.Set("nemesis.jump", p), and crashes come as often as before.
var (
	clockSkew  = DeclareFaults("clock.skew", "ahead", "behind")
	clockDrift = DeclareFaults("clock.drift", "fast", "slow")
)

// Clocks are a simulation's clock faults (Sim.Clocks). A zero field leaves
// its fault out: the zero Clocks keeps every clock true, and a run makes
// no clock choice.
type Clocks struct {
	// Skew is how far ahead or behind true time a skewed process's wall
	// clock starts.
	Skew time.Duration
	// Jump is how far a process's wall clock jumps, forward or back.
	Jump time.Duration
	// Drift is the fraction of true time a drifting process's clocks gain
	// or lose, in [0, 1): 0.01 is 10 ms a second.
	Drift float64
}

// clocksFor checks the simulation's clocks when a process is created: they
// must be valid, and the same as when the first process was (a process
// created before a change would have other faults than its peers).
func (s *Sim) clocksFor() Clocks {
	c := s.Clocks
	if !(c.Drift >= 0 && c.Drift < 1) { // NaN too
		panic(fmt.Sprintf("qenv: Sim.Clocks.Drift is %v: it is a fraction of true time a clock gains or loses, in [0, 1) (0.01: 10 ms a second); a slow clock at 1 or more would stop or run backwards", c.Drift))
	}
	if c.Skew < 0 || c.Jump < 0 {
		panic(fmt.Sprintf("qenv: Sim.Clocks %+v: Skew and Jump are sizes, not negative (a skew or jump goes either way)", c))
	}
	if s.clocksSet == nil {
		s.clocksSet = &c
	} else if *s.clocksSet != c {
		panic(fmt.Sprintf("qenv: Sim.Clocks changed from %+v to %+v after the first process was created: set them before creating processes", *s.clocksSet, c))
	}
	return c
}

// clockProcess chooses a new process's clock faults and registers its
// jumps.
func (s *Sim) clockProcess(p *Process) {
	c := s.clocksFor()
	if c.Skew > 0 {
		switch s.FaultOf(clockSkew) {
		case "ahead":
			p.clock = c.Skew
		case "behind":
			p.clock = -c.Skew
		}
	}
	if c.Drift > 0 {
		p.born = s.Now()
		switch s.FaultOf(clockDrift) {
		case "fast":
			p.rate = c.Drift
		case "slow":
			p.rate = -c.Drift
		}
	}
	if c.Jump > 0 {
		up := func() bool { return !p.down }
		s.nemesisOf("jump.forward."+p.Name, []*Process{p}, up, func() { p.clock += c.Jump })
		s.nemesisOf("jump.back."+p.Name, []*Process{p}, up, func() { p.clock -= c.Jump })
	}
}

// drift is how far p's clocks have drifted from true time by now.
func (p *Process) drift(now time.Time) time.Duration {
	if p.rate == 0 {
		return 0
	}
	return time.Duration(float64(now.Sub(p.born)) * p.rate)
}

// drifted is how long an interval of d timed on p's clock (a sleep, a
// call's timeout) lasts in true time: shorter on a fast clock, longer on
// a slow one. A task of no process (nil) keeps true time.
func (p *Process) drifted(d time.Duration) time.Duration {
	if p == nil || p.rate == 0 {
		return d
	}
	return time.Duration(float64(d) / (1 + p.rate))
}

// clockOf is the time on t's process's wall clock: true time, off by the
// process's skew, jumps and drift so far.
func (s *Sim) clockOf(t *task) time.Time {
	now := s.Now()
	if t.proc == nil {
		return now
	}
	return now.Add(t.proc.ClockOffset())
}

// ClockOffset is how far the process's wall clock is from true time: its
// skew, its jumps and its drift so far (Sim.Clocks).
func (p *Process) ClockOffset() time.Duration { return p.clock + p.drift(p.sim.Now()) }

// realStart is Mono's origin outside a simulation.
var realStart = time.Now() //qenv:ok the real monotonic clock, outside a simulation

// Mono is the task's process's monotonic clock reading: time since an
// arbitrary origin, moved by the process's drift but by no skew or jump,
// as Go's monotonic reading is. Measure an interval with Since:
//
//	start := qenv.Mono(ctx)
//	/* ... */
//	if qenv.Since(ctx, start) > ttl { /* ... */ }
//
// which is what time.Since(start) of a time.Now() reading does in Go: a
// time.Time from Now carries no monotonic reading, so Now(ctx).Sub is wall
// arithmetic, which a jump moves. Outside a simulation it is the real
// monotonic clock.
func Mono(ctx context.Context) time.Duration {
	t := taskOf(ctx)
	if t == nil {
		return time.Since(realStart) //qenv:ok the real monotonic clock, outside a simulation
	}
	s := t.sim
	now := s.Now()
	m := now.Sub(s.start)
	if s.nat != nil && !s.nat.coop {
		// the native runtime in real time: its times carry no monotonic
		// reading (nativeNow), so measure on the host's own
		m = time.Since(s.nat.monoStart) //qenv:ok the host's monotonic clock, in real time
	}
	if t.proc != nil {
		m += t.proc.drift(now)
	}
	return m
}

// Since is the monotonic time elapsed since a Mono reading, on the task's
// process's clock.
func Since(ctx context.Context, m time.Duration) time.Duration { return Mono(ctx) - m }

// WaitFor blocks until ready holds or d has passed on the task's process's
// monotonic clock (longer or shorter in true time on a drifting one). It
// reports whether ready held. Outside a simulation d is real time.
func WaitFor(ctx context.Context, ready func() bool, d time.Duration) bool {
	t := taskOf(ctx)
	if t == nil {
		return pollReal(ctx, ready, time.Now().Add(d)) //qenv:ok real time, outside a simulation
	}
	return waitUntil(ctx, ready, t.sim.Now().Add(t.proc.drifted(d)))
}

// ToVirtual is Sim.ToVirtual on p's wall clock: a real-clock time a
// library read in p, as p's clock shows it (its skew, jumps and drift so
// far), so a library that keeps time through the seam is off as p is.
func (p *Process) ToVirtual(real time.Time) time.Time {
	return p.sim.ToVirtual(real).Add(p.ClockOffset())
}

// ToReal is Sim.ToReal of a time on p's clock (the inverse of ToVirtual).
func (p *Process) ToReal(virtual time.Time) time.Time {
	return p.sim.ToReal(virtual.Add(-p.ClockOffset()))
}

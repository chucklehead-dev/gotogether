package qenv

import (
	"fmt"
	"time"
)

// Clock faults. A process reads its own clock through Now: with
// Sim.ClockFaults, each process's clock starts off true time by a skew
// (fault kind "clock.skew": ahead or behind by ClockSkew), and may jump at
// any read (fault kind "clock.jump": forward or backward by ClockJump),
// as an NTP step, a VM migration or a leap does. The simulation's own
// time (Sim.Now, sleeps, timers, deadlines) stays true: a clock fault
// changes what a process believes the time is, not when things happen.
// Code whose correctness does not depend on its clock (a fencing token,
// a timeout used only for liveness) is unaffected; code that trusts it
// (a lease checked against the holder's clock) is not.
//
// With Sim.ClockDrift, a process's clock also runs fast or slow (fault kind
// "clock.drift", chosen when the process is created): it gains or loses
// that fraction of true time, and the intervals it times with the same
// oscillator (its sleeps, its Service calls' timeouts) last a
// correspondingly shorter or longer true time. Drift
// breaks what skew cannot: an interval timed on one clock (a lease's
// "TTL since I asked").
var (
	clockSkew  = DeclareFaults("clock.skew", "ahead", "behind")
	clockJump  = DeclareFaults("clock.jump", "forward", "backward")
	clockDrift = DeclareFaults("clock.drift", "fast", "slow")
)

// Default clock fault sizes.
const (
	DefaultClockSkew = time.Second
	DefaultClockJump = 5 * time.Second
)

// skewProcess chooses a new process's skew (ClockFaults).
func (s *Sim) skewProcess(p *Process) {
	if !s.ClockFaults {
		return
	}
	d := s.ClockSkew
	if d == 0 {
		d = DefaultClockSkew
	}
	switch s.FaultOf(clockSkew) {
	case "ahead":
		p.clock = d
	case "behind":
		p.clock = -d
	}
	if s.ClockDrift < 0 || s.ClockDrift >= 1 {
		panic(fmt.Sprintf("qenv: Sim.ClockDrift is %v: it is a fraction of true time a clock gains or loses, in [0, 1) (0.01: 10 ms a second); a slow clock at 1 or more would stop or run backwards", s.ClockDrift))
	}
	if s.ClockDrift > 0 {
		p.born = s.Now()
		switch s.FaultOf(clockDrift) {
		case "fast":
			p.rate = s.ClockDrift
		case "slow":
			p.rate = -s.ClockDrift
		}
	}
}

// drift is how far p's clock has drifted from true time by now.
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

// clockOf is the time on t's process's clock: true time and the process's
// offset, which may jump now (ClockFaults).
func (s *Sim) clockOf(t *task) time.Time {
	now := s.Now()
	p := t.proc
	if p == nil || !s.ClockFaults {
		return now
	}
	d := s.ClockJump
	if d == 0 {
		d = DefaultClockJump
	}
	switch s.FaultOf(clockJump) {
	case "forward":
		p.clock += d
	case "backward":
		p.clock -= d
	}
	return now.Add(p.clock + p.drift(now))
}

// ClockOffset is how far the process's clock is from true time (clock
// faults: Sim.ClockFaults), its drift so far included.
func (p *Process) ClockOffset() time.Duration { return p.clock + p.drift(p.sim.Now()) }

// ToVirtual is Sim.ToVirtual on p's clock: a real-clock time a library
// read in p, as p's clock shows it (its skew, jumps and drift so far), so
// a library that keeps time through the seam is off as p is. It reads the
// offset without a jump (no choice is made).
func (p *Process) ToVirtual(real time.Time) time.Time {
	return p.sim.ToVirtual(real).Add(p.ClockOffset())
}

// ToReal is Sim.ToReal of a time on p's clock (the inverse of ToVirtual).
func (p *Process) ToReal(virtual time.Time) time.Time {
	return p.sim.ToReal(virtual.Add(-p.ClockOffset()))
}

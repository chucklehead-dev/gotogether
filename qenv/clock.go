package qenv

import "time"

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
// that fraction of true time, and its sleeps, timed by the same
// oscillator, last a correspondingly shorter or longer true time. Drift
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

// drifted is how long a sleep of d on t's process's clock lasts in true
// time: shorter on a fast clock, longer on a slow one.
func (t *task) drifted(d time.Duration) time.Duration {
	if t.proc == nil || t.proc.rate == 0 {
		return d
	}
	return time.Duration(float64(d) / (1 + t.proc.rate))
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

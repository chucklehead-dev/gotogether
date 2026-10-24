package qenv

import (
	"fmt"
	"time"
)

// Stalls are a process's failures short of a crash, as nemeses of every
// process made after they are set, crashable or not, a service's own
// among them (each left out when zero):
//   - "pause.<name>": the process stops for Pause (a SIGSTOP, a long GC,
//     a VM migrating), its tasks frozen at their next scheduling point
//     and resumed after, unaware: what a lease holder that finds its lease
//     still valid after a pause has done (DDIA's "process pauses");
//   - "slow.<name>": a gray failure, alive and slow: for SlowFor, each
//     scheduling point of its tasks costs Slow more virtual time, so
//     health checks pass while its work times out.
//
// Their families are "nemesis.pause" and "nemesis.slow" (a Rates key, a
// Script's "nemesis.pause.<name>"). Not on the native runtime, whose
// tasks run on real goroutines.
type Stalls struct {
	Pause         time.Duration
	Slow, SlowFor time.Duration
}

// stall is a process's state of Stalls.
type stall struct {
	pausedUntil, slowUntil time.Time
}

// stallNemeses registers p's stall nemeses.
func (s *Sim) stallNemeses(p *Process) {
	if s.nat != nil {
		return
	}
	if st := s.Stalls; st.Pause > 0 {
		s.Nemesis("pause."+p.Name, func() bool { return !p.down && !s.now.Before(p.stall.pausedUntil) }, func() {
			p.stall.pausedUntil = s.now.Add(st.Pause)
			if s.Log != nil {
				s.Log(fmt.Sprintf("%v pause %s for %v", s.Elapsed(), p.ID(), st.Pause))
			}
		})
	}
	if st := s.Stalls; st.Slow > 0 && st.SlowFor > 0 {
		s.Nemesis("slow."+p.Name, func() bool { return !p.down && !s.now.Before(p.stall.slowUntil) }, func() {
			p.stall.slowUntil = s.now.Add(st.SlowFor)
			if s.Log != nil {
				s.Log(fmt.Sprintf("%v slow %s for %v", s.Elapsed(), p.ID(), st.SlowFor))
			}
		})
	}
}

// stalled holds the running task while its process is paused, or for the
// extra time a scheduling point costs while it is slow. It runs where the
// task resumes: a task frozen by a pause finds the time passed when it
// goes on, as a paused process does.
func (t *task) stalled() {
	p := t.proc
	if p == nil || t.stalling || t.sim.nat != nil {
		return
	}
	t.stalling = true
	defer func() { t.stalling = false }()
	s := t.sim
	if s.now.Before(p.stall.pausedUntil) {
		t.wait(nil, p.stall.pausedUntil)
	}
	if s.now.Before(p.stall.slowUntil) {
		t.wait(nil, s.now.Add(s.Stalls.Slow))
	}
}

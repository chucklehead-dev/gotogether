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
	s.stallsFixed()
	if s.nat != nil {
		return
	}
	if st := s.Stalls; st.Pause > 0 {
		s.nemesisOf("pause."+p.Name, []*Process{p}, func() bool { return !p.down && !s.now.Before(p.stall.pausedUntil) }, func() {
			p.stall.pausedUntil = s.now.Add(st.Pause)
			for _, t := range s.tasks { // those runnable now stop where they are
				if t.proc == p && t.queued {
					s.unqueue(t)
					s.hold(t, p.stall.pausedUntil)
				}
			}
			if s.Log != nil {
				s.Log(fmt.Sprintf("%v pause %s for %v", s.Elapsed(), p.ID(), st.Pause))
			}
		})
	}
	if st := s.Stalls; st.Slow > 0 && st.SlowFor > 0 {
		s.nemesisOf("slow."+p.Name, []*Process{p}, func() bool { return !p.down && !s.now.Before(p.stall.slowUntil) }, func() {
			p.stall.slowUntil = s.now.Add(st.SlowFor)
			if s.Log != nil {
				s.Log(fmt.Sprintf("%v slow %s for %v", s.Elapsed(), p.ID(), st.SlowFor))
			}
		})
	}
}

// stallsFixed panics if Stalls changed after the first process was made:
// the processes made before would have their nemeses for other Stalls,
// or none, silently.
func (s *Sim) stallsFixed() {
	if len(s.procs) == 0 {
		return
	}
	if s.stallsSet == nil {
		st := s.Stalls
		s.stallsSet = &st
		return
	}
	if *s.stallsSet != s.Stalls {
		panic("qenv: Sim.Stalls changed after the first process was made: set it before creating processes")
	}
}

// holdUntil is when a task of a stalled process that becomes runnable
// now may run: after its process's pause, or one scheduling point's Slow
// later while it is slow; zero if it is not stalled.
func (s *Sim) holdUntil(t *task) time.Time {
	p := t.proc
	if p == nil || s.nat != nil {
		return time.Time{}
	}
	until := p.stall.pausedUntil
	if s.now.Before(p.stall.slowUntil) {
		if u := s.now.Add(s.Stalls.Slow); u.After(until) {
			until = u
		}
	}
	return until
}

// stalledAt reports whether the task's process is paused at now: its
// predicate waits are no candidates until it goes on.
func (t *task) stalledAt(now time.Time) bool {
	return t.proc != nil && now.Before(t.proc.stall.pausedUntil)
}

// hold keeps a runnable task off the run queue until until. Its wake is
// decided (a Signal's notification, a deadline passed): it runs then, as a
// paused process's thread goes on where it stopped.
func (s *Sim) hold(t *task, until time.Time) {
	t.held, t.heldUntil = true, until
	s.held = append(s.held, t)
}

// release queues the held tasks whose time has come, but those whose
// process was paused again meanwhile.
func (s *Sim) release() {
	keep := s.held[:0]
	for _, t := range s.held {
		switch {
		case t.done || t.killed:
			t.held = false
		case t.stalledAt(s.now):
			t.heldUntil = t.proc.stall.pausedUntil
			keep = append(keep, t)
		case s.now.Before(t.heldUntil):
			keep = append(keep, t)
		default:
			t.held = false
			if !t.queued {
				t.queued = true
				s.runq.add(t.id, 1, s)
			}
		}
	}
	for i := len(keep); i < len(s.held); i++ {
		s.held[i] = nil
	}
	s.held = keep
}

// nextRelease is when the first held task is released, or a paused
// process's predicate waiter is a candidate again.
func (s *Sim) nextRelease() (time.Time, bool) {
	var at time.Time
	ok := false
	for _, t := range s.held {
		if t.done || t.killed {
			continue
		}
		u := t.heldUntil
		if t.stalledAt(s.now) {
			u = t.proc.stall.pausedUntil
		}
		if !ok || u.Before(at) {
			at, ok = u, true
		}
	}
	for _, t := range s.pred { // a paused process's predicate waiter checks again when it goes on
		if t.waiting && !t.done && !t.killed && t.stalledAt(s.now) {
			if u := t.proc.stall.pausedUntil; !ok || u.Before(at) {
				at, ok = u, true
			}
		}
	}
	return at, ok
}

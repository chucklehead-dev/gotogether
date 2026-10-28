package qenv

import (
	"context"
	"fmt"
	"time"
)

// A Termination is how an orchestrator stops a process gracefully: a
// signal (SIGTERM; Kubernetes' eviction, a rolling restart, a scale-down),
// then a kill at the grace deadline if the process has not exited by
// then, then a replacement (the process restarted as its next
// incarnation, as a StatefulSet's pod comes back under its name). A
// crash has no grace: it is the kill alone.
type Termination struct {
	// Grace is the kill deadline after the signal (terminationGracePeriodSeconds;
	// default 30 s, Kubernetes' default).
	Grace time.Duration
	// Short, if set, is option "short" of the fault kind
	// "terminate.<name>.grace": the signal comes with this shorter grace
	// (a node-pressure eviction's). Option 0 is Grace.
	Short time.Duration
	// Budget, if set, enables a process's terminate nemesis only while
	// fewer than Budget of the processes registered with it are
	// terminating: a PodDisruptionBudget's maxUnavailable. Crashes are
	// involuntary and not budgeted, as a PDB does not cover them.
	Budget int
}

// DefaultGrace is a Termination's default Grace: Kubernetes' default
// terminationGracePeriodSeconds.
const DefaultGrace = 30 * time.Second

// termination is a process's registered Termination, the group its
// Budget counts, and the kind of its short option (none without Short).
type termination struct {
	t     Termination
	group []*Process
	short FaultKind
	has   bool // short is declared
}

// Terminations registers a graceful termination of each of procs as a
// nemesis, "terminate.<name>" (family terminate: "nemesis.terminate" is
// its Rates key, a Script names "nemesis.terminate.<name>"), enabled
// while the process is up, not already terminating, and t's Budget
// allows: the scheduler sends the signal at any step (Process.Terminate).
// A process is registered once.
func (s *Sim) Terminations(t Termination, procs ...*Process) {
	if t.Grace <= 0 {
		t.Grace = DefaultGrace
	}
	if t.Short < 0 || t.Short != 0 && t.Short >= t.Grace {
		panic(fmt.Sprintf("qenv: Termination.Short %v: give a grace shorter than Grace (%v), or 0 for none", t.Short, t.Grace))
	}
	group := append([]*Process(nil), procs...)
	for _, p := range procs {
		if p.term != nil {
			panic(fmt.Sprintf("qenv: %s's terminations registered twice: register each process once (Sim.Terminations)", p.Name))
		}
		tm := &termination{t: t, group: group}
		if t.Short > 0 {
			tm.short, tm.has = DeclareFaults("terminate."+p.Name+".grace", "short").at(p), true
		}
		p.term = tm
		s.nemesisOf("terminate."+p.Name, []*Process{p}, func() bool { return !p.down && !p.terminating && tm.allows() }, p.Terminate)
	}
}

// allows reports whether the budget allows one more termination.
func (tm *termination) allows() bool {
	if tm.t.Budget <= 0 {
		return true
	}
	n := 0
	for _, q := range tm.group {
		if q.terminating && !q.down {
			n++
		}
	}
	return n < tm.t.Budget
}

// Terminate sends the process its termination signal now, as the
// terminate nemesis does (a test's rolling restart calls it): every
// context Terminating gave its tasks is cancelled, Terminating reports
// true, and the process has its grace (its Terminations', else
// DefaultGrace) to exit (Exit). At the deadline a process still running
// is killed (a crash); either way it is then restarted, if no OnCrash
// hook restarted it. A process that is down or already terminating is
// left as it is.
func (p *Process) Terminate() {
	if p.down || p.terminating {
		return
	}
	s := p.sim
	p.terminating = true
	for _, cancel := range p.termCancels {
		cancel()
	}
	p.termCancels = nil
	grace := DefaultGrace
	if p.term != nil {
		grace = p.term.t.Grace
		if p.term.has && s.FaultOf(p.term.short) == "short" {
			grace = p.term.t.Short
		}
	}
	if s.Log != nil {
		s.Log(fmt.Sprintf("%v terminate %s (grace %v)", s.Elapsed(), p.ID(), grace))
	}
	inc := p.Incarnation
	s.spawn(s.base, "terminate."+p.Name, nil, func(ctx context.Context) {
		// the orchestrator's: the kill at the deadline, unless the
		// incarnation ended before (an exit, a crash)
		if !waitUntil(ctx, func() bool { return p.down || p.Incarnation != inc }, s.Now().Add(grace)) {
			if s.Log != nil {
				s.Log(fmt.Sprintf("%v kill %s at its grace", s.Elapsed(), p.ID()))
			}
			p.Crash()
		}
		if p.down && p.Incarnation == inc {
			p.Restart() // its replacement, unless a hook restarted it
		}
	})
	s.changed(p)
}

// Terminating reports whether the process was sent its termination and
// has not restarted since.
func (p *Process) Terminating() bool { return p.terminating && !p.down }

// Exit ends the process of its own accord, as a graceful termination's
// last step does (a drain done): every task is stopped as by Crash, the
// OnCrash hooks run (the doubles release what its tasks held), and a
// task of the process calling it stops in it. A process given a
// termination is restarted after (its replacement); any other stays down
// unless a hook restarts it.
func (p *Process) Exit() {
	if p.down {
		return
	}
	p.exiting = true
	p.Crash()
}

// Terminating returns a context derived from ctx (its values, its
// cancellation) that is also cancelled when ctx's process is sent its
// termination (Process.Terminate; already cancelled if it was): what a
// task that must wind down at SIGTERM reads, as signal.NotifyContext
// does in a real binary. Outside a simulation, or in a task of no
// process, it returns ctx.
func Terminating(ctx context.Context) context.Context {
	p := ProcessOf(ctx)
	if p == nil {
		return ctx
	}
	c, cancel := context.WithCancel(ctx)
	if p.terminating {
		cancel()
		return c
	}
	p.termCancels = append(p.termCancels, cancel)
	return c
}

// onChange registers a hook run after a process's life changes: it was
// sent its termination, it crashed or exited, it restarted. A router
// double's view follows them (Router).
func (s *Sim) onChange(h func(*Process)) { s.onLife = append(s.onLife, h) }

// changed runs the life-change hooks for p.
func (s *Sim) changed(p *Process) {
	for _, h := range s.onLife {
		h(p)
	}
}

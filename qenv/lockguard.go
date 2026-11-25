package qenv

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/chucklehead-dev/gotogether/internal/wovenrt"
	"github.com/chucklehead-dev/gotogether/qrun"
)

// Lock invariants: a property of the state a mutex guards that must hold
// whenever the lock is released, as Gobra attaches one to a mutex and
// proves it at every Unlock. Here it is checked, not proved: at every
// unlock of a guarded lock inside a simulation, while the lock is still
// held, so a critical section that leaves the state broken fails at the
// release that broke it, in the task that released it, rather than at
// whatever later read happens to notice. Inside the section the invariant
// may be false; only the release publishes the state to the lock's next
// holder.
//
// The unlocks it sees are the simulation's: woven code's (qenv/weave's
// aspects under Orchestrion, and qweave, which also weaves method values
// and promoted calls; both call package woven, which calls unlockKey), on
// New's scheduler and on the native runtime without a Platform. Under
// gosim (a native simulation with a Platform) sync is gosim's own and no
// unlock reaches qenv: Guard refuses there instead of checking nothing.
//
// The declarative form is a directive on the lock's field, woven by qweave
// at every Unlock of that field (DESIGN §13, "Lock invariants"):
//
//	type Account struct {
//		mu   sync.Mutex //quint:guards conserved
//		a, b int
//	}
//
//	func (x *Account) conserved() bool { return x.a+x.b == 100 }
//
// x.mu.Unlock() (a call, a defer, a method value) becomes
// woven.MutexUnlockGuarded, which hands unlockKey the field's invariants
// bound to x: checked as a Guard's are (after the Guards of the same
// mutex), named Account.conserved, "guarded at" the field's line. It needs
// no registration and no construction site, so zero values and every
// constructor are covered; what it cannot see is an Unlock not through the
// field (its address taken), which qweave refuses to weave.

// guardsKey carries a simulation's lock invariants in its context (New
// puts them in the base every task's context derives from), so Guard finds
// them from a task's context, from the simulation's Context, or through
// the calling goroutine's run (a task's goroutine, or the one that created
// the simulation). Per run: parallel explorations' simulations each have
// their own. Read-only.
var guardsKey = qrun.NewKey[*lockGuards]("qenv.guards")

// lockGuards are one simulation's lock invariants, by the lock's key (the
// *sync.Mutex or *sync.RWMutex woven code passes). They are used one task
// at a time, as the simulation is.
type lockGuards struct {
	sim    *Sim
	by     map[any][]*lockGuard
	closed bool // the simulation closed: its invariants are gone
}

type lockGuard struct {
	name  string
	site  string // where Guard registered it
	holds func() bool
}

// guardCall is the invariant being evaluated (Sim.guarding): every seam
// refuses while it is set.
type guardCall struct {
	g *lockGuard
	t *task
}

// Guard attaches an invariant to mu in the simulation ctx runs in: at every
// release of mu's exclusive hold by a task (Mutex.Unlock, RWMutex.Unlock;
// RUnlock is not checked, as a reader publishes nothing), holds is
// called just before the release, the lock still held, and if it returns
// false the task panics with a *LockInvariantError naming the invariant,
// the Unlock, the task and the scheduling step. Run re-raises it, and an
// exploration (qtest's System.Explore) reports the run as failed, its
// seed reproducing it. A task unwinding after a crash or at Close is not
// checked: its critical section was cut short, not finished.
//
// mu is a *sync.Mutex or a *sync.RWMutex, locked and unlocked by woven
// code (qenv/weave, qweave) or through package woven: those are the locks
// qenv simulates. Call Guard from a task, or from the goroutine that
// created the simulation, after New; several invariants on one lock are
// checked in the order registered, and they are dropped at Close.
//
// holds reads the state mu guards and returns. It must not change it, and
// must not call a seam (Yield, Wait, Sleep, Go, Spawn, a choice, a double's
// operation, a lock, Notify) or record a step: those would change the
// run's choices, so each one panics with the fix while holds runs. A
// passing guarded run makes exactly the choices the unguarded one makes.
//
// Outside a simulation Guard does nothing: a real Unlock is not
// intercepted there, and a registry outside any run would be one that
// parallel runs share. Under gosim, which simulates sync itself so that
// no Unlock reaches qenv, it panics rather than check nothing.
func Guard(ctx context.Context, mu sync.Locker, name string, holds func() bool) {
	if name == "" || holds == nil {
		panic("qenv: Guard needs the invariant's name and its holds function")
	}
	switch mu.(type) {
	case *sync.Mutex, *sync.RWMutex:
	default:
		panic(fmt.Sprintf("qenv: Guard of a %T: only a *sync.Mutex or a *sync.RWMutex is a lock qenv simulates (woven code's Lock and Unlock); "+
			"guard the mutex itself (&x.mu), not a Locker over it", mu))
	}
	gs, ok := qrun.Get(ctx, guardsKey)
	if !ok {
		return // outside a simulation
	}
	if gs.sim.nat != nil && gs.sim.nat.coop {
		panic(fmt.Sprintf("qenv: Guard(%q) on a native simulation with a Platform (gosim): gosim simulates sync itself, so no Unlock "+
			"reaches qenv and the invariant would never be checked; check it on New's scheduler (or NewNative(c, nil)) with the code woven, "+
			"or leave the Guard out of the gosim build", name))
	}
	if gs.closed {
		panic(fmt.Sprintf("qenv: Guard(%q) after its simulation closed; register it after New, before Close", name))
	}
	site := "?"
	if _, file, line, ok := runtime.Caller(1); ok {
		site = fmt.Sprintf("%s:%d", filepath.Base(file), line)
	}
	if gs.by == nil {
		gs.by = map[any][]*lockGuard{}
	}
	gs.by[mu] = append(gs.by[mu], &lockGuard{name: name, site: site, holds: holds})
}

// close drops the invariants (Sim.Close).
func (gs *lockGuards) close() {
	gs.closed, gs.by = true, nil
}

// checkGuards evaluates key's invariants as t releases its exclusive hold,
// the lock still held: those Guard registered, then fields, the ones its
// field declares (//quint:guards, which woven code passes to this
// Unlock).
func (s *Sim) checkGuards(t *task, key any, fields []wovenrt.Invariant) {
	gs := s.guards
	if len(gs.by) == 0 && len(fields) == 0 || t.killed {
		return
	}
	for _, g := range gs.by[key] {
		s.checkGuard(g, t)
	}
	for _, f := range fields {
		s.checkGuard(&lockGuard{name: f.Name, site: f.Site, holds: f.Holds}, t)
	}
}

// checkGuard panics with a *LockInvariantError if g is false.
func (s *Sim) checkGuard(g *lockGuard, t *task) {
	if !s.evalGuard(g, t) {
		panic(&LockInvariantError{Name: g.name, Guarded: g.site, Unlock: unlockSite(), Task: t.name, Step: s.steps, Native: s.nat != nil})
	}
}

// evalGuard runs g's holds with every seam refusing.
func (s *Sim) evalGuard(g *lockGuard, t *task) bool {
	s.guarding.Store(&guardCall{g, t})
	defer s.guarding.Store(nil)
	return g.holds()
}

// refuse panics if a lock invariant is being evaluated: what (a seam, of
// detail if not empty) would change the run's choices. Seams call it on
// every use, so it formats nothing unless it panics.
func (s *Sim) refuse(what, detail string) {
	if c := s.guarding.Load(); c != nil {
		if detail != "" {
			what += " (" + detail + ")"
		}
		panic(fmt.Sprintf("qenv: %s inside lock invariant %s's holds (guarded at %s, task %s): an invariant reads the state its lock guards "+
			"and returns; a seam (Yield, Wait, Sleep, Go, a choice, a double's operation, a lock, Notify) or a recorded step there would "+
			"change the run's choices. Read the state without it", what, c.g.name, c.g.site, c.t.name))
	}
}

// unlockSite is the code that called the Unlock being checked: the first
// frame outside qenv, package woven, its state and sync.
func unlockSite() string {
	pcs := make([]uintptr, 32)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	for {
		f, more := frames.Next()
		if !internalFrame(f.Function) {
			return fmt.Sprintf("%s:%d", filepath.Base(f.File), f.Line)
		}
		if !more {
			return "?"
		}
	}
}

// internalFrame reports whether fn is on the way from an Unlock to its
// check: qenv's own functions, woven's and wovenrt's, sync's.
func internalFrame(fn string) bool {
	for _, p := range []string{
		"github.com/chucklehead-dev/gotogether/qenv.",
		"github.com/chucklehead-dev/gotogether/woven.",
		"github.com/chucklehead-dev/gotogether/internal/wovenrt.",
		"sync.",
	} {
		if strings.HasPrefix(fn, p) {
			return true
		}
	}
	return false
}

// A LockInvariantError is a lock invariant (Guard) found false as its
// lock was released: the releasing task panics with it, Sim.Run re-raises
// it, and qtest's explorations report the run as failed (the value of its
// *qtest.RunPanic).
type LockInvariantError struct {
	Name    string // the invariant's, as Guard named it (Type.method for a //quint:guards field's)
	Guarded string // where Guard registered it, or where the //quint:guards field is declared (file:line)
	Unlock  string // the code that released the lock with the invariant false (file:line)
	Task    string // the task that released it
	Step    int    // the scheduling step it happened in (New's scheduler)
	Native  bool   // on the native runtime, which counts no steps
}

// Error says which invariant broke, where and in which task, and what to
// fix.
func (e *LockInvariantError) Error() string {
	at := fmt.Sprintf("step %d", e.Step)
	if e.Native {
		at = "native runtime"
	}
	return fmt.Sprintf("qenv: lock invariant %s (guarded at %s) broken at Unlock (%s) by task %s (%s): "+
		"the critical section left it false; fix the code between Lock and Unlock", e.Name, e.Guarded, e.Unlock, e.Task, at)
}

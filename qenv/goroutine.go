package qenv

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
)

// Which simulation each goroutine belongs to: its tasks' goroutines (qenv
// creates them), and the goroutine that created it (which runs it, and
// records around it). qobs asks for a step whose context carries no
// recorder, so that context.Background() inside a simulation records into
// the simulation's run (the resolver it installs). Go has no goroutine-local
// storage; the goroutine id comes from the runtime's stack header, read
// only when a task starts and on that fallback path.
// Process-wide.
var owners struct {
	sync.Mutex
	m map[int64][]owner // a stack: a goroutine may create a simulation inside another's lifetime
	// ended holds the contexts of tasks whose goroutines have exited,
	// until their simulation closes: a goroutine a task started with `go`
	// often outlives it, and its escaped steps still belong to the run
	// (hooks.SetCreatorRun). Go never reuses a goroutine id.
	ended map[int64]context.Context
}

// owner is a goroutine's simulation, and its task if it runs one (nil for
// the goroutine that created the simulation).
type owner struct {
	s *Sim
	t *task
}

// live counts the simulations not yet closed: with none, the per-step
// discard check costs an atomic load.
// Process-wide.
var live atomic.Int64

func init() {
	owners.m = map[int64][]owner{}
	owners.ended = map[int64]context.Context{}
	hooks.SetCreatorRun(func(id int64) (context.Context, bool) {
		owners.Lock()
		defer owners.Unlock()
		if st := owners.m[id]; len(st) > 0 {
			if o := st[len(st)-1]; o.t != nil {
				return o.t.ctx, true
			}
			return st[len(st)-1].s.base, true // the goroutine that created the simulation
		}
		ctx, ok := owners.ended[id]
		return ctx, ok
	})
	hooks.SetDiscard(func() bool {
		if live.Load() == 0 {
			return false
		}
		o, ok := ownerOf(goid())
		return ok && o.t != nil && o.t.killed
	})
	hooks.SetClock(func() (time.Time, bool) {
		if live.Load() == 0 {
			return time.Time{}, false
		}
		if o, ok := ownerOf(goid()); ok {
			return o.s.Now(), true // true time: a process's clock may be skewed, a step's time is the run's
		}
		return time.Time{}, false
	})
	hooks.SetResolver(func() (context.Context, bool) {
		if live.Load() == 0 {
			return nil, false
		}
		o, ok := ownerOf(goid())
		if !ok {
			return nil, false
		}
		if o.t != nil {
			return o.t.ctx, true
		}
		return o.s.base, true // the goroutine that created the simulation
	})
}

// current returns the context of the simulation task running on the
// calling goroutine, whatever context the caller has (none: woven code).
// It is how code rewritten at build time (qenv/weave) finds the
// simulation it runs in; false outside any task.
func current() (context.Context, bool) {
	o, ok := ownerOf(goid())
	if !ok || o.t == nil {
		return nil, false
	}
	return o.t.ctx, true
}

// goid is the calling goroutine's id.
func goid() int64 { return hooks.GoroutineID() }

func own(id int64, s *Sim, t *task) {
	owners.Lock()
	owners.m[id] = append(owners.m[id], owner{s, t})
	owners.Unlock()
}

func disown(id int64, s *Sim) {
	owners.Lock()
	defer owners.Unlock()
	st := owners.m[id]
	for i := len(st) - 1; i >= 0; i-- {
		if st[i].s == s {
			if t := st[i].t; t != nil && !s.forgotten {
				owners.ended[id] = t.ctx
				s.ended = append(s.ended, id)
			}
			st = append(st[:i], st[i+1:]...)
			break
		}
	}
	if len(st) == 0 {
		delete(owners.m, id)
	} else {
		owners.m[id] = st
	}
}

func ownerOf(id int64) (owner, bool) {
	owners.Lock()
	defer owners.Unlock()
	if st := owners.m[id]; len(st) > 0 {
		return st[len(st)-1], true
	}
	return owner{}, false
}

// forget drops s's ended tasks (owners.ended): once it closes, a step from
// a goroutine they started is no run's.
func forget(s *Sim) {
	owners.Lock()
	defer owners.Unlock()
	s.forgotten = true
	for _, id := range s.ended {
		delete(owners.ended, id)
	}
	s.ended = nil
}

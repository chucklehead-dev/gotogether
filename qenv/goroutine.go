package qenv

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
)

// Which simulation each goroutine belongs to: its tasks' goroutines (qenv
// creates them), and the goroutine that created it (which runs it, and
// records around it). qobs asks for a step whose context carries no
// recorder, so that context.Background() inside a simulation records into
// the simulation's run (the resolver it installs). Go has no goroutine-local
// storage; the goroutine id comes from the runtime's stack header, read
// only when a task starts and on that fallback path. The goroutines'
// simulations are in shards by goroutine id (shardOf), each with its own
// lock: parallel explorations' lookups do not wait on one another.
// Process-wide.
var owners struct {
	sync.Mutex // ended's, taken before a shard's
	// ended holds the contexts of tasks whose goroutines have exited,
	// until their simulation closes: a goroutine a task started with `go`
	// often outlives it, and its escaped steps still belong to the run
	// (hooks.SetCreatorRun). Go never reuses a goroutine id.
	ended map[int64]context.Context
}

// shards hold each goroutine's simulations: a stack, as a goroutine may
// create a simulation inside another's lifetime.
// Process-wide.
var shards [64]ownerShard

type ownerShard struct {
	sync.RWMutex
	m map[int64][]owner
	_ [32]byte // a shard to a cache line
}

// shardOf is goroutine id's shard.
func shardOf(id int64) *ownerShard { return &shards[uint64(id)%uint64(len(shards))] }

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
	for i := range shards {
		shards[i].m = map[int64][]owner{}
	}
	owners.ended = map[int64]context.Context{}
	hooks.SetCreatorRun(func(id int64) (context.Context, bool) {
		if o, ok := ownerOf(id); ok {
			if o.t != nil {
				return o.t.ctx, true
			}
			return o.s.base, true // the goroutine that created the simulation
		}
		// (disown adds a task's goroutine here before it leaves its shard)
		owners.Lock()
		defer owners.Unlock()
		ctx, ok := owners.ended[id]
		return ctx, ok
	})
	hooks.SetSite(func(ctx context.Context) hooks.Site {
		if live.Load() == 0 {
			return hooks.Site{} // the per-step check costs an atomic load
		}
		// the context's task first, as taskOf finds it: a task's step costs
		// no goroutine lookup
		if ctx != nil {
			if t, ok := ctx.Value(ctxKey{}).(*task); ok {
				return hooks.Site{Clock: t.sim, Discard: t.killed}
			}
		}
		if o, ok := ownerOf(goid()); ok {
			// true time: a process's clock may be skewed, a step's time is the run's
			return hooks.Site{Clock: o.s, Discard: o.t != nil && o.t.killed}
		}
		return hooks.Site{}
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
	sh := shardOf(id)
	sh.Lock()
	sh.m[id] = append(sh.m[id], owner{s, t})
	sh.Unlock()
}

func disown(id int64, s *Sim) {
	owners.Lock() // ended's, first: the goroutine is in ended before it leaves its shard
	defer owners.Unlock()
	sh := shardOf(id)
	sh.Lock()
	defer sh.Unlock()
	st := sh.m[id]
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
		delete(sh.m, id)
	} else {
		sh.m[id] = st
	}
}

func ownerOf(id int64) (owner, bool) {
	sh := shardOf(id)
	sh.RLock()
	defer sh.RUnlock()
	if st := sh.m[id]; len(st) > 0 {
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

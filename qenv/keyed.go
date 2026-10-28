package qenv

import (
	"context"
	"fmt"

	"github.com/chucklehead-dev/gotogether/internal/wovenrt"
)

func init() {
	wovenrt.Current, wovenrt.Lock, wovenrt.TryLock, wovenrt.Unlock = current, lockKey, tryLockKey, unlockKey
	wovenrt.RLock, wovenrt.TryRLock, wovenrt.RUnlock, wovenrt.Add, wovenrt.Wait = rLockKey, tryRLockKey, rUnlockKey, addKey, waitKey
	wovenrt.Go = func(ctx context.Context, fn func()) { Go(ctx, "go", func(context.Context) { fn() }) }
}

// Locks and wait groups keyed by the real object they stand for: what code
// rewritten at build time (qenv/weave's aspects, through package woven)
// calls in place of sync.Mutex, sync.RWMutex and sync.WaitGroup methods, so that blocking on them is
// waiting in the simulation, and taking them is a scheduling point Hegel or
// a seed chooses around. The real object is only a key; its own state is
// never touched while its users run in the simulation.
//
// The semantics are sync's: a waiting writer blocks new readers, an unlock
// of an unlocked mutex and a negative wait group counter panic.

type rwState struct {
	writer         bool
	readers        int
	writersWaiting int
}

type wgState struct{ n int }

func keyedOf[T any](ctx context.Context, key any, what string) (*T, *task) {
	t := taskOf(ctx)
	if t == nil {
		panic(fmt.Sprintf("qenv: %s outside a simulation task", what))
	}
	s := t.sim
	if s.keyed == nil {
		s.keyed = map[any]any{}
	}
	v, ok := s.keyed[key].(*T)
	if !ok {
		v = new(T)
		s.keyed[key] = v
	}
	return v, t
}

// lockKey takes the lock named by key exclusively (Mutex.Lock,
// RWMutex.Lock).
func lockKey(ctx context.Context, key any) {
	l, _ := keyedOf[rwState](ctx, key, "Lock")
	Yield(ctx)
	l.writersWaiting++
	Wait(ctx, func() bool { return !l.writer && l.readers == 0 })
	l.writersWaiting--
	l.writer = true
}

// tryLockKey takes the lock exclusively if it is free.
func tryLockKey(ctx context.Context, key any) bool {
	l, _ := keyedOf[rwState](ctx, key, "TryLock")
	Yield(ctx)
	if l.writer || l.readers > 0 {
		return false
	}
	l.writer = true
	return true
}

// unlockKey releases an exclusive hold.
func unlockKey(ctx context.Context, key any) {
	l, _ := keyedOf[rwState](ctx, key, "Unlock")
	if !l.writer {
		panic("qenv: unlock of unlocked mutex")
	}
	l.writer = false
}

// rLockKey takes the lock shared (RWMutex.RLock): it waits while a writer
// holds it or waits for it.
func rLockKey(ctx context.Context, key any) {
	l, _ := keyedOf[rwState](ctx, key, "RLock")
	Yield(ctx)
	Wait(ctx, func() bool { return !l.writer && l.writersWaiting == 0 })
	l.readers++
}

// tryRLockKey takes the lock shared if no writer holds or waits for it.
func tryRLockKey(ctx context.Context, key any) bool {
	l, _ := keyedOf[rwState](ctx, key, "TryRLock")
	Yield(ctx)
	if l.writer || l.writersWaiting > 0 {
		return false
	}
	l.readers++
	return true
}

// rUnlockKey releases a shared hold.
func rUnlockKey(ctx context.Context, key any) {
	l, _ := keyedOf[rwState](ctx, key, "RUnlock")
	if l.readers == 0 {
		panic("qenv: RUnlock of unlocked RWMutex")
	}
	l.readers--
}

// addKey adds n to the wait group named by key (WaitGroup.Add, Done).
func addKey(ctx context.Context, key any, n int) {
	g, _ := keyedOf[wgState](ctx, key, "WaitGroup.Add")
	g.n += n
	if g.n < 0 {
		panic("qenv: negative WaitGroup counter")
	}
}

// waitKey waits until the wait group's counter is zero (WaitGroup.Wait).
func waitKey(ctx context.Context, key any) {
	g, _ := keyedOf[wgState](ctx, key, "WaitGroup.Wait")
	Yield(ctx)
	Wait(ctx, func() bool { return g.n == 0 })
}

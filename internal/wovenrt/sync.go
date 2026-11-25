// Package wovenrt is the state behind package woven, the functions woven
// code calls (DESIGN §13, §17, §26, §60): the simulation's side of a woven sync
// call, which qenv installs at init, and the coverage blocks and mutants
// woven files register, which qcover and qmut read. It is kept out of the
// public API, and imports nothing of the core but qrun and runkeys, so that
// any other core package can be woven (mutated, for the core's own
// mutation testing) without an import cycle: woven code imports woven,
// which imports this.
package wovenrt

import "context"

// An Invariant is a lock invariant a //quint:guards field declares, as
// woven code passes it to an exclusive unlock of that field (woven's
// Invariant is its public form): the invariant's name (Type.method), where
// the field is declared (file:line), and the method, bound to the struct
// the field belongs to.
type Invariant struct {
	Name, Site string
	Holds      func() bool
}

// The simulation's side of a woven sync call. Current is the context of
// the simulation task running on the calling goroutine (false outside
// one, and in a program that links no qenv); the rest take that context
// and the real object as the key. Go starts fn as a new task of ctx's
// simulation (a //qenv:go statement). UnlockGuarded is Unlock that also
// checks the field's invariants (qweave's //quint:guards).
// Process-wide: installed by qenv at init.
var (
	Current       = func() (context.Context, bool) { return nil, false }
	Go            func(ctx context.Context, fn func())
	Lock          func(ctx context.Context, key any)
	TryLock       func(ctx context.Context, key any) bool
	Unlock        func(ctx context.Context, key any)
	UnlockGuarded func(ctx context.Context, key any, invs []Invariant)
	RLock         func(ctx context.Context, key any)
	TryRLock      func(ctx context.Context, key any) bool
	RUnlock       func(ctx context.Context, key any)
	Add           func(ctx context.Context, key any, n int)
	Wait          func(ctx context.Context, key any)
)

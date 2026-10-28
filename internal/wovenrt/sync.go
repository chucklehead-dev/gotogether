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

// The simulation's side of a woven sync call. Current is the context of
// the simulation task running on the calling goroutine (false outside
// one, and in a program that links no qenv); the rest take that context
// and the real object as the key. Go starts fn as a new task of ctx's
// simulation (a //qenv:go statement).
// Process-wide: installed by qenv at init.
var (
	Current  = func() (context.Context, bool) { return nil, false }
	Go       func(ctx context.Context, fn func())
	Lock     func(ctx context.Context, key any)
	TryLock  func(ctx context.Context, key any) bool
	Unlock   func(ctx context.Context, key any)
	RLock    func(ctx context.Context, key any)
	TryRLock func(ctx context.Context, key any) bool
	RUnlock  func(ctx context.Context, key any)
	Add      func(ctx context.Context, key any, n int)
	Wait     func(ctx context.Context, key any)
)

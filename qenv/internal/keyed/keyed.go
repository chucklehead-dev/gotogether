// Package keyed is how code woven at build time (qenv/weave) reaches the
// simulation it runs in, kept out of qenv's public API: qenv installs the
// functions at init. Locks and wait groups are keyed by the real object
// they stand for; the semantics are sync's.
package keyed

import "context"

// The simulation's side of a woven sync call. Current is the context of
// the simulation task running on the calling goroutine (false outside
// one); the rest take that context and the real object as the key.
var (
	Current  func() (context.Context, bool)
	Lock     func(ctx context.Context, key any)
	TryLock  func(ctx context.Context, key any) bool
	Unlock   func(ctx context.Context, key any)
	RLock    func(ctx context.Context, key any)
	TryRLock func(ctx context.Context, key any) bool
	RUnlock  func(ctx context.Context, key any)
	Add      func(ctx context.Context, key any, n int)
	Wait     func(ctx context.Context, key any)
)

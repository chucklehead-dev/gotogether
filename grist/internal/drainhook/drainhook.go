// Package drainhook hands gristsim a started node's drain (grist's
// Drain schedule, the code Main runs at SIGTERM), so a simulated
// cluster's node drains as a real one does without grist exporting its
// node: App.Start hands the node to the hook its context carries.
package drainhook

import (
	"context"
	"time"
)

// A Node is a started node, as App.Start hands it to the hook.
type Node interface {
	// Drain runs the node's drain schedule d (a grist.Drain, an
	// orchestrator's defaults for its zero fields) with srv's delay and
	// shutdown, then the node's settle (not recorded: a simulation's are
	// its Settle's), stop (the node's work ends) and the relay's flush.
	Drain(ctx context.Context, d any, srv Server, stop func()) error
}

// A Server is a simulated node's API as a drain's.
type Server interface {
	// Keep goes on serving, not ready, until until (the delay).
	Keep(ctx context.Context, until time.Time) error
	// Shutdown stops taking requests and waits for those in flight
	// until until, failing if some still are then.
	Shutdown(ctx context.Context, until time.Time) error
}

type key struct{}

// With is ctx carrying the hook f, which App.Start calls with the node
// it starts.
func With(ctx context.Context, f func(Node)) context.Context { return context.WithValue(ctx, key{}, f) }

// Of is the hook ctx carries, or nil.
func Of(ctx context.Context) func(Node) {
	f, _ := ctx.Value(key{}).(func(Node))
	return f
}

package grist

import (
	"context"
	"fmt"
)

// A Dep is one of an application's dependencies (a mailer, a bank, a
// ledger), reached through a context: a processor's effect reads it from
// its ctx (Of), and whatever runs the node attaches it (With: gristsim's
// Config.Setup, Main's Dependencies, a real tier's harness). One value
// per run, never a package-level one that runs in parallel would share;
// typed, so no application writes a context key of its own.
type Dep[T any] struct {
	name     string
	fallback T
	key      *depKey
}

type depKey struct{ name string }

// NewDep declares a dependency named name (for errors), whose Of is
// fallback where none was attached: a stand-in that refuses every call
// with the fix (attach one), so a node started without it fails its
// effects visibly instead of panicking.
func NewDep[T any](name string, fallback T) *Dep[T] {
	return &Dep[T]{name: name, fallback: fallback, key: &depKey{name: name}}
}

// Name is the dependency's name.
func (d *Dep[T]) Name() string { return d.name }

// With is ctx with v attached as d.
func (d *Dep[T]) With(ctx context.Context, v T) context.Context {
	return context.WithValue(ctx, d.key, v)
}

// Of is d as attached to ctx, else its fallback.
func (d *Dep[T]) Of(ctx context.Context) T {
	if v, ok := ctx.Value(d.key).(T); ok {
		return v
	}
	return d.fallback
}

// Attached reports whether d is attached to ctx.
func (d *Dep[T]) Attached(ctx context.Context) bool {
	_, ok := ctx.Value(d.key).(T)
	return ok
}

// String names the dependency.
func (d *Dep[T]) String() string { return fmt.Sprintf("grist dependency %s", d.name) }

// Package qenv is a stub of the real package for analysistest (GOPATH
// mode): the analyzers look at import paths, not the package's code.
package qenv

import (
	"context"
	"time"
)

// Sim is a simulation.
type Sim struct{}

// Context is the simulation's context.
func (*Sim) Context() context.Context { return context.Background() }

// Nominal is a chooser.
type Nominal struct{}

// Go starts fn as a task.
func Go(ctx context.Context, name string, fn func(ctx context.Context)) {}

// Now is the simulation's time.
func Now(ctx context.Context) time.Time { return time.Time{} }

// Sleep sleeps in virtual time.
func Sleep(ctx context.Context, d time.Duration) {}

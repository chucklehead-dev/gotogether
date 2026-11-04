package qrun_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qrun"
)

// Explicit simulation state must win even when the creating goroutine already
// has a different started run. Inheriting the same key would hide a reversed
// fallback order.
func TestCurrentSimulationPrecedesStartedRun(t *testing.T) {
	key := qrun.NewKey[string]("resolver-priority")
	end := qrun.Start(qrun.With(context.Background(), key, "started"))
	defer end()
	s := qenv.New(qenv.WithContext(qenv.Nominal{}, qrun.With(context.Background(), key, "simulation")))
	defer s.Close()
	if got, ok := qrun.Get(context.Background(), key); !ok || got != "simulation" {
		t.Fatalf("creator resolves %q, %v; want simulation", got, ok)
	}
	s.Spawn("task", func(context.Context) {
		if got, ok := qrun.Get(context.Background(), key); !ok || got != "simulation" {
			t.Errorf("task resolves %q, %v; want simulation", got, ok)
		}
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if got, ok := qrun.Get(context.Background(), key); !ok || got != "started" {
		t.Fatalf("after simulation closes: %q, %v; want started", got, ok)
	}
	end()
	if got, ok := qrun.Get(context.Background(), key); ok {
		t.Fatalf("after both scopes end: %q, %v", got, ok)
	}
}

// A live simulation on another goroutine must not suppress this goroutine's
// started run when the simulation resolver finds no local owner.
func TestCurrentStartedFallbackWithForeignSimulation(t *testing.T) {
	key := qrun.NewKey[string]("resolver-foreign-simulation")
	end := qrun.Start(qrun.With(context.Background(), key, "started"))
	defer end()
	ready, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() { //qenv:ok a foreign simulation owner for the isolation control
		defer close(done)
		s := qenv.New(qenv.WithContext(qenv.Nominal{}, qrun.With(context.Background(), key, "foreign")))
		defer s.Close()
		close(ready)
		<-release
	}()
	t.Cleanup(func() { close(release); <-done })
	<-ready
	if got, ok := qrun.Get(context.Background(), key); !ok || got != "started" {
		t.Fatalf("foreign live simulation blocked fallback: %q, %v", got, ok)
	}
	end()
	if got, ok := qrun.Get(context.Background(), key); ok {
		t.Fatalf("foreign simulation supplied another goroutine's value: %q, %v", got, ok)
	}
}

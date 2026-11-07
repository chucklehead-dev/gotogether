package model

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// A progress sink belongs to one simulation, including its native commands. It never
// supplies evidence to the report or changes the outcome of a check.
type simulationProgress struct {
	w       io.Writer
	binding string
}

type simulationProgressKey struct{}

func progressOf(ctx context.Context) *simulationProgress {
	p, _ := ctx.Value(simulationProgressKey{}).(*simulationProgress)
	return p
}

func (p *simulationProgress) phase(name string) func(string) {
	if p == nil || p.w == nil {
		return func(string) {}
	}
	start := time.Now()
	fmt.Fprintf(p.w, "simulate %s: START %s\n", p.binding, name)
	var once sync.Once
	return func(status string) {
		once.Do(func() {
			fmt.Fprintf(p.w, "simulate %s: END %s status=%s elapsed=%s\n", p.binding, name, status, time.Since(start).Round(time.Millisecond))
		})
	}
}

func phaseStatus(ctx context.Context, err error) string {
	if ctx.Err() != nil {
		return "canceled"
	}
	if err != nil {
		return "error"
	}
	return "completed"
}

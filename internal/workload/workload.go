// Package workload carries driver prerequisite observations within one run.
// These records never enter the application's native model trace.
package workload

import (
	"context"
	"sync"

	"github.com/chucklehead-dev/gotogether/qrun"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

// Read-only: each run installs its own Collector under this identity.
var key = qrun.NewKey[*Collector]("workload.phases")

// Collector records phase execution independently for each captured run.
type Collector struct {
	mu     sync.Mutex
	phases []runprofile.Phase
}

// With installs a collector in a driven run's context.
func With(ctx context.Context, c *Collector) context.Context { return qrun.With(ctx, key, c) }

// Define registers the workload's stages before any one starts. Later phases
// therefore remain visibly not_started if a prerequisite prevents reaching them.
func Define(ctx context.Context, phases []runprofile.Phase) {
	if c, ok := qrun.Get(ctx, key); ok {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.phases != nil {
			panic("workload: phases already defined for this run")
		}
		c.phases = make([]runprofile.Phase, len(phases))
		for i, p := range phases {
			c.phases[i] = p.Clone()
		}
	}
}

// Update replaces one stage's snapshot without retaining caller-owned buffers.
func Update(ctx context.Context, index int, p runprofile.Phase) {
	if c, ok := qrun.Get(ctx, key); ok {
		c.mu.Lock()
		defer c.mu.Unlock()
		if index < 0 || index >= len(c.phases) {
			panic("workload: phase index outside the declared plan")
		}
		c.phases[index] = p.Clone()
	}
}

// Phases returns an independent snapshot for evidence and navigation.
func (c *Collector) Phases() []runprofile.Phase {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]runprofile.Phase, len(c.phases))
	for i, p := range c.phases {
		out[i] = p.Clone()
	}
	return out
}

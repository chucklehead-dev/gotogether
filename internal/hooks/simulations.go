package hooks

import (
	"context"
	"sync"
	"time"
)

// Simulation records the effective policy at one Sim.Run call. Scheduler
// counters and limits apply only when Native is false.
type Simulation struct {
	Operation               string
	ID                      int
	Native                  bool
	Horizon                 time.Duration
	MaxSteps                int
	Hang                    time.Duration
	StepsBefore, StepsAfter int
	StepLimitReached        bool
	WatchdogReached         bool
}

// Simulations collects calls within one driven run. Different simulation
// instances receive distinct IDs, in the order in which they first run.
type Simulations struct {
	mu    sync.Mutex
	ids   map[any]int
	calls []Simulation
}

type simulationsKey struct{}

// WithSimulations installs a run-local collector.
func WithSimulations(ctx context.Context, s *Simulations) context.Context {
	return context.WithValue(ctx, simulationsKey{}, s)
}

// ObserveSimulation returns a completion callback, or nil outside a driven
// run. Reserving its position before scheduling preserves invocation order.
func ObserveSimulation(ctx context.Context, identity any, call Simulation) func(stepsAfter int, limited, hung bool) {
	s, _ := ctx.Value(simulationsKey{}).(*Simulations)
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.ids == nil {
		s.ids = map[any]int{}
	}
	id, ok := s.ids[identity]
	if !ok {
		id = len(s.ids) + 1
		s.ids[identity] = id
	}
	call.ID = id
	index := len(s.calls)
	s.calls = append(s.calls, call)
	s.mu.Unlock()
	return func(stepsAfter int, limited, hung bool) {
		s.mu.Lock()
		s.calls[index].StepsAfter = stepsAfter
		s.calls[index].StepLimitReached = limited
		s.calls[index].WatchdogReached = hung
		s.mu.Unlock()
	}
}

// Calls returns a value copy of the observed calls.
func (s *Simulations) Calls() []Simulation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Simulation(nil), s.calls...)
}

// Package qmut runs mutants of the code under test, woven by qweave
// (qweave -mutate): every mutant of a package is compiled into one build,
// each behind a switch (package woven's Cond, Pick and MutantOn, the woven
// builds' ABI), and a run activates one mutant through its
// context (WithMutant), found like a run's recorder (qrun). Runs of
// different mutants then share a process, and a simulation's seeds of one
// mutant run side by side (mutant schemata).
//
// A mutant that every exploration survives is code the model does not
// constrain: it can change and no check notices (qtest.System.Mutate).
package qmut

import (
	"context"
	"fmt"
	"sync"

	"github.com/chucklehead-dev/gotogether/internal/wovenrt"
	"github.com/chucklehead-dev/gotogether/qrun"
)

// A Mutant is one woven change.
type Mutant struct {
	ID   int
	File string
	Line int
	Func string
	// Op is the change: "negate if", "<= for <" and the like, "drop call".
	Op string
	// Code is the original code, for reports.
	Code string
}

func (m Mutant) String() string {
	return fmt.Sprintf("%s:%d %s: %s (%s)", m.File, m.Line, m.Func, m.Op, m.Code)
}

// Mutants are all registered mutants, by ID: the mutants qweave -mutate
// wove into the program, which register at init.
func Mutants() []Mutant {
	reg := wovenrt.Mutants()
	out := make([]Mutant, len(reg))
	for i, m := range reg {
		out[i] = Mutant(m)
	}
	return out
}

// WithMutant makes ctx's run run mutant id (its simulation's tasks, whose
// contexts derive from ctx).
func WithMutant(ctx context.Context, id int) context.Context {
	return qrun.With(ctx, wovenrt.Active, id+1)
}

// A Reach is the mutants whose code a run reached, on or off.
type Reach struct {
	mu  sync.Mutex
	ids map[int]bool
}

// Reached is whether mutant id's code ran.
func (r *Reach) Reached(id int) bool { r.mu.Lock(); defer r.mu.Unlock(); return r.ids[id] }

// WithReach makes ctx's run note the mutants whose code it reaches.
func WithReach(ctx context.Context, r *Reach) context.Context {
	return qrun.With(ctx, wovenrt.Reach, r.note)
}

func (r *Reach) note(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ids == nil {
		r.ids = map[int]bool{}
	}
	r.ids[id] = true
}

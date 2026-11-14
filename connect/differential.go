package connect

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/itf"
)

// An Observer is a Driver that reports, after each step, what its
// implementation observed: the replies it returned, a projection wider
// than the model's state. A Differential compares observations across its
// implementations; a plain Replay ignores them.
type Observer interface {
	Driver
	Observe() (any, error)
}

// Differential replays each step on several implementations of one
// interface (a store in memory and on Valkey), in the order of their
// names, and fails at the first step where they disagree: one fails where
// another does not, or their observations (Observe, else State) differ.
// Two implementations that each agree with the model can still disagree
// with each other where the model never goes (a negative budget, a
// trimmed history): replay traces of a looser model, whose inputs go
// there, with IgnoreModel, or vary the design's own inputs (Vary).
//
// Make a fresh one per trace, as a Driver (Run's newDriver).
type Differential struct {
	// Impls are the implementations, by name; each a Driver, and an
	// Observer or a StateDriver to be compared beyond its errors.
	Impls map[string]Driver
	// IgnoreModel compares the implementations with each other only,
	// not each StateDriver's state with the model's: for traces of a model
	// whose states describe the inputs, not the design.
	IgnoreModel bool
	// Vary, if set, runs once per step before the implementations and may
	// replace the step's picks (s.Picks, a copy of the trace's), so every
	// implementation gets the same input, one the model may never pick: a
	// withdrawal past the balance, a negative budget. Inside a Hegel test
	// case (qhegel.VaryTraces) it draws them, and Hegel shrinks them. A
	// varied input leaves the model's state behind: set IgnoreModel.
	Vary func(s *Step) error
}

func (d *Differential) names() []string { return slices.Sorted(maps.Keys(d.Impls)) }

// Step runs the step on every implementation. Some failing and some not
// is a disagreement. Every one failing fails the step, as a single
// driver's would, unless IgnoreModel: a looser model's input that every
// implementation refuses is one they agree on.
func (d *Differential) Step(s *Step) error {
	if len(d.Impls) < 2 {
		return fmt.Errorf("a Differential compares at least two implementations, and has %d: add another to Impls", len(d.Impls))
	}
	if d.Vary != nil {
		s.Picks = maps.Clone(s.Picks) // the trace's own stay as they are, for the next replay of it
		if s.Picks == nil {
			s.Picks = map[string]itf.Value{}
		}
		if err := recovered(func() error { return d.Vary(s) }); err != nil {
			return fmt.Errorf("varying the step's inputs: %w", err)
		}
	}
	var failed, succeeded []string
	for _, n := range d.names() {
		if err := call(d.Impls[n], s); err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", n, err))
		} else {
			succeeded = append(succeeded, n)
		}
	}
	switch {
	case len(failed) == 0:
		return nil
	case len(succeeded) == 0 && d.IgnoreModel:
		return nil // a looser model's input every implementation refuses: they agree
	case len(succeeded) == 0:
		return fmt.Errorf("every implementation failed: %s", strings.Join(failed, "; "))
	}
	return fmt.Errorf("the implementations disagree: %s failed, %s did not: a case the model may not cover; decide which is right, then fix the other",
		strings.Join(failed, " and "), strings.Join(succeeded, " and "))
}

// CheckStep compares each StateDriver's state with the model's (unless
// IgnoreModel), runs each StepChecker's own check, and compares every
// implementation's observation with the first's.
func (d *Differential) CheckStep(s *Step) error {
	type seen struct {
		name string
		v    any
	}
	var obs []seen
	for _, n := range d.names() {
		impl := d.Impls[n]
		if sd, ok := impl.(StateDriver); ok && !d.IgnoreModel {
			st, err := sd.State()
			if err != nil {
				return fmt.Errorf("%s: State(): %w", n, err)
			}
			if diff, err := compare(s.State, st); err != nil || len(diff) > 0 {
				if err != nil {
					return fmt.Errorf("%s: %w", n, err)
				}
				return fmt.Errorf("%s's state diverges from the model's:\n  %s", n, strings.Join(diff, "\n  "))
			}
		}
		if sc, ok := impl.(StepChecker); ok {
			if err := recovered(func() error { return sc.CheckStep(s) }); err != nil {
				return fmt.Errorf("%s: %w", n, err)
			}
		}
		var v any
		var err error
		switch x := impl.(type) {
		case Observer:
			v, err = x.Observe()
		case StateDriver:
			v, err = x.State()
		default:
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: observing: %w", n, err)
		}
		obs = append(obs, seen{n, v})
	}
	for _, o := range obs[min(1, len(obs)):] {
		if diff := diffNamed(obs[0].v, o.v, obs[0].name, o.name); len(diff) > 0 {
			return fmt.Errorf("the implementations disagree (%s and %s): a case the model may not cover; decide which is right, then fix the other:\n  %s",
				obs[0].name, o.name, strings.Join(diff, "\n  "))
		}
	}
	return nil
}

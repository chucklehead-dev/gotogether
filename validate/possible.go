package validate

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
)

// Possibility checks (P3): whether the model on its own can reach what a
// binding's simulation says it must (binding.Possible), searched with
// the Go evaluator from init by the model's step (qeval.Reach). The
// search is bounded: a possible it did not find is "not found within the
// bound" unless the search was complete (every state within the depth
// seen), and only then impossible.

// A PossibleSearch bounds Checker.Possible's search and sets its
// instance: Constants (NAME=expr, over the binding's), at most States
// search nodes (default 2000) and Depth transitions (default 30).
type PossibleSearch struct {
	Constants     []string
	States, Depth int
}

// A PossibleResult is the Go evaluator's verdict on one possible.
type PossibleResult struct {
	Possible binding.Possible
	// Found: Path are the transitions from init that reach it, each its
	// action's call as the model's text names it (the shortest, for a
	// predicate)
	Found bool
	Path  []string
	// Complete: the search saw every state within Depth, so a possible not
	// found is impossible within it; else Cut names the bound that cut it
	// ("states", "depth")
	Complete bool
	Cut      string
	// Nodes and Depth are the search's extent: the states it visited and
	// the longest path it expanded
	Nodes, Depth int
}

// Verdict is the result as one line: "reached by a, b, c", "impossible:
// no trace reaches it (the search saw the whole state graph, 25
// states)", or "not found within the bound (2000 states, depth 7; ...)".
func (r PossibleResult) Verdict() string {
	switch {
	case r.Found && len(r.Path) == 0:
		return "reached in the initial state"
	case r.Found:
		return "reached by " + strings.Join(r.Path, ", ")
	case r.Complete:
		return fmt.Sprintf("impossible: no trace reaches it (the search saw the whole state graph, %d states)", r.Nodes)
	}
	return fmt.Sprintf("not found within the bound (%d states, depth %d; the %s bound cut the search)", r.Nodes, r.Depth, r.Cut)
}

// Possible searches c's binding's model for each of ps with the Go
// evaluator, from init by the model's step. An error says why there is
// no search: the model outside the evaluator's fragment, no step action,
// a constant computed from a run's steps with no value given, a possible
// that names no action or parameter of the model.
func (c *Checker) Possible(ps []binding.Possible, cfg PossibleSearch) ([]PossibleResult, error) {
	b := *c.Binding
	b.Constants = maps.Clone(c.Binding.Constants)
	if b.Constants == nil {
		b.Constants = map[string]string{}
	}
	if b.Simulate != nil {
		for k, v := range b.Simulate.Constants {
			b.Constants[k] = v
		}
	}
	for _, o := range cfg.Constants {
		if k, v, ok := strings.Cut(o, "="); ok {
			b.Constants[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	cc := *c
	cc.Binding = &b
	type goal struct {
		when  string
		steps []binding.StepPattern
	}
	goals := make([]goal, len(ps))
	var preds []string
	for i, p := range ps {
		when, steps, err := p.Resolve(c.Model)
		if err != nil {
			return nil, err
		}
		goals[i] = goal{when, steps}
		if when != "" {
			preds = append(preds, when)
		}
	}
	e := evaluatorOf(&cc, func(path string) (*binding.EvalModule, error) { return b.PossibleModule(c.Model, path, preds) })
	if e.err != nil {
		return nil, fmt.Errorf("possible: the Go evaluator has no evaluation module for %s: %w", b.SpecPath(), e.err)
	}
	step, ok := e.em.Actions["step"]
	if !ok {
		return nil, fmt.Errorf("possible: %s has no step action: the search takes step's transitions", b.Module)
	}
	if len(e.em.Consts) > 0 {
		return nil, fmt.Errorf("possible: the binding computes constants from a run's steps (%s): give them values (the simulate section's constants, or -const)",
			strings.Join(slices.Sorted(maps.Keys(e.em.Consts)), ", "))
	}
	consts := map[string]qeval.Value{}
	init, err := e.m.Init(consts, e.em.Init)
	if err != nil {
		return nil, fmt.Errorf("possible: init: %w", err)
	}
	qgoals := make([]qeval.Goal, len(goals))
	for i, g := range goals {
		if g.when != "" {
			val := e.em.Possible[g.when]
			qgoals[i].Holds = func(s qeval.State) (bool, error) {
				v, err := e.m.Eval(consts, s, val)
				if err != nil {
					return false, fmt.Errorf("possible %q: %w", ps[i].Text(), err)
				}
				t, ok := v.(qeval.Bool)
				if !ok {
					return false, fmt.Errorf("possible %q is no Boolean: %s", ps[i].Text(), qeval.String(v))
				}
				return bool(t), nil
			}
			continue
		}
		for _, sp := range g.steps {
			m, err := e.stepMatcher(c.Model, sp)
			if err != nil {
				return nil, fmt.Errorf("possible %q: %w", ps[i].Text(), err)
			}
			qgoals[i].Steps = append(qgoals[i].Steps, m)
		}
	}
	bound := qeval.Bound{States: cfg.States, Depth: cfg.Depth}
	if bound.States <= 0 {
		bound.States = 2000
	}
	if bound.Depth <= 0 {
		bound.Depth = 30
	}
	rs, err := e.m.Reach(consts, init, step, qgoals, bound)
	if err != nil {
		return nil, fmt.Errorf("possible: %w", err)
	}
	out := make([]PossibleResult, len(ps))
	for i, r := range rs {
		pr := PossibleResult{Possible: ps[i], Found: r.Found, Complete: r.Complete, Cut: r.Cut, Nodes: r.Nodes, Depth: r.Depth}
		for _, t := range r.Path {
			if a, ok := branchEvent(c.Model, t); ok {
				pr.Path = append(pr.Path, a.String())
			} else {
				pr.Path = append(pr.Path, "step")
			}
		}
		out[i] = pr
	}
	return out, nil
}

// stepMatcher is whether a transition takes sp: its branch's action is
// sp's, each parameter sp names at sp's value.
func (e *evaluator) stepMatcher(m *binding.Model, sp binding.StepPattern) (func(qeval.Transition) bool, error) {
	want := make([]qeval.Value, len(sp.Values))
	for i, lit := range sp.Values {
		v, err := e.literal(lit)
		if err != nil {
			return nil, fmt.Errorf("step %s: %s = %s: %w", sp.Text, sp.Params[i], lit, err)
		}
		want[i] = v
	}
	return func(t qeval.Transition) bool {
		a, ok := branchEvent(m, t)
		if !ok || a.Action != sp.Action {
			return false
		}
		for i, p := range sp.Params {
			k := slices.Index(a.Params, p)
			if k < 0 || k >= len(a.Args) || qeval.Compare(a.Args[k], want[i]) != 0 {
				return false
			}
		}
		return true
	}, nil
}

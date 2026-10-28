package validate

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
)

// The state-graph explorer (P2): a binding's model, its simulation's
// instance, explored breadth first from init by its step with the Go
// evaluator (qeval.Model.Explore), every invariant checked in every state
// it reaches, its deadlocks and the actions it never takes reported, with
// no quint process. A bounded exhaustive alternative to simulate's
// sampling: an invariant no reachable state violates in a complete graph
// is proved for the instance; a violated one has the shortest
// counterexample, as a Quint run that replays it.

// A GraphSearch sets Checker.Graph's instance and bounds: Constants
// (NAME=expr, over the binding's and its simulate section's), the
// Invariants to check (nil: the binding's), Terminal (a predicate: a
// state the step cannot leave where it holds is an end the model means,
// not a deadlock; "": the binding's simulate.terminal), at most States
// states (default: the binding's simulate.graphStates, else 100000) and
// paths of at most Depth transitions (default: the binding's
// simulate.graphDepth, else none).
type GraphSearch struct {
	Constants     []string
	Invariants    []string
	Terminal      string
	States, Depth int
}

// DefaultGraphStates bounds a graph whose search and binding give no
// bound: 10^5 states, under a minute at the evaluator's usual cost.
const DefaultGraphStates = 100000

// A GraphResult is Checker.Graph's report on a model's state graph.
type GraphResult struct {
	// States, Starts and Transitions are the graph's extent: the states
	// found, how many of them are initial, and its edges (each a branch's
	// action with its arguments and choices, to the state it reaches);
	// Depth the deepest state's distance from its start
	States, Starts, Transitions, Depth int
	// Complete: no bound cut the search and the evaluator evaluated every
	// state, so every reachable state (within the depth bound, if one was
	// set: Bound) was checked. Else Cut says what cut it: "states",
	// "depth", "unsupported" (Stopped names the construct) or "error"
	// (the model's own runtime error, Stopped its message).
	Complete bool
	Cut      string
	Stopped  string
	// Bound is the search's bounds as given
	Bound GraphBound
	// Invariants are each invariant's verdict, in the order asked
	Invariants []InvariantVerdict
	// Deadlocks are the states the step cannot leave where Terminal does
	// not hold, Terminals those where it does; Deadlock the nearest
	// deadlock's path (nil for none)
	Deadlocks, Terminals int
	Deadlock             *Counterexample
	// Actions are the step's actions with how often each was taken, in
	// the order the step names them
	Actions []ActionCoverage
	// Elapsed is the search's time, the invariants' included (not the
	// model's compile)
	Elapsed time.Duration

	// graph is the explored graph, for what builds on it (P6's cycles)
	graph *qeval.Graph
}

// A GraphBound is the bounds a graph was searched with (0: none).
type GraphBound struct {
	States, Depth int
}

// An InvariantVerdict is one invariant over a state graph: violated in
// some reachable state (Counterexample the shortest path to the nearest),
// its evaluation failed (Error: the model's runtime error, with the path
// to the state; Unsupported: a construct the evaluator does not take, so
// it was not checked), or held in every state the graph holds (Checked
// of them).
type InvariantVerdict struct {
	Name           string
	Violated       bool
	Counterexample *Counterexample
	Error          string
	Unsupported    string
	Checked        int
}

// Held says the invariant was checked in every state of the graph and
// held in each (proved for the instance when the graph is complete).
func (v InvariantVerdict) Held() bool {
	return !v.Violated && v.Error == "" && v.Unsupported == ""
}

// A Counterexample is a path in the model's graph: its start, then each
// transition, with the state after it. Replay is the path as a Quint run
// over the binding's instance (its start init, or the start's values
// where init has several states; each step its action's call), ending in
// what the path shows (`.expect(not(inv))`, a deadlock's
// `.then(step.fail())`); Sampled are the steps whose action makes
// choices of its own, which quint's replay draws at random rather than
// the path's (where there are none it replays exactly).
type Counterexample struct {
	Steps   []GraphStep
	Replay  string
	Sampled []int
}

// A GraphStep is a step of a counterexample: the action's call as the
// model's text names it ("init" for the start), the choices it made
// itself (Picks: after the call's arguments), and the state after it.
type GraphStep struct {
	Call  string
	Picks []NamedValue
	State []NamedValue
}

// An ActionCoverage is how often the graph took one of the step's
// actions: the transitions that take it and the states it is enabled in
// (0 of each: never enabled in the graph).
type ActionCoverage struct {
	Action              string
	Transitions, States int
}

// NeverEnabled are the step's actions no transition of the graph takes:
// dead in the instance if the graph is complete, else not reached within
// its bound.
func (r *GraphResult) NeverEnabled() []string {
	var out []string
	for _, a := range r.Actions {
		if a.Transitions == 0 {
			out = append(out, a.Action)
		}
	}
	return out
}

// Extent is the graph as one line: "complete: 1234 states (2 initial),
// 5678 transitions, depth 12", or "truncated by the state bound (...)",
// "stopped: ..." when the evaluator could not go on.
func (r *GraphResult) Extent() string {
	n := fmt.Sprintf("%d states", r.States)
	if r.Starts > 1 {
		n += fmt.Sprintf(" (%d initial)", r.Starts)
	}
	n += fmt.Sprintf(", %d transitions, depth %d", r.Transitions, r.Depth)
	switch r.Cut {
	case "":
		return "complete: " + n
	case "states":
		return fmt.Sprintf("truncated by the state bound (%d): %s", r.Bound.States, n)
	case "depth":
		return fmt.Sprintf("truncated by the depth bound (%d): %s, states at the bound not expanded", r.Bound.Depth, n)
	}
	return fmt.Sprintf("stopped after %s: %s", n, r.Stopped)
}

// Graph explores c's binding's model with the Go evaluator, from init by
// its step, within cfg's bounds, and checks each invariant in every state
// it reaches. An error says why there is no graph: the binding has no
// evaluation module or no step action, computes constants from a run's
// steps with no value given, or names an invariant or terminal predicate
// quint refuses. A construct the evaluator does not take, met during the
// search, is no error: the result is cut "unsupported", naming it, and
// never complete.
func (c *Checker) Graph(cfg GraphSearch) (*GraphResult, error) {
	sim := c.Binding.Simulate
	if sim == nil {
		sim = &binding.Simulation{}
	}
	invs := cfg.Invariants
	if invs == nil {
		invs = c.Binding.Invariants
	}
	terminal := cfg.Terminal
	if terminal == "" {
		terminal = sim.Terminal
	}
	bound := qeval.Bound{States: cfg.States, Depth: cfg.Depth}
	if bound.States <= 0 {
		bound.States = sim.GraphStates
	}
	if bound.States <= 0 {
		bound.States = DefaultGraphStates
	}
	if bound.Depth <= 0 {
		bound.Depth = sim.GraphDepth
	}
	preds := slices.Clone(invs)
	if terminal != "" {
		preds = append(preds, terminal)
	}
	cc := c.simulationInstance(cfg.Constants)
	e := evaluatorOf(cc, func(path string) (*binding.EvalModule, error) { return cc.Binding.PossibleModule(c.Model, path, preds) })
	if e.err != nil {
		return nil, fmt.Errorf("graph: the Go evaluator has no evaluation module for %s: %w", c.Binding.SpecPath(), e.err)
	}
	step, ok := e.em.Actions["step"]
	if !ok {
		return nil, fmt.Errorf("graph: %s has no step action: the graph is step's transitions", c.Binding.Module)
	}
	if len(e.em.Consts) > 0 {
		return nil, fmt.Errorf("graph: the binding computes constants from a run's steps (%s): give them values (the simulate section's constants, or -const)",
			strings.Join(slices.Sorted(maps.Keys(e.em.Consts)), ", "))
	}
	// the time is the search's and the checks', not quint's compile
	started := time.Now()
	consts := map[string]qeval.Value{}
	// an edge's label is its branch's action and what it did (the step's
	// own choices are the action's arguments, or used by none: bank's
	// step picks a transfer's accounts for a deposit too)
	label := func(evs []qeval.Event) []qeval.Event {
		if k := branchIndex(c.Model, qeval.Transition{Events: evs}); k > 0 {
			return evs[k:]
		}
		return evs
	}
	g, err := e.m.Explore(consts, e.em.Init, step, bound, label)
	r := &GraphResult{States: len(g.States), Starts: g.Starts, Transitions: g.Edges(), Complete: g.Complete, Cut: g.Cut,
		Bound: GraphBound{bound.States, bound.Depth}, graph: g}
	if err != nil {
		var u *qeval.Unsupported
		var ee *qeval.EvalError
		switch {
		case errors.As(err, &u):
			r.Stopped = fmt.Sprintf("%s is outside the Go evaluator's fragment (%s): nothing past it was explored; quint's sampling (simulate without -graph) or -verify checks the model", u.Op, u.Why)
		case errors.As(err, &ee):
			r.Stopped = "the model's own error: " + ee.Msg
		default:
			return nil, fmt.Errorf("graph: %w", err)
		}
		if len(g.States) == 0 {
			r.Stopped = "init: " + r.Stopped
		}
	}
	for _, d := range g.Depth {
		r.Depth = max(r.Depth, d)
	}
	ce := &cexWriter{c: c, e: e, g: g}
	// each invariant in every state, in id order: the first violated is a
	// nearest one, its path from the parent tree a shortest
	for _, inv := range invs {
		v := InvariantVerdict{Name: inv}
		val := e.em.Possible[inv]
		for id, s := range g.States {
			x, err := e.m.Eval(consts, s, val)
			var u *qeval.Unsupported
			if errors.As(err, &u) {
				v.Unsupported = fmt.Sprintf("%s: %s", u.Op, u.Why)
				break
			}
			if err != nil {
				v.Error, v.Counterexample = err.Error(), ce.path(id, "")
				break
			}
			b, ok := x.(qeval.Bool)
			if !ok {
				return nil, fmt.Errorf("graph: invariant %s is no Boolean: %s", inv, qeval.String(x))
			}
			v.Checked++
			if !b {
				v.Violated, v.Counterexample = true, ce.path(id, fmt.Sprintf(".expect(not(%s))", inv))
				break
			}
		}
		r.Invariants = append(r.Invariants, v)
	}
	// deadlocks, the terminal states set apart
	for _, id := range g.Deadlocks() {
		if terminal != "" {
			x, err := e.m.Eval(consts, g.States[id], e.em.Possible[terminal])
			if err != nil {
				return nil, fmt.Errorf("graph: terminal %s in state %s: %w", terminal, stateText(e, g.States[id]), err)
			}
			if x == qeval.Bool(true) {
				r.Terminals++
				continue
			}
		}
		if r.Deadlocks++; r.Deadlock == nil {
			r.Deadlock = ce.path(id, ".then(step.fail())")
		}
	}
	// coverage: each transition's branch action, once per label
	branch := make([]string, len(g.Labels))
	for i, l := range g.Labels {
		if a, ok := branchEvent(c.Model, qeval.Transition{Events: l}); ok {
			branch[i] = a.Action
		}
	}
	cov := map[string]*ActionCoverage{}
	for _, a := range e.m.BranchActions(step) {
		if !c.Model.Helpers[a] {
			r.Actions = append(r.Actions, ActionCoverage{Action: a})
		}
	}
	for i := range r.Actions {
		cov[r.Actions[i].Action] = &r.Actions[i]
	}
	for _, out := range g.Out {
		in := map[string]bool{}
		for _, ed := range out {
			a := branch[ed.Label]
			if a == "" {
				continue
			}
			ac := cov[a]
			if ac == nil {
				// an action the step reaches through another's branch
				r.Actions = append(r.Actions, ActionCoverage{Action: a})
				for i := range r.Actions {
					cov[r.Actions[i].Action] = &r.Actions[i]
				}
				ac = cov[a]
			}
			ac.Transitions++
			if !in[a] {
				in[a] = true
				ac.States++
			}
		}
	}
	r.Elapsed = time.Since(started)
	return r, nil
}

// simulationInstance is c with its binding's constants overridden by its
// simulate section's, then by overrides (NAME=expr): the instance
// simulate runs.
func (c *Checker) simulationInstance(overrides []string) *Checker {
	b := *c.Binding
	b.Constants = maps.Clone(c.Binding.Constants)
	if b.Constants == nil {
		b.Constants = map[string]string{}
	}
	if b.Simulate != nil {
		maps.Copy(b.Constants, b.Simulate.Constants)
	}
	for _, o := range overrides {
		if k, v, ok := strings.Cut(o, "="); ok {
			b.Constants[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	cc := *c
	cc.Binding = &b
	return &cc
}

// stateText is a state as a report says it.
func stateText(e *evaluator, s qeval.State) string {
	var parts []string
	for _, v := range e.m.Vars {
		parts = append(parts, fmt.Sprintf("%s = %s", shortName(v), qeval.String(s[v])))
	}
	return strings.Join(parts, ", ")
}

// cexWriter writes a graph's paths as counterexamples.
type cexWriter struct {
	c *Checker
	e *evaluator
	g *qeval.Graph
}

// path is the counterexample of state id: its path from its start, and
// the Quint run that replays it, ending in end.
func (w *cexWriter) path(id int, end string) *Counterexample {
	named := func(s qeval.State) []NamedValue {
		var out []NamedValue
		for _, v := range w.e.m.Vars {
			out = append(out, NamedValue{Name: shortName(v), Value: qeval.String(s[v])})
		}
		return out
	}
	start := w.g.Start(id)
	cx := &Counterexample{Steps: []GraphStep{{Call: "init", State: named(start)}}}
	run := "init"
	if w.g.Starts > 1 {
		// init has several states: the replay starts in this one's values
		var parts []string
		for _, v := range w.e.m.Vars {
			parts = append(parts, fmt.Sprintf("%s' = %s", shortName(v), qeval.String(start[v])))
		}
		run = "all { " + strings.Join(parts, ", ") + " }"
		cx.Steps[0].Call = "init (" + stateText(w.e, start) + ")"
	}
	for i, t := range w.g.Path(id) {
		st := GraphStep{Call: "step", State: named(t.State)}
		call := "step"
		if k := branchIndex(w.c.Model, t); k >= 0 {
			// the replay applies the action itself: step's choice of it and
			// of its arguments made by the call; choices after it are the
			// action's own
			a := t.Events[k]
			st.Call, call = a.String(), callLiteral(a)
			for _, ev := range t.Events[k+1:] {
				if ev.Action == "" {
					st.Picks = append(st.Picks, NamedValue{Name: ev.Pick, Value: qeval.String(ev.Value)})
				}
			}
		} else {
			for _, p := range t.Picks() {
				st.Picks = append(st.Picks, NamedValue{Name: p.Pick, Value: qeval.String(p.Value)})
			}
		}
		if len(st.Picks) > 0 {
			cx.Sampled = append(cx.Sampled, i+1)
		}
		cx.Steps = append(cx.Steps, st)
		run += ".then(" + call + ")"
	}
	cx.Replay = run + end
	return cx
}

// callLiteral is an action's call with its arguments as Quint literals,
// positionally: deposit(5, "a").
func callLiteral(a qeval.Event) string {
	if len(a.Args) == 0 {
		return a.Action
	}
	parts := make([]string, len(a.Args))
	for i, v := range a.Args {
		parts[i] = qeval.String(v)
	}
	return a.Action + "(" + strings.Join(parts, ", ") + ")"
}

// branchIndex is the index in t's events of its branch's action
// (branchEvent's), -1 for none.
func branchIndex(m *binding.Model, t qeval.Transition) int {
	for i, a := range t.Events {
		if a.Action == "" || a.Action == "step" || m.Helpers[a.Action] {
			continue
		}
		return i
	}
	return -1
}

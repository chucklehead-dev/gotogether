package validate

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/itf"
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
// states and paths of at most Depth transitions (default: the binding's
// simulate.search, else DefaultSearchStates states and no depth bound). Eventually are the eventual vals
// judged as cycles (nil: the binding's), Unfair and Stops the step's
// actions not weakly fair (nil: the binding's simulate.unfair and
// simulate.stops; every other action is).
type GraphSearch struct {
	Constants     []string
	Invariants    []string
	Terminal      string
	States, Depth int
	Eventually    []string
	Unfair, Stops []string
}

// DefaultSearchStates bounds a search of the model alone (a graph, a
// possible's search) whose options and binding give no bound: 10^5
// states, under a minute at the evaluator's usual cost.
const DefaultSearchStates = 100000

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
	// Quiescent are the expanded states whose every edge returns to them
	// (only a stutter, such as a read, is enabled): not deadlocks, since
	// the step can take one, so a model whose step can always stutter (a
	// grist model's queries) has no deadlock at all, and these are where
	// it stops
	Quiescent int
	// Actions are the step's actions with how often each was taken, in
	// the order the step names them
	Actions []ActionCoverage
	// Eventually are each eventual val's verdict as cycles (P6), in the
	// order asked; Unfair and Stops the actions the search did not hold to
	// weak fairness (GraphSearch's)
	Eventually    []EventualVerdict
	Unfair, Stops []string
	// Elapsed is the search's time, the invariants' included (not the
	// model's compile)
	Elapsed time.Duration

	// graph is the explored graph, for what builds on it; e the
	// evaluator that explored it
	graph *qeval.Graph
	e     *evaluator
	keys  map[string]bool // its states' keys, made by Lacks
}

// Lacks is the first state of tr, a trace quint sampled of the same
// instance (its variables named as the model names them, mbt:: ones
// aside), that is no state of r's graph: its index, with the state as
// text, or -1. Every state quint reaches is reachable, so a complete
// graph lacking one is the Go evaluator's error: its "holds" and "live"
// rest on the evaluator alone, and this is quint's check of them
// (review 11, O1: a fold the evaluator folded in the wrong order made a
// complete graph of states quint never visits, missing the one it does).
func (r *GraphResult) Lacks(tr *itf.Trace) (int, string, error) {
	if r.graph == nil || r.e == nil {
		return -1, "", fmt.Errorf("graph: no graph to compare with")
	}
	if r.keys == nil {
		r.keys = map[string]bool{}
		for _, s := range r.graph.States {
			r.keys[r.e.m.Key(s)] = true
		}
	}
	for i, st := range tr.States {
		s := qeval.State{}
		for _, v := range r.e.m.Vars {
			short := v[strings.LastIndex(v, ":")+1:]
			x, ok := st.Vars[short]
			if !ok {
				return -1, "", fmt.Errorf("graph: quint's trace has no variable %s", short)
			}
			val, err := qeval.FromITF(x)
			if err != nil {
				return -1, "", fmt.Errorf("graph: quint's state %d: %w", i, err)
			}
			s[v] = val
		}
		if hooks.TamperEval.Load() == 8 && i == len(tr.States)-1 && i > 0 {
			return i, stateText(r.e, s), nil // the evaluator's graph made to lack quint's last state
		}
		if !r.keys[r.e.m.Key(s)] {
			return i, stateText(r.e, s), nil
		}
	}
	return -1, "", nil
}

// A GraphBound is the bounds a graph was searched with (0: none).
type GraphBound struct {
	States, Depth int
}

// An InvariantVerdict is one invariant over a state graph: violated in
// some reachable state (Counterexample the shortest path to the nearest
// the evaluator could check), its evaluation failed (Error: the model's
// runtime error, with the path to the state), or held in every state
// checked (Checked of them). Unsupported names a construct the evaluator
// does not take, met in Unchecked states, where the invariant was not
// checked: it is not Held then, though a violation elsewhere still is
// one.
type InvariantVerdict struct {
	Name           string
	Violated       bool
	Counterexample *Counterexample
	Error          string
	Unsupported    string
	Checked        int
	Unchecked      int
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
// `.then(step.fail())`). Each step's call is checked by the evaluator:
// the branch's call where it alone reaches the path's state, else step.
// Sampled are the steps whose call has other successors too, which
// quint's replay may draw instead of the path's (where there are none it
// replays exactly).
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
	evs, unfair, stops := cfg.Eventually, cfg.Unfair, cfg.Stops
	if evs == nil {
		evs = c.Binding.Eventually
	}
	if unfair == nil {
		unfair = sim.Unfair
	}
	if stops == nil {
		stops = sim.Stops
	}
	bound := searchBound(sim, cfg.States, cfg.Depth)
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
		Bound: GraphBound{bound.States, bound.Depth}, graph: g, e: e}
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
	ce := &cexWriter{c: c, e: e, g: g, step: step}
	// each invariant in every state, in id order: the first violated is a
	// nearest one, its path from the parent tree a shortest
	for _, inv := range invs {
		v := InvariantVerdict{Name: inv}
		val := e.em.Possible[inv]
		for id, s := range g.States {
			x, err := e.m.Eval(consts, s, val)
			var u *qeval.Unsupported
			if errors.As(err, &u) {
				// not checked here; the states after it still are, since a
				// violation in one is real whatever the others hold
				if v.Unchecked++; v.Unsupported == "" {
					v.Unsupported = fmt.Sprintf("%s: %s", u.Op, u.Why)
				}
				continue
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
			if _, ok := x.(qeval.Bool); !ok {
				return nil, fmt.Errorf("graph: terminal %s is no Boolean: %s", terminal, qeval.String(x))
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
	// states only a stutter leaves: no deadlock (each has an edge), and
	// where a step can always stutter (a read, a grist query) there is
	// never one, so they say where such a model stops
	for i, out := range g.Out {
		if g.Expanded[i] && len(out) > 0 && !slices.ContainsFunc(out, func(ed qeval.Edge) bool { return ed.To != i }) {
			r.Quiescent++
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
	// the eventual vals as cycles, every action weakly fair but those
	// named: a name the step never calls is an error, as a misspelled
	// one would leave an action fair
	r.Unfair, r.Stops = unfair, stops
	if len(evs) > 0 {
		for _, x := range []struct {
			field string
			list  []string
		}{{"unfair", unfair}, {"stops", stops}} {
			for _, a := range x.list {
				if !slices.ContainsFunc(r.Actions, func(ac ActionCoverage) bool { return ac.Action == a }) && a != "step" {
					var known []string
					for _, ac := range r.Actions {
						known = append(known, ac.Action)
					}
					return nil, fmt.Errorf("graph: simulate.%s names %s, which is no action of %s's step (its actions: %s)", x.field, a, c.Binding.Module, strings.Join(known, ", "))
				}
				if slices.Contains(unfair, a) && slices.Contains(stops, a) && x.field == "stops" {
					return nil, fmt.Errorf("graph: simulate lists %s as unfair and as stopping: an action that stops is in no cycle, an unfair one may be; pick one", a)
				}
			}
		}
		if r.Eventually, err = c.liveness(ce, evs, branch, unfair, stops); err != nil {
			return nil, err
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

// searchBound is a search's bounds: states and depth as given, else the
// binding's simulate.search, else DefaultSearchStates and no depth.
func searchBound(sim *binding.Simulation, states, depth int) qeval.Bound {
	b := qeval.Bound{States: states, Depth: depth}
	if b.States <= 0 && sim != nil {
		b.States = sim.Search.States
	}
	if b.States <= 0 {
		b.States = DefaultSearchStates
	}
	if b.Depth <= 0 && sim != nil {
		b.Depth = sim.Search.Depth
	}
	return b
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
	c    *Checker
	e    *evaluator
	g    *qeval.Graph
	step string
}

// replayStep is how a replay takes transition t from state prev: its
// branch's call, if that call alone reaches t's state (a step that does
// more than call its branch, `all { any { inc, dec }, n' = n + 1 }`,
// is replayed as step), else step; sampled when the call has other
// successors too, which quint may draw instead.
func (w *cexWriter) replayStep(prev qeval.State, t qeval.Transition, k int) (call string, sampled bool) {
	key := w.e.m.Key(t.State)
	reaches := func(name string, args []qeval.Value) (ok, sampled bool) {
		succs, err := w.e.m.Successors(map[string]qeval.Value{}, prev, name, args...)
		if err != nil {
			return false, false
		}
		for _, s := range succs {
			if w.e.m.Key(s) == key {
				return true, len(succs) > 1
			}
		}
		return false, false
	}
	if k >= 0 {
		a := t.Events[k]
		if ok, sampled := reaches(a.Action, a.Args); ok {
			return callLiteral(a), sampled
		}
	}
	_, sampled = reaches(w.step, nil)
	return "step", sampled
}

// named is a state's variables as a report names them.
func (w *cexWriter) named(s qeval.State) []NamedValue {
	var out []NamedValue
	for _, v := range w.e.m.Vars {
		out = append(out, NamedValue{Name: shortName(v), Value: qeval.String(s[v])})
	}
	return out
}

// path is the counterexample of state id: its path from its start, and
// the Quint run that replays it, ending in end.
func (w *cexWriter) path(id int, end string) *Counterexample {
	start := w.g.Start(id)
	cx := &Counterexample{Steps: []GraphStep{{Call: "init", State: w.named(start)}}}
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
	steps, calls, sampled := w.steps(start, w.g.Path(id), 1)
	cx.Steps = append(cx.Steps, steps...)
	cx.Sampled = sampled
	cx.Replay = run + calls + end
	return cx
}

// steps are transitions ts taken from state prev as a counterexample's
// steps, their replay's calls (".then(c)" each) and the steps (numbered
// from first) whose call quint may draw otherwise (Sampled).
func (w *cexWriter) steps(prev qeval.State, ts []qeval.Transition, first int) (out []GraphStep, calls string, sampled []int) {
	for i, t := range ts {
		st := GraphStep{Call: "step", State: w.named(t.State)}
		k := branchIndex(w.c.Model, t)
		if k >= 0 {
			// the replay applies the action itself where it can: step's
			// choice of it and of its arguments made by the call; choices
			// after it are the action's own
			a := t.Events[k]
			st.Call = a.String()
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
		// the call checked by the evaluator: the transition's state among
		// its successors, and its only one unless quint draws (Sampled)
		call, s := w.replayStep(prev, t, k)
		if s {
			sampled = append(sampled, first+i)
		}
		out = append(out, st)
		calls += ".then(" + call + ")"
		prev = t.State
	}
	return out, calls, sampled
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

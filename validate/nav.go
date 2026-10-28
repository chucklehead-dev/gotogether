package validate

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/formats"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// The model navigator (P5): a binding's model, state by state, with the
// Go evaluator. A node is a state and the path of transitions that
// reached it from its start (init, or the state after a recorded run's
// first calls); its branches are the transitions the model's step takes
// there, each with the action it applies and the choices it makes, and
// its disabled actions are the step's actions no transition takes, each
// with why (the explanation a rejected step's report gives). The
// navigator judges nothing: a node is what the model allows, as the
// evaluator computes it (TestAgreesWithQuint holds its transitions to
// quint's traces).

// NavStart is where a navigation starts: the model's init (no Steps), or
// the state after the first At calls of actor Actor's plan of a recorded
// run (At < 0: all of them; Actor "" the run's only actor checked).
type NavStart struct {
	Steps []qtrace.Step
	Actor string
	At    int
}

// A Navigator walks a binding's model from a start (Checker.Nav).
type Navigator struct {
	c      *Checker
	e      *evaluator
	consts map[string]qeval.Value
	start  qeval.State
	// from says where the start is, as a node shows it
	from string
	// path is the start's own path: the run's calls that reached it
	path []string
}

// A Node is a state of the model the navigator reached: its id (the path
// from the start, as -follow takes it), how it was reached, its state,
// its invariants' values there, its branches and its disabled actions.
type Node struct {
	ID         string       `json:"id"`
	Start      string       `json:"start"`
	Path       []string     `json:"path"`
	State      []NamedValue `json:"state"`
	Changed    []string     `json:"changed,omitempty"`
	Invariants []NamedValue `json:"invariants,omitempty"`
	Branches   []Edge       `json:"branches"`
	Disabled   []Disabled   `json:"disabled,omitempty"`
	// Folded is how many transitions Fold took past its path (the node's
	// Changed are then against the state it folded from)
	Folded int `json:"folded,omitempty"`
}

// An Edge is a branch of a node: a transition of the model's step. Take
// is how a path names it (the action's call, its arguments as Quint
// literals; #n after it when other transitions share it); Label the
// call with its parameters' names; Picks the choices the step made.
type Edge struct {
	Take   string       `json:"take"`
	Label  string       `json:"label"`
	Action string       `json:"action"`
	Args   []NamedValue `json:"args,omitempty"`
	Picks  []NamedValue `json:"picks,omitempty"`
}

// A Disabled action is one of the model's step's actions no transition
// of a node takes, with why (its branch's first false condition).
type Disabled struct {
	Action string       `json:"action"`
	Why    *Explanation `json:"why,omitempty"`
}

// Nav is a navigator of c's binding's model from start, with the Go
// evaluator: an error if the model is outside its fragment, its step has
// no evaluation (a binding with no step action), the binding computes
// constants from steps and no run is given, or the run's calls before
// At are not all transitions of the model (the error says which, and
// why).
func (c *Checker) Nav(start NavStart) (*Navigator, error) {
	e := evaluatorFor(c)
	if e.err != nil {
		return nil, fmt.Errorf("nav: the Go evaluator has no evaluation module for %s: %w", c.Binding.SpecPath(), e.err)
	}
	if _, ok := e.em.Actions["step"]; !ok {
		return nil, fmt.Errorf("nav: %s has no step action: the navigator's branches are step's transitions", c.Binding.Module)
	}
	n := &Navigator{c: c, e: e, from: "init"}
	if start.Steps == nil {
		if len(e.em.Consts) > 0 {
			return nil, fmt.Errorf("nav: the binding computes constants from a run's steps (%s): open a run (-run), whose steps give them", strings.Join(slices.Sorted(maps.Keys(e.em.Consts)), ", "))
		}
		n.consts = map[string]qeval.Value{}
		s, err := e.m.Init(n.consts, e.em.Init)
		if err != nil {
			return nil, fmt.Errorf("nav: init: %w", err)
		}
		n.start = s
		return n, nil
	}
	traces, _ := c.Binding.Reconstruct(start.Steps)
	var tr *qtrace.Trace
	var actors []string
	for _, t := range traces {
		actors = append(actors, nameOf(t.Actor))
		if start.Actor == "" && len(traces) == 1 || start.Actor != "" && (t.Actor == start.Actor || nameOf(t.Actor) == start.Actor) {
			tr = t
		}
	}
	if tr == nil {
		return nil, fmt.Errorf("nav: the run's actors the binding checks are %v: name one (-actor)", actors)
	}
	plan, err := c.Binding.Plan(c.Model, tr)
	if err != nil {
		return nil, fmt.Errorf("nav: actor %s: %w", nameOf(tr.Actor), err)
	}
	if n.consts, err = e.constants(plan); err != nil {
		return nil, fmt.Errorf("nav: %w", err)
	}
	states, failed, err := e.start(c, n.consts, plan)
	switch {
	case err != nil:
		return nil, fmt.Errorf("nav: the run's start: %w", err)
	case failed:
		return nil, fmt.Errorf("nav: the run's start is no state of the model")
	}
	s := states[0]
	at := start.At
	if at < 0 || at > len(plan.Calls) {
		at = len(plan.Calls)
	}
	for k := range at {
		call := plan.Calls[k]
		args, err := e.args(c, call)
		if err != nil {
			return nil, fmt.Errorf("nav: call %d: %w", k+1, err)
		}
		next, ok, err := e.m.Step(n.consts, s, e.em.Actions[call.Action], args...)
		if err != nil {
			return nil, fmt.Errorf("nav: call %d (%s): %w", k+1, call.Quint, err)
		}
		if !ok {
			why := e.explain(c, plan, k, nil)
			return nil, fmt.Errorf("nav: call %d (%s) is not a transition of the model from the state before it: open the run at %d, its last state\n%s", k+1, call.Quint, k, why.String("  "))
		}
		s = next
		n.path = append(n.path, call.Quint)
	}
	n.start = s
	n.from = fmt.Sprintf("actor %s's state after %d of its %d calls", nameOf(tr.Actor), at, len(plan.Calls))
	return n, nil
}

// args are call's arguments as the evaluator's values, in the action's
// parameters' order.
func (e *evaluator) args(c *Checker, call binding.Call) ([]qeval.Value, error) {
	var out []qeval.Value
	for _, p := range c.Model.Actions[call.Action] {
		v, err := e.literal(call.Args[p.Name])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// SplitPath splits a path as -follow gives it, its transitions separated
// by commas outside brackets and string literals: "admit(\"a\"),release(\"a\")".
func SplitPath(s string) []string {
	var out []string
	depth, inStr, start := 0, false, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case inStr && c == '\\':
			i++
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	if t := strings.TrimSpace(s[start:]); t != "" || len(out) > 0 {
		out = append(out, t)
	}
	return out
}

// Node is the node path reaches from the start: each of its transitions a
// branch's Take (or its Label, or an action's bare name when one branch
// applies it, or #n for the n-th branch, from 1), taken in turn.
func (n *Navigator) Node(path ...string) (*Node, error) {
	s, prev, taken, err := n.walk(path)
	if err != nil {
		return nil, err
	}
	return n.node(s, prev, taken)
}

// maxFold bounds a fold: a model whose single branches go on forever
// (a clock's tick) stops after as many.
const maxFold = 1000

// Fold is the node path reaches, then the only branch of each node
// after it taken until a node with none or several: a linear stretch of
// the model folded into one move, its Folded the transitions taken past
// path and its Changed against the state path reached. A state seen
// again (a cycle of single branches) or maxFold transitions stop it.
func (n *Navigator) Fold(path ...string) (*Node, error) {
	s, prev, taken, err := n.walk(path)
	if err != nil {
		return nil, err
	}
	from, folded := s, 0
	seen := map[string]bool{n.key(s): true}
	for folded < maxFold {
		edges, ts, err := n.edges(s)
		if err != nil {
			return nil, err
		}
		if len(edges) != 1 {
			break
		}
		next := ts[0].State
		k := n.key(next)
		if seen[k] {
			break
		}
		seen[k] = true
		s = next
		taken = append(taken, edges[0].Take)
		folded++
	}
	if folded > 0 {
		prev = from
	}
	nd, err := n.node(s, prev, taken)
	if err != nil {
		return nil, err
	}
	nd.Folded = folded
	return nd, nil
}

// key is s as one string: its variables' values in the model's order.
func (n *Navigator) key(s qeval.State) string {
	var b strings.Builder
	for _, v := range n.e.m.Vars {
		b.WriteString(qeval.String(s[v]))
		b.WriteByte(0)
	}
	return b.String()
}

// walk takes path from the start: the state it reaches, the state before
// its last transition (nil for none), and its transitions as branches'
// Take name them.
func (n *Navigator) walk(path []string) (s, prev qeval.State, taken []string, err error) {
	s = n.start
	for i, p := range path {
		edges, ts, err := n.edges(s)
		if err != nil {
			return nil, nil, nil, err
		}
		k, err := pick(edges, p)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("nav: step %d of the path (%s): %w", i+1, p, err)
		}
		prev, s = s, ts[k].State
		taken = append(taken, edges[k].Take)
	}
	return s, prev, taken, nil
}

// pick is the branch p names among edges.
func pick(edges []Edge, p string) (int, error) {
	p = strings.TrimSpace(p)
	if rest, ok := strings.CutPrefix(p, "#"); ok {
		k, err := strconv.Atoi(rest)
		if err != nil || k < 1 || k > len(edges) {
			return 0, fmt.Errorf("no branch %s: the node has %d", p, len(edges))
		}
		return k - 1, nil
	}
	norm := func(s string) string { return strings.Join(strings.Fields(s), "") }
	var byName []int
	for i, e := range edges {
		if norm(e.Take) == norm(p) || norm(e.Label) == norm(p) {
			return i, nil
		}
		if e.Action == p {
			byName = append(byName, i)
		}
	}
	if len(byName) == 1 {
		return byName[0], nil
	}
	var takes []string
	for _, e := range edges {
		takes = append(takes, e.Take)
	}
	if len(byName) > 1 {
		return 0, fmt.Errorf("%d branches apply %s: name one with its arguments (%s)", len(byName), p, strings.Join(takes, ", "))
	}
	return 0, fmt.Errorf("no branch takes %s here; the branches: %s", p, strings.Join(takes, ", "))
}

// edges are the branches of the model's step in s, and the transitions
// they are.
func (n *Navigator) edges(s qeval.State) ([]Edge, []qeval.Transition, error) {
	ts, err := n.e.m.Transitions(n.consts, s, n.e.em.Actions["step"])
	if err != nil {
		return nil, nil, fmt.Errorf("nav: the step's transitions: %w", err)
	}
	var edges []Edge
	var kept []qeval.Transition
	count := map[string]int{}
	for _, t := range ts {
		a, ok := branchEvent(n.c.Model, t)
		if !ok {
			continue
		}
		var lits []string
		e := Edge{Label: a.String(), Action: a.Action}
		for i, v := range a.Args {
			lits = append(lits, qeval.String(v))
			name := ""
			if i < len(a.Params) {
				name = a.Params[i]
			}
			e.Args = append(e.Args, NamedValue{Name: name, Value: qeval.String(v)})
		}
		for _, p := range t.Picks() {
			e.Picks = append(e.Picks, NamedValue{Name: p.Pick, Value: qeval.String(p.Value)})
		}
		e.Take = a.Action
		if len(lits) > 0 {
			e.Take += "(" + strings.Join(lits, ", ") + ")"
		}
		count[e.Take]++
		if c := count[e.Take]; c > 1 {
			e.Take += "#" + strconv.Itoa(c)
		}
		edges, kept = append(edges, e), append(kept, t)
	}
	return edges, kept, nil
}

// node is the node of state s, reached by taken from the start (prev
// the state before its last transition).
func (n *Navigator) node(s, prev qeval.State, taken []string) (*Node, error) {
	m := n.e.m
	out := &Node{ID: strings.Join(taken, ","), Start: n.from, Path: append(slices.Clone(n.path), taken...), Branches: []Edge{}}
	for _, v := range m.Vars {
		out.State = append(out.State, NamedValue{Name: m.Display(v), Value: qeval.String(s[v])})
		if prev != nil && qeval.Compare(s[v], prev[v]) != 0 {
			out.Changed = append(out.Changed, m.Display(v))
		}
	}
	for _, inv := range n.c.Binding.Invariants {
		v, err := m.Eval(n.consts, s, n.e.em.Invariants[inv])
		val := "?"
		if err == nil {
			val = qeval.String(v)
		}
		out.Invariants = append(out.Invariants, NamedValue{Name: inv, Value: val})
	}
	edges, _, err := n.edges(s)
	if err != nil {
		return nil, err
	}
	out.Branches = append(out.Branches, edges...)
	enabled := map[string]bool{}
	for _, e := range edges {
		enabled[e.Action] = true
	}
	step := n.e.em.Actions["step"]
	for _, a := range m.BranchActions(step) {
		if enabled[a] || n.c.Model.Helpers[a] {
			continue
		}
		d := Disabled{Action: a}
		w, err := m.Explain(n.consts, s, a, step)
		switch {
		case err != nil:
			d.Why = &Explanation{Action: a, Unexplained: err.Error()}
		case w != nil:
			d.Why = &Explanation{Action: a}
			shown := map[string]bool{}
			d.Why.Conditions = conditions(w.Chain, shown)
			for _, br := range w.Branches {
				d.Why.Branches = append(d.Why.Branches, Branch{Name: br.Branch, Conditions: conditions(br.Chain, shown)})
			}
			d.Why.Reading = gristReading(n.e, n.consts, s, w)
		}
		out.Disabled = append(out.Disabled, d)
	}
	return out, nil
}

// String is the node as a person reads it: its path, its state, its
// branches numbered as #n takes them, its disabled actions with why.
func (nd *Node) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "node %q (from %s)\n", nd.ID, nd.Start)
	if len(nd.Path) > 0 {
		fmt.Fprintf(&b, "  path: %s\n", strings.Join(nd.Path, ", "))
	}
	fmt.Fprintf(&b, "  state:\n")
	for _, v := range nd.State {
		mark := " "
		if slices.Contains(nd.Changed, v.Name) {
			mark = "*"
		}
		fmt.Fprintf(&b, "   %s%s = %s\n", mark, v.Name, v.Value)
	}
	for _, v := range nd.Invariants {
		fmt.Fprintf(&b, "  invariant %s: %s\n", v.Name, v.Value)
	}
	fmt.Fprintf(&b, "  branches (%d):\n", len(nd.Branches))
	for i, e := range nd.Branches {
		fmt.Fprintf(&b, "    #%d %s\n", i+1, e.Take)
	}
	for _, d := range nd.Disabled {
		fmt.Fprintf(&b, "  disabled: %s\n", d.Action)
		if d.Why != nil {
			b.WriteString(d.Why.String("    "))
		}
	}
	return b.String()
}

// RunNode is a node of qtest's run navigator (DESIGN §58), as `gotogether
// nav -run ... -json` prints it (the test binary writes it; its types are
// here so that the CLI, which a test package does not build, reads it): ID is "<schedule>@<k>", the choices before k fixed.
type RunNode struct {
	// Protocol is the navigation protocol of the binary that wrote the
	// node (NavProtocol; "" before it had one: nav/1)
	Protocol string `json:"protocol,omitempty"`
	Run      string `json:"run"`
	ID       string `json:"id"`
	Schedule string `json:"schedule"`
	At       int    `json:"at"`
	// Choice is the choice the node is before; nil when the run made
	// none there (it ended after At choices)
	Choice   *RunChoice  `json:"choice,omitempty"`
	Branches []RunBranch `json:"branches"`
	// Steps are the steps recorded before the choice, Tasks the
	// simulation's tasks then (Sim.Tasks), Elapsed its virtual time
	Steps   []string `json:"steps"`
	Tasks   []string `json:"tasks,omitempty"`
	Elapsed string   `json:"elapsed,omitempty"`
	// Model is each actor's model state after Steps (the Go evaluator's
	// replay of its plan), or why there is none (a step the model
	// rejects: the explanation)
	Model []ActorState `json:"model,omitempty"`
	// Verdict is the whole run the node makes (nominal after it):
	// "pass", or its report's reason
	Verdict string `json:"verdict"`
	// Folded is how many choices the node advanced through to reach At,
	// asked to fold (RunRequest.Fold): choices that were no decision
	Folded int `json:"folded,omitempty"`
	// Saved is the corpus entry the node's run was saved to
	// (GOTOGETHER_NAV_SAVE), Notes what else is to know
	Saved string   `json:"saved,omitempty"`
	Notes []string `json:"notes,omitempty"`
}

// A RunChoice is the choice a run node is before: its kind, how many
// options, and their names (the option the base run took, Taken).
type RunChoice struct {
	Kind    string   `json:"kind"`
	N       int      `json:"n"`
	Options []string `json:"options,omitempty"`
	Taken   int      `json:"taken"`
}

// A RunBranch is an option of a run node's choice and the node taking
// it reaches.
type RunBranch struct {
	Option int    `json:"option"`
	Name   string `json:"name,omitempty"`
	Child  string `json:"child"`
}

// An ActorState is an actor's model state at a run node: its variables,
// or why the evaluator has none (Error).
type ActorState struct {
	Actor string       `json:"actor"`
	State []NamedValue `json:"state,omitempty"`
	Error string       `json:"error,omitempty"`
}

// NavProtocol is the run navigation protocol this version speaks: the
// RunRequest, RunResponse, RunSession and RunNode lines between a test
// binary (GOTOGETHER_NAV) and a navigator (gotogether nav,
// gotogether-nav), each built against its own version of gotogether. A
// session's first line and a node name it.
const NavProtocol = "gotogether.nav/1"

// CheckNavProtocol refuses a protocol a session or node names that this
// version does not speak, with the fix (a later one: upgrade the
// navigator). "" is a binary's from before the protocol had a name:
// nav/1.
func CheckNavProtocol(p string) error {
	if p == "" {
		return nil
	}
	if _, err := formats.Nav.Check(p); err != nil {
		return fmt.Errorf("the test binary's navigation protocol: %w", err)
	}
	return nil
}

// A RunRequest asks a run navigation session (GOTOGETHER_NAV=serve: the
// test binary reads one a line) for a node: Node as GOTOGETHER_NAV names
// one ("" or start, fail, @k, <schedule>@k), then the options of Follow
// and Take taken in turn (as GOTOGETHER_NAV_FOLLOW); Fold advances it
// through the choices that are no decision (one option, or nominal where
// the base run's are too) to the next that is; Save keeps its run in the
// exploration's corpus.
type RunRequest struct {
	Node   string `json:"node,omitempty"`
	Follow []int  `json:"follow,omitempty"`
	Take   *int   `json:"take,omitempty"`
	Fold   bool   `json:"fold,omitempty"`
	Save   bool   `json:"save,omitempty"`
}

// A RunResponse is a session's line: first its Session, before any
// request, then for each request its Node or the Error that it named
// none (the session goes on).
type RunResponse struct {
	Session *RunSession `json:"session,omitempty"`
	Node    *RunNode    `json:"node,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// A RunSession names the run a session navigates and how many choices
// its base run made (but entropy); Notes say what to know of every node
// (the base run drew entropy, or did not replay).
type RunSession struct {
	// Protocol is the session's navigation protocol (NavProtocol; ""
	// from a binary built before it had one: nav/1): a navigator
	// refuses one it does not speak (CheckNavProtocol)
	Protocol string   `json:"protocol,omitempty"`
	Run      string   `json:"run"`
	Choices  int      `json:"choices"`
	Notes    []string `json:"notes,omitempty"`
}

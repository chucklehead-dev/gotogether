package validate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/validate/internal/evalshare"
)

// Why a step was rejected (H2). quint's report of a step the model
// cannot take says which step and the state before it, not which of the
// action's conditions is false. When the binding's model is in the Go
// evaluator's fragment, the check asks it (qeval.Explain): the rejected
// call's action, applied to the step's arguments in the state before it,
// descended conjunct by conjunct (all {}, and, the actions and
// definitions it calls, lets, a quantifier's witness, a choice's first
// value), to the first condition that is false, shown as Quint text with
// its free names' values; and the actions the model's step could take
// there instead. quint stays the judge: the explanation is added to
// quint's report, never a verdict, and one the evaluator cannot give (a
// model outside its fragment, a state it reaches otherwise, a step it
// takes) says so beside quint's report.

// An Explanation is why the model cannot take a rejected step, by the Go
// evaluator (Failure.Why): an addition to quint's report, never a
// verdict.
type Explanation struct {
	// Action is the call explained, as the model names it: admit(client = "c")
	Action string `json:"action"`
	// Conditions are the false conditions from the action's own
	// conjunct to the most specific one (the last), each where it is
	Conditions []Condition `json:"conditions,omitempty"`
	// Branches, for an any {} none of whose branches is enabled: each
	// branch's own conditions
	Branches []Branch `json:"branches,omitempty"`
	// Reading is a grist application's reading of the conditions, in
	// its terms: a read missing from a decision's read set, a concurrent
	// append, a key answered or applied twice
	Reading string `json:"reading,omitempty"`
	// Enabled are the actions the model's step could take in that state
	// instead, with their arguments; More how many others there are.
	// They are the model's moves from that state, not records: an action
	// enabled many ways is shown with the example whose arguments agree
	// most with the rejected step's.
	Enabled []string `json:"enabled,omitempty"`
	More    int      `json:"more,omitempty"`
	// Expected is the step of the rejected step's own action the model
	// can take there for the same request (its arguments but those the
	// binding fills from the outcome, observations or error): the answer
	// the model gives the request the code answered otherwise. Changes
	// are what it would change in the model's state.
	Expected string   `json:"expected,omitempty"`
	Changes  []Change `json:"changes,omitempty"`
	// Unexplained says why there is no explanation, when there is none
	Unexplained string `json:"unexplained,omitempty"`
}

// A Condition is a false condition of an explanation: In where it is (an
// action or definition applied, a quantifier's witness), Expr the
// condition as Quint text, Values its free names' and operands' values.
type Condition struct {
	In     string       `json:"in"`
	Expr   string       `json:"expr"`
	Values []NamedValue `json:"values,omitempty"`
}

// A NamedValue is a named value of an explanation, as a Quint literal.
type NamedValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// A Branch is one branch of an any {}: its name (the first action it
// applies) and its false conditions.
type Branch struct {
	Name       string      `json:"name"`
	Conditions []Condition `json:"conditions,omitempty"`
}

// String is the explanation as a report's lines, each prefixed with pfx.
func (x *Explanation) String(pfx string) string { return x.render(pfx, false) }

// render is String, verbose with every example of the actions enabled
// instead and every change.
func (x *Explanation) render(pfx string, verbose bool) string {
	var b strings.Builder
	if x.Unexplained != "" {
		fmt.Fprintf(&b, "%swhy: not explained: %s\n", pfx, x.Unexplained)
		return b.String()
	}
	fmt.Fprintf(&b, "%swhy: the model's %s is disabled:\n", pfx, x.Action)
	writeConditions(&b, pfx+"  ", x.Conditions)
	for _, br := range x.Branches {
		fmt.Fprintf(&b, "%s    branch %s:\n", pfx, br.Name)
		writeConditions(&b, pfx+"      ", br.Conditions)
	}
	if x.Reading != "" {
		fmt.Fprintf(&b, "%s  grist: %s\n", pfx, x.Reading)
	}
	if x.Expected != "" {
		switch {
		case len(x.Changes) == 0:
			fmt.Fprintf(&b, "%s  the model expects: %s, which changes nothing\n", pfx, x.Expected)
		default:
			writeChanges(&b, pfx+"  ", "the model expects: "+x.Expected+", which changes:", x.Changes, verbose)
		}
	}
	enabled := x.Enabled
	if !verbose {
		// an example drawn from the instance's constants read as a
		// record to on-call: the count says enough, -v shows them
		enabled = make([]string, len(x.Enabled))
		for i, l := range x.Enabled {
			if a, _, ok := strings.Cut(l, " ways, e.g. "); ok {
				l = a + " ways)"
			}
			enabled[i] = l
		}
	}
	const head = "enabled instead (the model's moves from this state, not records)"
	switch {
	case len(enabled) > 0 && x.More > 0:
		fmt.Fprintf(&b, "%s  %s: %s, and %d more\n", pfx, head, strings.Join(enabled, ", "), x.More)
	case len(enabled) > 0:
		fmt.Fprintf(&b, "%s  %s: %s\n", pfx, head, strings.Join(enabled, ", "))
	}
	return b.String()
}

func writeConditions(b *strings.Builder, pfx string, cs []Condition) {
	for _, c := range cs {
		fmt.Fprintf(b, "%sin %s: %s is false\n", pfx, c.In, c.Expr)
		if len(c.Values) > 0 {
			parts := make([]string, len(c.Values))
			for i, v := range c.Values {
				parts[i] = v.Name + " = " + v.Value
			}
			fmt.Fprintf(b, "%s  where %s\n", pfx, strings.Join(parts, ", "))
		}
	}
}

// enabledShown is how many of the actions enabled instead a report names.
const enabledShown = 6

// conditions are cs as an explanation's, each value once: one an
// earlier condition showed (shown, by name and value) is left out.
func conditions(cs []qeval.Cond, shown map[string]bool) []Condition {
	out := make([]Condition, len(cs))
	for i, c := range cs {
		out[i] = Condition{In: c.In, Expr: c.Expr}
		for _, v := range c.Values {
			s := qeval.String(v.Value)
			if len(s) > 240 {
				s = s[:240] + " ..."
			}
			if k := v.Name + "\x00" + s; !shown[k] {
				shown[k] = true
				out[i].Values = append(out[i].Values, NamedValue{Name: v.Name, Value: s})
			}
		}
	}
	return out
}

// explainResult adds to res's failures, trace t's plan p's, what the Go
// evaluator can say of them, if it can: to a rejected step (a
// conformance failure of kind "disabled") why the model cannot take it,
// the step the model expects there instead and what that would change;
// to a mismatch or an invariant violated after a step, what the step
// changed in the model's state (I20). A check that reads only verdicts
// (a search's candidates) and GOTOGETHER_EVAL=quint ask nothing.
func (c *Checker) explainResult(p *binding.Plan, res *Result, o Options) {
	if c.mode.history != nil || o.VerdictsOnly || c.Options.evalMode() == "quint" {
		return
	}
	var e *evaluator
	get := func() *evaluator {
		if e == nil {
			e = evaluatorFor(c)
		}
		return e
	}
	if f := res.Failure; f != nil && f.Kind == "disabled" && res.Conforms == Fail && f.Index >= 0 && f.Index < len(p.Calls) {
		if e := get(); e.err != nil {
			f.Why = &Explanation{Action: p.Calls[f.Index].Quint, Unexplained: "the Go evaluator has no evaluation module for this binding (" + firstLine(e.err.Error()) + "): quint's report above is all there is"}
		} else {
			f.Why = e.explain(c, p, f.Index, f.State)
		}
	}
	for _, f := range res.failures() {
		if f.Kind != "state" && f.Kind != "invariant" || f.Index < 0 || f.Index >= len(p.Calls) {
			continue
		}
		if e := get(); e.err == nil {
			f.Changes = e.changed(c, p, f.Index, f.State)
		}
	}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// callArgs are call's arguments as the evaluator's values, in the
// order of its action's parameters.
func (e *evaluator) callArgs(c *Checker, call binding.Call) ([]qeval.Value, error) {
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

// before is the state the evaluator's replay of plan's calls before call
// i reaches, with the instance's constants, or why there is none (a
// disagreement with quint's replay, a model it cannot evaluate).
func (e *evaluator) before(c *Checker, plan *binding.Plan, i int) (map[string]qeval.Value, qeval.State, string) {
	consts, err := e.constants(plan)
	if err != nil {
		return nil, nil, err.Error()
	}
	states, failed, err := e.start(c, consts, plan)
	switch {
	case err != nil:
		return nil, nil, err.Error()
	case failed:
		return nil, nil, "the Go evaluator finds the trace's start no state of the model, where quint started it: a disagreement (report it)"
	}
	s := states[0]
	for k := range i {
		as, err := e.callArgs(c, plan.Calls[k])
		if err != nil {
			return nil, nil, err.Error()
		}
		next, ok, err := e.m.Step(consts, s, e.em.Actions[plan.Calls[k].Action], as...)
		switch {
		case err != nil:
			return nil, nil, fmt.Sprintf("replaying step %d: %v", k+1, err)
		case ok && !e.checkpointMatches(&plan.Calls[k], s, next):
			return nil, nil, fmt.Sprintf("step %d is a snapshot checkpoint that disagrees with the complete state before it", k+1)
		case !ok:
			return nil, nil, fmt.Sprintf("the Go evaluator cannot take step %d, which quint took: a disagreement (report it)", k+1)
		}
		s = next
	}
	return consts, s, ""
}

// changed is what plan's call i changed in the model's state: from the
// state before it to the one after, quint's (quintAfter, as quint
// reported it: the successor the evaluator finds equal to it), else the
// evaluator's own; nil when the evaluator cannot say.
func (e *evaluator) changed(c *Checker, plan *binding.Plan, i int, quintAfter map[string]itf.Value) []Change {
	consts, s, why := e.before(c, plan, i)
	if why != "" {
		return nil
	}
	call := plan.Calls[i]
	as, err := e.callArgs(c, call)
	if err != nil {
		return nil
	}
	succ, err := e.m.Successors(consts, s, e.em.Actions[call.Action], as...)
	if err != nil || len(succ) == 0 {
		return nil
	}
	after := succ[0]
	if len(quintAfter) > 0 {
		q, err := qeval.StateOf(e.m, itf.State{Vars: quintAfter})
		if err != nil {
			return nil
		}
		k := e.m.Key(q)
		found := false
		for _, n := range succ {
			if e.m.Key(n) == k {
				after, found = n, true
				break
			}
		}
		if !found {
			return nil // the evaluator reaches other states than quint's: no diff of quint's
		}
	}
	return stateDiff(s, after, e.m.Display)
}

// explain is why the model cannot take plan's call i, from the state
// the evaluator's replay of the calls before it reaches (quint's state
// there, as quint reported it, when it has one: a different state is no
// explanation of quint's rejection).
func (e *evaluator) explain(c *Checker, plan *binding.Plan, i int, quintState map[string]itf.Value) *Explanation {
	call := plan.Calls[i]
	x := &Explanation{Action: call.Quint}
	no := func(why string, a ...any) *Explanation {
		x.Unexplained = fmt.Sprintf(why, a...)
		return x
	}
	consts, s, why := e.before(c, plan, i)
	if why != "" {
		return no("%s", why)
	}
	if len(quintState) > 0 {
		if q, err := qeval.StateOf(e.m, itf.State{Vars: quintState}); err == nil && e.m.Key(q) != e.m.Key(s) {
			return no("the Go evaluator reaches another state before the step than quint's (above): a disagreement (report it)")
		}
	}
	as, err := e.callArgs(c, call)
	if err != nil {
		return no("%v", err)
	}
	if call.Checkpoint {
		next, enabled, err := e.m.Step(consts, s, e.em.Actions[call.Action], as...)
		if err == nil && enabled && !e.checkpointMatches(&call, s, next) {
			return no("the snapshot checkpoint disagrees with the complete state before it; it cannot reset replay")
		}
	}
	w, err := e.m.Explain(consts, s, "", e.em.Actions[call.Action], as...)
	switch {
	case err != nil:
		return no("%v", err)
	case w == nil:
		return no("the Go evaluator takes this step from the state before it (quint's replay may have chosen a value of a nondet that disables it, where another enables it): quint's report above is the verdict")
	}
	x.Action = w.Action
	shown := map[string]bool{}
	x.Conditions = conditions(w.Chain, shown)
	for _, br := range w.Branches {
		x.Branches = append(x.Branches, Branch{Name: br.Branch, Conditions: conditions(br.Chain, shown)})
	}
	x.Reading = gristReading(e, consts, s, w)
	x.Enabled, x.More, x.Expected, x.Changes = e.enabledInstead(c, consts, s, call, as)
	return x
}

// enabledInstead are the actions the model's step can take in s, each
// named once: with its arguments if it is enabled one way, else with how
// many ways and an example, the one whose arguments agree most with the
// rejected call's (rejected, its arguments as values), so an example
// reads the record's values where the model allows them; up to
// enabledShown, and how many more. expected is the transition of the
// rejected call's own action for the same request (every parameter the
// binding does not fill from the step's outcome, observations or error
// equal), if there is one: the answer the model gives the request the
// code answered otherwise; changes what it changes from s.
func (e *evaluator) enabledInstead(c *Checker, consts map[string]qeval.Value, s qeval.State, rejected binding.Call, args []qeval.Value) (out []string, more int, expected string, changes []Change) {
	step, ok := e.em.Actions["step"]
	if !ok {
		return nil, 0, "", nil
	}
	ts, err := e.m.Transitions(consts, s, step)
	if err != nil {
		return nil, 0, "", nil
	}
	// the request's parameters (those the binding does not fill from the
	// step's outcome, observations or error) weigh twice the response's:
	// an example of the rejected action is the same request answered
	// otherwise before another request answered alike
	want := map[string]qeval.Value{}
	request := map[string]bool{}
	tmpl := c.Binding.Actions[rejected.Action].Args
	for k, p := range c.Model.Actions[rejected.Action] {
		if k < len(args) {
			want[p.Name] = args[k]
		}
		t := tmpl[p.Name]
		request[p.Name] = !strings.Contains(t, ".outcome") && !strings.Contains(t, ".obs") && !strings.Contains(t, ".error")
	}
	requests := 0
	for _, r := range request {
		if r {
			requests++
		}
	}
	// agree is how much a's arguments agree with the rejected call's, and
	// whether it is the same request (every request parameter equal)
	agree := func(a qeval.Event) (int, bool) {
		n, same := 0, 0
		for k, p := range a.Params {
			if v, ok := want[p]; ok && k < len(a.Args) && qeval.Compare(v, a.Args[k]) == 0 {
				n++
				if request[p] {
					n++
					same++
				}
			}
		}
		return n, same == requests && requests < len(want)
	}
	type way struct {
		label string
		agree int
	}
	var names []string
	ways := map[string][]way{}
	best := -1
	for _, t := range ts {
		a, ok := branchEvent(c.Model, t)
		if !ok {
			continue
		}
		l := a.String()
		if ways[a.Action] == nil {
			names = append(names, a.Action)
		}
		n, same := agree(a)
		if !slices.ContainsFunc(ways[a.Action], func(w way) bool { return w.label == l }) {
			ways[a.Action] = append(ways[a.Action], way{l, n})
		}
		if a.Action == rejected.Action && same && n > best {
			best, expected, changes = n, l, stateDiff(s, t.State, e.m.Display)
		}
	}
	for _, n := range names {
		ws := ways[n]
		if len(ws) <= 3 {
			for _, w := range ws {
				out = append(out, w.label)
			}
			continue
		}
		ex := ws[0]
		for _, w := range ws[1:] {
			if w.agree > ex.agree {
				ex = w
			}
		}
		out = append(out, fmt.Sprintf("%s (%d ways, e.g. %s)", n, len(ws), clip(ex.label, 160)))
	}
	if len(out) > enabledShown {
		return out[:enabledShown], len(out) - enabledShown, expected, changes
	}
	return out, 0, expected, changes
}

// branchEvent is how a transition of the model's step names itself: the
// first action it applies below step (a frame condition, a helper, is
// part of its caller's).
func branchEvent(m *binding.Model, t qeval.Transition) (qeval.Event, bool) {
	return evalshare.BranchEvent(m, t)
}

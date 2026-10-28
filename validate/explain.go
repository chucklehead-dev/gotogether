package validate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/itf"
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
	// instead, with their arguments; More how many others there are
	Enabled []string `json:"enabled,omitempty"`
	More    int      `json:"more,omitempty"`
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
func (x *Explanation) String(pfx string) string {
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
	switch {
	case len(x.Enabled) > 0 && x.More > 0:
		fmt.Fprintf(&b, "%s  enabled instead: %s, and %d more\n", pfx, strings.Join(x.Enabled, ", "), x.More)
	case len(x.Enabled) > 0:
		fmt.Fprintf(&b, "%s  enabled instead: %s\n", pfx, strings.Join(x.Enabled, ", "))
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

// explainResult adds to res's rejected step (a conformance failure of
// kind "disabled", trace t's plan p's) why the model cannot take it, by
// the Go evaluator, if it can say. A check that reads only verdicts (a
// search's candidates) and GOTOGETHER_EVAL=quint ask nothing.
func (c *Checker) explainResult(p *binding.Plan, res *Result, o Options) {
	f := res.Failure
	if f == nil || f.Kind != "disabled" || res.Conforms != Fail || f.Index < 0 || f.Index >= len(p.Calls) ||
		c.mode.history != nil || o.VerdictsOnly || c.Options.evalMode() == "quint" {
		return
	}
	e := evaluatorFor(c)
	if e.err != nil {
		f.Why = &Explanation{Action: p.Calls[f.Index].Quint, Unexplained: "the Go evaluator has no evaluation module for this binding (" + firstLine(e.err.Error()) + "): quint's report above is all there is"}
		return
	}
	f.Why = e.explain(c, p, f.Index, f.State)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
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
	consts, err := e.constants(plan)
	if err != nil {
		return no("%v", err)
	}
	states, failed, err := e.start(c, consts, plan)
	switch {
	case err != nil:
		return no("%v", err)
	case failed:
		return no("the Go evaluator finds the trace's start no state of the model, where quint started it: a disagreement (report it)")
	}
	s := states[0]
	args := func(call binding.Call) ([]qeval.Value, error) {
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
	for k := range i {
		as, err := args(plan.Calls[k])
		if err != nil {
			return no("%v", err)
		}
		next, ok, err := e.m.Step(consts, s, e.em.Actions[plan.Calls[k].Action], as...)
		switch {
		case err != nil:
			return no("replaying step %d: %v", k+1, err)
		case !ok:
			return no("the Go evaluator cannot take step %d, which quint took: a disagreement (report it)", k+1)
		}
		s = next
	}
	if len(quintState) > 0 {
		if q, err := qeval.StateOf(e.m, itf.State{Vars: quintState}); err == nil && e.m.Key(q) != e.m.Key(s) {
			return no("the Go evaluator reaches another state before the step than quint's (above): a disagreement (report it)")
		}
	}
	as, err := args(call)
	if err != nil {
		return no("%v", err)
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
	x.Enabled, x.More = e.enabledInstead(c, consts, s)
	return x
}

// enabledInstead are the actions the model's step can take in s, each
// named once: with its arguments if it is enabled one way, else with how
// many ways and the first (a step that chooses its arguments is enabled
// many ways); up to enabledShown, and how many more.
func (e *evaluator) enabledInstead(c *Checker, consts map[string]qeval.Value, s qeval.State) ([]string, int) {
	step, ok := e.em.Actions["step"]
	if !ok {
		return nil, 0
	}
	ts, err := e.m.Transitions(consts, s, step)
	if err != nil {
		return nil, 0
	}
	var names []string
	ways := map[string][]string{}
	for _, t := range ts {
		a, ok := branchEvent(c.Model, t)
		if !ok {
			continue
		}
		l := a.String()
		if ways[a.Action] == nil {
			names = append(names, a.Action)
		}
		if !slices.Contains(ways[a.Action], l) {
			ways[a.Action] = append(ways[a.Action], l)
		}
	}
	var out []string
	for _, n := range names {
		if ls := ways[n]; len(ls) <= 3 {
			out = append(out, ls...)
			continue
		}
		l := ways[n][0]
		if len(l) > 160 {
			l = l[:160] + " ..."
		}
		out = append(out, fmt.Sprintf("%s (%d ways, e.g. %s)", n, len(ways[n]), l))
	}
	if len(out) > enabledShown {
		return out[:enabledShown], len(out) - enabledShown
	}
	return out, 0
}

// branchEvent is how a transition of the model's step names itself: the
// first action it applies below step (a frame condition, a helper, is
// part of its caller's).
func branchEvent(m *binding.Model, t qeval.Transition) (qeval.Event, bool) {
	for _, a := range t.Actions() {
		if a.Action == "step" || m.Helpers[a.Action] {
			continue
		}
		return a, true
	}
	return qeval.Event{}, false
}

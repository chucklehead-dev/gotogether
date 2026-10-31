package validate

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

func (c *Checker) hasChoiceCalls(p *binding.Plan) bool {
	if c.Model == nil {
		return false
	}
	if p.Start != nil && len(c.Model.Chooses(p.Start.Action)) > 0 {
		return true
	}
	if len(c.Model.Chooses("init")) != 0 {
		return true
	}
	for _, call := range p.Calls {
		if len(c.Model.Chooses(call.Action)) != 0 {
			return true
		}
	}
	return false
}

func (c *Checker) choiceNoVerdict(r *Result, status, reason string, explored int) *Result {
	limit := c.Options.ChoiceLimit
	if limit == 0 {
		limit = DefaultChoiceLimit
	}
	r.Conforms, r.Failure = NotEvaluated, nil
	r.Invariants = nil
	for _, name := range c.Binding.Invariants {
		r.Invariants = append(r.Invariants, InvariantResult{Name: name, Verdict: NotEvaluated})
	}
	if r.Settled {
		for _, name := range c.Binding.Eventually {
			r.Invariants = append(r.Invariants, InvariantResult{Name: EventuallyName(name), Verdict: NotEvaluated, Eventually: true})
		}
	}
	r.Choices = &ChoiceSearch{Status: status, Reason: reason, Limit: limit, Explored: explored, Confirmation: "not_performed"}
	r.Issues = append(r.Issues, qtrace.Issue{Severity: "warn", Kind: "model-choice", Msg: "no verdict: " + reason})
	return r
}

// choiceNode retains a complete explanation, not a greedy local successor.
// bad records first violations on this path, including initial-state checks.
type choiceNode struct {
	state  qeval.State
	parent *choiceNode
	bad    map[string]int
	at     int
}

func (c *Checker) checkChoices(ctx context.Context, tr *qtrace.Trace, p *binding.Plan, r *Result, o Options) (*Result, error) {
	refuse := func(reason string) (*Result, error) { return c.choiceNoVerdict(r, "unsupported", reason, 0), nil }
	if c.compiled != nil {
		return refuse("compiled mode cannot confirm a pinned model-choice witness with Quint")
	}
	if c.mode.history != nil || len(c.Binding.Hidden) > 0 || c.Binding.Partition != "" || p.Start != nil || slices.ContainsFunc(p.Calls, func(call binding.Call) bool { return call.Checkpoint }) || c.resumes(tr) || len(c.Binding.Within) > 0 {
		return refuse("finite pinned choices currently require an ordinary trace without history search, hidden actions, partition, snapshot, resume or within checks")
	}
	if len(tr.Lost) > 0 || len(tr.Gaps) > 0 || slices.ContainsFunc(p.Calls, func(call binding.Call) bool { return call.Step != nil && call.Step.Ambiguous }) {
		return refuse("finite pinned choices require complete observed records without assumed effects")
	}
	if o.ChoiceLimit < 0 {
		return nil, fmt.Errorf("choice limit must be nonnegative (0 uses the default)")
	}
	limit := o.ChoiceLimit
	if limit == 0 {
		limit = DefaultChoiceLimit
	}
	detail := &ChoiceSearch{Limit: limit, Confirmation: "not_performed"}
	r.Choices = detail
	explored := 0
	unsupported := func(err error) (*Result, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		out := c.choiceNoVerdict(r, "unsupported", err.Error(), explored)
		out.Choices.Exhaustive = detail.Exhaustive
		return out, nil
	}
	exhausted := func() (*Result, error) {
		return c.choiceNoVerdict(r, "exhausted", fmt.Sprintf("finite model-choice search reached its limit of %d expanded states", limit), explored), nil
	}
	e := evaluatorFor(c)
	if e.err != nil {
		return unsupported(e.err)
	}
	consts, err := e.constants(p)
	if err != nil {
		return unsupported(err)
	}
	initial, err := e.m.Successors(consts, nil, e.em.Init)
	if err != nil {
		return unsupported(err)
	}
	if len(initial) == 0 {
		return refuse("finite pinned choices require at least one admitted initial state; init is disabled")
	}
	mark := func(n *choiceNode) error {
		for _, name := range c.Binding.Invariants {
			if _, bad := n.bad[name]; bad {
				continue
			}
			value, err := e.m.Eval(consts, n.state, e.em.Invariants[name])
			if err != nil {
				return err
			}
			if value != qeval.Bool(true) {
				n.bad[name] = n.at
			}
		}
		return nil
	}
	// Each admitted initial state starts a separate whole-path explanation.
	// Charge roots before observations, just as transitions are charged below.
	var frontier []*choiceNode
	seenRoots := map[string]bool{}
	for _, state := range initial {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := e.m.Key(state)
		if seenRoots[key] {
			continue
		}
		seenRoots[key] = true
		if explored >= limit {
			return exhausted()
		}
		explored++
		root := &choiceNode{state: state, bad: map[string]int{}, at: -1}
		if err := mark(root); err != nil {
			return unsupported(err)
		}
		frontier = append(frontier, root)
	}
	for i, call := range p.Calls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		args, err := e.args(c, call)
		if err != nil {
			return unsupported(err)
		}
		var next []*choiceNode
		enabled := false
		for _, n := range frontier {
			transitions, err := e.m.Transitions(consts, n.state, e.em.Actions[call.Action], args...)
			if err != nil {
				return unsupported(err)
			}
			// Equivalent successors need only one explanation from this parent.
			seen := map[string]bool{}
			for _, transition := range transitions {
				key := e.m.Key(transition.State)
				if seen[key] {
					continue
				}
				seen[key] = true
				if explored >= limit {
					return exhausted()
				}
				explored++
				enabled = true
				if !e.checkpointMatches(&call, n.state, transition.State) {
					continue
				}
				ok, err := e.holds(consts, transition.State, &call)
				if err != nil {
					return unsupported(err)
				}
				if !ok {
					continue
				}
				child := &choiceNode{state: transition.State, parent: n, bad: maps.Clone(n.bad), at: i}
				if err := mark(child); err != nil {
					return unsupported(err)
				}
				next = append(next, child)
			}
		}
		if len(next) == 0 {
			detail.Status, detail.Explored, detail.Exhaustive = "rejected", explored, true
			detail.Reason = "every finite model path is disabled or disagrees with the observed projection; negative proof is exhaustive Go search"
			r.Conforms = Fail
			kind := "disabled"
			if enabled {
				kind = "state"
			}
			r.Failure = &Failure{Kind: kind, Index: i, Call: call.Quint, Step: call.Step, Expect: call.Expect, ExpectTemplate: call.ExpectTemplate, Quint: detail.Reason}
			for _, name := range c.Binding.Invariants {
				r.Invariants = append(r.Invariants, InvariantResult{Name: name, Verdict: NotEvaluated})
			}
			return r, nil
		}
		frontier = next
	}
	if tr.Settled {
		for _, n := range frontier {
			for _, name := range c.Binding.Eventually {
				v, err := e.m.Eval(consts, n.state, e.em.Eventually[name])
				if err != nil {
					return unsupported(err)
				}
				if v != qeval.Bool(true) {
					n.bad[EventuallyName(name)] = len(p.Calls) - 1
				}
			}
		}
	}
	detail.Status, detail.Explored, detail.Exhaustive = "matched", explored, true
	r.Conforms = Pass
	var names []string
	names = append(names, c.Binding.Invariants...)
	if tr.Settled {
		for _, name := range c.Binding.Eventually {
			names = append(names, EventuallyName(name))
		}
	}
	keeps := func(n *choiceNode, required []string) bool {
		for _, name := range required {
			if _, bad := n.bad[name]; bad {
				return false
			}
		}
		return true
	}
	possible := func(required []string) bool {
		for _, n := range frontier {
			if keeps(n, required) {
				return true
			}
		}
		return false
	}
	selected := frontier[0]
	for _, n := range frontier {
		if keeps(n, names) {
			selected = n
			break
		}
	}
	// If every property can hold separately but no path keeps them together,
	// identify a deletion-minimal conflicting set. An unrelated always-true
	// invariant must not be reported as part of that conflict.
	conflict := []string{}
	if !possible(names) {
		conflict = slices.Clone(names)
		for i := 0; i < len(conflict); {
			candidate := append(slices.Clone(conflict[:i]), conflict[i+1:]...)
			if !possible(candidate) {
				conflict = candidate
			} else {
				i++
			}
		}
	}
	for _, name := range names {
		ir := InvariantResult{Name: name, Verdict: Pass, Eventually: strings.HasPrefix(name, "eventually ")}
		if !possible([]string{name}) || slices.Contains(conflict, name) {
			ir.Verdict = Fail
			note := "every whole model path matching the observations violates " + name
			if possible([]string{name}) {
				note = "no one whole model path jointly satisfies " + strings.Join(conflict, ", ") + "; this is a joint-property conflict"
			}
			at := len(p.Calls) - 1
			if pos, ok := selected.bad[name]; ok {
				at = pos
			}
			ir.Failure = &Failure{Kind: "invariant", Index: at, Note: note}
			if at >= 0 {
				ir.Failure.Step = p.Calls[at].Step
			}
		}
		r.Invariants = append(r.Invariants, ir)
	}
	vars, err := c.choiceVariables(e.m)
	if err != nil {
		return unsupported(err)
	}
	path := make([]*choiceNode, len(p.Calls)+1)
	for n := selected; n != nil; n = n.parent {
		path[n.at+1] = n
	}
	stateExpect := func(state qeval.State) (string, error) {
		var terms []string
		for _, v := range e.m.Vars {
			literal, err := choiceStateLiteral(state[v])
			if err != nil {
				return "", fmt.Errorf("model variable %s: %w", vars[v], err)
			}
			terms = append(terms, vars[v]+" == "+literal)
		}
		if len(terms) == 0 {
			return "true", nil
		}
		return "and { " + strings.Join(terms, ", ") + " }", nil
	}
	pinned := *p
	pinned.Calls = slices.Clone(p.Calls)
	root := path[0]
	initialExpect, err := stateExpect(root.state)
	if err != nil {
		return unsupported(err)
	}
	initExpr, err := e.m.PinAction(consts, nil, e.em.Init, nil, root.state, vars)
	if err != nil {
		return unsupported(err)
	}
	pinned.Start = &binding.Call{Quint: "(" + initExpr + ")", Action: "init", Expect: initialExpect}
	detail.Initialization = &ChoiceInitialization{Action: "init", Expression: initExpr}
	for i, call := range p.Calls {
		args, err := e.args(c, call)
		if err != nil {
			return unsupported(err)
		}
		expr, err := e.m.PinAction(consts, path[i].state, e.em.Actions[call.Action], args, path[i+1].state, vars)
		if err != nil {
			return unsupported(err)
		}
		pinned.Calls[i].Quint = "(" + expr + ")"
		pinned.Calls[i].Expect, err = stateExpect(path[i+1].state)
		if err != nil {
			return unsupported(err)
		}
		if call.Expect != "" {
			pinned.Calls[i].Expect = "(" + call.Expect + ") and (" + pinned.Calls[i].Expect + ")"
		}
		step := ChoiceStep{Index: i, Action: call.Action, Expression: expr}
		if call.Step != nil {
			step.Recorder, step.Seq = call.Step.Recorder, call.Step.Seq
		}
		detail.Witness = append(detail.Witness, step)
	}
	// Confirmation uses original model variables/actions, specialized choices,
	// and exact intermediate-state checks. It is never an arbitrary assignment
	// of evaluator-produced post-states.
	confirmed, err := validatePlanIn(ctx, c.Binding, tr, &pinned, o, newResult(c.Binding, tr, &pinned), false)
	if err != nil {
		return nil, err
	}
	if confirmed.Conforms != Pass || (passed(r) && !passed(confirmed)) {
		return nil, fmt.Errorf("pinned model-choice witness disagrees with Quint: %s", confirmed.Summary())
	}
	for _, name := range names {
		_, violated := selected.bad[name]
		want := Pass
		if violated {
			want = Fail
		}
		found := slices.IndexFunc(confirmed.Invariants, func(v InvariantResult) bool { return v.Name == name })
		if found < 0 || confirmed.Invariants[found].Verdict != want {
			return nil, fmt.Errorf("pinned model-choice witness property %s disagrees with Quint (selected path expected %s): %s", name, want, confirmed.Summary())
		}
	}
	detail.Confirmation = "path_confirmed"
	detail.Reason = "finite Go search over whole paths; Quint confirmed the selected original-action witness, not nonexistence of other paths"
	r.Module, r.Output, r.ITF = confirmed.Module, confirmed.Output, confirmed.ITF
	for i := range r.Invariants {
		if f := r.Invariants[i].Failure; f != nil {
			pos := f.Index + 1
			if pos >= 0 && pos < len(path) {
				f.State = map[string]itf.Value{}
				for _, v := range e.m.Vars {
					value, err := toITF(path[pos].state[v])
					if err == nil {
						f.State[vars[v]] = value
					}
				}
			}
		}
	}
	return r, nil
}

// Only unambiguous visible variables can be referenced in the original module.
// Display names are not identity: imported aliases sharing a suffix are refused.
func (c *Checker) choiceVariables(m *qeval.Model) (map[string]string, error) {
	out := map[string]string{}
	used := map[string]bool{}
	for _, v := range m.Vars {
		var matches []string
		for _, name := range c.Model.Vars {
			if v == name || strings.HasSuffix(v, "::"+name) {
				matches = append(matches, name)
			}
		}
		if len(matches) != 1 || used[matches[0]] {
			return nil, fmt.Errorf("model variable %s has no unambiguous visible name for pinned replay", v)
		}
		out[v] = matches[0]
		used[matches[0]] = true
	}
	return out, nil
}

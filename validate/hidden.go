package validate

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// DefaultHiddenDepth is how many hidden actions (binding.Hidden) may run
// between two recorded steps, unless the binding says (HiddenDepth).
const DefaultHiddenDepth = 4

// hiddenCap bounds the states a search over hidden actions keeps at a
// step: reaching it is no verdict.
const hiddenCap = 20000

// checkHidden judges a trace of a binding with hidden actions: the states
// the model may be in are searched, each recorded step taken from any
// state the hidden actions reach (at most the depth of them since the
// step before), its expect holding after it. The trace conforms if some
// path takes every step; the invariants hold if some path also keeps them
// all; eventual vals of a settled trace hold if some final state, hidden
// actions after the last step included (they may run before the system
// is quiet: a probe recorded after the settle observes what they did),
// has them. It is the Go evaluator's search (quint cannot
// replay a step whose choices are unknown), and says so; an evaluator
// that cannot evaluate the model is an error, never a verdict.
//
// With a resume (binding's Resume) and a trace whose first recorder lost
// its prefix, the search starts from every state the resume admits that
// keeps the invariants (resume.go), and quint confirms its verdict.
func (c *Checker) checkHidden(ctx context.Context, tr *qtrace.Trace) (*Result, error) {
	plan, err := c.Binding.Plan(c.Model, tr)
	if err != nil {
		return nil, err
	}
	res := newResult(c.Binding, tr, plan)
	res.Calls, res.Steps = plan.Calls, len(plan.Calls)
	if plan.Start != nil {
		res.Steps++ // the snapshot it starts from
	}
	e := evaluatorFor(c)
	if e.err != nil {
		what, fix := fmt.Sprintf("hidden actions (%s) are", strings.Join(c.Binding.Hidden, ", ")), ""
		if len(c.Binding.Hidden) == 0 {
			what, fix = fmt.Sprintf("the states resume %s admits are", c.Binding.Resume), ": record a snapshot step at each log's head instead (snapshot: restore)"
		}
		return nil, fmt.Errorf("%s searched by the Go evaluator, which has none for this binding: %w%s", what, e.err, fix)
	}
	consts := map[string]qeval.Value{}
	for k, name := range e.em.Consts {
		v, err := e.literal(plan.Constants[k])
		if err != nil {
			return nil, err
		}
		consts[name] = v
	}
	h := &hiddenSearch{c: c, plan: plan, e: e, consts: consts, calls: plan.Calls, depth: c.Binding.HiddenDepth}
	if h.depth == 0 {
		h.depth = DefaultHiddenDepth
	}
	if l, ok := resumeLost(c.Binding, tr); ok {
		return c.checkResume(ctx, tr, plan, res, h, l)
	}
	res, _, err = c.judgeHidden(tr, plan, res, h)
	return res, err
}

// judgeHidden is checkHidden's search and its verdicts, and, for a
// verdict of every check passing, the start its witness path began in
// (h.starts' index; -1 otherwise, or with no starts).
func (c *Checker) judgeHidden(tr *qtrace.Trace, plan *binding.Plan, res *Result, h *hiddenSearch) (*Result, int, error) {
	out, err := h.run(nil)
	if err != nil {
		return nil, -1, err
	}
	if len(c.Binding.Hidden) > 0 {
		note := fmt.Sprintf("judged by the Go evaluator's search over hidden actions (%s, at most %d between steps): quint cannot replay steps whose choices are unknown",
			strings.Join(c.Binding.Hidden, ", "), h.depth)
		res.Issues = append(res.Issues, qtrace.Issue{Severity: "info", Kind: "hidden", Msg: note})
	}
	switch {
	case out.start:
		st := plan.Start
		res.Conforms = Fail
		res.Failure = &Failure{Kind: "disabled", Index: -1, Call: st.Quint, Step: st.Step, Expect: st.Expect, ExpectTemplate: st.ExpectTemplate,
			Quint: fmt.Sprintf("the snapshot %s is no state of the model: its action is disabled, or its expect false, with the values it recorded", st.Action)}
		for _, inv := range c.Binding.Invariants {
			res.Invariants = append(res.Invariants, InvariantResult{Name: inv, Verdict: NotEvaluated})
		}
		return res, -1, nil
	case out.capped:
		res.Conforms = NotEvaluated
		res.Issues = append(res.Issues, qtrace.Issue{Severity: "warn", Kind: "hidden",
			Msg: fmt.Sprintf("no verdict: the states the hidden actions reach passed %d at step %d; lower hiddenDepth, or record more of the actions", hiddenCap, out.at+1)})
		return res, -1, nil
	case out.at >= 0 && out.cut:
		// a rejection within the depth is not the model's: a longer run of
		// hidden actions may take the step (it once said Fail)
		res.Conforms = NotEvaluated
		res.Issues = append(res.Issues, qtrace.Issue{Severity: "warn", Kind: "hidden",
			Msg: fmt.Sprintf("no verdict: no state within %d hidden actions of the step before takes step %d (%s), and more hidden actions reach states the depth left out: raise hiddenDepth, or record more of the actions",
				h.depth, out.at+1, h.calls[out.at].Action)})
		for _, inv := range c.Binding.Invariants {
			res.Invariants = append(res.Invariants, InvariantResult{Name: inv, Verdict: NotEvaluated})
		}
		return res, -1, nil
	case out.at >= 0:
		res.Conforms = Fail
		res.Failure = h.failure(out)
		kind := "hidden"
		if len(c.Binding.Hidden) == 0 {
			kind = "resume"
		}
		res.Issues = append(res.Issues, qtrace.Issue{Severity: "info", Kind: kind, Msg: res.Failure.Quint})
		for _, inv := range c.Binding.Invariants {
			res.Invariants = append(res.Invariants, InvariantResult{Name: inv, Verdict: NotEvaluated})
		}
		return res, -1, nil
	}
	res.Conforms = Pass
	final := out // the outcome whose final states witness a pass
	// the invariants on one path, as a replay keeps them all; when no path
	// keeps them all, those no path keeps alone fail, or, each kept on some
	// path, every one does
	if len(c.Binding.Invariants) > 0 {
		all, err := h.run(c.Binding.Invariants)
		if err != nil {
			return nil, -1, err
		}
		if all.at < 0 && !all.capped {
			final = all // (with none keeping them all the run fails anyway: its eventual vals as before)
		}
		verdict := func(o hiddenOutcome, inv, why string) InvariantResult {
			ir := InvariantResult{Name: inv, Verdict: Pass}
			switch {
			case o.capped, o.at >= 0 && o.cut: // past the cap, or no path within the depth: a longer one may keep it
				ir.Verdict = NotEvaluated
			case o.at >= 0:
				ir.Verdict = Fail
				ir.Failure = &Failure{Kind: "invariant", Index: o.at, Note: why}
				if o.initial {
					ir.Failure.Index = -1 // the initial state violates it (the snapshot's, if the trace starts from one)
					if plan.Start != nil {
						ir.Failure.Step = plan.Start.Step
					}
				} else {
					ir.Failure.Step = plan.Calls[o.at].Step
				}
			}
			return ir
		}
		switch {
		case all.capped || all.at < 0:
			for _, inv := range c.Binding.Invariants {
				res.Invariants = append(res.Invariants, verdict(all, inv, ""))
			}
		default:
			var irs []InvariantResult
			alone := false
			for _, inv := range c.Binding.Invariants {
				o, err := h.run([]string{inv})
				if err != nil {
					return nil, -1, err
				}
				ir := verdict(o, inv, fmt.Sprintf("every path the hidden actions allow violates %s", inv))
				alone = alone || ir.Verdict == Fail
				irs = append(irs, ir)
			}
			if !alone { // each kept on some path, no path keeps them all
				irs = irs[:0]
				for _, inv := range c.Binding.Invariants {
					irs = append(irs, verdict(all, inv, "no one path the hidden actions allow keeps every invariant"))
				}
			}
			res.Invariants = append(res.Invariants, irs...)
		}
	}
	if tr.Settled && len(c.Binding.Eventually) > 0 {
		// in the final states of the paths that kept the invariants (final),
		// all together, as one replay judges them: a val holding on another
		// path than the invariants', or than another val's, is no pass
		together := false
		for _, s := range final.states {
			ok, err := h.eventualAll(s, true)
			if err != nil {
				return nil, -1, err
			}
			together = together || ok
		}
		for _, ev := range c.Binding.Eventually {
			ok, err := h.eventually(final.states, ev)
			if err != nil {
				return nil, -1, err
			}
			ir := InvariantResult{Name: EventuallyName(ev), Verdict: Pass, Eventually: true}
			switch {
			case together:
			case !ok && final.cut || ok && final.cut: // no final state within the depth has them; one past it may
				ir.Verdict = NotEvaluated
			case !ok:
				ir.Verdict = Fail
				ir.Failure = &Failure{Kind: "invariant", Index: len(plan.Calls) - 1, Note: fmt.Sprintf("%s holds in no state the run may have settled in (on a path keeping the invariants), hidden actions after its last step included", ev)}
			default:
				ir.Verdict = Fail
				ir.Failure = &Failure{Kind: "invariant", Index: len(plan.Calls) - 1, Note: "no one state the run may have settled in (on a path keeping the invariants) has every eventual val"}
			}
			res.Invariants = append(res.Invariants, ir)
		}
	}
	if len(c.Binding.Hidden) > 0 { // (a resume's are judged on its witness's replay)
		for _, w := range c.Binding.Within {
			res.Invariants = append(res.Invariants, InvariantResult{Name: w.Name(), Verdict: NotEvaluated, Within: true})
		}
		if len(c.Binding.Within) > 0 {
			res.Issues = append(res.Issues, qtrace.Issue{Severity: "warn", Kind: "within", Msg: "bounded-response obligations not judged: a trace with hidden actions has no one replay to time"})
		}
	}
	if !passed(res) || res.Conforms != Pass || slices.ContainsFunc(res.Invariants, func(i InvariantResult) bool { return i.Verdict != Pass && !i.Within }) {
		return res, -1, nil
	}
	w, err := h.witness(final, tr.Settled)
	return res, w, err
}

// hiddenSearch is one search over a trace's states.
type hiddenSearch struct {
	c    *Checker
	plan *binding.Plan
	// starts, if set, are the states a resume admits, where the search
	// starts instead of init (each state it reaches carries its start)
	starts []qeval.State
	e      *evaluator
	consts map[string]qeval.Value
	calls  []binding.Call
	depth  int
}

// hiddenOutcome is a search's: the step no state could take (-1: every
// one was), whether it was disabled everywhere or taken with its expect
// false, the states the run may end in (after hidden actions), and
// whether the states passed the cap.
type hiddenOutcome struct {
	at       int
	initial  bool // at 0: no initial state keeps the invariants
	start    bool // the snapshot the trace starts from is no state of the model
	disabled bool
	states   []qeval.State
	origins  []int // each final state's start (h.starts' index), -1 without starts
	final    int
	capped   bool
	// cut: a closure the depth ended (one more hidden action reached a new
	// state): a step no state took, an invariant no path kept, an eventual
	// val no final state had, may be a longer path's
	cut bool
}

// run searches the trace's paths whose states keep the invariants keep
// (none: conformance alone).
func (h *hiddenSearch) run(keep []string) (hiddenOutcome, error) {
	ok := func(s qeval.State) (bool, error) {
		for _, inv := range keep {
			v, err := h.e.m.Eval(h.consts, s, h.e.em.Invariants[inv])
			if err != nil {
				return false, err
			}
			if v != qeval.Bool(true) {
				return false, nil
			}
		}
		return true, nil
	}
	// init's states, the snapshot's the trace starts from (none: a
	// snapshot that is no state of the model, a conformance failure), or
	// a resume's, each the origin of what it reaches
	var frontier []qeval.State
	if h.starts != nil {
		frontier = h.starts
	} else if h.plan.Start == nil {
		f, err := h.e.m.Successors(h.consts, nil, h.e.em.Init)
		if err != nil {
			return hiddenOutcome{}, err
		}
		frontier = f
	}
	if h.starts == nil && h.plan.Start != nil {
		f, failed, err := h.e.start(h.c, h.consts, h.plan)
		if err != nil {
			return hiddenOutcome{}, err
		}
		if failed {
			return hiddenOutcome{at: -1, start: true}, nil
		}
		frontier = f
	}
	// each frontier state's start (h.starts' index; -1 without them): the
	// first that reached it at this step, which the rest of the trace may
	// take from it as from any other (what follows depends on the state
	// alone). Kept per step, never across steps: a state that recurs is
	// another start's at another step.
	origins := make([]int, len(frontier))
	for i := range origins {
		origins[i] = -1
		if h.starts != nil {
			origins[i] = i
		}
	}
	frontier, origins, err := filterWith(frontier, origins, ok)
	if err != nil {
		return hiddenOutcome{}, err
	}
	if len(frontier) == 0 {
		return hiddenOutcome{at: 0, initial: true}, nil
	}
	cut := false // some closure so far was ended by the depth: a failure is no verdict
	for i, call := range h.calls {
		closure, from, capped, c, err := h.close(frontier, origins, ok)
		if err != nil || capped {
			return hiddenOutcome{at: i, capped: capped}, err
		}
		cut = cut || c
		var args []qeval.Value
		if params, ok := h.c.Model.Actions[call.Action]; ok {
			for _, p := range params {
				v, err := h.e.literal(call.Args[p.Name])
				if err != nil {
					return hiddenOutcome{}, err
				}
				args = append(args, v)
			}
		}
		var xs []qeval.Value // the expect's arguments, once
		if call.Expect != "" {
			if call.ExpectArgs == nil {
				return hiddenOutcome{}, &qeval.Unsupported{Op: "expect", Why: call.Action + "'s expect is not text and fields"}
			}
			for _, a := range call.ExpectArgs {
				v, err := h.e.literal(a)
				if err != nil {
					return hiddenOutcome{}, err
				}
				xs = append(xs, v)
			}
		}
		var next []qeval.State
		var nextFrom []int
		seen, enabled := map[string]bool{}, false
		if hooks.TamperEval.Load() == 4 {
			closure = nil // a wrong evaluator: no state takes a step
		}
		for j, s := range closure {
			succ, err := h.e.m.Successors(h.consts, s, h.e.em.Actions[call.Action], args...)
			if err != nil {
				return hiddenOutcome{}, err
			}
			for _, s2 := range succ {
				enabled = true
				if call.Expect != "" && hooks.TamperEval.Load() != 5 {
					v, err := h.e.m.Eval(h.consts, s2, h.e.em.Expects[call.Action], xs...)
					if err != nil {
						return hiddenOutcome{}, err
					}
					if v != qeval.Bool(true) {
						continue
					}
				}
				if keepIt, err := ok(s2); err != nil {
					return hiddenOutcome{}, err
				} else if !keepIt {
					continue
				}
				if k := h.e.m.Key(s2); !seen[k] {
					seen[k] = true
					next, nextFrom = append(next, s2), append(nextFrom, from[j])
				}
			}
		}
		if len(next) == 0 {
			return hiddenOutcome{at: i, disabled: !enabled, cut: cut}, nil
		}
		frontier, origins = next, nextFrom
	}
	final, from, capped, c, err := h.close(frontier, origins, ok)
	return hiddenOutcome{at: -1, states: final, origins: from, final: len(final), capped: capped, cut: cut || c}, err
}

// close is states with every state the hidden actions reach from them in
// at most the depth of steps, that keep ok; capped if they passed the cap,
// cut if one more hidden action reaches a state they do not hold (the
// depth, not the model, ended the closure).
func (h *hiddenSearch) close(states []qeval.State, origins []int, ok func(qeval.State) (bool, error)) (all []qeval.State, from []int, capped, cut bool, err error) {
	seen := map[string]bool{}
	for i, s := range states {
		if k := h.e.m.Key(s); !seen[k] {
			seen[k] = true
			all, from = append(all, s), append(from, origins[i])
		}
	}
	layer, layerFrom := all, from
	for d := 0; d <= h.depth && len(layer) > 0; d++ {
		var next []qeval.State
		var nextFrom []int
		for j, s := range layer {
			for _, a := range h.c.Binding.Hidden {
				succ, err := h.e.m.Successors(h.consts, s, h.e.em.Actions[a])
				if err != nil {
					return nil, nil, false, false, err
				}
				for _, s2 := range succ {
					if k := h.e.m.Key(s2); !seen[k] {
						keep, err := ok(s2)
						if err != nil {
							return nil, nil, false, false, err
						}
						seen[k] = true
						if !keep {
							continue
						}
						if d == h.depth {
							return all, from, false, true, nil // a state one past the depth: cut
						}
						all, from = append(all, s2), append(from, layerFrom[j])
						next, nextFrom = append(next, s2), append(nextFrom, layerFrom[j])
						if len(all) > hiddenCap {
							return nil, nil, true, false, nil
						}
					}
				}
			}
		}
		layer, layerFrom = next, nextFrom
	}
	return all, from, false, false, nil
}

// eventually reports whether ev holds in some of states.
func (h *hiddenSearch) eventually(states []qeval.State, ev string) (bool, error) {
	for _, s := range states {
		v, err := h.e.m.Eval(h.consts, s, h.e.em.Eventually[ev])
		if err != nil {
			return false, err
		}
		if v == qeval.Bool(true) {
			return true, nil
		}
	}
	return false, nil
}

// failure is the step no path takes, as a report says it.
func (h *hiddenSearch) failure(o hiddenOutcome) *Failure {
	call := h.calls[o.at]
	f := &Failure{Index: o.at, Call: call.Action, Step: call.Step, Expect: call.Expect, ExpectTemplate: call.ExpectTemplate}
	switch {
	case len(h.c.Binding.Hidden) == 0 && o.disabled: // a resume's search
		f.Kind, f.Quint = "disabled", fmt.Sprintf("from no state %s admits do the steps before it reach a state that enables %s", h.c.Binding.Resume, call.Action)
	case len(h.c.Binding.Hidden) == 0:
		f.Kind, f.Quint = "state", fmt.Sprintf("%s is enabled, but from no state %s admits does its expect hold after it", call.Action, h.c.Binding.Resume)
	case o.disabled:
		f.Kind, f.Quint = "disabled", fmt.Sprintf("no state the hidden actions reach (at most %d since the step before) enables %s", h.depth, call.Action)
	default:
		f.Kind, f.Quint = "state", fmt.Sprintf("%s is enabled, but its expect holds after it in no state the hidden actions reach", call.Action)
	}
	return f
}

// filter keeps the states ok keeps.
func filter(states []qeval.State, ok func(qeval.State) (bool, error)) ([]qeval.State, error) {
	var out []qeval.State
	for _, s := range states {
		keep, err := ok(s)
		if err != nil {
			return nil, err
		}
		if keep {
			out = append(out, s)
		}
	}
	return out, nil
}

// witness is the start of a path that passes every check: o's first
// final state in which, of a settled trace, every eventual val holds
// (eventual reports which); -1 with no starts.
func (h *hiddenSearch) witness(o hiddenOutcome, settled bool) (int, error) {
	if h.starts == nil {
		return -1, nil
	}
	for i, s := range o.states {
		if ok, err := h.eventualAll(s, settled); err != nil {
			return -1, err
		} else if ok {
			return o.origins[i], nil
		}
	}
	return -1, fmt.Errorf("the search over the states %s admits passed with no witness: a bug of the search (validate's hiddenSearch)", h.c.Binding.Resume)
}

// eventualAll reports whether every eventual val holds in s, of a settled
// trace (of another, none is judged).
func (h *hiddenSearch) eventualAll(s qeval.State, settled bool) (bool, error) {
	if !settled {
		return true, nil
	}
	for _, ev := range h.c.Binding.Eventually {
		v, err := h.e.m.Eval(h.consts, s, h.e.em.Eventually[ev])
		if err != nil {
			return false, err
		}
		if v != qeval.Bool(true) {
			return false, nil
		}
	}
	return true, nil
}

// filterWith keeps the states ok keeps, with their origins.
func filterWith(states []qeval.State, origins []int, ok func(qeval.State) (bool, error)) ([]qeval.State, []int, error) {
	var out []qeval.State
	var from []int
	for i, s := range states {
		keep, err := ok(s)
		if err != nil {
			return nil, nil, err
		}
		if keep {
			out, from = append(out, s), append(from, origins[i])
		}
	}
	return out, from, nil
}

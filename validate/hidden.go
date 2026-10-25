package validate

import (
	"fmt"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
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
// path takes every step; invariants hold if some path also keeps each;
// eventual vals of a settled trace hold if some final state, after hidden
// actions too, has them. It is the Go evaluator's search (quint cannot
// replay a step whose choices are unknown), and says so; an evaluator
// that cannot evaluate the model is an error, never a verdict.
func (c *Checker) checkHidden(tr *qtrace.Trace) (*Result, error) {
	plan, err := c.Binding.Plan(c.Model, tr)
	if err != nil {
		return nil, err
	}
	res := newResult(c.Binding, tr, plan)
	res.Calls, res.Steps = plan.Calls, len(plan.Calls)
	e := evaluatorFor(c)
	if e.err != nil {
		return nil, fmt.Errorf("hidden actions (%s) are searched by the Go evaluator, which has none for this binding: %v",
			strings.Join(c.Binding.Hidden, ", "), e.err)
	}
	consts := map[string]qeval.Value{}
	for k, name := range e.em.Consts {
		v, err := e.literal(plan.Constants[k])
		if err != nil {
			return nil, err
		}
		consts[name] = v
	}
	h := &hiddenSearch{c: c, e: e, consts: consts, calls: plan.Calls, depth: c.Binding.HiddenDepth}
	if h.depth == 0 {
		h.depth = DefaultHiddenDepth
	}
	out, err := h.run(nil)
	if err != nil {
		return nil, err
	}
	note := fmt.Sprintf("judged by the Go evaluator's search over hidden actions (%s, at most %d between steps): quint cannot replay steps whose choices are unknown",
		strings.Join(c.Binding.Hidden, ", "), h.depth)
	res.Issues = append(res.Issues, qtrace.Issue{Severity: "info", Kind: "hidden", Msg: note})
	switch {
	case out.capped:
		res.Conforms = NotEvaluated
		res.Issues = append(res.Issues, qtrace.Issue{Severity: "warn", Kind: "hidden",
			Msg: fmt.Sprintf("no verdict: the states the hidden actions reach passed %d at step %d; lower hiddenDepth, or record more of the actions", hiddenCap, out.at+1)})
		return res, nil
	case out.at >= 0:
		res.Conforms = Fail
		res.Failure = h.failure(out)
		res.Issues = append(res.Issues, qtrace.Issue{Severity: "info", Kind: "hidden", Msg: res.Failure.Quint})
		for _, inv := range c.Binding.Invariants {
			res.Invariants = append(res.Invariants, InvariantResult{Name: inv, Verdict: NotEvaluated})
		}
		return res, nil
	}
	res.Conforms = Pass
	for _, inv := range c.Binding.Invariants {
		o, err := h.run([]string{inv})
		if err != nil {
			return nil, err
		}
		ir := InvariantResult{Name: inv, Verdict: Pass}
		switch {
		case o.capped:
			ir.Verdict = NotEvaluated
		case o.at >= 0 || o.final == 0:
			ir.Verdict = Fail
			step := -1
			if o.at >= 0 {
				step = o.at
			}
			ir.Failure = &Failure{Kind: "invariant", Index: step, Note: fmt.Sprintf("every path the hidden actions allow violates %s by step %d", inv, max(step, 0)+1)}
			if step >= 0 {
				ir.Failure.Step = plan.Calls[step].Step
			}
		}
		res.Invariants = append(res.Invariants, ir)
	}
	if tr.Settled {
		for _, ev := range c.Binding.Eventually {
			ok, err := h.eventually(out.states, ev)
			if err != nil {
				return nil, err
			}
			ir := InvariantResult{Name: EventuallyName(ev), Verdict: Pass, Eventually: true}
			if !ok {
				ir.Verdict = Fail
				ir.Failure = &Failure{Kind: "invariant", Index: len(plan.Calls) - 1, Note: fmt.Sprintf("%s holds in no state the run may have settled in, hidden actions after its last step included", ev)}
			}
			res.Invariants = append(res.Invariants, ir)
		}
	}
	for _, w := range c.Binding.Within {
		res.Invariants = append(res.Invariants, InvariantResult{Name: w.Name(), Verdict: NotEvaluated, Within: true})
	}
	if len(c.Binding.Within) > 0 {
		res.Issues = append(res.Issues, qtrace.Issue{Severity: "warn", Kind: "within", Msg: "bounded-response obligations not judged: a trace with hidden actions has no one replay to time"})
	}
	return res, nil
}

// hiddenSearch is one search over a trace's states.
type hiddenSearch struct {
	c      *Checker
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
	disabled bool
	states   []qeval.State
	final    int
	capped   bool
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
	frontier, err := h.e.m.Successors(h.consts, nil, h.e.em.Init)
	if err != nil {
		return hiddenOutcome{}, err
	}
	if frontier, err = filter(frontier, ok); err != nil {
		return hiddenOutcome{}, err
	}
	for i, call := range h.calls {
		closure, capped, err := h.close(frontier, ok)
		if err != nil || capped {
			return hiddenOutcome{at: i, capped: capped}, err
		}
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
		var next []qeval.State
		seen, enabled := map[string]bool{}, false
		for _, s := range closure {
			succ, err := h.e.m.Successors(h.consts, s, h.e.em.Actions[call.Action], args...)
			if err != nil {
				return hiddenOutcome{}, err
			}
			for _, s2 := range succ {
				enabled = true
				if call.Expect != "" {
					if call.ExpectArgs == nil {
						return hiddenOutcome{}, &qeval.Unsupported{Op: "expect", Why: call.Action + "'s expect is not text and fields"}
					}
					var xs []qeval.Value
					for _, a := range call.ExpectArgs {
						v, err := h.e.literal(a)
						if err != nil {
							return hiddenOutcome{}, err
						}
						xs = append(xs, v)
					}
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
					next = append(next, s2)
				}
			}
		}
		if len(next) == 0 {
			return hiddenOutcome{at: i, disabled: !enabled}, nil
		}
		frontier = next
	}
	final, capped, err := h.close(frontier, ok)
	return hiddenOutcome{at: -1, states: final, final: len(final), capped: capped}, err
}

// close is states with every state the hidden actions reach from them in
// at most the depth of steps, that keep ok; capped if they passed the cap.
func (h *hiddenSearch) close(states []qeval.State, ok func(qeval.State) (bool, error)) ([]qeval.State, bool, error) {
	seen := map[string]bool{}
	var all []qeval.State
	for _, s := range states {
		if k := h.e.m.Key(s); !seen[k] {
			seen[k] = true
			all = append(all, s)
		}
	}
	layer := all
	for range h.depth {
		var next []qeval.State
		for _, s := range layer {
			for _, a := range h.c.Binding.Hidden {
				succ, err := h.e.m.Successors(h.consts, s, h.e.em.Actions[a])
				if err != nil {
					return nil, false, err
				}
				for _, s2 := range succ {
					if k := h.e.m.Key(s2); !seen[k] {
						keep, err := ok(s2)
						if err != nil {
							return nil, false, err
						}
						seen[k] = true
						if !keep {
							continue
						}
						all = append(all, s2)
						next = append(next, s2)
						if len(all) > hiddenCap {
							return nil, true, nil
						}
					}
				}
			}
		}
		if len(next) == 0 {
			break
		}
		layer = next
	}
	return all, false, nil
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
	if o.disabled {
		f.Kind, f.Quint = "disabled", fmt.Sprintf("no state the hidden actions reach (at most %d since the step before) enables %s", h.depth, call.Action)
	} else {
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

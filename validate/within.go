package validate

import (
	"fmt"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// withinITF is c's options for a run's check: with ITF when the binding
// has bounded-response obligations, which are judged on the replay's
// states.
func (c *Checker) withinITF(o Options) Options {
	if len(c.Binding.Within) > 0 && c.within == nil && !c.shadowed {
		o.NoITF, o.VerdictsOnly = false, false
	}
	return o
}

// judgeWithin adds r's verdicts of the binding's bounded-response
// obligations (binding.Within), judged on the states quint's replay of
// tr reached (r.ITF), each timed by the step that reached it: the state
// before a trace's first step at its time. Each time After becomes true,
// Holds must hold in a state no later than Bound after: held later is a
// violation, as is never held in a trace that settled (nothing changes
// after) or that went on past the bound; an obligation still open when a
// trace that did not settle ends before its bound is no verdict (not among
// r's invariants, an info issue says so). The vals
// are the Go evaluator's to compute on quint's states: one it refuses is
// no verdict, said as an issue.
func (c *Checker) judgeWithin(tr *qtrace.Trace, r *Result) {
	if len(c.Binding.Within) == 0 || c.within != nil || c.shadowed {
		return
	}
	// not judged: no verdict among the invariants (a conforming run is not
	// failed for what was not computed), a warning that says why; an
	// exploration claiming conformance fails if no run judged them
	none := func(why string) {
		r.Issues = append(r.Issues, qtrace.Issue{Severity: "warn", Kind: "within", Msg: "bounded-response obligations not judged: " + why})
	}
	if r.Conforms != Pass {
		for _, w := range c.Binding.Within {
			// as invariants of a trace that does not conform
			r.Invariants = append(r.Invariants, InvariantResult{Name: w.Name(), Verdict: NotEvaluated, Within: true})
		}
		return
	}
	if r.ITF == "" {
		none("the replay wrote no ITF")
		return
	}
	trace, err := itf.ReadFile(r.ITF)
	if err != nil {
		none(err.Error())
		return
	}
	if len(trace.States) != len(r.Calls)+1 {
		none(fmt.Sprintf("the replay's ITF has %d states for %d calls", len(trace.States), len(r.Calls)))
		return
	}
	e := evaluatorFor(c)
	if e.err != nil {
		none("the Go evaluator, which computes their vals on quint's states, has none for the binding: " + e.err.Error())
		return
	}
	plan, err := c.Binding.Plan(c.Model, tr)
	if err != nil {
		none(err.Error())
		return
	}
	consts := map[string]qeval.Value{}
	for k, name := range e.em.Consts {
		v, err := e.literal(plan.Constants[k])
		if err != nil {
			none(err.Error())
			return
		}
		consts[name] = v
	}
	// each state's time (the initial state's, the first step's: the run's
	// start is unknown) and what reached it
	times := make([]time.Time, len(trace.States))
	steps := make([]*qtrace.Step, len(trace.States))
	names := make([]string, len(trace.States))
	names[0] = "the initial state"
	if len(tr.Steps) > 0 {
		times[0] = tr.Steps[0].Time
	}
	for k, call := range r.Calls {
		times[k+1] = times[k]
		if call.Step != nil {
			times[k+1], steps[k+1] = call.Step.Time, call.Step
			names[k+1] = fmt.Sprintf("%s (seq %d)", call.Step.Action, call.Step.Seq)
		} else {
			names[k+1] = "an inferred " + call.Action
		}
		if times[k+1].Before(times[k]) {
			// a linearized or Lamport-ordered trace: its order is not its
			// times', and a duration along it means nothing
			none(fmt.Sprintf("the steps' times go back along the order checked (%s before %s): a bound is judged on a trace in time order", names[k], names[k+1]))
			return
		}
	}
	vals := map[string][]bool{}
	for _, w := range c.Binding.Within {
		for _, v := range []string{w.After, w.Holds} {
			if vals[v] != nil {
				continue
			}
			bs := make([]bool, len(trace.States))
			for k, st := range trace.States {
				s, err := qeval.StateOf(e.m, st)
				if err == nil {
					var x qeval.Value
					if x, err = e.m.Eval(consts, s, e.em.Vals[v]); err == nil {
						bs[k] = x == qeval.Bool(true)
					}
				}
				if err != nil {
					none(fmt.Sprintf("%s in state %d: %v", v, k, err))
					return
				}
			}
			vals[v] = bs
		}
	}
	end := times[len(times)-1]
	for _, w := range c.Binding.Within {
		res := judgeRule(w, vals[w.After], vals[w.Holds], times, steps, names, end, tr.Settled)
		if res.Verdict == NotEvaluated {
			// a run cut short has work pending by right, as for eventual vals
			r.Issues = append(r.Issues, qtrace.Issue{Severity: "info", Kind: "within",
				Msg: w.Name() + ": not judged: the trace ended before its bound with the obligation open, and recorded no settle"})
			continue
		}
		r.Invariants = append(r.Invariants, res)
	}
}

// judgeRule is one obligation's verdict over the states' values of its
// vals and their times.
func judgeRule(w binding.Within, after, holds []bool, times []time.Time, steps []*qtrace.Step, names []string, end time.Time, settled bool) InvariantResult {
	res := InvariantResult{Name: w.Name(), Verdict: Pass, Within: true}
	open := false
	for k := range after {
		if !after[k] || k > 0 && after[k-1] {
			continue // no trigger here: After is false, or was true already
		}
		at := func(i int) string { return names[i] }
		met := -1
		for j := k; j < len(holds); j++ {
			if holds[j] {
				met = j
				break
			}
		}
		switch {
		case met >= 0 && times[met].Sub(times[k]) <= w.Bound:
			continue
		case met >= 0:
			res.Verdict = Fail
			res.Failure = &Failure{Kind: "within", Index: k - 1, Step: steps[k], Note: fmt.Sprintf("%s became true after %s, and %s held only after %s, %s later (bound %s)",
				w.After, at(k), w.Holds, at(met), times[met].Sub(times[k]), w.BoundText)}
			return res
		case settled || end.Sub(times[k]) > w.Bound: // at the bound, a step of the same instant may still respond
			res.Verdict = Fail
			how := "the trace settled"
			if !settled {
				how = fmt.Sprintf("the trace went on %s", end.Sub(times[k]))
			}
			res.Failure = &Failure{Kind: "within", Index: k - 1, Step: steps[k], Note: fmt.Sprintf("%s became true after %s, and %s never held after it (bound %s; %s)",
				w.After, at(k), w.Holds, w.BoundText, how)}
			return res
		default:
			open = true // the trace ended before the bound, unsettled
		}
	}
	if open {
		res.Verdict = NotEvaluated
	}
	return res
}

// Key is a run's key for a cache of its verdict against c: its steps
// (qtrace.Key), with their times when the binding has bounded-response
// obligations, judged on them (a slow run and a fast one of the same
// steps once shared a verdict). Two runs of one key get one verdict.
func (c *Checker) Key(steps []qtrace.Step) string {
	k := qtrace.Key(steps)
	if len(c.Binding.Within) == 0 || len(steps) == 0 {
		return k
	}
	var b strings.Builder
	b.WriteString(k)
	for _, s := range steps {
		fmt.Fprintf(&b, "|%d", s.Time.Sub(steps[0].Time)) // relative: runs started at other times share a verdict
	}
	return b.String()
}

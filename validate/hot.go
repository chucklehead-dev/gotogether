package validate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Liveness in runs (P6's cheap half): a run that did not settle, whose
// model states repeat with an eventual val false all the while, went
// round a cycle of the model's states that never made it true: a retry
// storm, a livelock, a requeue loop. That is evidence, not a verdict:
// the run might have left the cycle had it gone on (nothing checks the
// cycle's fairness: an action enabled throughout it a longer run would
// take), a run cut short has work pending by right, and progress the
// model abstracts away (a counter that wraps, a retry count it does not
// keep) repeats a state as a storm does. So it is reported beside the
// run's verdicts, saying what repeated, and changes none.

// A HotCycle is a run's states repeating with eventual vals false: the
// state after call From (0: the start) is the state after call To, Vals
// (the eventual vals) false in every state between, and at least one of
// Steps (calls From+1 to To, as the model's text names them) changed
// the state. Repeats counts the run's visits to that state (a visit's
// stutters one) with them false since they last held, State the state.
type HotCycle struct {
	Vals     []string
	From, To int
	Steps    []string
	Repeats  int
	State    []NamedValue
}

// String is the cycle as a report line.
func (h *HotCycle) String() string {
	steps := h.Steps
	if len(steps) > 6 {
		steps = append(slices.Clone(steps[:5]), fmt.Sprintf("... (%d more)", len(steps)-5))
	}
	return fmt.Sprintf("%s false throughout steps %d-%d, which return the model to its state after step %d (in it %d times with %s false): %s",
		strings.Join(h.Vals, ", "), h.From+1, h.To, h.From, h.Repeats, plural(len(h.Vals), "it", "them"), strings.Join(steps, "; "))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// MarkHot looks, in each actor's trace of steps that recorded no settle,
// for its model states repeating with an eventual val false all the
// while (the latest such cycle), and puts it on the actor's result in rep
// (Result.Hot): evidence for a run that did not settle, judged by
// nothing. It needs the Go evaluator: a binding it cannot replay (hidden
// actions, a resume, a construct it refuses) gets none, as does a
// checker of a history's orders (Linearize's), whose order is no run's.
func (c *Checker) MarkHot(rep *Report, steps []qtrace.Step) {
	if rep == nil || len(c.Binding.Eventually) == 0 || len(c.Binding.Hidden) > 0 || c.mode.history != nil {
		return
	}
	e := evaluatorFor(c)
	if e.err != nil {
		return
	}
	e = e.replaying()
	traces, _ := reconstruct(c.Binding, steps)
	for _, t := range traces {
		if t.Settled || c.resumes(t) {
			continue
		}
		i := slices.IndexFunc(rep.Actors, func(r *Result) bool { return r.Actor == t.Actor })
		if i < 0 {
			continue
		}
		if h, err := c.hotCycle(e, t); err == nil && h != nil {
			rep.Actors[i].Hot = h
		}
	}
}

// hotCycle is trace t's latest hot cycle, or nil: its replay's states
// (to the step the model refuses, if one does), each eventual val in
// each, then the last state entered (not stuttered in: a run of reads is
// no cycle) whose key an earlier state shares, with some val false at
// every position between.
func (c *Checker) hotCycle(e *evaluator, t *qtrace.Trace) (*HotCycle, error) {
	plan, err := c.plan(t, t)
	if err != nil {
		return nil, err
	}
	consts, err := e.constants(plan)
	if err != nil {
		return nil, err
	}
	var states []qeval.State
	if _, err := e.walk(c, consts, plan, 0, nil, false, false, func(s qeval.State) { states = append(states, s) }); err != nil {
		return nil, err
	}
	evs := c.Binding.Eventually
	// falseRun[v][p]: the positions up to p since v last held (0 where it
	// holds at p)
	falseRun := make([][]int, len(evs))
	keys := make([]string, len(states))
	for p, s := range states {
		keys[p] = e.m.Key(s)
	}
	for k, ev := range evs {
		falseRun[k] = make([]int, len(states))
		for p, s := range states {
			x, err := e.m.Eval(consts, s, e.em.Eventually[ev])
			if err != nil {
				return nil, err
			}
			if x != qeval.Bool(true) {
				falseRun[k][p] = 1
				if p > 0 {
					falseRun[k][p] += falseRun[k][p-1]
				}
			}
		}
	}
	last := map[string][]int{}
	for p, k := range keys {
		last[k] = append(last[k], p)
	}
	for j := len(states) - 1; j > 0; j-- {
		if keys[j] == keys[j-1] {
			continue // a stutter (a read) continues the visit: a cycle ends where it starts
		}
		ps := last[keys[j]]
		// the latest earlier position of the same state (a change between:
		// j's state is not its predecessor's)
		for x := slices.Index(ps, j) - 1; x >= 0; x-- {
			i := ps[x]
			var vals []string
			for k, ev := range evs {
				if falseRun[k][j] > j-i {
					vals = append(vals, ev)
				}
			}
			if len(vals) == 0 {
				break // an earlier one has a true val between too
			}
			h := &HotCycle{Vals: vals, From: i, To: j}
			for _, call := range plan.Calls[i:j] {
				h.Steps = append(h.Steps, call.Quint)
			}
			// the visits to it since the vals were last true (a visit's
			// stutters one)
			for _, p := range ps[:x+2] {
				if p > 0 && keys[p] == keys[p-1] {
					continue
				}
				hot := true
				for _, v := range vals {
					hot = hot && falseRun[slices.Index(evs, v)][j] > j-p
				}
				if hot {
					h.Repeats++
				}
			}
			for _, v := range e.m.Vars {
				h.State = append(h.State, NamedValue{Name: shortName(v), Value: qeval.String(states[j][v])})
			}
			return h, nil
		}
	}
	return nil, nil
}

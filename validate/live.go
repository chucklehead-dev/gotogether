package validate

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
)

// Liveness as cycles (P6): each of a binding's eventual vals over the
// state graph Checker.Graph explored. A fair cycle of states where the
// val is false, reachable from init, is a behaviour of the model that
// never makes it true: "can stay hot forever", a lasso of the shortest
// path to the cycle's component and a cycle in it. Fairness is weak, per
// action of the step (its family: the branch's action, whatever its
// arguments), with stuttering: every action is weakly fair unless the
// binding's simulate.unfair or simulate.stops says otherwise (DESIGN
// §62). A lasso found in a truncated graph is still one; "no fair cycle"
// proves the val for the instance only when the graph is complete.

// An EventualVerdict is one eventual val over a state graph: Hot when a
// fair cycle keeps it false forever (Lasso the one nearest init), else
// none was found (proved absent when the graph is complete and every
// state evaluated it). HotStates are the expanded states where it is
// false; Lassos the fair components of them, Unfair those fairness rules
// out (UnfairBy the weakly fair action the nearest of them leaves
// enabled throughout and never takes). Unsupported names a construct
// the evaluator does not take, met in Unchecked states, which are then in
// no cycle (so a lasso found is real, and none found proves nothing);
// Error is the model's runtime error evaluating it, with the path to the
// state (Counterexample).
type EventualVerdict struct {
	Name           string
	Hot            bool
	Lasso          *HotLasso
	HotStates      int
	Lassos, Unfair int
	UnfairBy       string
	Unsupported    string
	Unchecked      int
	Error          string
	Counterexample *Counterexample
}

// Live says no fair cycle keeps the val false, and every state evaluated
// it: proved for the instance when the graph is complete.
func (v EventualVerdict) Live() bool {
	return !v.Hot && v.Error == "" && v.Unsupported == ""
}

// A HotLasso is a behaviour that keeps an eventual val false forever:
// Prefix, the shortest path to the cycle's first state (the val may hold
// before it), then Cycle, steps that return to that state (Repeated),
// the val false in every one, repeated forever. A Stutter has no cycle:
// no weakly fair action is enabled in that state (only unfair ones,
// those that stop, or stutters), so the model may stay there. Takes are
// the actions the cycle takes, Obliged the weakly fair ones enabled in
// every state of it (each taken: that is what makes it fair), Component
// the states of its component. Replay is the prefix and one turn of the
// cycle as a Quint run, ending in an expect that the val is false and
// the state the repeated one: quint checks that the cycle is a path of
// the model returning to its start, not that it is fair nor that the val
// stays false forever, which no quint run can.
type HotLasso struct {
	Prefix    *Counterexample
	Cycle     []GraphStep
	Stutter   bool
	Repeated  []NamedValue
	Takes     []string
	Obliged   []string
	Component int
	Replay    string
	Sampled   []int
}

// liveness judges each eventual val over the graph of ce: the families
// are the labels' branch actions (branch, "" for a transition no action
// names, which is step's own), fair as unfair and stops say.
func (c *Checker) liveness(ce *cexWriter, evs []string, branch []string, unfair, stops []string) ([]EventualVerdict, error) {
	g, e := ce.g, ce.e
	consts := map[string]qeval.Value{}
	names := map[string]int{}
	fam := qeval.Families{Of: make([]int, len(g.Labels))}
	for i, a := range branch {
		if a == "" {
			a = "step"
		}
		f, ok := names[a]
		if !ok {
			f = len(fam.Fair)
			names[a] = f
			fam.Fair = append(fam.Fair, qeval.WeakFair)
		}
		fam.Of[i] = f
	}
	famName := make([]string, len(names))
	for a, f := range names {
		famName[f] = a
	}
	for _, x := range []struct {
		list []string
		kind qeval.Fairness
	}{{unfair, qeval.Unfair}, {stops, qeval.Stops}} {
		for _, a := range x.list {
			if f, ok := names[a]; ok {
				fam.Fair[f] = x.kind
			}
		}
	}
	var out []EventualVerdict
	for _, ev := range evs {
		v := EventualVerdict{Name: ev}
		val, ok := e.em.Eventually[ev]
		if !ok {
			return nil, fmt.Errorf("graph: %s is not one of the binding's eventual vals (%s)", ev, strings.Join(c.Binding.Eventually, ", "))
		}
		hot := make([]bool, len(g.States))
		for id, s := range g.States {
			if !g.Expanded[id] {
				continue // in no cycle: its edges are unknown
			}
			x, err := e.m.Eval(consts, s, val)
			var u *qeval.Unsupported
			if errors.As(err, &u) {
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
				return nil, fmt.Errorf("graph: eventual val %s is no Boolean: %s", ev, qeval.String(x))
			}
			if !b {
				hot[id] = true
				v.HotStates++
			}
		}
		if v.Error != "" {
			out = append(out, v)
			continue
		}
		for _, comp := range g.HotComponents(hot, fam) {
			if !comp.Fair() {
				if v.Unfair++; v.UnfairBy == "" {
					v.UnfairBy = famName[comp.Missed[0]]
				}
				continue
			}
			if v.Lassos++; v.Lasso == nil {
				v.Hot, v.Lasso = true, ce.lasso(ev, comp, famName, fam)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// lasso is a fair component's lasso as a report gives it.
func (w *cexWriter) lasso(ev string, comp qeval.Component, famName []string, fam qeval.Families) *HotLasso {
	l := comp.Lasso
	entry := w.g.States[l.Entry]
	h := &HotLasso{Prefix: w.path(l.Entry, ""), Stutter: l.Cycle == nil, Repeated: w.named(entry), Component: len(comp.States)}
	for _, f := range comp.Obliged {
		h.Obliged = append(h.Obliged, famName[f])
	}
	var ts []qeval.Transition
	at := l.Entry
	for _, ed := range l.Cycle {
		ts = append(ts, qeval.Transition{Events: w.g.Labels[ed.Label], State: w.g.States[ed.To]})
		if a := famName[fam.Of[ed.Label]]; ed.To != at && !slices.Contains(h.Takes, a) {
			h.Takes = append(h.Takes, a)
		}
		at = ed.To
	}
	steps, calls, sampled := w.steps(entry, ts, len(h.Prefix.Steps))
	h.Cycle, h.Sampled = steps, append(slices.Clone(h.Prefix.Sampled), sampled...)
	// the replay: the prefix, one turn, then the val false in the state
	// the turn returns to
	conds := []string{"not(" + ev + ")"}
	for _, v := range w.e.m.Vars {
		conds = append(conds, fmt.Sprintf("%s == %s", shortName(v), qeval.String(entry[v])))
	}
	h.Replay = h.Prefix.Replay + calls + ".expect(and { " + strings.Join(conds, ", ") + " })"
	return h
}

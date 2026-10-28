package main

import (
	"fmt"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate"
)

// reportEventually prints simulate -graph's verdicts on the eventual vals
// (validate.EventualVerdict, DESIGN §62), each as the simulate section
// classifies it: in holds, a fair cycle keeping it false fails; in
// allows, one is what the model allows (the code's to leave); else it is
// reported, not failed. A val no fair cycle keeps false is live: proved
// for the instance in a complete graph, bounded in a truncated one.
// It returns the exit code its verdicts make (1 for a failure) and
// whether one was unchecked (the evaluator refused it somewhere, and no
// cycle was found).
func reportEventually(r *validate.GraphResult, holds, allows map[string]bool) (code int, unchecked bool) {
	if len(r.Eventually) == 0 {
		return 0, false
	}
	fairness := "every action weakly fair"
	var but []string
	if len(r.Unfair) > 0 {
		but = append(but, "unfair: "+strings.Join(r.Unfair, ", "))
	}
	if len(r.Stops) > 0 {
		but = append(but, "stopping: "+strings.Join(r.Stops, ", "))
	}
	if len(but) > 0 {
		fairness += " but " + strings.Join(but, "; ") + " (simulate.unfair, simulate.stops)"
	}
	fmt.Printf("  cycles  %s judged as cycles of states where each is false, %s; a stutter is no step\n", plural(len(r.Eventually), "eventual val"), fairness)
	for _, v := range r.Eventually {
		ev := v.Name
		if v.Error != "" {
			code = 1
			fmt.Printf("  FAIL    %s: evaluating it fails in a reachable state (%s): the model's own error, fix the model. The path:\n%s", ev, v.Error, cexText(v.Counterexample))
			continue
		}
		if v.Hot {
			switch {
			case holds[ev]:
				code = 1
				fmt.Printf("  FAIL    %s: the model should drive it, and a fair cycle keeps it false forever: fix the model if it must, move it to simulate.allows if it is the code's "+
					"to reach, or name the actions a run stops before it settles (simulate.stops) or need not take (simulate.unfair). The lasso:\n%s", ev, lassoText(ev, v))
			case allows[ev]:
				fmt.Printf("  allows  %s: a fair cycle keeps it false forever (the code must leave it): %s, then %s forever\n", ev, callsOf(v.Lasso.Prefix), cycleOf(v.Lasso))
			default:
				fmt.Printf("  hot     %s: a fair cycle keeps it false forever (unclassified: simulate.holds if the model must drive it, which then fails; simulate.allows if it is the code's to reach). The lasso:\n%s",
					ev, lassoText(ev, v))
			}
			continue
		}
		note := ""
		if v.Unfair > 0 {
			note = fmt.Sprintf("; fairness rules out %s (the nearest leaves %s enabled throughout, never taken)", plural(v.Unfair, "hot cycle"), v.UnfairBy)
		}
		switch {
		case v.Unsupported != "":
			unchecked = true
			fmt.Printf("  unchecked %s: the Go evaluator cannot evaluate it in %s (%s), which are in no cycle, and no fair cycle of the others keeps it false%s\n",
				ev, plural(v.Unchecked, "state"), v.Unsupported, note)
		case r.Complete && allows[ev]:
			fmt.Printf("  live    %s: no fair cycle keeps it false: the model drives it itself (the whole graph, %d states%s; it could be in simulate.holds)%s\n", ev, r.States, depthNote(r), note)
		case r.Complete:
			fmt.Printf("  live    %s: no fair cycle keeps it false (the whole graph, %d states%s)%s\n", ev, r.States, depthNote(r), note)
		default:
			fmt.Printf("  bounded %s: no fair cycle among the %d states explored keeps it false, which proves nothing past them%s\n", ev, r.States, note)
		}
	}
	return code, unchecked
}

// lassoText is a lasso as a report prints it: the path to the cycle,
// the cycle, its replay.
func lassoText(ev string, v validate.EventualVerdict) string {
	l := v.Lasso
	var b strings.Builder
	pre := cexText(l.Prefix)
	pre = pre[:strings.LastIndex(pre, "    replay: ")]
	b.WriteString(pre)
	if l.Stutter {
		fmt.Fprintf(&b, "    then nothing, forever: no weakly fair action is enabled in that state (%s false there)\n", ev)
	} else {
		fmt.Fprintf(&b, "    then this cycle, forever, %s false in each of its states (%s of a component of %d; taking %s",
			ev, plural(len(l.Cycle), "step"), l.Component, strings.Join(l.Takes, ", "))
		if len(l.Obliged) > 0 {
			fmt.Fprintf(&b, ", every weakly fair action enabled throughout it taken: %s", strings.Join(l.Obliged, ", "))
		}
		b.WriteString("):\n")
		n := len(l.Prefix.Steps)
		for i, st := range l.Cycle {
			fmt.Fprintf(&b, "    %d. %s", n+i, st.Call)
			if len(st.Picks) > 0 {
				var ps []string
				for _, p := range st.Picks {
					ps = append(ps, p.Name+" = "+p.Value)
				}
				fmt.Fprintf(&b, " choosing %s", strings.Join(ps, ", "))
			}
			var vs []string
			for _, x := range st.State {
				vs = append(vs, x.Name+" = "+x.Value)
			}
			again := ""
			if i == len(l.Cycle)-1 {
				again = fmt.Sprintf(" (step %d's state again)", n-1)
			}
			fmt.Fprintf(&b, "\n         %s%s\n", strings.Join(vs, ", "), again)
		}
	}
	fmt.Fprintf(&b, "    replay: run r = %s\n", l.Replay)
	b.WriteString("    (quint replays the path and one turn of the cycle back to its state; no quint run can check that it repeats forever, nor its fairness: the Go evaluator's search does)\n")
	if len(l.Sampled) > 0 {
		fmt.Fprintf(&b, "    (steps %v have other successors than the path's: quint's replay draws among them, so it may not reproduce the path)\n", l.Sampled)
	}
	if v.Unfair > 0 {
		fmt.Fprintf(&b, "    fairness rules out %s (the nearest leaves %s enabled throughout, never taken)\n", plural(v.Unfair, "hot cycle"), v.UnfairBy)
	}
	return b.String()
}

// cycleOf is a lasso's cycle as one line.
func cycleOf(l *validate.HotLasso) string {
	if l.Stutter {
		return "nothing (no weakly fair action enabled)"
	}
	var cs []string
	for _, st := range l.Cycle {
		cs = append(cs, st.Call)
	}
	return "(" + strings.Join(cs, ", ") + ")"
}

package validate

import (
	"fmt"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// hostStall is the cause of a bounded response's failure that a host
// stall could account for (ExcuseStalls): Reason's "no verdict (host
// stall)".
const hostStall = "host stall"

// ExcuseStalls is r with each bounded-response failure that a stall of
// the host could by itself account for made no verdict (Reason's "no
// verdict (host stall)"), as a lost tail makes a failure no verdict: the
// system may have been in time on a host that ran it. r is not changed (a
// report's verdicts are shared by every run of its steps, which need not
// have stalled).
//
// A response that came late is excused when the stalls cover at least
// its lateness (how long after its due time it took effect at the
// earliest) within the window from its trigger to the response: had the
// host run all that while, the response could have come in time. A
// stall outside the window, or shorter than the lateness (a 100ms stall
// in a response 600ms late), does not excuse it; nor does any stall
// excuse a response that never came, nor another check's failure
// (conformance, an invariant, an eventual val), which no delay explains.
// The stalls' times and the steps' are wall-clock times of one host.
func (r *Report) ExcuseStalls(stalls []qtrace.Stall) *Report {
	if r == nil || len(stalls) == 0 {
		return r
	}
	out := *r
	out.Actors = make([]*Result, len(r.Actors))
	for i, a := range r.Actors {
		out.Actors[i] = a.excuseStalls(stalls)
	}
	return &out
}

// excuseStalls is ExcuseStalls of one actor's result: a copy, if it
// excuses anything.
func (r *Result) excuseStalls(stalls []qtrace.Stall) *Result {
	var c *Result
	for _, o := range r.withins {
		if o.how != "late" {
			continue
		}
		late := o.met.Sub(o.due)
		stalled, in := qtrace.StalledWithin(stalls, o.at, o.met)
		if late <= 0 || stalled < late {
			continue
		}
		i := -1
		for k, inv := range r.Invariants {
			if inv.Name == o.name && inv.Within && inv.Verdict == Fail {
				i = k
			}
		}
		if i < 0 {
			continue
		}
		if c == nil {
			cp := *r
			cp.Invariants = append([]InvariantResult(nil), r.Invariants...)
			cp.Issues = append([]qtrace.Issue(nil), r.Issues...)
			cp.Absent = append([]Absence(nil), r.Absent...)
			c = &cp
		}
		said := make([]string, len(in))
		for k, s := range in {
			said[k] = s.String()
		}
		why := fmt.Sprintf("its response came %s late, and the host stalled %s of the %s from its trigger to it (%s): a stall the system did not make, long enough to make it late by itself",
			late.Round(time.Millisecond), stalled.Round(time.Millisecond), o.met.Sub(o.at).Round(time.Millisecond), strings.Join(said, "; "))
		c.Invariants[i].Verdict, c.Invariants[i].cause = NotEvaluated, hostStall
		if c.noVerdict == "" {
			c.noVerdict = hostStall
		}
		c.Issues = append(c.Issues, qtrace.Issue{Severity: "warn", Kind: "stall", Msg: fmt.Sprintf("%s: no verdict (host stall): %s; run it again on a quiet host", o.name, why)})
		for k := range c.Absent {
			if a := &c.Absent[k]; a.Kind == "within" && a.Name == o.name && a.Cause == Defect {
				a.Cause, a.Why = Stalled, o.note+": "+why
			}
		}
	}
	if c == nil {
		return r
	}
	return c
}

// Stalled reports whether r has no verdict for host stalls alone
// (ExcuseStalls): it conforms everywhere, nothing FAILs, its
// reconstruction has no error, and every check without a verdict is a
// bounded response a stall excused.
func (r *Report) Stalled() bool {
	if r == nil || r.Unsettled != "" {
		return false
	}
	for _, i := range r.Issues {
		if i.Severity == "error" {
			return false
		}
	}
	stalled := false
	for _, a := range r.Actors {
		if a.Conforms != Pass || len(a.Gaps) > 0 {
			return false
		}
		for _, i := range a.Issues {
			if i.Severity == "error" {
				return false
			}
		}
		for _, inv := range a.Invariants {
			switch {
			case inv.Verdict == Pass:
			case inv.Verdict == NotEvaluated && inv.cause == hostStall:
				stalled = true
			default:
				return false
			}
		}
	}
	return stalled
}

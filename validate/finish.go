package validate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A Finish is whether a run is a finished one, not a prefix of one
// (Report.Finished): what qtest.Finishes claims and `gotogether validate
// -require-settle` requires, judged here once so the two agree.
type Finish struct {
	// Verdict is Pass (finished), Fail (certainly not) or NotEvaluated (not
	// known: a lost tail may hold the settle, the journey's end or a
	// call's completion; a search out of its budget).
	Verdict Verdict
	// Reason names what is missing in a word, as the CLI's result line
	// counts it: "incomplete (pending calls)", "incomplete (no settle)",
	// "incomplete (no eventual val)", "incomplete (eventual val false)",
	// "no verdict (lost tail)", "no verdict"; or the run's own failure
	// (Result.Reason) when it does not conform. "" when finished.
	Reason string
	// Actor is the actor the reason is of ("" the whole run).
	Actor string
	// Why says what is missing and the fix.
	Why string
}

// errNoEnd is why a binding with no eventual val finishes no run.
// Read-only.
var errNoEnd = errors.New("the binding states no eventual val, so nothing says where the journey ends: add the journey's end as an eventual val on a ghost flag its last step sets (eventually: [ended])")

// Finishable is nil if b can say a run finished, else why not: a binding
// with no eventual val says nothing of where a journey ends, and a
// settled prefix of one would pass.
func Finishable(b *binding.Binding) error {
	if len(b.Eventually) == 0 {
		return errNoEnd
	}
	return nil
}

// Finished says whether the run is a finished one: it conforms, it settled,
// every eventual val was judged at the settle and holds, and every invoked
// call completed. A call left pending is unfinished whatever a search
// (ResolvePending) placed it as: its caller never got its answer. A part
// certainly missing decides (the first found is named); a run settled with
// no eventual val judged is missed next, since such a binding finishes no
// run; else a part with no verdict leaves it NotEvaluated.
func (r *Report) Finished() Finish {
	miss := func(reason, actor, why string) Finish { return Finish{Fail, reason, actor, why} }
	if r.Unsettled != "" {
		return miss("incomplete (no settle)", "", "the system did not settle ("+r.Unsettled+")")
	}
	for _, is := range r.Issues {
		if is.Severity == "error" {
			return miss("issue", "", "the steps could not be put in order ("+is.String()+")")
		}
	}
	if len(r.Actors) == 0 {
		return miss("incomplete (no settle)", "", "the run recorded no step")
	}
	var unknowns []string
	unknownActor := ""
	settled, lostTail, eventuals := false, false, 0
	for _, a := range r.Actors {
		if a.Conforms == Fail {
			return miss(a.Reason(), a.Actor, fmt.Sprintf("actor %s does not conform (%s)", a.Actor, a.Reason()))
		}
		for _, is := range a.Issues {
			if is.Severity == "error" {
				return miss("issue", a.Actor, fmt.Sprintf("actor %s's steps could not be put in order (%s)", a.Actor, is))
			}
		}
		if a.Conforms == NotEvaluated {
			unknowns, unknownActor = append(unknowns, fmt.Sprintf("actor %s's conformance has no verdict", a.Actor)), a.Actor
		}
		for _, inv := range a.Invariants {
			switch {
			case inv.Verdict == Fail && inv.Eventually:
				return miss("incomplete (eventual val false)", a.Actor, fmt.Sprintf("%s is false where actor %s settled: the run stopped short of the journey's end (a prefix of it)", inv.Name, a.Actor))
			case inv.Verdict == Fail:
				return miss("violates "+inv.Name, a.Actor, fmt.Sprintf("actor %s violates %s", a.Actor, inv.Name))
			case inv.Verdict == NotEvaluated && a.Conforms == Pass:
				unknowns, unknownActor = append(unknowns, fmt.Sprintf("%s has no verdict in actor %s", inv.Name, a.Actor)), a.Actor
			}
			if inv.Eventually {
				eventuals++
			}
		}
		for _, p := range a.Pending {
			if !inLostTail(a, p) {
				return miss("incomplete (pending calls)", a.Actor, fmt.Sprintf("call %s (process %s) was invoked and never completed: answer every call before the settle, "+
					"and record its completion (Call.Done); a call ResolvePending places is still one its caller never got the answer to", p.String(), p.Process))
			}
			unknowns, unknownActor = append(unknowns, fmt.Sprintf("call %s may have completed in a lost tail", p.String())), a.Actor
		}
		settled = settled || a.Settled
		lostTail = lostTail || len(a.Lost) > 0
	}
	switch {
	case settled && eventuals == 0:
		return miss("incomplete (no eventual val)", "", errNoEnd.Error())
	case !settled && lostTail:
		return Finish{NotEvaluated, "no verdict (lost tail)", unknownActor, strings.Join(append(unknowns,
			"a recorder lost its tail, which may hold the settle, the journey's end or a call's completion: no verdict; check a log that kept its steps (a qobs.Synced sink)"), "; ")}
	case !settled:
		return miss("incomplete (no settle)", "", "the run recorded no settle last, so its eventual vals were not judged: record qobs.Settled after the driver's calls are answered and the collector drained "+
			"(Sim.Settle in a simulation), with nothing recorded after it")
	case len(unknowns) > 0:
		return Finish{NotEvaluated, "no verdict", unknownActor, strings.Join(unknowns, "; ")}
	}
	return Finish{Verdict: Pass}
}

// inLostTail reports whether pending call p's completion may be in a lost
// tail of a's: it was invoked before the cut of some recorder of a's
// steps (a search re-sequences the caller's recorder: any lost tail is
// taken as its).
func inLostTail(a *Result, p qtrace.Step) bool {
	for _, l := range a.Lost {
		if !p.Time.After(l.At.Add(qtrace.RealTimeSlack)) {
			return true
		}
	}
	return false
}

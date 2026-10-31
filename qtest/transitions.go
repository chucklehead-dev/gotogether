package qtest

import (
	"fmt"
	"slices"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// TransitionsConform judges only whether captured transitions conform, under the
// ordering, search witnesses and assumptions disclosed by the native report.
// It does not judge invariants, settlement, completion or composition, and never
// changes the report's verdict. Use Conforms for full model and composition
// checks; Finishes also requires settlement and completed invocations.
//
// With no actors it checks every judged actor. Arguments require exact Result.Actor
// identities (including "" for an unnamed actor and full partition labels), not
// globs or display aliases. Missing actors, empty evidence and unjudged transition
// verdicts are unknown: None or Not cannot turn them into a counterexample.
// Capture or validation errors taint the affected evidence. A definite failure on
// another clean selected actor still disproves the claim, unless the capture has
// a global capture or validation error. Native loss and search limitations remain in the
// report; a judged pass describes the captured history, not missing execution.
// Over runs, used alone it means All(TransitionsConform(actors...)).
func TransitionsConform(actors ...string) Expectation {
	required := slices.Clone(actors)
	slices.Sort(required)
	required = slices.Compact(required)
	desc := "conformance of captured transitions (without invariants, settlement or composition)"
	if len(required) > 0 {
		desc += fmt.Sprintf(" for exact actors %q", required)
	}
	value := func(r *validate.Report) tri { v, _ := transitionConformance(r, required); return v }
	return Expectation{desc: desc, check: func(r *validate.Report) bool { return value(r) == yes }, tri: value,
		every: true, alone: true, undecided: true, why: func(r *validate.Report) string { _, why := transitionConformance(r, required); return why }}
}

func transitionConformance(r *validate.Report, required []string) (tri, string) {
	if r == nil {
		return unknown, "no native report: collect and validate the captured transitions"
	}
	if transitionIssues(r.Issues) {
		return unknown, "global capture or validation errors: inspect the native report before judging transitions"
	}
	found := make(map[string]bool, len(required))
	selected := 0
	uncertain := ""
	for _, a := range r.Actors {
		if a == nil {
			if len(required) == 0 {
				uncertain = "missing actor result: collect and validate the captured transitions"
			}
			continue
		}
		if len(required) > 0 && !slices.Contains(required, a.Actor) {
			continue
		}
		found[a.Actor] = true
		selected++
		switch {
		case transitionIssues(a.Issues):
			uncertain = fmt.Sprintf("actor %q has capture or validation errors: inspect the native report before judging transitions", a.Actor)
		case a.Steps <= 0:
			uncertain = fmt.Sprintf("actor %q has no checked transitions: capture steps and validate them", a.Actor)
		case a.Failure != nil && a.Failure.Kind == "error":
			uncertain = fmt.Sprintf("actor %q has a transition-check error: resolve the validator error before making a conformance claim", a.Actor)
		case a.Conforms == validate.Fail:
			return no, fmt.Sprintf("actor %q has a definite transition conformance failure", a.Actor)
		case a.Conforms != validate.Pass:
			uncertain = fmt.Sprintf("actor %q has no transition verdict: inspect the native report's evidence and search limits", a.Actor)
		}
	}
	for _, actor := range required {
		if !found[actor] {
			return unknown, fmt.Sprintf("required exact actor %q was not judged: capture its steps and check the binding's actor selection", actor)
		}
	}
	if selected == 0 {
		return unknown, "no actors with checked transitions: capture steps and check the binding's actor selection"
	}
	if uncertain != "" {
		return unknown, uncertain
	}
	return yes, ""
}

func transitionIssues(issues []qtrace.Issue) bool {
	return slices.ContainsFunc(issues, func(i qtrace.Issue) bool { return i.Severity == "error" })
}

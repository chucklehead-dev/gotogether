package qtest

import (
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate"
)

// SameFailure: the run fails as r does, by each failing actor's first
// failure's kind (disabled, state, invariant, within, eventual) and name
// (the step's action, or the invariant's, the within's or the eventual
// val's), whatever the steps' seqs, ids and arguments: what a shrunk
// run must keep showing (ActionShrink.Keep's default, of the original's
// first failing rerun). A run whose actors fail so too, and others
// otherwise, still shows it; one where a failing actor of r fails
// another way, or not at all, does not, so a shrink does not drift to
// another bug its smaller sequences show. A run that cannot tell (a
// search out of its budget, a lost tail, which may hold the failure)
// has no verdict for it: not reproduced, never a pass.
func SameFailure(r *Run) Expectation {
	var want []string
	if r != nil && r.Report != nil {
		want = firstFailures(r.Report)
	}
	desc := "the same failure as a run that does not fail"
	if len(want) > 0 {
		desc = "the same failure (" + strings.Join(want, "; ") + ")"
	}
	value := func(rep *validate.Report) tri {
		switch {
		case len(want) == 0:
			return no
		case rep == nil:
			return unknown
		}
		if undecided(rep) {
			return unknown
		}
		for _, a := range rep.Actors {
			if len(a.Lost) > 0 {
				return unknown
			}
		}
		got := firstFailures(rep)
		for _, w := range want {
			i := slices.Index(got, w)
			if i < 0 {
				return no
			}
			got = slices.Delete(got, i, i+1) // two actors failing alike want two
		}
		return yes
	}
	return Expectation{desc: desc, check: func(rep *validate.Report) bool { return value(rep) == yes }, tri: value,
		why: func(rep *validate.Report) string {
			got := firstFailures(rep)
			if len(got) == 0 {
				return "it fails nowhere"
			}
			return "it fails with " + strings.Join(got, "; ")
		}}
}

// firstFailures are rep's failing actors' first failures, each as
// "kind name", sorted: the conformance failure (disabled or state, the
// step's action), else the invariant failing first in the replay (an
// invariant, a within or an eventual val). Actors are not named, since a
// rerun may name them anew (a process's random id).
func firstFailures(rep *validate.Report) []string {
	var out []string
	for _, a := range rep.Actors {
		if f := a.Failure; f != nil && a.Conforms == validate.Fail {
			out = append(out, f.Kind+" "+failedAction(f))
			continue
		}
		var first *validate.InvariantResult
		for i := range a.Invariants {
			inv := &a.Invariants[i]
			if inv.Verdict != validate.Fail {
				continue
			}
			if first == nil || at(inv) < at(first) {
				first = inv
			}
		}
		if first == nil {
			continue
		}
		kind := "invariant"
		switch {
		case first.Within:
			kind = "within"
		case first.Eventually:
			kind = "eventual"
		}
		out = append(out, kind+" "+first.Name)
	}
	slices.Sort(out)
	return out
}

// failedAction is the action of a conformance failure's step, or of its
// Quint call when it has no step (an inferred one).
func failedAction(f *validate.Failure) string {
	if f.Step != nil && f.Step.Action != "" {
		return f.Step.Action
	}
	call, _, _ := strings.Cut(f.Call, "(")
	return call
}

// at is where an invariant first fails in the replay (its call's
// index); one with no failure step sorts last.
func at(inv *validate.InvariantResult) int {
	if inv.Failure == nil {
		return int(^uint(0) >> 1)
	}
	return inv.Failure.Index
}

// signatureOf is how rep fails, its first failures together, for a
// message.
func signatureOf(rep *validate.Report) string {
	if rep == nil {
		return "no report"
	}
	if f := firstFailures(rep); len(f) > 0 {
		return strings.Join(f, "; ")
	}
	if rep.OK() {
		return "it passes"
	}
	return fmt.Sprintf("no failure of a step (%s)", reasonOf(rep))
}

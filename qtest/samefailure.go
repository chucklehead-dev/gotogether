package qtest

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/validate"
)

// SameFailure: the run fails as r does, by each failing actor's first
// failure, in the replay's order: its kind (disabled, state, invariant,
// within, eventual) and name (the step's action, with a state
// mismatch's observed fields that differ from the model's, or the
// invariant's, the within's or the eventual val's), whatever the steps'
// seqs, ids and arguments: what a shrunk run must keep showing
// (ActionShrink.Keep's default, of the original's first rerun with a
// decided failure). A run whose actors fail so too, and others
// otherwise, still shows it; one where a failing actor of r fails
// another way, or not at all, does not, so a shrink does not drift to
// another bug its smaller sequences show. A run with no verdict for a
// part the first failure is judged by (an undecided actor, a lost tail,
// a gap, an invariant of a conforming actor left unjudged, which may have
// failed first) has no verdict for it: not reproduced, never a pass.
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
		case rep == nil || unjudgedFailure(rep):
			return unknown
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
			if rep != nil && unjudgedFailure(rep) {
				return "it has no verdict for a part a failure is judged by (an undecided actor, a lost tail, dropped steps, an invariant not judged)"
			}
			got := firstFailures(rep)
			if len(got) == 0 {
				return "it fails nowhere"
			}
			return "it fails with " + strings.Join(got, "; ")
		}}
}

// unjudgedFailure reports whether rep lacks a verdict that its actors' first
// failures depend on: an actor whose conformance is undecided, a lost
// tail or a gap (either may hold an earlier failure), or a conforming actor's
// invariant, within or eventual val with no verdict (which may have
// failed first). A non-conforming actor's invariants are judged by no
// check (the replay stops at its failure): its first failure is the
// conformance failure.
func unjudgedFailure(rep *validate.Report) bool {
	if undecided(rep) {
		return true
	}
	for _, a := range rep.Actors {
		if len(a.Lost) > 0 || len(a.Gaps) > 0 {
			return true
		}
		if a.Conforms != validate.Pass {
			continue
		}
		for _, inv := range a.Invariants {
			if inv.Verdict == validate.NotEvaluated {
				return true
			}
		}
	}
	return false
}

// firstFailures are rep's failing actors' first failures, each as
// "kind name", sorted: of the conformance failure (disabled or state, the
// step's action) and the failing invariants (an invariant, a within or
// an eventual val), the one at the earliest step of the replay, the
// conformance failure on a tie. Actors are not named, since a rerun may
// name them anew (a process's random id).
func firstFailures(rep *validate.Report) []string {
	var out []string
	for _, a := range rep.Actors {
		first, at := "", 0
		take := func(f string, i int) {
			if first == "" || i < at {
				first, at = f, i
			}
		}
		if f := a.Failure; f != nil && a.Conforms == validate.Fail {
			name := f.Kind + " " + failedAction(f)
			if f.Kind == "state" {
				if m := mismatched(f); m != "" {
					name += " (" + m + ")"
				}
			}
			take(name, index(f))
		}
		for i := range a.Invariants {
			inv := &a.Invariants[i]
			if inv.Verdict != validate.Fail {
				continue
			}
			kind := "invariant"
			switch {
			case inv.Within:
				kind = "within"
			case inv.Eventually:
				kind = "eventual"
			}
			take(kind+" "+inv.Name, index(inv.Failure))
		}
		if first != "" {
			out = append(out, first)
		}
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

// index is where a failure is in the replay (its call's index); one
// with none sorts last.
func index(f *validate.Failure) int {
	if f == nil || f.Index < 0 {
		return int(^uint(0) >> 1)
	}
	return f.Index
}

// obsConjunct is one conjunct of an expect that compares a state
// variable with an observation: `balance == {{.obs.balance}}`.
var obsConjunct = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*==\s*\{\{\s*\.obs\.([A-Za-z0-9_]+)\s*\}\}\s*$`)

// mismatched names a state failure's observed fields that differ from
// the model's state after the step ("obs.balance"), for the expect's
// conjuncts that compare a variable with an observation; "" when the
// expect says no more (another shape, no state), so the failure is
// known by its action alone. A mismatch on another field of the same
// action is another failure.
func mismatched(f *validate.Failure) string {
	if f.Step == nil || f.State == nil || f.ExpectTemplate == "" {
		return ""
	}
	var fields []string
	for _, c := range conjuncts(f.ExpectTemplate) {
		m := obsConjunct.FindStringSubmatch(c)
		if m == nil {
			continue
		}
		model, ok := f.State[m[1]]
		obs, seen := f.Step.Obs[m[2]]
		if !ok || !seen {
			continue
		}
		if v, err := itf.Encode(obs); err != nil || !v.Equal(model) {
			fields = append(fields, "obs."+m[2])
		}
	}
	slices.Sort(fields)
	return strings.Join(fields, ", ")
}

// conjuncts splits a Quint boolean at its top-level `and`s (outside
// brackets and quotes).
func conjuncts(expr string) []string {
	var out []string
	depth, start := 0, 0
	inStr := false
	for i := 0; i < len(expr); i++ {
		switch c := expr[i]; {
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '(' || c == '{' || c == '[':
			depth++
		case c == ')' || c == '}' || c == ']':
			depth--
		case depth == 0 && strings.HasPrefix(expr[i:], " and "):
			out = append(out, expr[start:i])
			start = i + len(" and ")
			i = start - 1
		}
	}
	return append(out, expr[start:])
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

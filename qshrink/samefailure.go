package qshrink

import (
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// Shown is whether a rerun showed the failure a shrink keeps.
type Shown int

const (
	// Undecided: the rerun has no verdict for the failure (a lost tail, an
	// undecided actor, an invariant not judged): neither reproduced nor
	// not, never a pass. Result.Undecided counts them.
	Undecided Shown = iota
	// Reproduced: the rerun shows the failure (Result.Reproduced).
	Reproduced
	// NotReproduced: the rerun was judged and does not show it (a pass,
	// or another failure; Result.NotReproduced).
	NotReproduced
)

// A Keep is the failure each smaller sequence must still show: Shows
// judges a rerun's report, Why says how one that does not show it fails
// instead ("" if there is nothing to add), for a message. SameFailure is
// the default; qtest's expectations (Violates, Rejects, Mismatch) are
// Keeps through ShrinkActions.
type Keep interface {
	Shows(rep *validate.Report) Shown
	Why(rep *validate.Report) string
	String() string
}

// SameFailure: the run fails as rep does, by each failing actor's first
// failure, in the replay's order: its kind (disabled, state, invariant,
// within, eventual) and name (the step's action, with a state
// mismatch's observed fields that differ from the model's, or the
// invariant's, the within's or the eventual val's), whatever the steps'
// seqs, ids and arguments: what a shrunk run must keep showing (a
// shrink's default Keep, of the original's first rerun with a decided
// failure). A run whose actors fail so too, and others otherwise, still
// shows it; one where a failing actor of rep fails another way, or not
// at all, does not, so a shrink does not drift to another bug its
// smaller sequences show. A run with no verdict for a part the first
// failure is judged by (an undecided actor, a lost tail, a gap, an
// invariant of a conforming actor left unjudged, which may have failed
// first) has no verdict for it: Undecided, never a pass.
//
// A composition-only non-pass has no primary actor fingerprint and is refused.
// With both primary and composition checks, this criterion preserves the primary
// actor failure only; it does not fingerprint or preserve composition failures.
func SameFailure(rep *validate.Report) Keep {
	var want []string
	if rep != nil {
		want = firstFailures(rep)
	}
	if rep != nil && rep.Composition != nil && len(want) == 0 {
		if status, _ := rep.Composition.Problem(); status != receipt.Pass {
			panic("qshrink.SameFailure: composition-only failure: reproduce the registered System; shrinking requires a component-aware criterion")
		}
	}
	return sameFailure{want: want, primaryOnly: rep != nil && rep.Composition != nil}
}

type sameFailure struct {
	want        []string
	primaryOnly bool
}

func (s sameFailure) String() string {
	if len(s.want) == 0 {
		return "the same failure as a run that does not fail"
	}
	scope := "the same failure"
	if s.primaryOnly {
		scope = "the same primary actor failure"
	}
	return scope + " (" + strings.Join(s.want, "; ") + ")"
}

// Shows reports whether rep fails as the run SameFailure was given.
func (s sameFailure) Shows(rep *validate.Report) Shown {
	switch {
	case len(s.want) == 0:
		return NotReproduced
	case rep == nil || unjudgedFailure(rep):
		return Undecided
	}
	got := firstFailures(rep)
	for _, w := range s.want {
		i := slices.Index(got, w)
		if i < 0 {
			return NotReproduced
		}
		got = slices.Delete(got, i, i+1) // two actors failing alike want two
	}
	return Reproduced
}

// Why says how rep fails, when it does not show the failure: for a
// claim's message.
func (s sameFailure) Why(rep *validate.Report) string {
	if rep != nil && unjudgedFailure(rep) {
		return "it has no verdict for a part a failure is judged by (an undecided actor, a lost tail, dropped steps, an invariant not judged)"
	}
	var got []string
	if rep != nil {
		got = firstFailures(rep)
	}
	if len(got) == 0 && rep != nil && rep.Composition != nil {
		if status, _ := rep.Composition.Problem(); status != receipt.Pass {
			return "only a composition non-pass is present; this criterion preserves a primary actor failure"
		}
	}
	if len(got) == 0 {
		return "it fails nowhere"
	}
	return "it fails with " + strings.Join(got, "; ")
}

// unjudgedFailure reports whether rep lacks a verdict that its actors' first
// failures depend on (unjudgedActor), as validate's Reason reads them: a
// failure that stands is the actor's first unless a check with no
// verdict failed before it.
func unjudgedFailure(rep *validate.Report) bool {
	for _, a := range rep.Actors {
		if unjudgedActor(a) {
			return true
		}
	}
	return false
}

// unjudgedActor reports whether a's first failure is unknown. With no
// failure that stands (a FAIL): when its conformance is undecided, it lost
// a tail or has a gap (either may hold a failure), or it conforms and an
// invariant, within or eventual val has no verdict. With one: when a
// check with no verdict failed before it, or a conforming actor's check
// was not judged at all (it may have failed first). A failure that
// stands beside a gap or a lost tail precedes what they lost (validate
// judged it so), and a non-conforming actor's invariants are judged by
// no check (the replay stops at its failure): its first failure is the
// conformance failure.
func unjudgedActor(a *validate.Result) bool {
	at := func(f *validate.Failure) int {
		if f == nil {
			return int(^uint(0) >> 1)
		}
		return f.Index
	}
	first, failed := 0, false
	stand := func(f *validate.Failure) {
		if !failed || at(f) < first {
			first, failed = at(f), true
		}
	}
	if a.Conforms == validate.Fail && (a.Failure == nil || a.Failure.Kind != "error") {
		stand(a.Failure)
	}
	for _, inv := range a.Invariants {
		if inv.Verdict == validate.Fail && (inv.Failure == nil || inv.Failure.Kind != "error") {
			stand(inv.Failure)
		}
	}
	if !failed {
		if a.Conforms == validate.Fail && a.Failure != nil && a.Failure.Kind == "error" {
			return true
		}
		for _, i := range a.Invariants {
			if i.Verdict == validate.Fail && i.Failure != nil && i.Failure.Kind == "error" {
				return true
			}
		}
		if a.Conforms == validate.NotEvaluated || len(a.Lost) > 0 || len(a.Gaps) > 0 {
			return true
		}
		for _, inv := range a.Invariants {
			if a.Conforms == validate.Pass && inv.Verdict == validate.NotEvaluated {
				return true
			}
		}
		return false
	}
	if a.Conforms == validate.Fail && a.Failure != nil && a.Failure.Kind == "error" && at(a.Failure) < first {
		return true
	}
	if a.Conforms == validate.NotEvaluated && at(a.Failure) < first {
		return true
	}
	for _, inv := range a.Invariants {
		switch {
		case inv.Verdict == validate.Fail && inv.Failure != nil && inv.Failure.Kind == "error":
			if at(inv.Failure) < first {
				return true
			}
		case inv.Verdict != validate.NotEvaluated:
		case inv.Failure != nil && at(inv.Failure) < first:
			return true
		case inv.Failure == nil && a.Conforms == validate.Pass:
			return true
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
		if f := a.Failure; f != nil && f.Kind != "error" && a.Conforms == validate.Fail {
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
			if inv.Verdict != validate.Fail || inv.Failure != nil && inv.Failure.Kind == "error" {
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

// mismatched names a state failure's observed fields that differ from
// the model's state after the step ("obs.balance"), for the expect's
// conjuncts that compare a variable with an observation
// (binding.ObsComparisons); "" when the expect says no more (another
// shape, no state), so the failure is known by its action alone. A
// mismatch on another field of the same action is another failure.
func mismatched(f *validate.Failure) string {
	if f.Step == nil || f.State == nil || f.ExpectTemplate == "" {
		return ""
	}
	var fields []string
	for _, c := range binding.ObsComparisons(f.ExpectTemplate) {
		model, ok := f.State[c.Var]
		obs, seen := f.Step.Obs[c.Field]
		if !ok || !seen {
			continue
		}
		if v, err := itf.Encode(obs); err != nil || !v.Equal(model) {
			fields = append(fields, "obs."+c.Field)
		}
	}
	slices.Sort(fields)
	return strings.Join(slices.Compact(fields), ", ")
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
	return fmt.Sprintf("no failure of a step (%s)", rep.Reason())
}

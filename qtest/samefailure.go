package qtest

import (
	"github.com/chucklehead-dev/gotogether/qshrink"
	"github.com/chucklehead-dev/gotogether/validate"
)

// SameFailure: the run fails as r does, by each failing actor's first
// failure, in the replay's order: its kind (disabled, state, invariant,
// within, eventual) and name (the step's action, with a state
// mismatch's observed fields that differ from the model's, or the
// invariant's, the within's or the eventual val's), whatever the steps'
// seqs, ids and arguments: what a shrunk run must keep showing
// (ActionShrink.Keep's default, of the original's first rerun with a
// decided failure; qshrink.SameFailure). A run whose actors fail so too,
// and others otherwise, still shows it; one where a failing actor of r
// fails another way, or not at all, does not, so a shrink does not
// drift to another bug its smaller sequences show. A run with no verdict
// for a part the first failure is judged by (an undecided actor, a lost
// tail, a gap, an invariant of a conforming actor left unjudged, which
// may have failed first) has no verdict for it: not reproduced, never a
// pass.
//
// With registered composition checks this preserves the primary actor failure
// only. A composition-only non-pass is refused: reproduce the registered System;
// shrinking it requires a component-aware criterion.
func SameFailure(r *Run) Expectation {
	var rep *validate.Report
	if r != nil {
		rep = r.Report
	}
	return keepExpectation(qshrink.SameFailure(rep))
}

// keepExpectation is a shrink's Keep as an expectation of a run: met
// where it shows, missed where it does not, unknown without a verdict.
func keepExpectation(k qshrink.Keep) Expectation {
	value := func(rep *validate.Report) tri {
		switch k.Shows(rep) {
		case qshrink.Reproduced:
			return yes
		case qshrink.NotReproduced:
			return no
		}
		return unknown
	}
	return Expectation{desc: k.String(), check: func(rep *validate.Report) bool { return value(rep) == yes }, tri: value, why: k.Why}
}

// shrinkKeep is an expectation as a shrink's Keep: what a rerun must
// show, Unknown where the run has no verdict for it.
type shrinkKeep struct{ e Expectation }

func (k shrinkKeep) String() string { return k.e.String() }

func (k shrinkKeep) Shows(rep *validate.Report) qshrink.Shown {
	switch k.e.value(rep) {
	case yes:
		return qshrink.Reproduced
	case no:
		return qshrink.NotReproduced
	}
	return qshrink.Undecided
}

func (k shrinkKeep) Why(rep *validate.Report) string {
	if k.e.why == nil {
		return ""
	}
	return k.e.why(rep)
}

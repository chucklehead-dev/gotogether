package qtest

import "github.com/chucklehead-dev/gotogether/qshrink"

// ErrDiverged is what an ActionShrink's Rerun returns (wrapped or not)
// when it could not do the actions it was given: a click on a button the
// page does not show, a cancel of a job that was never submitted, a
// driver's reproduction that went elsewhere (qshrink.ErrDiverged). Such
// a rerun says nothing of the sequence: ShrinkActions counts it
// (ShrunkActions.Diverged), never as a pass nor as a failure.
// Read-only.
var ErrDiverged = qshrink.ErrDiverged

// Calibration sets how ShrinkActions reruns a sequence whose failure
// may not show every time (qshrink.Calibration: the original's
// baseline, each candidate's reruns from the rate's lower bound, the
// budget).
type Calibration = qshrink.Calibration

// A Rate is how often a sequence reproduced its failure: Reproduced of
// Of reruns that ran it (qshrink.Rate).
type Rate = qshrink.Rate

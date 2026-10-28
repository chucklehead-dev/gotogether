package qtest

import (
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Regression tests for review 11's silent wrong answers: each input once
// got a verdict quint does not give.

// A fold whose order matters (a round robin over a set) agreed folded
// sorted and reversed, so the Go evaluator passed a step quint's replay
// rejects: the verdict is quint's, a Mismatch, by default as under
// GOTOGETHER_EVAL=quint (F1).
func TestSoundFoldOrderIsQuints(t *testing.T) {
	RequireQuint(t)
	t.Parallel()
	steps := []qtrace.Step{{Action: "place", Recorder: "c", Seq: 1, Time: time.Unix(1, 0), Obs: map[string]any{"s2": int64(1)}}}
	run := Validate(t, "testdata/rr.yaml", steps)
	run.Expect(t, Mismatch("place"))
	if run.Report.Actors[0].JudgedBy() != "quint" {
		t.Errorf("judged by %s, want quint:\n%s", run.Report.Actors[0].JudgedBy(), run.Report.Actors[0].Summary())
	}
}

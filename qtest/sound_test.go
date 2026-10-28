package qtest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
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

// A pending inc invoked before finish: placed nowhere (the first
// resolution that conforms) it violates ok, placed before finish it
// keeps it. ResolvePending once judged the invariants on the first and
// said Violates, where with Linearize too the same history kept them
// (F3). Both now take the placement that keeps them.
func TestSoundResolveKeepsInvariants(t *testing.T) {
	RequireQuint(t)
	t.Parallel()
	at := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
	steps := []qtrace.Step{
		{Action: "inc", Recorder: "c", Seq: 1, Time: at(1), Invocation: true},
		{Action: "finish", Recorder: "c", Seq: 2, Time: at(2)},
	}
	for says, opts := range map[string][]Option{"inc() took effect first": {ResolvePending()}, "inc() at step 1": {ResolvePending(), Linearize()}} {
		run := Validate(t, "testdata/flag.yaml", steps, opts...)
		run.Expect(t, Conforms())
		if !strings.Contains(fmt.Sprint(run.Report.Issues), says) {
			t.Errorf("%d options: issues %v, want %q", len(opts), run.Report.Issues, says)
		}
	}
	// finished before inc was invoked: no placement keeps ok
	late := []qtrace.Step{
		{Action: "finish", Recorder: "c", Seq: 1, Time: at(1)},
		{Action: "inc", Recorder: "c", Seq: 2, Time: at(2), Invocation: true},
	}
	Validate(t, "testdata/flag.yaml", late, ResolvePending()).Expect(t, Violates("ok"))
}

// An action that chooses inside (any {} of four assignments, a die's
// nondet) whose result the expect reads: quint's replay draws one choice
// at random, so three validations of one input once failed at different
// steps, or passed (F2). A failure at or after the choice is no verdict
// now, its issue naming the action, the choice and the fix; a draw that
// passes stands (a path the model takes).
func TestSoundInternalChoiceIsNoVerdict(t *testing.T) {
	RequireQuint(t)
	t.Parallel()
	var flips []qtrace.Step
	for j := 1; j <= 3; j++ {
		flips = append(flips, qtrace.Step{Action: "flipit", Recorder: "c", Seq: uint64(j), Time: time.Unix(int64(j), 0), Obs: map[string]any{"x": int64(4)}})
	}
	roll := []qtrace.Step{{Action: "roll", Recorder: "c", Seq: 1, Time: time.Unix(1, 0), Obs: map[string]any{"x": int64(3)}}}
	for _, c := range []struct {
		binding string
		steps   []qtrace.Step
		choice  string
	}{{"testdata/flip.yaml", flips, "any {} of 4 enabled branches"}, {"testdata/die.yaml", roll, "nondet v"}} {
		for range 3 {
			run := Validate(t, c.binding, c.steps)
			a := run.Report.Actors[0]
			if a.Conforms == validate.Fail {
				t.Fatalf("%s: a sampled failure reported as a verdict:\n%s", c.binding, a.Summary())
			}
			if s := fmt.Sprint(a.Issues); !strings.Contains(s, c.choice) || !strings.Contains(s, "expose the choice as a parameter") {
				t.Errorf("%s: issues %s, want the choice (%s) and the fix named", c.binding, s, c.choice)
			}
		}
	}
	// a value no successor gives is a Mismatch whatever the draw
	roll[0].Obs["x"] = int64(7)
	Validate(t, "testdata/die.yaml", roll).Expect(t, Mismatch("roll"))
}

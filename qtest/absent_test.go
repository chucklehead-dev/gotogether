package qtest

import (
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Absent states why a step is missing without walking the report, and
// Undecided(Violates(w), "uncollected") that a within has no verdict
// for that reason: a production-style trace (no settle) whose leader is
// due 3s after a heal at 1s is a FAIL with its watermark past the window
// when the leader is absent, a PASS when it is present, and no verdict
// (uncollected) with its watermark before the window; a call pending
// under a watermark and no settle neither completes nor is pending.
func TestAbsentClaims(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(s int) time.Time { return time.Unix(1_800_000_000, 0).Add(time.Duration(s) * time.Second) }
	steps := func(last string, end int) []qtrace.Step {
		return []qtrace.Step{
			{Action: "crash", Recorder: "n", Seq: 1, Time: at(0)},
			{Action: "heal", Recorder: "n", Seq: 2, Time: at(1)},
			{Action: last, Recorder: "n", Seq: 3, Time: at(end)},
		}
	}
	w := "within 3s after healed: hasLeader"
	bind := "../validate/testdata/within.yaml"
	absent := Validate(t, bind, qtrace.MarkComplete(steps("tick", 2), "n", at(10)))
	absent.Expect(t, Absent(w, validate.Defect), Violates(w))
	present := Validate(t, bind, qtrace.MarkComplete(steps("elect", 2), "n", at(10)))
	present.Expect(t, Conforms())
	late := Validate(t, bind, qtrace.MarkComplete(steps("tick", 6), "n", at(3)))
	late.Expect(t, Absent(w, validate.Uncollected), Undecided(Violates(w), "uncollected"))
	Runs(absent, present, late).Expect(t, Some(Absent(w, validate.Defect)), None(Absent(w, validate.Dropped)))

	dep := func(seq uint64, balance int64) qtrace.Step {
		return qtrace.Step{Action: "deposit", Recorder: "r", Seq: seq, Time: at(int(seq)), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": balance}}
	}
	inv := qtrace.Step{Action: "withdraw", Recorder: "r", Seq: 2, Time: at(2), Invocation: true, Args: map[string]any{"amount": int64(3)}}
	funds := "../validate/testdata/funds.binding.yaml"
	collecting := Validate(t, funds, qtrace.MarkComplete([]qtrace.Step{dep(1, 5), inv, dep(3, 10)}, "r", at(3)))
	collecting.Expect(t, Absent("withdraw", validate.Uncollected), Undecided(Completes(), "not be collected yet"), Undecided(Finishes(), "still being collected"))
	whole := Validate(t, funds, []qtrace.Step{dep(1, 5), inv, dep(3, 10)})
	whole.Expect(t, Absent("withdraw", validate.Defect), Pending("withdraw"))
}

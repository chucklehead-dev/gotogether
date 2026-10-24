package qtest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A withdrawal that overlapped the deposit it depended on returned first:
// in recording order the model cannot take it, and Linearize finds the
// order the intervals allow, the deposit first.
func TestLinearizeOption(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) } // units beyond qtrace.RealTimeSlack
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "a", Seq: 1, Thread: "a", Invoked: at(0), Time: at(10),
			Args: map[string]any{"amount": int64(60)}, Obs: map[string]any{"balance": int64(60)}},
		{Action: "withdraw", Recorder: "b", Seq: 1, Thread: "b", Invoked: at(1), Time: at(5),
			Args: map[string]any{"amount": int64(50)}, Obs: map[string]any{"balance": int64(10)}},
	}
	Validate(t, "../validate/testdata/account.yaml", steps).Expect(t, Rejects("withdraw"))
	// a budget of one order checks only the recording order: no verdict,
	// which is not a rejection
	short := Validate(t, "../validate/testdata/account.yaml", steps, LinearizeBudget(1))
	if short.Report.OK() || short.Report.Actors[0].Conforms != validate.NotEvaluated || !strings.Contains(fmt.Sprint(short.Report.Issues), "LinearizeBudget(4)") {
		t.Errorf("out of budget: %s %v", short.Report.Summary(), short.Report.Issues)
	}
	if Rejects("withdraw").check(short.Report) {
		t.Error("Rejects matched a search that ran out of budget")
	}
	run := Validate(t, "../validate/testdata/account.yaml", steps, Linearize())
	run.Expect(t, Conforms())
	if run.Steps[0].Action != "deposit" {
		t.Errorf("checked in the order %v", run.Steps)
	}
	// a withdrawal that began after the deposit returned, reporting a
	// balance no order real time allows explains
	steps[1].Invoked, steps[1].Time = at(11), at(12)
	steps[1].Obs["balance"] = int64(-50)
	Validate(t, "../validate/testdata/account.yaml", steps, Linearize()).Expect(t, Mismatch("withdraw"))
}

// Steps lost from a recorder fail a linearized run as they fail one
// checked as recorded: the order checked is re-sequenced onto one thread,
// where the gap once vanished.
func TestLinearizeKeepsGaps(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "a", Seq: 1, Thread: "a", Time: time.Unix(1, 0),
			Args: map[string]any{"amount": int64(60)}, Obs: map[string]any{"balance": int64(60)}},
		{Action: "deposit", Recorder: "a", Seq: 3, Thread: "a", Time: time.Unix(3, 0),
			Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(70)}},
	}
	run := Validate(t, "../validate/testdata/account.yaml", steps, Linearize())
	if run.Report.OK() || !strings.Contains(fmt.Sprint(run.Report.Actors[0].Issues), "missing seq [2]") {
		t.Fatalf("a gap passed: %s", run.Report.Summary())
	}
}

// A linearized history keeps what is no step: a call never completed is
// still pending (Completes fails), and a settle still lets the eventual
// properties be judged, as without Linearize.
func TestLinearizeKeepsPendingAndSettle(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "a", Seq: 1, Thread: "a", Invoked: at(0), Time: at(10),
			Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(10)}},
		{Action: "withdraw", Recorder: "a", Seq: 2, Thread: "a", Invocation: true, Time: at(11),
			Args: map[string]any{"amount": int64(10)}},
		{Action: "settled", Recorder: "a", Seq: 3, Settled: true, Time: at(12)},
	}
	funds := "../validate/testdata/funds.binding.yaml"
	for _, opts := range [][]Option{nil, {Linearize()}} {
		run := Validate(t, funds, steps, opts...)
		run.Expect(t, Conforms(), Settles())
		if a := run.Report.Actors[0]; Completes().Holds(run) || len(a.Pending) != 1 || !a.Settled {
			t.Errorf("%d options: pending %v, settled %v", len(opts), a.Pending, a.Settled)
		}
	}
	// the withdrawal completed: the account is empty when the run settles
	done := append(steps[:2:2], qtrace.Step{Action: "withdraw", Recorder: "a", Seq: 3, Thread: "a", Call: 2, Time: at(13),
		Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(0)}},
		qtrace.Step{Action: "settled", Recorder: "a", Seq: 4, Settled: true, Time: at(14)})
	for _, opts := range [][]Option{nil, {Linearize()}} {
		Validate(t, funds, done, opts...).Expect(t, Completes(), Violates(validate.EventuallyName("funded")))
	}
	// with a step after the settle, the run did not end settled: nothing judged
	late := append(append([]qtrace.Step(nil), done...), qtrace.Step{Action: "deposit", Recorder: "a", Seq: 5, Thread: "a", Time: at(15),
		Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}})
	run := Validate(t, funds, late)
	run.Expect(t, Conforms())
	if Settles().Holds(run) {
		t.Error("Settles held of a run that went on after its settle")
	}
}

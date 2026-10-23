package qtest

import (
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A withdrawal that overlapped the deposit it depended on returned first:
// in recording order the model cannot take it, and Linearize finds the
// order the intervals allow, the deposit first.
func TestLinearizeOption(t *testing.T) {
	RequireQuint(t)
	at := func(ms int) time.Time { return time.Unix(0, 0).Add(time.Duration(ms) * time.Millisecond) }
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "a", Seq: 1, Thread: "a", Invoked: at(0), Time: at(10),
			Args: map[string]any{"amount": int64(60)}, Obs: map[string]any{"balance": int64(60)}},
		{Action: "withdraw", Recorder: "b", Seq: 1, Thread: "b", Invoked: at(1), Time: at(5),
			Args: map[string]any{"amount": int64(50)}, Obs: map[string]any{"balance": int64(10)}},
	}
	Validate(t, "../validate/testdata/account.yaml", steps).Expect(t, Rejects("withdraw"))
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

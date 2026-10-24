package qtest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A withdrawal left pending (its caller crashed) and a later deposit's
// balance: 12 says the withdrawal took effect before it, 15 that it never
// did, 99 that nothing explains the run. Checked as recorded, a pending
// call is no step and 12 is a mismatch.
func TestResolvePending(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
	history := func(balance int) []qtrace.Step {
		return []qtrace.Step{
			{Action: "deposit", Recorder: "c", Seq: 1, Time: at(1), Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(10)}},
			{Action: "withdraw", Recorder: "c", Seq: 2, Time: at(2), Invocation: true, Args: map[string]any{"amount": int64(3)}},
			{Action: "deposit", Recorder: "c", Seq: 3, Time: at(3), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(balance)}},
		}
	}
	const account = "../validate/testdata/account.yaml"
	Validate(t, account, history(12)).Expect(t, Mismatch("deposit"))
	for _, c := range []struct {
		balance int
		says    string
	}{
		{12, "withdraw(amount=3) took effect after step 1"},
		{15, "withdraw(amount=3) had no effect"},
	} {
		run := Validate(t, account, history(c.balance), ResolvePending())
		run.Expect(t, Conforms())
		if !strings.Contains(fmt.Sprint(run.Report.Issues), c.says) {
			t.Errorf("balance %d: issues %v, want %q", c.balance, run.Report.Issues, c.says)
		}
		if Completes().Holds(run) {
			t.Errorf("balance %d: a resolved call completed: its caller never knew", c.balance)
		}
	}
	Validate(t, account, history(99), ResolvePending()).Expect(t, Mismatch("deposit"))
}

// Calls that completed before a pending one stay paired, and the pending
// one stays pending, however the resolution re-sequences the steps (their
// call ids once named records of the new sequence: Completes held). A call
// invoked at the same instant as a step recorded before it takes effect
// only after that step: deposit(3) cannot explain a balance of 8 seen by
// the deposit(5) recorded first.
func TestResolvePendingKeepsCallsAndOrder(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
	const account = "../validate/testdata/account.yaml"
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "c", Seq: 1, Time: at(1), Invocation: true, Args: map[string]any{"amount": int64(10)}},
		{Action: "deposit", Recorder: "c", Seq: 2, Time: at(2), Call: 1, Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(10)}},
		{Action: "deposit", Recorder: "c", Seq: 3, Time: at(3), Invocation: true, Args: map[string]any{"amount": int64(5)}},
		{Action: "deposit", Recorder: "c", Seq: 4, Time: at(4), Call: 3, Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(15)}},
		{Action: "withdraw", Recorder: "c", Seq: 5, Time: at(5), Invocation: true, Args: map[string]any{"amount": int64(3)}},
	}
	run := Validate(t, account, steps, ResolvePending())
	run.Expect(t, Conforms())
	if Completes().Holds(run) || !Pending("withdraw").Holds(run) {
		t.Errorf("the pending withdrawal vanished: %s", run.Report.Summary())
	}
	same := []qtrace.Step{
		{Action: "deposit", Recorder: "c", Seq: 1, Time: at(1), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(8)}},
		{Action: "deposit", Recorder: "c", Seq: 2, Time: at(1), Invocation: true, Args: map[string]any{"amount": int64(3)}},
	}
	Validate(t, account, same, ResolvePending()).Expect(t, Mismatch("deposit"))
}

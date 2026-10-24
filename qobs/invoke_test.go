package qobs

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// An invocation is written when it happens, and its completion names it:
// read back, the completed call is one step (with its invocation's keys;
// a real-time interval only with "invoked", which would make every
// simulated run's cache key its own) and the one never completed is
// pending, no step. A second
// Done records nothing; a nil call (no recorder) is inert.
func TestInvokeAndDone(t *testing.T) {
	var buf bytes.Buffer
	sink := NewJSONLSink(&buf, PerStep)
	r := NewRecorder(sink, WithActor("acct"))
	ctx := context.Background()
	c := r.Invoke(ctx, "deposit", "amount", 5)
	c.Done(nil, "obs.balance", 5)
	c.Done(errors.New("again"))
	r.Invoke(ctx, "withdraw", "amount", 2) // its caller never returns
	var none *Call
	none.Done(nil)

	steps, err := qtrace.ReadJSONL(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 || !steps[0].Invocation || steps[1].Call != steps[0].Seq || !steps[2].Invocation {
		t.Fatalf("recorded %+v, want deposit's invocation, its completion, withdraw's invocation", steps)
	}
	traces, issues := qtrace.Reconstruct(steps)
	if len(issues) > 0 || len(traces) != 1 {
		t.Fatalf("traces %v, issues %v", traces, issues)
	}
	tr := traces[0]
	if len(tr.Steps) != 1 || tr.Steps[0].Action != "deposit" || tr.Steps[0].Args["amount"] != int64(5) ||
		tr.Steps[0].Obs["balance"] != int64(5) || !tr.Steps[0].Invoked.IsZero() || tr.Steps[0].Outcome != "ok" {
		t.Errorf("steps %+v, want deposit(amount=5) completed, with no real-time interval unless asked", tr.Steps)
	}
	if len(tr.Pending) != 1 || tr.Pending[0].Action != "withdraw" || tr.Pending[0].Args["amount"] != int64(2) {
		t.Errorf("pending %+v, want withdraw(amount=2)", tr.Pending)
	}
}

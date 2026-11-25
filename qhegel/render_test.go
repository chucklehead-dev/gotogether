package qhegel_test

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Render numbers steps only: an effect record (a double's send, recorded
// under its recorder's own process) is listed as an effect, unnumbered,
// and marks no restart; a step of another process still does.
func TestRenderEffects(t *testing.T) {
	steps := []qtrace.Step{
		{Action: "effect", Process: "rec", Seq: 1, Record: qtrace.Record{Effect: "rpc.counter.request"}, Args: map[string]any{"value": int64(1)}},
		{Action: "apply", Process: "counter", Seq: 2, Args: map[string]any{"id": int64(1)}},
		{Action: "effect", Process: "rec", Seq: 3, Record: qtrace.Record{Effect: "rpc.counter.request"}, Args: map[string]any{"value": int64(2)}},
		{Action: "apply", Process: "counter", Seq: 4, Args: map[string]any{"id": int64(2)}},
		{Action: "apply", Process: "counter2", Seq: 5, Args: map[string]any{"id": int64(3)}},
	}
	want := "     effect rpc.counter.request(value=1) at seq 1\n" +
		"  1. apply(id=1)\n" +
		"     effect rpc.counter.request(value=2) at seq 3\n" +
		"  2. apply(id=2)\n" +
		"  -- process 2 (restart)\n" +
		"  3. apply(id=3)\n"
	if got := qhegel.Render(steps); got != want {
		t.Errorf("Render\n%s\nwant\n%s", got, want)
	}
}

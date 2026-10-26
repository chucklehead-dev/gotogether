package qobs_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// qobs.Ref stands in a key's place, alone: the keys around it stay
// pairs, a parameter named ref stays an argument, an invocation's ref
// goes with its completion (whose own wins), and a log record's
// quint.ref is its step's.
func TestRef(t *testing.T) {
	mem := &qobs.MemorySink{}
	r := qobs.NewRecorder(mem)
	ctx := context.Background()
	r.Record(ctx, "submit", nil, "job", "j1", qobs.Ref("trace.jsonl:42"), "ref", "a parameter", "obs.state", "accepted")
	c := r.Invoke(ctx, "cancel", qobs.Ref("trace.jsonl:43"), "job", "j1")
	c.Done(nil, "obs.state", "cancelled")
	d := r.Invoke(ctx, "cancel", qobs.Ref("trace.jsonl:44"), "job", "j2")
	d.Done(nil, qobs.Ref("trace.jsonl:45"))
	slog.New(qobs.NewSlogHandler(nil)).InfoContext(qobs.WithRecorder(ctx, r), "ready", qobs.ActionKey, "ready", "job", "j1", qtrace.KeyRef, "shots/46.webp")
	steps := mem.Steps()
	if len(steps) != 6 {
		t.Fatalf("%d steps: %+v", len(steps), steps)
	}
	s := steps[0]
	if s.Ref != "trace.jsonl:42" || s.Args["job"] != "j1" || s.Args["ref"] != "a parameter" || s.Obs["state"] != "accepted" {
		t.Errorf("submit: %+v", s)
	}
	if steps[1].Ref != "trace.jsonl:43" || steps[2].Ref != "trace.jsonl:43" || steps[2].Obs["state"] != "cancelled" || steps[2].Args["job"] != "j1" {
		t.Errorf("cancel: %+v / %+v", steps[1], steps[2])
	}
	if steps[4].Ref != "trace.jsonl:45" {
		t.Errorf("a completion's own ref: %+v", steps[4])
	}
	if steps[5].Ref != "shots/46.webp" || steps[5].Args["job"] != "j1" || len(steps[5].Args) != 1 {
		t.Errorf("slog: %+v", steps[5])
	}
}

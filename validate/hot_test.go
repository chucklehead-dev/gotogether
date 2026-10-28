package validate

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// hotRun is a run of hot.qnt's actions, recorded by one recorder.
func hotRun(actions ...string) []qtrace.Step {
	var out []qtrace.Step
	for i, a := range actions {
		out = append(out, qtrace.Step{Action: a, Recorder: "q", Seq: uint64(i + 1), Time: atSec(i)})
	}
	return out
}

// MarkHot on runs of hot.qnt's queue that did not settle: a storm of
// retries after a submit goes round (1, false), (1, true) with drained
// false, reported with its steps (the trailing read a stutter, not part
// of it) and the run's visits to the state; reads alone are stutters, no cycle; a cycle the val is true in
// is none; a settled run gets none, its eventual vals judged instead.
// No verdict changes.
func TestMarkHot(t *testing.T) {
	skip.Quint(t)
	t.Parallel()
	c, err := NewChecker("testdata/hot.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	check := func(steps []qtrace.Step) *Result {
		t.Helper()
		rep, err := c.Check(context.Background(), steps)
		if err != nil || len(rep.Actors) != 1 {
			t.Fatalf("check: %v, %+v", err, rep)
		}
		ok := rep.OK()
		c.MarkHot(rep, steps)
		if rep.OK() != ok {
			t.Fatalf("MarkHot changed the verdict")
		}
		return rep.Actors[0]
	}
	storm := check(hotRun("submit", "retry", "read", "retry", "retry", "retry", "read"))
	h := storm.Hot
	if h == nil || !slices.Equal(h.Vals, []string{"drained"}) || h.From != 4 || h.To != 6 || h.Repeats != 3 {
		t.Fatalf("storm: %+v", h)
	}
	if !slices.Equal(h.Steps, []string{"retry", "retry"}) || !strings.Contains(storm.Summary(), "  hot cycle (evidence, no verdict: the run did not settle): drained false throughout steps 5-6") {
		t.Fatalf("storm's steps %v\n%s", h.Steps, storm.Summary())
	}
	if r := check(hotRun("submit", "read", "read", "read")); r.Hot != nil {
		t.Fatalf("reads: %+v", r.Hot)
	}
	if r := check(hotRun("submit", "process", "submit", "process")); r.Hot != nil {
		t.Fatalf("drained between: %+v", r.Hot)
	}
	settled := append(hotRun("submit", "retry", "retry"), qtrace.Step{Action: "settled", Settled: true, Recorder: "q", Seq: 4, Time: atSec(4)})
	if r := check(settled); r.Hot != nil || !r.Settled {
		t.Fatalf("settled: %+v", r)
	}
}

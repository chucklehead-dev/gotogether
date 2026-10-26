package validate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A failing step's report shows its ref, the driver's record of it, and
// the ref is no model data: runs that differ only in their refs share a
// cache key and a verdict.
func TestFailingStepShowsRef(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/account.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	run := func(ref string) []qtrace.Step {
		return []qtrace.Step{
			{Action: "deposit", Recorder: "r", Seq: 1, Time: time.Unix(1, 0), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}},
			{Action: "deposit", Recorder: "r", Seq: 2, Time: time.Unix(2, 0), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(11)}, Ref: ref},
		}
	}
	a, b := run("bombadil/trace.jsonl:42 screenshot=screenshots/42.webp"), run("bombadil/trace.jsonl:7")
	if c.Key(a) != c.Key(b) {
		t.Errorf("refs changed the key:\n%s\n%s", c.Key(a), c.Key(b))
	}
	ra, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := c.Check(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if ra.OK() || rb.OK() || ra.Actors[0].Reason() != rb.Actors[0].Reason() {
		t.Fatalf("verdicts: %q, %q\n%s", ra.Actors[0].Reason(), rb.Actors[0].Reason(), ra.Summary())
	}
	if s := ra.Summary(); !strings.Contains(s, "ref:      bombadil/trace.jsonl:42 screenshot=screenshots/42.webp") {
		t.Errorf("the failing step's ref is not shown:\n%s", s)
	}
}

package qtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// A run that does not settle, its model states going round a cycle with
// an eventual val false (hot.qnt's queue: a job submitted, then retries
// forever), has the cycle on its report: evidence beside the verdicts,
// which it changes none of (None(Settles()) as before). A run that
// settles has none.
func TestUnsettledRunsHotCycle(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	sys := func(storm bool) System {
		return System{Binding: "../validate/testdata/hot.yaml", Run: func(c qenv.Chooser) error {
			s := qenv.New(c)
			defer s.Close()
			s.Spawn("worker", func(ctx context.Context) {
				qobs.Record(ctx, "submit", nil)
				for storm {
					qobs.Record(ctx, "retry", nil)
					qenv.Sleep(ctx, time.Second)
				}
				qobs.Record(ctx, "process", nil)
			})
			return s.Settle(time.Minute)
		}}
	}
	x := sys(true).Explore(t, ExploreConfig{Seeds: 1})
	x.Expect(t, None(Settles()))
	rep := x.Runs[0].Report
	if h := rep.Actors[0].Hot; h == nil || h.Repeats < 10 || strings.Join(h.Steps, " ") != "retry retry" {
		t.Fatalf("storm: %+v\n%s", h, rep.Summary())
	}
	if !strings.Contains(rep.Summary(), "  hot cycle (evidence, no verdict: the run did not settle): drained false throughout steps") {
		t.Fatalf("storm's report:\n%s", rep.Summary())
	}
	calm := sys(false).Explore(t, ExploreConfig{Seeds: 1})
	calm.Expect(t, Conforms(), Settles())
	if h := calm.Runs[0].Report.Actors[0].Hot; h != nil {
		t.Fatalf("calm: %+v", h)
	}
}

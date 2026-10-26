package qtest

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Runs of the same steps share a verdict, whatever their refs, and each
// run's report shows its own failing step's ref: a verdict taken from
// the cache, or from another run of the batch, links to the run's own
// artifact, not to the run it was reached on.
func TestCachedVerdictKeepsTheRunsRef(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	var n atomic.Int64
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		ref := fmt.Sprintf("driver/trace.jsonl:%d", n.Add(1))
		qobs.Record(qenv.ContextOf(c), "deposit", nil, "amount", 7, "obs.balance", 8, qobs.Ref(ref))
		return nil
	}}
	for i := 0; i < 2; i++ { // the second exploration's verdicts are all the cache's
		x := sys.Explore(t, ExploreConfig{Seeds: 3})
		x.Expect(t, All(Mismatch("deposit")))
		if x.Cached < 2 {
			t.Errorf("exploration %d: %d runs judged from an earlier verdict, want at least 2", i+1, x.Cached)
		}
		for _, r := range x.Runs {
			f := r.Report.Actors[0].Failure
			if f == nil || f.Step == nil || f.Step.Ref != r.Recorded[0].Ref {
				t.Errorf("exploration %d: run %s shows ref %q, recorded %q", i+1, r.ID(), f.Step.Ref, r.Recorded[0].Ref)
			}
		}
	}
}

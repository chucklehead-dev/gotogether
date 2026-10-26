package qtest

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
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
			if f == nil || f.Step == nil || f.Step.Ref != r.Recorded[0].Ref || f.Step.Recorder != r.Recorded[0].Recorder {
				t.Errorf("exploration %d: run %s shows ref %q of recorder %s, recorded %q by %s", i+1, r.ID(), f.Step.Ref, f.Step.Recorder, r.Recorded[0].Ref, r.Recorded[0].Recorder)
			}
		}
	}
}

// A log with no seqs (all 0) still rebases each failing step onto its
// own: steps are found by what they are, in order, not by seq alone.
func TestRebaseWithoutSeqs(t *testing.T) {
	t.Parallel()
	mk := func(rec string, refs ...string) []qtrace.Step {
		var out []qtrace.Step
		for i, r := range refs {
			out = append(out, qtrace.Step{Action: "deposit", Recorder: rec, Time: time.Unix(int64(i), 0), Ref: r})
		}
		return out
	}
	from, to := mk("a", "a:1", "a:2"), mk("b", "b:1", "b:2")
	failing := from[1]
	rep := &validate.Report{Actors: []*validate.Result{{Actor: "default", Conforms: validate.Fail, Failure: &validate.Failure{Kind: "state", Step: &failing}}}}
	got := rebase(rep, from, to).Actors[0].Failure.Step
	if got.Ref != "b:2" || got.Recorder != "b" || got.Process != "b" {
		t.Errorf("rebased: %+v", got)
	}
	if rep.Actors[0].Failure.Step.Ref != "a:2" {
		t.Error("rebase modified the cached report")
	}
}

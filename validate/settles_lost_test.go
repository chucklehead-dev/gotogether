package validate

import (
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A within's settles: a lost tail bounded in time (a crash's) leaves the
// settles after its bound, an unbounded one none.
func TestSettlesAfterABoundedLoss(t *testing.T) {
	t0 := time.Unix(1000, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	steps := func(bound time.Time) []qtrace.Step {
		return []qtrace.Step{
			{Action: "inc", Recorder: "srv", Seq: 1, Time: at(0)},
			{Action: "lost", Recorder: "srv", Lost: "suffix", LostAfter: 1, Time: at(0), LostBefore: bound},
			{Action: "settled", Recorder: "h", Seq: 1, Time: at(5), Settled: true},
			{Action: "settled", Recorder: "h", Seq: 2, Time: at(20), Settled: true},
		}
	}
	if got := settlesOf(steps(at(10))); len(got) != 1 || !got[0].Time.Equal(at(20)) {
		t.Errorf("bounded at 10ms: settles %v; want the one at 20ms", got)
	}
	if got := settlesOf(steps(time.Time{})); len(got) != 0 {
		t.Errorf("unbounded: settles %v; want none", got)
	}
}

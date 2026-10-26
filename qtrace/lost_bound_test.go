package qtrace

import (
	"bytes"
	"testing"
	"time"
)

// A lost tail bounded in time (a crash's: the process was reaped by then)
// voids only a settle that a lost step may follow: a settle later than
// the bound, by more than the slack, settles the run. An unbounded loss
// voids every settle.
func TestBoundedLossVoidsOnlyEarlierSettles(t *testing.T) {
	t0 := time.Unix(1000, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	run := func(bound time.Time, settle int) bool {
		steps := []Step{
			{Action: "inc", Recorder: "srv", Seq: 1, Time: at(0)},
			{Action: "saw", Recorder: "client", Seq: 1, Time: at(1)},
			{Action: "lost", Recorder: "srv", Process: "srv", Lost: "suffix", LostAfter: 1, Time: at(0), LostBefore: bound},
			{Action: "settled", Recorder: "client", Seq: 2, Time: at(settle), Settled: true},
		}
		trs, _ := Reconstruct(steps)
		if len(trs) != 1 {
			t.Fatalf("%d traces", len(trs))
		}
		if len(trs[0].Lost) != 1 || !trs[0].Lost[0].Before.Equal(bound) {
			t.Fatalf("losses %+v; want the bounded one", trs[0].Lost)
		}
		return trs[0].Settled
	}
	if !run(at(10), 20) {
		t.Error("a settle after the crash's bound did not settle the run")
	}
	if run(at(10), 5) {
		t.Error("a settle before the crash's bound settled the run: a lost step may follow it")
	}
	if run(at(10), 10) {
		t.Error("a settle within the slack of the bound settled the run")
	}
	if run(time.Time{}, 20) {
		t.Error("an unbounded loss let a settle settle the run")
	}
	// a recorder's bounded and unbounded losses together: unbounded
	steps := []Step{
		{Action: "inc", Recorder: "srv", Seq: 1, Time: at(0)},
		{Action: "lost", Recorder: "srv", Lost: "suffix", LostAfter: 1, Time: at(0), LostBefore: at(10)},
		{Action: "lost", Recorder: "srv", Lost: "suffix", LostAfter: 1, Time: at(0)},
		{Action: "settled", Recorder: "client", Seq: 1, Time: at(20), Settled: true},
	}
	if trs, _ := Reconstruct(steps); len(trs) != 1 || trs[0].Settled || !trs[0].Lost[0].Before.IsZero() {
		t.Errorf("a bounded and an unbounded loss merged into a bounded one: %+v", trs)
	}
	// the bound survives the step log
	s := Step{Action: "lost", Recorder: "srv", Lost: "suffix", LostAfter: 1, Time: at(0), LostBefore: at(10)}
	b, err := s.MarshalLine()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadJSONL(bytes.NewReader(b))
	if err != nil || len(got) != 1 || !got[0].LostBefore.Equal(at(10)) {
		t.Fatalf("read back %+v, %v", got, err)
	}
}

// A loss's bound decides whether a settle settles, so it is in the
// verdict cache's key: two bounds, two keys; and of two bounded losses of
// one recorder the later bound is kept.
func TestLossBoundInKeyAndMerge(t *testing.T) {
	t0 := time.Unix(1000, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	key := func(before time.Time) string {
		var k Keyer
		return k.Step(&Step{Action: "lost", Recorder: "srv", Lost: "suffix", LostAfter: 1, Time: at(0), LostBefore: before})
	}
	if key(at(10)) == key(at(30)) || key(at(10)) == key(time.Time{}) {
		t.Errorf("keys %q, %q, %q: a bound is not in the key", key(at(10)), key(at(30)), key(time.Time{}))
	}
	steps := []Step{
		{Action: "inc", Recorder: "srv", Seq: 1, Time: at(0)},
		{Action: "lost", Recorder: "srv", Lost: "suffix", LostAfter: 1, Time: at(0), LostBefore: at(10)},
		{Action: "lost", Recorder: "srv", Lost: "suffix", LostAfter: 1, Time: at(0), LostBefore: at(30)},
		{Action: "settled", Recorder: "client", Seq: 1, Time: at(20), Settled: true},
	}
	if trs, _ := Reconstruct(steps); len(trs) != 1 || trs[0].Settled || !trs[0].Lost[0].Before.Equal(at(30)) {
		t.Errorf("two bounds merged into %+v; want the later (30ms), which the settle at 20ms precedes", trs[0].Lost)
	}
}

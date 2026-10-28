package qtrace

import (
	"testing"
	"time"
)

// A run's records (an invocation, a settle, a loss, a watermark) are no
// steps of its trace: Reconstruct reads each into the trace's own field,
// and every step it keeps has its Record zero, whatever the records were.
func TestTraceStepsHaveNoRecord(t *testing.T) {
	at := func(s int) time.Time { return time.Unix(int64(s), 0) }
	steps := []Step{
		{Action: "submit", Recorder: "r", Seq: 1, Time: at(1), Record: Record{Invocation: true}},
		{Action: "submit", Recorder: "r", Seq: 2, Time: at(2), Call: 1},
		{Action: "submit", Recorder: "r", Seq: 3, Time: at(3), Record: Record{Invocation: true}},
		{Action: "settled", Recorder: "r", Seq: 4, Time: at(4), Record: Record{Settled: true}},
		{Action: "complete", Recorder: "r", Seq: 5, Time: at(5), Record: Record{CompleteThrough: at(5)}},
	}
	lost := append(steps[:len(steps):len(steps)], Step{Action: "lost", Recorder: "r", Seq: 6, Time: at(6), Record: Record{Lost: "suffix", LostAfter: 6}})
	for _, c := range []struct {
		steps         []Step
		settled, lost bool
	}{{steps, true, false}, {lost, false, true}} {
		traces, _ := Reconstruct(c.steps)
		if len(traces) != 1 {
			t.Fatalf("%d traces", len(traces))
		}
		tr := traces[0]
		if len(tr.Steps) != 1 || len(tr.Pending) != 1 || tr.Settled != c.settled || (len(tr.Lost) == 1) != c.lost || len(tr.Collected) != 1 {
			t.Fatalf("steps %d, pending %d, settled %v, lost %v, collected %v", len(tr.Steps), len(tr.Pending), tr.Settled, tr.Lost, tr.Collected)
		}
		for _, s := range tr.Steps {
			if s.Record != (Record{}) || !s.Model() {
				t.Errorf("step %s has a record: %+v", s.Action, s.Record)
			}
		}
	}
}

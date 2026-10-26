package qtrace

import (
	"reflect"
	"testing"
	"time"
)

// A loss says what it lost in each of its four forms: a prefix up to a
// seq or of unknown length, a tail after a seq or of a log without seqs.
func TestLostString(t *testing.T) {
	for _, c := range []struct {
		l    Lost
		want string
	}{
		{Lost{Recorder: "r", Prefix: true, After: 2}, "recorder r lost seqs 1..2"},
		{Lost{Recorder: "r", Prefix: true}, "recorder r lost its first steps"},
		{Lost{Recorder: "r", After: 7}, "recorder r lost its tail after seq 7"},
		{Lost{Recorder: "r"}, "recorder r lost its tail"},
	} {
		if got := c.l.String(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.l, got, c.want)
		}
	}
}

// A suffix with After 0 is after the last step recorded, seqs or none:
// its record takes that step's time (a recorder whose steps carry seqs
// included).
func TestMarkLostSuffixAfterTheLastStep(t *testing.T) {
	a := Step{Action: "a", Recorder: "r", Actor: "x", Seq: 1, Time: time.Unix(5, 0)}
	b := Step{Action: "b", Recorder: "r", Actor: "x", Seq: 2, Time: time.Unix(6, 0)}
	steps := MarkLost([]Step{a, b}, Lost{Recorder: "r"})
	if rec := steps[2]; rec.Lost != "suffix" || !rec.Time.Equal(b.Time) || rec.Actor != "x" {
		t.Errorf("record %+v, want a suffix at %v of actor x", rec, b.Time)
	}
}

// A loss record's lost_after may be 0 (a prefix of unknown length, a tail
// after the last step): only a negative one is no seq.
func TestLostAfterZeroIsASeq(t *testing.T) {
	s, err := FromAttributes(time.Unix(1, 0), map[string]any{KeyAction: "lost", KeyLost: "prefix", KeyLostAfter: int64(0)})
	if err != nil || s.Lost != "prefix" || s.LostAfter != 0 {
		t.Errorf("step %+v, err %v", s, err)
	}
	if _, err := FromAttributes(time.Unix(1, 0), map[string]any{KeyAction: "lost", KeyLost: "prefix", KeyLostAfter: int64(-1)}); err == nil {
		t.Errorf("lost_after -1 accepted")
	}
}

// One loss of each kind per recorder, prefixes first: the longest prefix
// (the one its seqs show, timed by its first step, against a declared
// shorter one), the earliest cut (the declared suffix after seq 7, at its
// time, against one after seq 9). A recorder whose losses are declared
// suffix first still lists its prefix first.
func TestReconstructMergesLosses(t *testing.T) {
	at := func(s int64) time.Time { return time.Unix(s, 0) }
	step := func(rec, actor string, seq uint64, ts int64) Step {
		return Step{Action: "a", Recorder: rec, Actor: actor, Process: "p", Seq: seq, Time: at(ts), Args: map[string]any{}}
	}
	loss := func(rec, kind string, after uint64, ts int64) Step {
		return Step{Action: "lost", Recorder: rec, Process: rec, Lost: kind, LostAfter: after, Time: at(ts)}
	}
	steps := []Step{
		step("r", "x", 3, 3), step("r", "x", 4, 4),
		loss("r", "prefix", 1, 100), loss("r", "suffix", 9, 9), loss("r", "suffix", 7, 7),
		step("s", "y", 1, 1), step("s", "y", 2, 2),
		loss("s", "suffix", 0, 2), loss("s", "prefix", 0, 50),
	}
	traces, _ := Reconstruct(steps)
	got := map[string][]Lost{}
	for _, tr := range traces {
		got[tr.Actor] = tr.Lost
	}
	want := map[string][]Lost{
		"x": {{Recorder: "r", Prefix: true, After: 2, At: at(3)}, {Recorder: "r", After: 7, At: at(7)}},
		"y": {{Recorder: "s", Prefix: true, At: at(50)}, {Recorder: "s", At: at(2)}},
	}
	for a, w := range want {
		if !reflect.DeepEqual(got[a], w) {
			t.Errorf("actor %s lost %+v, want %+v", a, got[a], w)
		}
	}
}

// A recorder whose every step was lost gives its loss to the actor its
// record names (a crashed incarnation's), and a recorder with steps gives
// its loss to their actors only, not to an actor its record names.
func TestSteplessRecorderLossReachesItsActor(t *testing.T) {
	at := func(s int64) time.Time { return time.Unix(s, 0) }
	steps := []Step{
		{Action: "a", Recorder: "r", Actor: "x", Process: "p", Seq: 1, Time: at(1), Args: map[string]any{}},
		{Action: "lost", Recorder: "r", Actor: "y", Process: "p", Lost: "suffix", LostAfter: 1, Time: at(1)},
		{Action: "b", Recorder: "s", Actor: "y", Process: "q", Seq: 1, Time: at(2), Args: map[string]any{}},
		{Action: "lost", Recorder: "k", Actor: "y", Process: "q", Lost: "suffix", Time: at(3)},
	}
	traces, _ := Reconstruct(steps)
	got := map[string][]Lost{}
	for _, tr := range traces {
		got[tr.Actor] = tr.Lost
	}
	if w := []Lost{{Recorder: "r", After: 1, At: at(1)}}; !reflect.DeepEqual(got["x"], w) {
		t.Errorf("x lost %+v, want %+v", got["x"], w)
	}
	if w := []Lost{{Recorder: "k", At: at(3)}}; !reflect.DeepEqual(got["y"], w) {
		t.Errorf("y lost %+v, want %+v", got["y"], w)
	}
}

// An actor recorded by two recorders lists their losses in recorder
// order, every time (the recorders come from a map).
func TestLossesInRecorderOrder(t *testing.T) {
	steps := []Step{
		{Action: "a", Recorder: "b", Actor: "z", Process: "p", Seq: 2, Time: time.Unix(1, 0), Args: map[string]any{}},
		{Action: "a", Recorder: "a", Actor: "z", Process: "p", Seq: 2, Time: time.Unix(2, 0), Args: map[string]any{}},
	}
	for range 30 {
		traces, _ := Reconstruct(steps)
		if len(traces) != 1 || len(traces[0].Lost) != 2 || traces[0].Lost[0].Recorder != "a" || traces[0].Lost[1].Recorder != "b" {
			t.Fatalf("%+v", traces)
		}
	}
}

// A step its recorder recorded after its settle unsettles the run: the
// settle said nothing of it.
func TestStepAfterSettleUnsettles(t *testing.T) {
	steps := []Step{
		{Action: "a", Recorder: "r", Actor: "x", Process: "p", Seq: 1, Time: time.Unix(1, 0), Args: map[string]any{}},
		{Action: "settled", Recorder: "r", Actor: "x", Process: "p", Seq: 2, Settled: true, Time: time.Unix(2, 0)},
		{Action: "b", Recorder: "r", Actor: "x", Process: "p", Seq: 3, Time: time.Unix(3, 0), Args: map[string]any{}},
	}
	traces, _ := Reconstruct(steps)
	if len(traces) != 1 || traces[0].Settled {
		t.Errorf("%+v: want one trace, not settled", traces)
	}
	traces, _ = Reconstruct(steps[:2])
	if len(traces) != 1 || !traces[0].Settled {
		t.Errorf("%+v: want one trace, settled", traces)
	}
}

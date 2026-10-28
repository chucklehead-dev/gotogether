package qtrace

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// What a watermark and a gap say, each in both of its forms.
func TestWatermarkAndGapStrings(t *testing.T) {
	at := time.Unix(10, 5).UTC()
	for _, c := range []struct{ got, want string }{
		{Watermark{Recorder: "r"}.String(), "recorder r states no watermark (its log taken whole)"},
		{Watermark{Recorder: "r", Through: at}.String(), "recorder r is complete through 1970-01-01T00:00:10.000000005Z"},
		{Gap{Recorder: "r", First: 3, Last: 3}.String(), "recorder r lost seq 3"},
		{Gap{Recorder: "r", First: 3, Last: 5}.String(), "recorder r lost seqs 3..5"},
	} {
		if c.got != c.want {
			t.Errorf("%q, want %q", c.got, c.want)
		}
	}
}

// CollectedThrough: a recorder's watermark only rises; its dense seqs
// raise it to the last step before a hole, whatever order the log has
// them in; AllRecorders' raises every recorder's but one whose watermark
// a search carried; a recorder that stated none, and none was stated for
// every one, is not there.
func TestCollectedThrough(t *testing.T) {
	ms := func(n int) time.Time { return time.Unix(0, int64(n)*int64(time.Millisecond)) }
	step := func(rec string, seq uint64, at int) Step {
		return Step{Action: "x", Recorder: rec, Process: rec, Actor: "A", Seq: seq, Time: ms(at)}
	}
	steps := []Step{
		// a: seqs 1, 2 and 4, logged out of order, its watermark at 1ms:
		// complete through seq 2's time (the hole at 3 stops it)
		step("a", 2, 20), step("a", 1, 10), step("a", 4, 40),
		{Action: "complete", Recorder: "a", Time: ms(1), Record: Record{CompleteThrough: ms(1)}},
		// b: no seqs, a watermark at 50ms, then a lower one
		step("b", 0, 5),
		{Action: "complete", Recorder: "b", Time: ms(50), Record: Record{CompleteThrough: ms(50)}},
		{Action: "complete", Recorder: "b", Time: ms(30), Record: Record{CompleteThrough: ms(30)}},
		// c: no watermark of its own, AllRecorders' at 60ms
		step("c", 0, 5),
		{Action: "complete", Recorder: AllRecorders, Time: ms(60), Record: Record{CompleteThrough: ms(60)}},
		// d: a carried watermark at 2ms: neither AllRecorders' nor its seqs raise it
		step("d", 1, 5), step("d", 2, 7),
		CarryWatermark("d", "A", ms(2), false),
		// a loss record is no step
		{Recorder: "e", Record: Record{Lost: "tail"}},
	}
	got := CollectedThrough(steps)
	want := map[string]time.Time{"a": ms(60), "b": ms(60), "c": ms(60), "d": ms(2)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("with AllRecorders at 60ms: %v, want %v", got, want)
	}
	// without AllRecorders' record, each its own
	var own []Step
	for _, s := range steps {
		if s.Recorder != AllRecorders {
			own = append(own, s)
		}
	}
	got = CollectedThrough(own)
	want = map[string]time.Time{"a": ms(20), "b": ms(50), "d": ms(2)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("each its own: %v, want %v", got, want)
	}
	// a watermark below its recorder's dense seqs, which raise it
	if got := CollectedThrough([]Step{step("a", 1, 10), step("a", 2, 20), {Action: "complete", Recorder: "a", Record: Record{CompleteThrough: ms(15)}}}); !got["a"].Equal(ms(20)) {
		t.Errorf("dense seqs past the watermark: %v, want 20ms", got)
	}
	// one above them stands
	if got := CollectedThrough([]Step{step("a", 1, 10), step("a", 2, 20), {Action: "complete", Recorder: "a", Record: Record{CompleteThrough: ms(25)}}}); !got["a"].Equal(ms(25)) {
		t.Errorf("a watermark past the seqs: %v, want 25ms", got)
	}
}

// A trace's CompleteThrough is the earliest of its recorders'
// watermarks, and says whether every recorder stated one.
func TestTraceCompleteThrough(t *testing.T) {
	t3, t5 := time.Unix(3, 0), time.Unix(5, 0)
	for _, c := range []struct {
		name    string
		tr      Trace
		through time.Time
		every   bool
	}{
		{"none stated", Trace{}, time.Time{}, false},
		{"both", Trace{Collected: []Watermark{{"a", t5}, {"b", t3}}}, t3, true},
		{"both, the other order", Trace{Collected: []Watermark{{"a", t3}, {"b", t5}}}, t3, true},
		{"one whole", Trace{Collected: []Watermark{{"a", t5}, {"b", time.Time{}}}}, t5, false},
		{"carried whole", Trace{Collected: []Watermark{{"a", t5}}, carriedWhole: true}, t5, false},
	} {
		through, every := c.tr.CompleteThrough()
		if !through.Equal(c.through) || every != c.every {
			t.Errorf("%s: %v %v, want %v %v", c.name, through, every, c.through, c.every)
		}
	}
}

// Reconstruct reads watermarks into each actor's Collected (nil when
// none is stated), and times a gap by the kept steps either side.
func TestReconstructCollectedAndGaps(t *testing.T) {
	ms := func(n int) time.Time { return time.Unix(0, int64(n)*int64(time.Millisecond)) }
	steps := []Step{
		{Action: "x", Recorder: "r", Process: "r", Actor: "A", Seq: 1, Time: ms(10)},
		{Action: "x", Recorder: "r", Process: "r", Actor: "A", Seq: 3, Time: ms(30)},
	}
	traces, _ := Reconstruct(steps)
	if len(traces) != 1 || traces[0].Collected != nil {
		t.Fatalf("no watermark: collected %v", traces[0].Collected)
	}
	if want := []Gap{{Recorder: "r", First: 2, Last: 2, After: ms(10), Before: ms(30)}}; !reflect.DeepEqual(traces[0].Gaps, want) {
		t.Errorf("gaps %v, want %v", traces[0].Gaps, want)
	}
	traces, _ = Reconstruct(MarkComplete(steps, "r", ms(40)))
	if want := []Watermark{{"r", ms(40)}}; len(traces) != 1 || !reflect.DeepEqual(traces[0].Collected, want) {
		t.Errorf("a watermark at 40ms: collected %v, want %v", traces[0].Collected, want)
	}
}

// A filter's window clamps a watermark past its end to just before it,
// and leaves one inside it alone.
func TestFilterClampsWatermarks(t *testing.T) {
	until := time.Unix(100, 0)
	f := Filter{Until: until}
	for _, c := range []struct{ at, want time.Time }{
		{until.Add(time.Second), until.Add(-1)},
		{until, until.Add(-1)},
		{until.Add(-time.Second), until.Add(-time.Second)},
	} {
		// a watermark goes with a step of its recorder kept
		got := f.Apply([]Step{{Action: "x", Recorder: "r", Seq: 1, Time: until.Add(-time.Hour)}, {Action: "complete", Recorder: "r", Record: Record{CompleteThrough: c.at}}})
		if len(got) != 2 || !got[1].CompleteThrough.Equal(c.want) {
			t.Errorf("a watermark at %v: %v, want %v", c.at, got, c.want)
		}
	}
}

// A carried watermark's whole flag is in its key: a check of an order
// whose recorder stated none is another check.
func TestKeyOfCarriedWatermarks(t *testing.T) {
	var k Keyer
	at := time.Unix(1, 0)
	whole, part := CarryWatermark("r", "A", at, true), CarryWatermark("r", "A", at, false)
	plain := Step{Action: "complete", Recorder: "r", Process: "r", Actor: "A", Time: at, Record: Record{CompleteThrough: at}}
	kw, kp, kn := k.Step(&whole), k.Step(&part), k.Step(&plain)
	if kw == kp || kp == kn || kw == kn {
		t.Errorf("carried whole, carried and stated watermarks share keys: %q %q %q", kw, kp, kn)
	}
}

// A watermark attribute is a time in Unix nanoseconds, above zero.
func TestFromAttributesCompleteThrough(t *testing.T) {
	for _, c := range []struct {
		v  any
		ok bool
	}{{int64(5), true}, {int64(0), false}, {int64(-1), false}, {"soon", false}} {
		s, err := FromAttributes(time.Time{}, map[string]any{KeyAction: "complete", KeyCompleteThrough: c.v})
		if (err == nil) != c.ok {
			t.Errorf("%v: err %v", c.v, err)
		}
		if c.ok && !s.CompleteThrough.Equal(time.Unix(0, 5)) {
			t.Errorf("%v: complete through %v", c.v, s.CompleteThrough)
		}
	}
}

// A record read twice with no observations or order keys is one record
// whether a reader made their maps empty or nil; a duplicate that does
// differ is named by its action (a record without one as "a record") and
// whether it is an invocation.
func TestDuplicateRecords(t *testing.T) {
	a := Step{Action: "put", Recorder: "r", Process: "r", Actor: "A", Seq: 1, Time: time.Unix(1, 0)}
	for _, b := range []Step{
		with(a, func(s *Step) { s.Obs = map[string]any{} }),
		with(a, func(s *Step) { s.Order = map[string]int64{} }),
	} {
		traces, issues := Reconstruct([]Step{a, b})
		if len(issues) != 1 || issues[0].Severity != "info" || len(traces) != 1 || len(traces[0].Issues) != 0 {
			t.Errorf("%+v read twice: issues %v, trace issues %v", b, issues, traces[0].Issues)
		}
	}
	nameless := with(a, func(s *Step) { s.Action = "" })
	invoked := with(a, func(s *Step) { s.Invocation = true })
	traces, _ := Reconstruct([]Step{nameless, invoked})
	if len(traces) != 1 || len(traces[0].Issues) != 1 || !strings.Contains(traces[0].Issues[0].Msg, "(a record of actor A, then put invoked of actor A)") {
		t.Errorf("issues %v", traces[0].Issues)
	}
}

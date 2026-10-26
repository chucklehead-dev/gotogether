package qtrace

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// A log that starts after seq 1 lost a prefix, which is no gap: its first
// state is not init, and the issue names the fix (a snapshot step). A
// hole after its first seq is still a gap, counted from there.
func TestLostPrefixIsNoGap(t *testing.T) {
	steps := []Step{st("x", 3, "", 0), st("y", 4, "", 0), st("z", 6, "", 0)}
	other := st("w", 1, "", 0)
	other.Recorder = "s"
	traces, _ := Reconstruct(append(steps, other))
	tr := traces[0]
	kinds := map[string]Issue{}
	for _, i := range tr.Issues {
		kinds[i.Kind] = i
	}
	lp, gap := kinds["lost-prefix"], kinds["gap"]
	if lp.Severity != "error" || !strings.Contains(lp.Msg, "recorder r lost seqs 1..2") || !strings.Contains(lp.Msg, "snapshot") || !strings.Contains(lp.Msg, "resume: resumeAny") {
		t.Errorf("lost prefix: %v", tr.Issues)
	}
	if gap.Severity != "error" || !strings.Contains(gap.Msg, "[5]") || !strings.Contains(gap.Msg, "of 3..6") {
		t.Errorf("gap: %v", tr.Issues)
	}
	if len(tr.Issues) != 2 {
		t.Errorf("issues: %v", tr.Issues)
	}
	want := Lost{Recorder: "r", Prefix: true, After: 2, At: steps[0].Time}
	if len(tr.Lost) != 1 || tr.Lost[0] != want {
		t.Errorf("Lost %+v, want %+v", tr.Lost, want)
	}
	// a log from seq 1 lost nothing, its hole at 2 a gap
	traces, _ = Reconstruct([]Step{st("x", 1, "", 0), st("y", 3, "", 0)})
	if tr := traces[0]; len(tr.Lost) != 0 || len(tr.Issues) != 1 || tr.Issues[0].Kind != "gap" {
		t.Errorf("a gap at 2: lost %v, issues %v", tr.Lost, tr.Issues)
	}
}

// A torn log's last recorder lost its tail: ReadJSONL says so with a loss
// record, which Reconstruct keeps as the trace's Lost, and which leaves a
// settle recorded before the cut settling nothing (what came after it is
// unknown).
func TestReadJSONLLostTail(t *testing.T) {
	var b bytes.Buffer
	in := []Step{
		{Action: "a", Recorder: "r", Seq: 1, Actor: "x", Time: time.Unix(1, 0)},
		{Action: "settled", Recorder: "r", Seq: 2, Actor: "x", Settled: true, Time: time.Unix(2, 0)},
		{Action: "b", Recorder: "r", Seq: 3, Actor: "x", Time: time.Unix(3, 0)},
	}
	if err := WriteJSONL(&b, in); err != nil {
		t.Fatal(err)
	}
	whole := b.String()
	got, err := ReadJSONL(strings.NewReader(whole[:len(whole)-10]))
	var tt *TornTail
	if !errors.As(err, &tt) || len(got) != 3 || got[2].Lost != "suffix" || got[2].LostAfter != 2 || !got[2].Time.Equal(time.Unix(2, 0)) {
		t.Fatalf("torn: %+v, %v", got, err)
	}
	// the record survives a log written again
	var again bytes.Buffer
	if err := WriteJSONL(&again, got); err != nil {
		t.Fatal(err)
	}
	if back, err := ReadJSONL(&again); err != nil || back[2].Lost != "suffix" || back[2].LostAfter != 2 {
		t.Fatalf("read back: %+v, %v", back, err)
	}
	traces, issues := Reconstruct(got)
	tr := traces[0]
	if len(tr.Steps) != 1 || tr.Settled || len(tr.Lost) != 1 || tr.Lost[0] != (Lost{Recorder: "r", After: 2, At: time.Unix(2, 0)}) {
		t.Fatalf("steps %v settled %v lost %+v", tr.Steps, tr.Settled, tr.Lost)
	}
	if len(tr.Issues) != 0 || len(issues) != 1 || issues[0].Kind != "settle" {
		t.Errorf("issues %v, trace's %v", issues, tr.Issues)
	}
	// the whole log settles
	whole2, _ := ReadJSONL(strings.NewReader(strings.Join(strings.SplitAfter(whole, "\n")[:2], "")))
	if traces, _ := Reconstruct(whole2); !traces[0].Settled {
		t.Errorf("the log up to its settle did not settle")
	}
}

// MarkLost declares what a log cannot show: a prefix of a log without
// seqs is a lost prefix as one with them is.
func TestMarkLost(t *testing.T) {
	a := Step{Action: "a", Recorder: "r", Actor: "x", Time: time.Unix(5, 0)}
	b := Step{Action: "b", Recorder: "r", Actor: "x", Time: time.Unix(6, 0)}
	steps := MarkLost([]Step{a, b}, Lost{Recorder: "r", Prefix: true})
	if len(steps) != 3 || steps[2].Lost != "prefix" || !steps[2].Time.Equal(a.Time) || steps[2].Model() {
		t.Fatalf("marked: %+v", steps)
	}
	traces, _ := Reconstruct(steps)
	tr := traces[0]
	if len(tr.Steps) != 2 || len(tr.Issues) != 1 || tr.Issues[0].Kind != "lost-prefix" || !strings.Contains(tr.Issues[0].Msg, "lost its first steps") {
		t.Errorf("steps %v issues %v", tr.Steps, tr.Issues)
	}
	// a suffix is timed by the last step kept
	steps = MarkLost([]Step{a, b}, Lost{Recorder: "r"})
	if !steps[2].Time.Equal(b.Time) {
		t.Errorf("suffix at %v", steps[2].Time)
	}
	if Key(steps) == Key([]Step{a, b}) {
		t.Errorf("a loss record is not in the key")
	}
	// a selection of the steps keeps what they cannot show
	if kept := (Filter{Actions: []string{"b"}}).Apply(steps); len(kept) != 2 || kept[1].Lost != "suffix" {
		t.Errorf("filtered: %+v", kept)
	}
	// and drops them with the recorder whose every step it dropped
	if kept := (Filter{Actions: []string{"c"}}).Apply(steps); len(kept) != 0 {
		t.Errorf("filtered: %+v", kept)
	}
}

// A completion whose invocation record fell in a lost prefix is bounded
// by nothing before it (an interval open at its start), not an error:
// Linearize orders it where real time and the model allow.
func TestLinearizeLostInvocation(t *testing.T) {
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
	write := Step{Action: "write", Recorder: "c", Seq: 3, Call: 1, Actor: "r", Process: "p", Time: at(5), Args: map[string]any{"v": int64(1)}}
	steps := []Step{
		write,
		op("d", 1, "read", 0, 0, 1),
		op("d", 2, "read", 1, 6, 7),
	}
	traces, issues := Reconstruct(steps)
	if len(issues) != 0 || len(traces) != 1 {
		t.Fatalf("issues %v", issues)
	}
	for _, i := range traces[0].Issues {
		if i.Kind != "lost-prefix" {
			t.Errorf("issue %v", i)
		}
	}
	l, err := Linearize(traces[0].Steps, LinearizeOptions{}, func(orders [][]Step) ([]int, []string, error) { return register(orders), nil, nil })
	if err != nil || l.Verdict != Linearizable {
		t.Fatalf("%v %v", l, err)
	}
	if got := l.Order[1].Action; got != "write" {
		t.Errorf("order %v", l.Order)
	}
}

// A torn log written by several recorders loses each one's tail: the cut
// line, whose recorder it may not name, was one of theirs, and the writer
// stopped for all.
func TestReadJSONLTornSeveralRecorders(t *testing.T) {
	var b bytes.Buffer
	in := []Step{
		{Action: "a", Recorder: "r", Seq: 1, Time: time.Unix(1, 0)},
		{Action: "b", Recorder: "s", Seq: 1, Time: time.Unix(2, 0)},
		{Action: "c", Recorder: "r", Seq: 2, Time: time.Unix(3, 0)},
		{Action: "d", Recorder: "s", Seq: 2, Time: time.Unix(4, 0)},
	}
	if err := WriteJSONL(&b, in); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadJSONL(strings.NewReader(b.String()[:b.Len()-10]))
	lost := map[string]uint64{}
	for _, s := range got {
		if s.Lost == "suffix" {
			lost[s.Recorder] = s.LostAfter
		}
	}
	if len(lost) != 2 || lost["r"] != 2 || lost["s"] != 1 {
		t.Errorf("losses %v, want r after 2, s after 1", lost)
	}
}

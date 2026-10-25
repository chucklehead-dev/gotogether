package qtrace

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func st(action string, seq uint64, thread string, order int64) Step {
	s := Step{Action: action, Seq: seq, Recorder: "r", Actor: "a", Process: "p", Thread: thread,
		Time: time.Unix(0, int64(seq)), Args: map[string]any{}}
	if order > 0 {
		s.Order = map[string]int64{"batch": order}
	}
	return s
}

func actions(t *Trace) string {
	var out []string
	for _, s := range t.Steps {
		out = append(out, s.Action)
	}
	return strings.Join(out, " ")
}

// B started second (batch 2) but recorded its start first; B's table write
// is recorded before A's start. The domain clock puts A's start first, and
// program order keeps B's write after B's start.
func TestDomainClockReorders(t *testing.T) {
	steps := []Step{
		st("startB", 1, "B", 2),
		st("tableB", 2, "B", 0),
		st("startA", 3, "A", 1),
		st("tableA", 4, "A", 0),
	}
	traces, issues := Reconstruct(steps)
	if len(issues) != 0 || len(traces) != 1 || len(traces[0].Issues) != 0 {
		t.Fatalf("issues %v %v", issues, traces[0].Issues)
	}
	if got := actions(traces[0]); got != "startA startB tableB tableA" {
		t.Fatalf("order: %s", got)
	}
}

// Without thread keys, seq order is only a preference and the same input
// would put tableB before startB: program order matters.
func TestWithoutThreadsSeqIsOnlyPreferred(t *testing.T) {
	steps := []Step{st("startB", 1, "", 2), st("tableB", 2, "", 0), st("startA", 3, "", 1)}
	traces, _ := Reconstruct(steps)
	if got := actions(traces[0]); got != "tableB startA startB" {
		t.Fatalf("order: %s", got)
	}
}

func TestGapsAndDuplicates(t *testing.T) {
	steps := []Step{st("x", 1, "", 0), st("x", 1, "", 0), st("y", 3, "", 0)}
	traces, issues := Reconstruct(steps)
	if len(issues) != 1 || issues[0].Kind != "duplicate" {
		t.Fatalf("issues: %v", issues)
	}
	if len(traces[0].Issues) != 1 || traces[0].Issues[0].Kind != "gap" || !strings.Contains(traces[0].Issues[0].Msg, "[2]") {
		t.Fatalf("trace issues: %v", traces[0].Issues)
	}
}

// An incarnation that acts after its successor started (a zombie) is an
// error: replayed as restarts, the split brain once validated.
func TestProcessesAndOverlap(t *testing.T) {
	a, b, c := st("x", 1, "", 0), st("y", 2, "", 0), st("z", 3, "", 0)
	a.Process, b.Process, c.Process = "p1", "p2", "p1"
	traces, _ := Reconstruct([]Step{a, b, c})
	tr := traces[0]
	if strings.Join(tr.Processes, ",") != "p1,p2" || len(tr.Issues) != 1 || tr.Issues[0].Kind != "overlap" || tr.Issues[0].Severity != "error" {
		t.Fatalf("processes %v issues %v", tr.Processes, tr.Issues)
	}
}

func TestContradictoryKeysReportCycle(t *testing.T) {
	// program order says 1 before 2, the domain clock says 2 before 1
	steps := []Step{st("a", 1, "T", 2), st("b", 2, "T", 1)}
	traces, _ := Reconstruct(steps)
	if len(traces[0].Issues) != 1 || traces[0].Issues[0].Kind != "cycle" || len(traces[0].Steps) != 2 {
		t.Fatalf("issues %v", traces[0].Issues)
	}
}

func TestStepLogRoundTrip(t *testing.T) {
	s := st("writeTable", 3, "T", 5)
	s.Args["batch"], s.Args["gen"], s.Args["ok"] = int64(2), "g1", true
	s.Obs = map[string]any{"rows": int64(4)}
	s.Outcome, s.Error = "ambiguous", "timeout"
	var buf strings.Builder
	if err := WriteJSONL(&buf, []Step{s}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadJSONL(strings.NewReader(buf.String()))
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	g := got[0]
	if g.Action != "writeTable" || g.Seq != 3 || g.Thread != "T" || g.Order["batch"] != 5 || g.Args["batch"] != int64(2) ||
		g.Args["gen"] != "g1" || g.Args["ok"] != true || g.Obs["rows"] != int64(4) || g.Outcome != "ambiguous" ||
		g.Error != "timeout" || !g.Time.Equal(s.Time) || g.Process != "p" || g.Actor != "a" {
		t.Fatalf("round trip: %+v\n%s", g, buf.String())
	}
}

// A Lamport clock across nodes: steps with equal values are concurrent, but
// each must precede every step with a larger value, not just its neighbour.
func TestDomainClockGroups(t *testing.T) {
	mk := func(a string, rec string, clock, at int64) Step {
		return Step{Action: a, Seq: 1, Recorder: rec, Actor: "x", Process: "p", Time: time.Unix(0, at),
			Order: map[string]int64{"lamport": clock}}
	}
	// a's wall clock is late: by time alone it would come last
	steps := []Step{mk("a", "n1", 3, 10), mk("b", "n2", 3, 2), mk("c", "n3", 4, 3)}
	traces, _ := Reconstruct(steps)
	got := actions(traces[0])
	if got != "b a c" && got != "a b c" {
		t.Fatalf("order: %s", got)
	}
}

// An actor whose only operation was invoked and never completed still
// has a trace, with no steps and that call pending; the invocation's seq
// counts, so no gap is reported. A settle after it does not settle that
// trace: an actor that took no step has no eventual property to judge (its
// state is the model's initial one, whatever its call did).
func TestReconstructOnlyPending(t *testing.T) {
	traces, issues := Reconstruct([]Step{{Action: "withdraw", Seq: 1, Recorder: "r", Actor: "a", Invocation: true, Time: time.Unix(1, 0)},
		{Action: "settled", Seq: 2, Recorder: "r", Actor: "a", Settled: true, Time: time.Unix(2, 0)}})
	if len(issues) > 0 || len(traces) != 1 || len(traces[0].Steps) != 0 || len(traces[0].Pending) != 1 || len(traces[0].Issues) > 0 || traces[0].Settled {
		t.Fatalf("traces %+v, issues %v", traces, issues)
	}
}

// A settle settles a trace only if no other recorder recorded a step
// later in time: a harness's settle while a server is still at work once
// settled the run, and its eventual vals were judged on a system that was
// not quiet.
func TestSettleAcrossRecorders(t *testing.T) {
	at := func(u int) time.Time { return time.Unix(100, 0).Add(time.Duration(u) * time.Second) }
	dep := func(seq uint64, u int) Step {
		return Step{Action: "deposit", Recorder: "s", Process: "p", Seq: seq, Thread: "s", Time: at(u)}
	}
	settle := Step{Action: "settled", Recorder: "h", Process: "p", Seq: 1, Settled: true, Time: at(2)}
	traces, issues := Reconstruct([]Step{dep(1, 1), settle, dep(2, 3)})
	if traces[0].Settled || !strings.Contains(fmt.Sprint(issues), "after recorder h's settle") {
		t.Errorf("a step after the settle elsewhere: settled %v, issues %v", traces[0].Settled, issues)
	}
	traces, issues = Reconstruct([]Step{dep(1, 1), dep(2, 1), settle})
	if !traces[0].Settled || len(issues) > 0 {
		t.Errorf("every step before the settle: settled %v, issues %v", traces[0].Settled, issues)
	}
}

// Machines ordered by a domain clock compare their own clocks with no
// other's: a completion's invocation time from its record is no
// contradiction of the Lamport order on a machine 4 ms behind (it once
// made the trace an error, in every check).
func TestRecordTimesAcrossSkewedMachines(t *testing.T) {
	at := func(ms int) time.Time { return time.Unix(0, 0).Add(time.Duration(ms) * time.Millisecond) }
	steps := []Step{
		{Action: "write", Recorder: "c1", Seq: 1, Thread: "c1", Invocation: true, Time: at(100), Order: map[string]int64{"lamport": 1}},
		{Action: "write", Recorder: "c1", Seq: 2, Thread: "c1", Call: 1, Time: at(101), Order: map[string]int64{"lamport": 2}},
		{Action: "read", Recorder: "c2", Seq: 1, Thread: "c2", Invocation: true, Time: at(96), Order: map[string]int64{"lamport": 3}},
		{Action: "read", Recorder: "c2", Seq: 2, Thread: "c2", Call: 1, Time: at(97), Order: map[string]int64{"lamport": 4}},
	}
	traces, issues := Reconstruct(steps)
	if len(issues) > 0 || len(traces[0].Issues) > 0 {
		t.Errorf("issues %v %v", issues, traces[0].Issues)
	}
	if got := actions(traces[0]); got != "write read" {
		t.Errorf("order %s", got)
	}
}

// A pending call's effect placed by a search is in its caller's process
// but no incarnation of it: placed before that process's own steps, it
// once marked the process started, and its steps read as a second
// incarnation (an overlap error in every such run of wallet's).
func TestAssumedStepIsNoIncarnation(t *testing.T) {
	steps := []Step{
		{Action: "transfer", Recorder: "x", Thread: "x", Seq: 1, Process: "A", Ambiguous: true},
		{Action: "read", Recorder: "x", Thread: "x", Seq: 2, Process: "B"},
		{Action: "read", Recorder: "x", Thread: "x", Seq: 3, Process: "A"},
	}
	trs, _ := Reconstruct(steps)
	for _, i := range trs[0].Issues {
		if i.Kind == "overlap" {
			t.Errorf("an assumed step read as an incarnation: %v", i)
		}
	}
	if fmt.Sprint(trs[0].Processes) != "[A B]" {
		t.Errorf("processes %v, want [A B] (numbered in order of appearance)", trs[0].Processes)
	}
}

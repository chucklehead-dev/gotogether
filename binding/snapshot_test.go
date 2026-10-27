package binding

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A snapshot action must exist, take the state as parameters, assign
// every variable (through the actions it calls too) and read none (a
// trace starts in it from no state): lint says which, and the fix.
func TestLintSnapshot(t *testing.T) {
	skip.Quint(t)
	m, err := LoadModel("quint", "testdata/snapshot.qnt", "snap")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ snapshot, want string }{
		{"restore", ""},
		// assigns y through sameY (no "leaves y unassigned"), but reads it
		{"setX", "snapshot setX reads y: a trace starts in it from no state"},
		{"onlyX", "snapshot onlyX leaves y unassigned: a snapshot assigns every state variable from its parameters (y' = ...)"},
		{"restor", "snapshot: restor is not an action of snap (did you mean restore?)"},
		{"init", "snapshot init: the model's init is no snapshot"},
		{"sameY", "snapshot sameY takes no parameters"},
	} {
		errs := (&Binding{Snapshot: c.snapshot}).Lint(m)
		got := strings.Join(errs, "\n")
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) || c.snapshot == "setX" && strings.Contains(got, "unassigned") {
			t.Errorf("snapshot %s: lint %q, want %q", c.snapshot, got, c.want)
		}
	}
}

// A resume must exist, take no parameters, assign every variable and be
// no step of the binding's: lint says which, and the fix.
func TestLintResume(t *testing.T) {
	skip.Quint(t)
	m, err := LoadModel("quint", "testdata/snapshot.qnt", "snap")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		b    Binding
		want string
	}{
		{Binding{Resume: "resumeAny"}, ""},
		{Binding{Resume: "resumeX"}, "resume resumeX leaves y unassigned: a resume assigns every state variable (y' = ...)"},
		{Binding{Resume: "restore"}, "resume restore(a: int, b: int) takes parameters"},
		{Binding{Resume: "resumAny"}, "resume: resumAny is not an action of snap (did you mean resumeAny?)"},
		{Binding{Resume: "step"}, "resume step: the model's step is no resume"},
		{Binding{Resume: "resumeAny", Actions: map[string]Action{"resumeAny": {}}}, "resume resumeAny is bound too"},
		{Binding{Resume: "resumeY"}, "resume resumeY reads y: a trace starts in it from no state"},
		{Binding{Snapshot: "restoreY"}, "snapshot restoreY reads y: a trace starts in it from no state"},
	} {
		got := strings.Join(c.b.Lint(m), "\n")
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("resume %s: lint %q, want %q", c.b.Resume, got, c.want)
		}
	}
}

// A snapshot step first in its trace is the plan's start, not a call; one
// anywhere else is skipped, with an issue that says so.
func TestPlanSnapshotStart(t *testing.T) {
	b := &Binding{Snapshot: "restore", Actions: map[string]Action{"restore": {Args: map[string]string{"a": "{{.a}}", "b": "{{.b}}"}}, "setX": {Args: map[string]string{"v": "{{.v}}"}}}}
	restore := qtrace.Step{Action: "restore", Seq: 5, Recorder: "r", Process: "p", Args: map[string]any{"a": int64(3), "b": int64(4)}}
	setX := qtrace.Step{Action: "setX", Seq: 6, Recorder: "r", Process: "p", Args: map[string]any{"v": int64(1)}}
	p, err := b.Plan(nil, &qtrace.Trace{Steps: []qtrace.Step{restore, setX}, Processes: []string{"p"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Start == nil || p.Start.Quint != "restore(3, 4)" || p.Start.Step.Seq != 5 || len(p.Calls) != 1 || len(p.Issues) != 0 {
		t.Fatalf("start %+v calls %v issues %v", p.Start, p.Calls, p.Issues)
	}
	restore.Seq, setX.Seq = 7, 5
	p, err = b.Plan(nil, &qtrace.Trace{Steps: []qtrace.Step{setX, restore}, Processes: []string{"p"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Start != nil || len(p.Calls) != 1 || len(p.Issues) != 1 || !strings.Contains(p.Issues[0].Msg, "snapshot not first in its trace: skipped") {
		t.Fatalf("start %+v calls %v issues %v", p.Start, p.Calls, p.Issues)
	}
}

// StepsToITF starts the trace of a log whose head was lost at its
// snapshot: state 0 is the snapshot's action and picks, and the lost
// prefix is said as where the trace starts.
func TestStepsToITFSnapshot(t *testing.T) {
	b := &Binding{Snapshot: "restore", Actions: map[string]Action{"restore": {Args: map[string]string{"a": "{{.a}}", "b": "{{.b}}"}}, "setX": {Args: map[string]string{"v": "{{.v}}"}}}}
	steps := []qtrace.Step{
		{Action: "restore", Seq: 5, Recorder: "r", Process: "p", Args: map[string]any{"a": int64(3), "b": int64(4)}},
		{Action: "setX", Seq: 6, Recorder: "r", Process: "p", Args: map[string]any{"v": int64(1)}},
	}
	out, issues, err := b.StepsToITF(steps)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("%d traces", len(out))
	}
	var data []byte
	for _, d := range out {
		data = d
	}
	tr, err := itf.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.States) != 2 || tr.States[0].Action != "restore" || tr.States[0].Picks["a"].Quint() != "3" || tr.States[1].Action != "setX" {
		t.Errorf("states %+v", tr.States)
	}
	if len(issues) != 1 || issues[0].Severity != "info" || !strings.Contains(issues[0].Msg, "judged from the state recorded by restore at seq 5") {
		t.Errorf("issues %v", issues)
	}
}

// A binding's actors: decide StepsToITF's traces as they decide every
// check's (it once wrote every actor's), and those left out are named
// with their step counts in an info issue whenever any are (the doc
// promised it; only a binding matching none said so).
func TestStepsToITFActors(t *testing.T) {
	b := &Binding{Actors: []string{"!*.clients"}, Actions: map[string]Action{"setX": {Args: map[string]string{"v": "{{.v}}"}}}}
	var steps []qtrace.Step
	for i, a := range []string{"t/1", "t/1.clients", "t/1.clients", "t/2"} {
		steps = append(steps, qtrace.Step{Action: "setX", Seq: uint64(i + 1), Recorder: "r", Actor: a, Process: "p", Args: map[string]any{"v": int64(i)}})
	}
	out, issues, err := b.StepsToITF(steps)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out["t/1"] == nil || out["t/2"] == nil {
		t.Errorf("traces of %d actors, want t/1 and t/2", len(out))
	}
	if len(issues) != 1 || issues[0].Severity != "info" || issues[0].Kind != "actors" || !strings.Contains(issues[0].Msg, "left out, another binding's (actors: !*.clients): t/1.clients (2 steps, 0 pending)") {
		t.Errorf("issues %v, want the clients named with their steps", issues)
	}
}

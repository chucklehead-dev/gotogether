package binding

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A snapshot action must exist, take the state as parameters and assign
// every variable (through the actions it calls too): lint says which it
// leaves unassigned, and the fix.
func TestLintSnapshot(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	m, err := LoadModel("quint", "testdata/snapshot.qnt", "snap")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ snapshot, want string }{
		{"restore", ""},
		{"setX", ""},
		{"onlyX", "snapshot onlyX leaves y unassigned: a snapshot assigns every state variable from its parameters (y' = ...)"},
		{"restor", "snapshot: restor is not an action of snap (did you mean restore?)"},
		{"init", "snapshot init: the model's init is no snapshot"},
		{"sameY", "snapshot sameY takes no parameters"},
	} {
		errs := (&Binding{Snapshot: c.snapshot}).Lint(m)
		got := strings.Join(errs, "\n")
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("snapshot %s: lint %q, want %q", c.snapshot, got, c.want)
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

package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
)

// The incident is recorded by the actual service, not assembled as synthetic
// steps. Moving its model and capture must preserve the rejected call and the
// captured identity, while the navigation cursor remains a model state.
func TestJobqueueIncidentPortableNavigation(t *testing.T) {
	requireQuint(t)
	base := t.TempDir()
	origin := filepath.Join(base, "original")
	cmd := exec.CommandContext(t.Context(), "go", "run", "./cmd/jobqueue-incident", "-out", origin)
	cmd.Dir = filepath.Join(root, "examples", "jobqueue")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("recording the real incident: %v\n%s", err, out)
	}
	for _, name := range []string{"jobqueue.binding.yaml", "model/jobqueue.qnt"} {
		b, err := os.ReadFile(filepath.Join(cmd.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(origin, name)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before := checkJobqueueIncidentFrame(t, origin)
	moved := filepath.Join(base, "moved")
	if err := os.Rename(origin, moved); err != nil {
		t.Fatal(err)
	}
	after := checkJobqueueIncidentFrame(t, moved)
	if before.Actor != after.Actor || before.Recorder != after.Recorder || before.Seq != after.Seq {
		t.Fatalf("relocation changed captured identity: before=%+v after=%+v", before, after)
	}
}

func TestRejectedCaptureRequiresCheckedNativeIdentity(t *testing.T) {
	native := qtrace.Step{Actor: "selected", Recorder: "capture", Seq: 23, Action: "ack", Outcome: "ok"}
	other := &validate.Result{Actor: "other", Conforms: validate.Fail, Failure: &validate.Failure{Kind: "disabled", Index: 4, Step: &native}}
	for _, tc := range []struct {
		name      string
		selected  string
		actors    []*validate.Result
		wantError bool
	}{
		{"different actor", "selected", []*validate.Result{{Actor: "selected", Conforms: validate.Pass}, other}, true},
		{"ambiguous actor", "", []*validate.Result{{Actor: "selected", Conforms: validate.Pass}, other}, true},
		{"no verdict", "selected", []*validate.Result{{Actor: "selected", Conforms: validate.NotEvaluated}}, true},
		{"evaluation error", "selected", []*validate.Result{{Actor: "selected", Conforms: validate.Fail, Failure: &validate.Failure{Kind: "error", Index: 4, Step: &native}}}, true},
		{"inferred step", "selected", []*validate.Result{{Actor: "selected", Conforms: validate.Fail, Failure: &validate.Failure{Kind: "disabled", Index: 4}}}, false},
		{"unidentified step", "selected", []*validate.Result{{Actor: "selected", Conforms: validate.Fail, Failure: &validate.Failure{Kind: "disabled", Index: 4, Step: &qtrace.Step{Actor: "selected", Action: "ack"}}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := rejectedRecordOf(&validate.Report{Actors: tc.actors}, tc.selected, 4)
			if ref != nil || (err != nil) != tc.wantError {
				t.Fatalf("unestablished identity became inspectable: %+v, %v", ref, err)
			}
		})
	}
	valid := &validate.Report{Actors: []*validate.Result{{Actor: "selected", Conforms: validate.Fail, Failure: &validate.Failure{Kind: "disabled", Index: 4, Step: &native}}}}
	ref, err := rejectedRecordOf(valid, "selected", 4)
	if err != nil || ref == nil || ref.Seq != 23 {
		t.Fatalf("native sequence was replaced by cursor index: %+v, %v", ref, err)
	}
	if ref, err := rejectedRecordOf(valid, "selected", 3); ref != nil || err == nil {
		t.Fatalf("mismatched navigation boundary became failure evidence: %+v, %v", ref, err)
	}
}

type incidentCaptureNav struct {
	ModelNav
	steps []qtrace.Step
}

func (n incidentCaptureNav) Record(ref diagnostic.RecordRef) (*diagnostic.RecordDetail, error) {
	return diagnostic.LookupRecord(n.steps, ref)
}

func TestRejectedEvidenceLookupErrorDoesNotInventRecord(t *testing.T) {
	ref := diagnostic.RecordRef{Actor: "selected", Recorder: "capture", Seq: 23}
	for _, tc := range []struct {
		name  string
		steps []qtrace.Step
	}{
		{"missing", nil},
		{"ambiguous", []qtrace.Step{
			{Actor: ref.Actor, Recorder: ref.Recorder, Seq: ref.Seq, Action: "ack", Outcome: "ok"},
			{Actor: ref.Actor, Recorder: ref.Recorder, Seq: ref.Seq, Action: "ack", Outcome: "stale"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := incidentCaptureNav{ModelNav: &evidenceModel{node: &validate.Node{}}, steps: tc.steps}
			nav := rejectedHistoryNav{ModelNav: base, ref: ref}
			d := drive(t, Config{Model: nav})
			before := *d.m.cur()
			d.keys("e", "enter")
			if d.m.cur().record != nil || !strings.Contains(d.m.Status(), tc.name) {
				t.Fatalf("%s capture became an inspected record: status=%s", tc.name, d.m.Status())
			}
			d.keys("backspace")
			if d.m.cur().m != before.m || len(d.m.cur().path) != len(before.path) {
				t.Fatal("failed inspection moved the model cursor")
			}
		})
	}
}

func checkJobqueueIncidentFrame(t *testing.T, dir string) diagnostic.RecordRef {
	t.Helper()
	file, err := input.Read(filepath.Join(dir, "incident.jsonl"), input.Unknown)
	if err != nil {
		t.Fatal(err)
	}
	checker, err := validate.NewChecker(filepath.Join(dir, "jobqueue.binding.yaml"), validate.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	report, err := checker.Check(t.Context(), file.Steps)
	if err != nil {
		t.Fatal(err)
	}
	doc := validate.NewDocument("validate")
	doc.AddReport(report)
	doc.Verdict, doc.Exit = string(report.Verdict().Verdict), 1
	// Use exactly the native data agents receive, including the difference
	// between the inferred replay step and the recorder sequence.
	var machine validate.Document
	if err := json.Unmarshal([]byte(doc.JSON()), &machine); err != nil {
		t.Fatal(err)
	}
	if machine.Verdict != "fail" || len(machine.Actors) != 1 || machine.Actors[0].Conformance != "fail" || len(machine.Actors[0].Failures) != 1 {
		t.Fatalf("want one rejected actor, got %+v", machine.Actors)
	}
	failure := machine.Actors[0].Failures[0]
	if failure.Check != "conformance" || failure.Kind != "disabled" || failure.Quint != `ack("w1", 1, Ok)` || failure.Record == nil {
		t.Fatalf("wrong incident failure: %+v", failure)
	}
	r := failure.Record
	if r.Actor != "jobqueue" || r.Recorder != "jobqueue-incident-incident" || r.Seq != 4 || r.Step != 5 || r.Action != "ack" || r.Outcome != "ok" {
		t.Fatalf("capture/replay identity mismatch: %+v", r)
	}
	ref := diagnostic.RecordRef{Actor: r.Actor, Recorder: r.Recorder, Seq: r.Seq}
	nav, title, why, err := Failing(checker, validate.NavStart{Steps: file.Steps, Actor: r.Actor, At: -1})
	if err != nil {
		t.Fatal(err)
	}
	d := drive(t, Config{Title: "model jobqueue: " + title, Model: nav, Why: why})
	frame := d.screen()
	for _, want := range []string{"before call 5", `ack("w1", 1, Ok)`, "o == Stale is false", "jobs = Map(1 -> Pending)"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("incident frame lost %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "verdict pass") {
		t.Fatalf("a cursor must not acquire a passing whole-history verdict:\n%s", frame)
	}
	inspect := func() {
		t.Helper()
		before := *d.m.cur()
		evidence := d.keys("e").screen()
		for _, want := range []string{"captured history evidence", "original collected history", "conformance failure", "independent of model cursor", "seq 4"} {
			if !strings.Contains(evidence, want) {
				t.Fatalf("failure evidence lost %q:\n%s", want, evidence)
			}
		}
		captured := d.keys("enter").screen()
		for _, want := range []string{"Captured record · no model judgment or cursor move", "actor: jobqueue", "recorder: jobqueue-incident-incident · seq 4", "action: ack", "outcome: ok", "process: queue-2"} {
			if !strings.Contains(captured, want) {
				t.Fatalf("captured failure inspector lost %q:\n%s", want, captured)
			}
		}
		d.keys("backspace", "backspace")
		if d.m.cur().m != before.m || d.m.cur().nav != before.nav || d.m.cur().cursor != before.cursor || len(d.m.cur().path) != len(before.path) {
			t.Fatal("inspection moved the model cursor")
		}
	}
	inspect()
	// The same navigator exposes the exact native record used by the report,
	// without navigating to or inventing a counterfactual captured record.
	detail, err := nav.(recordNav).Record(ref)
	if err != nil || detail.Action != r.Action || detail.Outcome != r.Outcome || !sameRecord(detail.Reference, ref) {
		t.Fatalf("navigator changed record identity: detail=%+v error=%v", detail, err)
	}
	counterfactual := d.keys("/", "ack", "enter", "enter").screen()
	if !strings.Contains(counterfactual, `* finished = Map(("w1", 1) -> 0)`) {
		t.Fatalf("allowed stale ack did not move the model:\n%s", counterfactual)
	}
	inspect() // the fixed history's rejected record survives hypothetical moves
	detail, err = nav.(recordNav).Record(ref)
	if err != nil || detail.Outcome != "ok" || report.Verdict().Verdict != validate.Fail {
		t.Fatalf("a model branch changed recorded incident evidence: detail=%+v error=%v", detail, err)
	}
	if back := d.keys("backspace").screen(); !strings.Contains(back, "o == Stale is false") {
		t.Fatalf("back lost the original failing boundary:\n%s", back)
	}
	for _, control := range []string{"faithful", "repaired"} {
		captured, err := input.Read(filepath.Join(dir, control+".jsonl"), input.Unknown)
		if err != nil {
			t.Fatal(err)
		}
		judged, err := checker.Check(t.Context(), captured.Steps)
		if err != nil || !judged.OK() {
			t.Fatalf("%s control is not passing: %v, %v", control, judged, err)
		}
		if fake, _, _, err := Failing(checker, validate.NavStart{Steps: captured.Steps, Actor: r.Actor, At: -1}); err == nil || fake != nil {
			t.Fatalf("%s acquired a fabricated failing cursor: %v, %v", control, fake, err)
		}
	}
	return ref
}

// Snapshot checkpoints compare replayed state; they are not disabled calls.
func TestFailingSnapshotCheckpoint(t *testing.T) {
	requireQuint(t)
	checker, err := validate.NewChecker(filepath.Join(root, "validate/testdata/snapshot.yaml"), validate.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "r", Thread: "client", Seq: 1, Time: time.Unix(0, 10000000), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}},
		{Action: "restore", Recorder: "r", Thread: "client", Seq: 2, Time: time.Unix(0, 20000000), Args: map[string]any{"b": int64(5), "n": int64(2)}},
	}
	report, err := checker.Check(t.Context(), steps)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Actors) != 1 || report.Actors[0].Failure == nil || report.Actors[0].Failure.Kind != "state" {
		t.Fatalf("expected checkpoint state failure: %+v", report)
	}
	nav, title, why, err := Failing(checker, validate.NavStart{Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nav.(interface{ rejectedRecord() diagnostic.RecordRef }); ok {
		t.Fatalf("checkpoint cursor gained rejected-call overlay: %T", nav)
	}
	node, err := nav.Node()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, value := range node.State {
		values[value.Name] = value.Value
	}
	if values["balance"] != "5" || values["ops"] != "1" {
		t.Fatalf("checkpoint reset accepted prefix: %+v", node.State)
	}
	d := drive(t, Config{Model: nav, Title: title, Why: why})
	screen := d.m.View().Content
	for _, want := range []string{"before call 2", "checkpoint disagrees", "restore", "ops", "balance"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing %q:\n%s", want, screen)
		}
	}
	changed := drive(t, Config{Model: nav, Title: "arbitrary display title", Why: why})
	if !strings.Contains(changed.m.View().Content, "recorded checkpoint disagrees with replayed state") {
		t.Fatal("checkpoint heading depends on display title")
	}
	if strings.Contains(screen, "model rejects") || strings.Contains(screen, "e evidence") {
		t.Fatalf("checkpoint misrepresented as disabled call:\n%s", screen)
	}
}

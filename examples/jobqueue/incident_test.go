package jobqueue

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/schemas"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// An incident's native identity survives report, exact-capture inspection and
// replay. Display paths may change when evidence moves; they are not identity.
type incidentIdentity struct {
	Actor, Recorder, Process, Action, Outcome, Check, Kind, Quint string
	Seq                                                           uint64
	Step                                                          int
}

func TestIncidentWorkflow(t *testing.T) {
	qtest.RequireQuint(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	build := func(dir, pkg, name string) string {
		t.Helper()
		path := filepath.Join(bin, name)
		cmd := exec.CommandContext(t.Context(), "go", "build", "-o", path, pkg)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, out)
		}
		return path
	}
	cli := build(root, "./cmd/gotogether", "gotogether")
	producer := build(filepath.Join(root, "examples", "jobqueue"), "./cmd/jobqueue-incident", "jobqueue-incident")
	base := t.TempDir()
	bundle := filepath.Join(base, "original", "bundle")
	if err := os.MkdirAll(filepath.Join(bundle, "model"), 0o755); err != nil {
		t.Fatal(err)
	}
	inputs := []string{"jobqueue.binding.yaml", filepath.Join("model", "jobqueue.qnt")}
	hashes := map[string][32]byte{}
	for _, path := range inputs {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		hashes[path] = sha256.Sum256(body)
		if err := os.WriteFile(filepath.Join(bundle, path), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	invoke := func(binary, cwd string, want int, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), binary, args...)
		cmd.Dir = cwd
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				t.Fatalf("start %s: %v", binary, err)
			}
		}
		if code != want {
			t.Fatalf("%s %v: exit %d, want %d\nstdout: %s\nstderr: %s", binary, args, code, want, &out, &stderr)
		}
		return out.Bytes()
	}
	document := func(body []byte, verdict string, exit int) validate.Document {
		t.Helper()
		if problems, err := schemas.Check(schemas.Report(), body); err != nil || len(problems) != 0 {
			t.Fatalf("invalid native report: %v (%v)\n%s", problems, err, body)
		}
		var doc validate.Document
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Format != validate.ReportFormat || doc.Verdict != verdict || doc.Exit != exit {
			t.Fatalf("report verdict=%s exit=%d format=%s, want %s/%d", doc.Verdict, doc.Exit, doc.Format, verdict, exit)
		}
		if verdict == "error" {
			if doc.Error == "" {
				t.Fatal("data/tool error lost its diagnostic")
			}
		} else if doc.Receipt == nil || doc.Receipt.Scope != "collected_history" || doc.Receipt.OtherExecutions != receipt.NotChecked {
			t.Fatalf("history judgment lost its bounded scope: %+v", doc.Receipt)
		}
		return doc
	}
	failure := func(doc validate.Document) incidentIdentity {
		t.Helper()
		if len(doc.Actors) != 1 || len(doc.Actors[0].Failures) != 1 {
			t.Fatalf("want one actor and one conformance failure: %+v", doc.Actors)
		}
		a, f := doc.Actors[0], doc.Actors[0].Failures[0]
		if f.Record == nil || a.Actor != "jobqueue" || a.Conformance != "fail" || f.Check != "conformance" || f.Kind != "disabled" || f.Verdict != "fail" {
			t.Fatalf("want native disabled conformance failure: %+v", f)
		}
		r := f.Record
		if r.Actor != a.Actor || r.Recorder != "jobqueue-incident-incident" || r.Seq != 4 || r.Step != 5 || r.Action != "ack" || r.Outcome != "ok" || r.Process != "queue-2" {
			t.Fatalf("wrong captured/model identity: %+v", r)
		}
		return incidentIdentity{r.Actor, r.Recorder, r.Process, r.Action, r.Outcome, f.Check, f.Kind, f.Quint, r.Seq, r.Step}
	}

	invoke(producer, base, 0, "-out", bundle)
	var incident validate.Document
	for _, c := range []struct {
		name, verdict string
		exit          int
	}{{"faithful", "pass", 0}, {"incident", "fail", 1}, {"repaired", "pass", 0}} {
		manifest := filepath.Join(bundle, c.name+".run.json")
		body := invoke(cli, base, c.exit, "validate", "-binding", filepath.Join(bundle, inputs[0]), "-json", "-dir", filepath.Join(bundle, "work-"+c.name), "-manifest", manifest, filepath.Join(bundle, c.name+".jsonl"))
		doc := document(body, c.verdict, c.exit)
		if c.name == "incident" {
			incident = doc
		} else if len(doc.Actors) != 1 || doc.Actors[0].Conformance != "pass" || len(doc.Actors[0].Failures) != 0 {
			t.Fatalf("%s control did not pass unchanged checks: %+v", c.name, doc.Actors)
		}
	}
	want := failure(incident)
	// The repaired implementation also accepts a fresh valid lease. Merely
	// rejecting every ack cannot satisfy this control.
	f, err := os.Open(filepath.Join(bundle, "repaired.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	steps, err := qtrace.ReadJSONL(f)
	f.Close()
	if err != nil || len(steps) != 7 || steps[3].Outcome != "stale" || steps[6].Action != "ack" || steps[6].Outcome != "ok" {
		t.Fatalf("repaired real run must reject stale and accept fresh ack: steps=%+v error=%v", steps, err)
	}
	for _, path := range inputs {
		body, err := os.ReadFile(filepath.Join(bundle, path))
		if err != nil || sha256.Sum256(body) != hashes[path] {
			t.Fatalf("model/binding changed during repair: %s: %v", path, err)
		}
	}

	// The file's four records become five model calls through an inferred
	// restart. A cursor before model call five is -at 4, never seq-1.
	navBody := invoke(cli, base, 0, "nav", "-binding", filepath.Join(bundle, inputs[0]), "-run", filepath.Join(bundle, "incident.jsonl"), "-at", "4", "-json")
	var node validate.Node
	if err := json.Unmarshal(navBody, &node); err != nil {
		t.Fatal(err)
	}
	wantPath := []string{"enqueue(1)", `lease("w1", 1)`, "restart", `perform("w1", 1)`}
	if !reflect.DeepEqual(node.Path, wantPath) {
		t.Fatalf("cursor opened the wrong model prefix: %v", node.Path)
	}
	var navFields map[string]json.RawMessage
	if err := json.Unmarshal(navBody, &navFields); err != nil {
		t.Fatal(err)
	}
	if _, exists := navFields["verdict"]; exists {
		t.Fatal("a navigation cursor asserted a whole-history verdict")
	}
	ref, _ := json.Marshal(diagnostic.RecordRef{Actor: want.Actor, Recorder: want.Recorder, Seq: want.Seq})
	inspect := func(dir, cwd string) {
		t.Helper()
		body := invoke(cli, cwd, 0, "nav", "-run", filepath.Join(dir, "incident.jsonl"), "-record", string(ref), "-json")
		var detail diagnostic.RecordDetail
		if err := json.Unmarshal(body, &detail); err != nil {
			t.Fatal(err)
		}
		if detail.Reference.Actor != want.Actor || detail.Reference.Recorder != want.Recorder || detail.Reference.Seq != want.Seq || detail.Action != want.Action || detail.Outcome != want.Outcome || detail.Process != want.Process {
			t.Fatalf("exact captured record differs from report identity: %+v", detail)
		}
	}
	inspect(bundle, base)
	manifestBody, err := os.ReadFile(filepath.Join(bundle, "incident.run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var original validate.Manifest
	if err := json.Unmarshal(manifestBody, &original); err != nil {
		t.Fatal(err)
	}
	paths := []string{original.Model.Binding.Path, original.Model.Spec.Path}
	for _, input := range original.Inputs {
		paths = append(paths, input.Path)
	}
	for _, file := range original.Model.Imports {
		paths = append(paths, file.Path)
	}
	for _, path := range paths {
		clean := filepath.Clean(filepath.FromSlash(path))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || len(path) == 0 {
			t.Fatalf("bundle manifest depends on external path %q", path)
		}
	}
	moved := filepath.Join(base, "moved-bundle")
	if err := os.Rename(bundle, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(bundle)); err != nil {
		t.Fatal(err)
	}
	unrelated := t.TempDir()
	replayedManifest := filepath.Join(moved, "replayed.run.json")
	body := invoke(cli, unrelated, 1, "validate", "-replay", filepath.Join(moved, "incident.run.json"), "-manifest", replayedManifest, "-dir", filepath.Join(moved, "replay-work"), "-json")
	if got := failure(document(body, "fail", 1)); got != want {
		t.Fatalf("relocated replay identity changed: got %+v want %+v", got, want)
	}
	replayed, err := validate.ReadManifest(replayedManifest)
	if err != nil {
		t.Fatal(err)
	}
	if same, note := original.Verdict.Same(replayed.Verdict); !same {
		t.Fatalf("native manifest verdict changed: %s", note)
	}
	inspect(moved, unrelated)

	// A changed or absent capture is a data error, never the incident's
	// model failure. An extra blank line keeps the records semantically equal
	// while proving replay refuses hash changes before judging them.
	input := filepath.Join(moved, "incident.jsonl")
	clean, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	// A valid native header without observations has no judgment. Exit one
	// alone cannot distinguish it from the incident's actual counterexample.
	end := bytes.IndexByte(clean, '\n')
	if end < 0 {
		t.Fatal("native capture has no header line")
	}
	empty := filepath.Join(moved, "empty.jsonl")
	if err := os.WriteFile(empty, clean[:end+1], 0o644); err != nil {
		t.Fatal(err)
	}
	noVerdict := document(invoke(cli, unrelated, 1, "validate", "-binding", filepath.Join(moved, inputs[0]), "-json", empty), "no verdict", 1)
	if noVerdict.Receipt.Status != receipt.NotChecked || len(noVerdict.Actors) != 0 {
		t.Fatalf("empty capture gained a model judgment: %+v", noVerdict)
	}
	if err := os.WriteFile(input, append(append([]byte(nil), clean...), '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	document(invoke(cli, unrelated, 2, "validate", "-replay", filepath.Join(moved, "incident.run.json"), "-json"), "error", 2)
	if err := os.WriteFile(input, clean, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(input); err != nil {
		t.Fatal(err)
	}
	document(invoke(cli, unrelated, 2, "validate", "-replay", filepath.Join(moved, "incident.run.json"), "-json"), "error", 2)
}

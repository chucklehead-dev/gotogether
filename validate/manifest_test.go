package validate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// bundle is a directory with a copy of the account binding and model, a
// step log and an artifact, and a manifest of them (no verdict), as a
// run's files travel.
func bundle(t *testing.T) (dir string, m *Manifest) {
	t.Helper()
	dir = t.TempDir()
	for _, f := range []string{"account.yaml", "account.qnt"} {
		b, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	steps := qtrace.MarkLost([]qtrace.Step{{Action: "deposit", Recorder: "r", Seq: 1, Time: time.Unix(1, 0), Args: map[string]any{"amount": int64(5)}}},
		qtrace.Lost{Recorder: "r", After: 1})
	f, err := os.Create(filepath.Join(dir, "run.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := qtrace.WriteJSONL(f, steps); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.WriteFile(filepath.Join(dir, "shot.webp"), []byte("pixels"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := binding.Load(filepath.Join(dir, "account.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m = NewManifest(Options{Quint: "no-such-quint"})
	if err := m.SetBinding(b); err != nil {
		t.Fatal(err)
	}
	if err := m.AddInput(filepath.Join(dir, "run.jsonl"), "steps", steps); err != nil {
		t.Fatal(err)
	}
	if err := m.AddArtifact("screenshot", filepath.Join(dir, "shot.webp")); err != nil {
		t.Fatal(err)
	}
	m.Driver = &Driver{Name: "bombadil", Seed: "42", Args: []string{"--headless"}}
	return dir, m
}

// A manifest written and read back is the same, its paths under its
// directory written relative to it, so the directory moves whole; a
// file of another format is refused with the fix.
func TestManifestRoundTrip(t *testing.T) {
	t.Parallel()
	dir, m := bundle(t)
	if m.Inputs[0].Steps != 1 || len(m.Inputs[0].Lost) != 1 || m.Inputs[0].Lost[0].Kind != "suffix" || m.Model.Constants["LIMIT"] != "100" {
		t.Fatalf("manifest: %+v", m)
	}
	p := filepath.Join(dir, "run.manifest.json")
	if err := m.Write(p); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), `"path": "run.jsonl"`) || strings.Contains(string(raw), dir) {
		t.Errorf("paths not relative to the manifest:\n%s", raw)
	}
	got, err := ReadManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, m) {
		t.Errorf("round trip:\n%+v\n%+v", got, m)
	}
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	got, err = ReadManifest(filepath.Join(moved, "run.manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if w, err := got.Verify(); err != nil || len(w) > 0 || got.Inputs[0].Path != filepath.Join(moved, "run.jsonl") {
		t.Errorf("moved: %v %v %s", w, err, got.Inputs[0].Path)
	}
	if !IsManifest(filepath.Join(moved, "run.manifest.json")) {
		t.Error("IsManifest: false")
	}
	other := filepath.Join(moved, "evidence.json")
	os.WriteFile(other, []byte(`{"format": "gotogether.evidence/2", "runs": 1}`), 0o644)
	if _, err := ReadManifest(other); err == nil || !strings.Contains(err.Error(), `not a run manifest of "gotogether.run/1"`) || IsManifest(other) {
		t.Errorf("another format: %v", err)
	}
}

// A replay judges the files the manifest hashed: a changed input,
// binding or spec, or a missing one, is refused, naming each; a changed
// artifact, which no verdict reads, is a warning.
func TestManifestVerifyRefusesChanges(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		file, says string
		warn       bool
	}{
		{"run.jsonl", "the input", false},
		{"account.yaml", "the binding", false},
		{"account.qnt", "the spec", false},
		{"shot.webp", "artifact screenshot", true},
	} {
		dir, m := bundle(t)
		if w, err := m.Verify(); err != nil || len(w) > 0 {
			t.Fatalf("unchanged: %v %v", w, err)
		}
		f, err := os.OpenFile(filepath.Join(dir, c.file), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString("\n")
		f.Close()
		w, err := m.Verify()
		switch {
		case c.warn && (err != nil || len(w) != 1 || !strings.Contains(w[0], c.says)):
			t.Errorf("%s: %v %v", c.file, w, err)
		case !c.warn && (err == nil || !strings.Contains(err.Error(), c.says+" "+filepath.Join(dir, c.file)+" changed") || !strings.Contains(err.Error(), "restore it")):
			t.Errorf("%s: %v", c.file, err)
		}
		os.Remove(filepath.Join(dir, c.file))
		if _, err := m.Verify(); !c.warn && (err == nil || !strings.Contains(err.Error(), "put it back")) {
			t.Errorf("%s missing: %v", c.file, err)
		}
	}
}

// A run's verdict is its actors': a failure decides, then no verdict; an
// actor that passes but is incomplete fails with why; two verdicts are
// the same at the same failing step, whatever its ref.
func TestVerdictOf(t *testing.T) {
	t.Parallel()
	step := &qtrace.Step{Action: "deposit", Recorder: "r", Seq: 2, Ref: "trace.jsonl:2"}
	pass := &Result{Actor: "a", Conforms: Pass}
	fail := &Result{Actor: "b", Conforms: Fail, Failure: &Failure{Kind: "state", Step: step}}
	none := &Result{Actor: "c", Conforms: NotEvaluated, noVerdict: "lost steps"}
	v := VerdictOf([]*Result{pass, none, fail}, nil)
	if v.Verdict != Fail || v.Reason != "no verdict (lost steps)" || v.Failing == nil || v.Failing.Actor != "b" || v.Failing.Ref != "trace.jsonl:2" || len(v.Actors) != 3 || v.Actors[1].Verdict != NotEvaluated {
		t.Errorf("%+v", v)
	}
	if v := VerdictOf([]*Result{pass, none}, nil); v.Verdict != NotEvaluated || v.Failing != nil {
		t.Errorf("no verdict: %+v", v)
	}
	if v := VerdictOf([]*Result{pass}, map[*Result]string{pass: "incomplete (no settle)"}); v.Verdict != Fail || v.Reason != "incomplete (no settle)" {
		t.Errorf("incomplete: %+v", v)
	}
	if v := VerdictOf(nil, nil); v.Verdict != NotEvaluated {
		t.Errorf("nothing: %+v", v)
	}
	w := VerdictOf([]*Result{pass, none, fail}, nil)
	w.Failing.Ref = "another run's line"
	if !v.Same(w) {
		t.Error("a ref made another verdict")
	}
	w.Failing.Seq = 3
	if VerdictOf([]*Result{pass, none, fail}, nil).Same(w) {
		t.Error("another failing step is the same verdict")
	}
}

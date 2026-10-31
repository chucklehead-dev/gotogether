package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/schemas"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestInitLibraryBindingUsesExplicitProperties(t *testing.T) {
	skip.Quint(t)
	dir := filepath.Join(t.TempDir(), "catalog")
	if err := runInit(io.Discard, io.Discard, []string{"-kind", "library", "-dir", dir, "-no-tidy", "catalog"}); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(dir, "catalog.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "gotogether") || strings.Contains(string(source), "qenv") || strings.Contains(string(source), "qobs") {
		t.Fatal("production library must stay independent of the observing test driver")
	}
	mutant, err := os.ReadFile(filepath.Join(dir, "testdata", "mutants", "aliasSnapshot", "catalog.go"))
	if err != nil {
		t.Fatal(err)
	}
	if diff := lineDiff(string(source), string(mutant)); diff != 1 {
		t.Fatalf("aliasing contrast differs in %d lines, want one", diff)
	}
	path := filepath.Join(dir, "catalog.binding.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if problems, err := schemas.Check(schemas.Binding(), raw); err != nil || len(problems) > 0 {
		t.Fatalf("binding schema: %v %v", err, problems)
	}
	b, err := binding.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := binding.LoadModel("quint", b.SpecPath(), b.Module)
	if err != nil {
		t.Fatal(err)
	}
	if findings := b.LintFindings(m); len(findings) > 0 {
		t.Fatalf("generated binding does not fit its model: %+v", findings)
	}
	if len(b.Invariants) == 0 || len(b.Eventually) != 0 {
		t.Fatalf("synchronous snapshot contract needs safety properties: %+v", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "sim_test.go")); !os.IsNotExist(err) {
		t.Fatal("library starter unexpectedly creates a simulation harness")
	}
}

// This is the public workflow from a generated standalone module: real calls,
// a source overlay, fresh captured records and portable manifest replay. The
// model/binding stay unchanged across faithful and contrast executions.
func TestInitLibraryStandaloneWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("full mode: builds and tests a generated standalone module")
	}
	skip.Quint(t)
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "catalog")
	var output bytes.Buffer
	if err := runInit(&output, &output, []string{"-kind", "library", "-dir", dir, "-replace", repo, "catalog"}); err != nil {
		t.Fatalf("init: %v\n%s", err, &output)
	}
	frozen := map[string][]byte{}
	for _, name := range []string{"catalog.go", "catalog.binding.yaml", "model/catalog.qnt"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		frozen[name] = data
	}
	run := func(wd string, env []string, program string, args ...string) (int, []byte) {
		t.Helper()
		cmd := exec.Command(program, args...)
		cmd.Dir = wd
		cmd.Env = append(append(os.Environ(), "GOWORK=off"), env...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		out := stdout.Bytes()
		if program == "go" {
			out = append(out, stderr.Bytes()...)
		} else if stderr.Len() > 0 {
			t.Logf("CLI stderr: %s", stderr.String())
		}
		if err == nil {
			return 0, out
		}
		var status *exec.ExitError
		if errors.As(err, &status) {
			return status.ExitCode(), out
		}
		t.Fatalf("start %s: %v", program, err)
		return 2, nil
	}
	if code, out := run(dir, nil, "go", "test", "-short", "-count=1", "-v", "./..."); code != 0 || !strings.Contains(string(out), "--- PASS: TestMutants/aliasSnapshot") {
		t.Fatalf("generated go test: exit%d\n%s", code, out)
	}
	cli := filepath.Join(t.TempDir(), "gotogether")
	if code, out := run(repo, nil, "go", "build", "-o", cli, "./cmd/gotogether"); code != 0 {
		t.Fatalf("CLI build: exit%d %s", code, out)
	}
	documentIn := func(wd string, expected int, args ...string) validate.Document {
		t.Helper()
		code, out := run(wd, nil, cli, args...)
		var result validate.Document
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("CLI must emit one JSON report: exit%d %v\n%s", code, err, out)
		}
		if code != expected || result.Exit != expected {
			t.Fatalf("unexpected CLI status: got%d/report%d want%d\n%s", code, result.Exit, expected, out)
		}
		return result
	}
	document := func(expected int, args ...string) validate.Document { return documentIn(dir, expected, args...) }
	graph := document(0, "simulate", "-binding", "catalog.binding.yaml", "-graph", "-json")
	reached := map[string]bool{}
	for _, check := range graph.Checks {
		if check.Kind == "possible" && check.Verdict == "reached" {
			reached[check.Name] = true
		}
	}
	if !reached["an old snapshot survives a later write"] || !reached["captured value differs from current"] {
		t.Fatalf("model did not demonstrate its snapshot scenario: %+v", graph)
	}
	overlay := filepath.Join(dir, "alias-overlay.json")
	data, _ := json.Marshal(map[string]any{"Replace": map[string]string{filepath.Join(dir, "catalog.go"): filepath.Join(dir, "testdata", "mutants", "aliasSnapshot", "catalog.go")}})
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, mutant := range []bool{false, true} {
		label := "faithful"
		expected := 0
		if mutant {
			label = "alias"
			expected = 1
		}
		evidence := filepath.Join(dir, "evidence", label)
		env := []string{"GOTOGETHER_DEMO_DIR=" + evidence}
		args := []string{"test", "-count=1", "-run", "^TestDemoEvidence$", "-v", "."}
		if mutant {
			env = append(env, "GOTOGETHER_DEMO_MUTANT=aliasSnapshot")
			args = append([]string{"test", "-overlay", overlay}, args[1:]...)
		}
		if code, out := run(dir, env, "go", args...); code != 0 {
			t.Fatalf("actual %s capture: exit%d\n%s", label, code, out)
		}
		stepsPath := filepath.Join(evidence, "steps.jsonl")
		manifest := filepath.Join(evidence, "manifest.json")
		captured, err := os.Open(stepsPath)
		if err != nil {
			t.Fatal(err)
		}
		observedSteps, err := qtrace.ReadJSONL(captured)
		captured.Close()
		if err != nil {
			t.Fatal(err)
		}
		oldRead := false
		for _, step := range observedSteps {
			if step.Action == "read" && step.Args["id"] == int64(1) {
				want := int64(1)
				if mutant {
					want = 2
				}
				if value := step.Obs["value"]; value != want {
					t.Fatalf("%s observed old snapshot %v, want actual value %d", label, value, want)
				}
				oldRead = true
			}
		}
		if !oldRead {
			t.Fatal("captured workload never observed its old snapshot")
		}

		judged := document(expected, "validate", "-binding", "catalog.binding.yaml", "-dir", filepath.Join(evidence, "judge"), "-json", stepsPath)
		replayed := document(expected, "validate", "-replay", manifest, "-dir", filepath.Join(evidence, "replay"), "-json")
		if !mutant {
			if judged.Verdict != "pass" || replayed.Verdict != "pass" {
				t.Fatal("faithful capture/replay did not pass")
			}
			continue
		}
		for _, result := range []validate.Document{judged, replayed} {
			if result.Verdict != "fail" || len(result.Actors) != 1 || len(result.Actors[0].Failures) != 1 {
				t.Fatalf("source contrast was not a model mismatch: %+v", result)
			}
			failure := result.Actors[0].Failures[0]
			if failure.Kind != "state" || failure.Record == nil || failure.Record.Action != "read" {
				t.Fatalf("wrong source contrast failure: %+v", failure)
			}
		}
		input, err := os.Open(stepsPath)
		if err != nil {
			t.Fatal(err)
		}
		steps, err := qtrace.ReadJSONL(input)
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		observed := false
		for i := range steps {
			if steps[i].Action == "read" {
				if value := steps[i].Obs["value"]; value == nil {
					t.Fatal("source contrast has no actual read observation")
				}
				delete(steps[i].Obs, "value")
				observed = true
			}
		}
		if !observed {
			t.Fatal("capture contains no read")
		}
		missing := filepath.Join(evidence, "missing.jsonl")
		out, err := os.Create(missing)
		if err != nil {
			t.Fatal(err)
		}
		err = qtrace.WriteJSONL(out, steps)
		out.Close()
		if err != nil {
			t.Fatal(err)
		}
		malformed := document(2, "validate", "-binding", "catalog.binding.yaml", "-dir", filepath.Join(evidence, "missing-check"), "-json", missing)
		encoded, _ := json.Marshal(malformed)
		if malformed.Verdict != "error" || !strings.Contains(string(encoded), "obs.value") {
			t.Fatalf("missing observation was not an evidence error: %s", encoded)
		}
	}
	// An expectation label alone must not manufacture a mutant catch.
	env := []string{"GOTOGETHER_DEMO_DIR=" + filepath.Join(dir, "evidence", "false-mutant"), "GOTOGETHER_DEMO_MUTANT=aliasSnapshot"}
	if code, out := run(dir, env, "go", "test", "-count=1", "-run", "^TestDemoEvidence$", "-v", "."); code == 0 {
		t.Fatalf("unmutated implementation satisfied a fake contrast label:\n%s", out)
	}
	for name, want := range frozen {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("contrast changed frozen contract/source %s: %v", name, err)
		}
	}
	relocated := t.TempDir()
	for _, label := range []string{"faithful", "alias"} {
		source := filepath.Join(dir, "evidence", label)
		target := filepath.Join(relocated, label)
		err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			destination := filepath.Join(target, rel)
			if entry.IsDir() {
				return os.MkdirAll(destination, 0700)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(destination, data, 0600)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(dir, dir+"-hidden"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("original project path still exists during relocation check")
	}
	for _, label := range []string{"faithful", "alias"} {
		expected := 0
		if label == "alias" {
			expected = 1
		}
		result := documentIn(relocated, expected, "validate", "-replay", filepath.Join(relocated, label, "manifest.json"), "-dir", filepath.Join(relocated, label, "relocated-replay"), "-json")
		verdict := "pass"
		if expected == 1 {
			verdict = "fail"
		}
		if result.Verdict != verdict {
			t.Fatalf("relocated replay verdict %q, want %s", result.Verdict, verdict)
		}
		if expected == 1 && (len(result.Actors) != 1 || len(result.Actors[0].Failures) != 1 || result.Actors[0].Failures[0].Kind != "state" || result.Actors[0].Failures[0].Record == nil || result.Actors[0].Failures[0].Record.Action != "read") {
			t.Fatalf("relocated replay lost semantic mismatch: %+v", result)
		}
	}
}

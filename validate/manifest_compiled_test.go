package validate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

func compiledGraph(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"instance", "models", "artifacts", "captures"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	spec, err := os.ReadFile("testdata/account.qnt")
	if err != nil {
		t.Fatal(err)
	}
	spec = []byte(strings.Replace(string(spec), "module account {", "module account {\n  import support.* from \"./support\"", 1))
	body, err := os.ReadFile("testdata/account.yaml")
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), "spec: account.qnt", "spec: ../models/main.qnt", 1))
	for p, data := range map[string][]byte{"instance/binding.yaml": body, "models/main.qnt": spec, "models/support.qnt": []byte("module support { pure val zero = 0 }\n")} {
		if err := os.WriteFile(filepath.Join(dir, p), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	cm, err := Compile(filepath.Join(dir, "instance/binding.yaml"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.Write(filepath.Join(dir, "artifacts/model.json")); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Imported model layouts and deliberately absent source files survive export
// byte-for-byte. Absence is coverage, not permission to weaken file integrity.
func TestCompiledManifestNestedGraph(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	original := compiledGraph(t)
	for _, mode := range []string{"present", "partial-main-absent", "partial-import-absent", "absent"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "original")
			if err := os.CopyFS(dir, os.DirFS(original)); err != nil {
				t.Fatal(err)
			}
			absent := map[string]bool{"models/main.qnt": mode == "partial-main-absent" || mode == "absent", "models/support.qnt": mode == "partial-import-absent" || mode == "absent"}
			for p, remove := range absent {
				if remove {
					if err := os.Remove(filepath.Join(dir, p)); err != nil {
						t.Fatal(err)
					}
				}
			}
			bp, artifact := filepath.Join(dir, "instance/binding.yaml"), filepath.Join(dir, "artifacts/model.json")
			c, err := NewCompiledChecker(bp, artifact, Options{})
			if err != nil {
				t.Fatal(err)
			}
			steps := compiledSteps("deposit", 5, 5)
			report, err := c.Check(t.Context(), steps)
			if err != nil || !report.OK() {
				t.Fatalf("check: %v %v", err, report)
			}
			m, err := NewManifest(RunRecord{Compiled: artifact, Binding: c.Binding, Results: report.Actors})
			if err != nil {
				t.Fatal(err)
			}
			if m.Format != ManifestFormat || m.Model.Compiled == nil || m.Tools.Quint != "" || m.Tools.Backend != "" {
				t.Fatalf("recipe: %+v", m)
			}
			if _, err := m.Verify(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(artifact)
			if err != nil {
				t.Fatal(err)
			}
			exported := filepath.Join(t.TempDir(), "bundle")
			if err := m.Bundle(exported); err != nil {
				t.Fatal(err)
			}
			if err := m.Write(filepath.Join(exported, "run.json")); err != nil {
				t.Fatal(err)
			}
			relocated := filepath.Join(t.TempDir(), "relocated")
			if err := os.Rename(exported, relocated); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			got, err := ReadManifest(filepath.Join(relocated, "run.json"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := got.Verify(); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(got.Model.Compiled.Path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("artifact rewritten: %v", err)
			}
			replay, err := NewCompiledChecker(got.Model.Binding.Path, got.Model.Compiled.Path, Options{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := replay.Check(t.Context(), steps)
			if err != nil || !result.OK() {
				t.Fatalf("relocated check: %v %v", err, result)
			}
			cm, paths, err := got.Model.compiledSources()
			if err != nil {
				t.Fatal(err)
			}
			for i, f := range paths {
				name := filepath.Base(f.Path)
				wasAbsent := absent["models/"+name]
				if wasAbsent {
					data, err := os.ReadFile(filepath.Join(original, "models", name))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(filepath.Dir(f.Path), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(f.Path, data, 0644); err != nil {
						t.Fatal(err)
					}
					if _, err := got.Verify(); err == nil || !strings.Contains(err.Error(), "absent when judged") {
						t.Fatalf("coverage silently upgraded for %s: %v", cm.Sources[i].Path, err)
					}
					os.Remove(f.Path)
				} else {
					data, err := os.ReadFile(f.Path)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(f.Path); err != nil {
						t.Fatal(err)
					}
					if _, err := got.Verify(); err == nil || !strings.Contains(err.Error(), "present when judged") {
						t.Fatalf("coverage silently weakened for %s: %v", cm.Sources[i].Path, err)
					}
					if err := os.WriteFile(f.Path, data, 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestCompiledManifestBindsLoadedJudgment(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	bp, artifact := compiledCopy(t, "account", "account.qnt")
	checker, err := NewCompiledChecker(bp, artifact, Options{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := checker.Check(t.Context(), compiledSteps("deposit", 5, 5))
	if err != nil {
		t.Fatal(err)
	}
	record := RunRecord{Compiled: artifact, Binding: checker.Binding, Results: result.Actors}
	raw, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, append(raw, ' '), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManifest(record); err == nil || !strings.Contains(err.Error(), "artifact changed since") {
		t.Fatalf("old judgment reattributed to replaced artifact: %v", err)
	}
	if err := os.WriteFile(artifact, raw, 0644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(filepath.Dir(bp), "account.qnt")
	src, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManifest(record); err == nil || !strings.Contains(err.Error(), "source coverage changed") {
		t.Fatalf("old judgment lost source coverage: %v", err)
	}
	absent, err := NewCompiledChecker(bp, artifact, Options{})
	if err != nil {
		t.Fatal(err)
	}
	absentResult, err := absent.Check(t.Context(), compiledSteps("deposit", 5, 5))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, src, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManifest(RunRecord{Compiled: artifact, Binding: absent.Binding, Results: absentResult.Actors}); err == nil || !strings.Contains(err.Error(), "source coverage changed") {
		t.Fatalf("old judgment gained source coverage: %v", err)
	}
	if _, err := NewManifest(RunRecord{Binding: checker.Binding, Results: result.Actors, Options: Options{Quint: "must-not-run"}}); err == nil || !strings.Contains(err.Error(), "evaluation mode") {
		t.Fatalf("forgotten artifact accepted: %v", err)
	}
	if _, err := NewManifest(RunRecord{Compiled: artifact, Binding: checker.Binding, Results: []*Result{{Conforms: Pass}}}); err == nil || !strings.Contains(err.Error(), "evaluation mode") {
		t.Fatalf("source judgment mislabeled compiled: %v", err)
	}
}

func TestCompiledManifestRefusesBrokenArtifactBindingLink(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	bp, artifact := compiledCopy(t, "account", "account.qnt")
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	var cm CompiledModel
	if err := json.Unmarshal(data, &cm); err != nil {
		t.Fatal(err)
	}
	cm.Binding.Path = "different/binding.yaml"
	data, err = json.Marshal(cm)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, data, 0644); err != nil {
		t.Fatal(err)
	}
	b, err := binding.Load(bp)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewManifest(RunRecord{Compiled: artifact, Binding: b})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "out")
	if err := m.Bundle(dir); err == nil || !strings.Contains(err.Error(), "refer relatively") {
		t.Fatalf("broken portable graph accepted: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("partial publication: %v", err)
	}
}

// Constants inlined into IR cannot be changed by only relabeling the binding.
// Runtime constant slots remain legitimate parameters of that same artifact.
func TestCompiledConstantOverrideRecipe(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	bp, artifact := compiledCopy(t, "account", "account.qnt")
	compiled, err := NewCompiledChecker(bp, artifact, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.WithConstants(map[string]string{"LIMIT": "1"}); err == nil || !strings.Contains(err.Error(), "inlines constant LIMIT") {
		t.Fatalf("inlined constant changed without recompilation: %v", err)
	}
	subset, err := compiled.WithInvariants("underLimit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := subset.WithConstants(map[string]string{"LIMIT": "100"}); err != nil {
		t.Fatalf("unchanged constant after subset: %v", err)
	}
	source, err := NewChecker(bp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	source, err = source.WithConstants(map[string]string{"LIMIT": "1"})
	if err != nil {
		t.Fatal(err)
	}
	fail, err := source.Check(context.Background(), compiledSteps("deposit", 5, 5))
	if err != nil || fail.OK() {
		t.Fatalf("contrast source LIMIT1 did not reject property: %v %v", err, fail)
	}
	body, err := os.ReadFile(bp)
	if err != nil {
		t.Fatal(err)
	}
	body = bytes.Replace(body, []byte("LIMIT: '100'"), []byte("LIMIT: '{{ 100 }}'"), 1)
	if err := os.WriteFile(bp, body, 0644); err != nil {
		t.Fatal(err)
	}
	cm, err := Compile(bp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.Write(artifact); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewCompiledChecker(bp, artifact, Options{})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = runtime.WithConstants(map[string]string{"LIMIT": "1"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Check(t.Context(), compiledSteps("deposit", 5, 5))
	if err != nil || result.OK() {
		t.Fatalf("runtime constant failed to affect property: %v %v", err, result)
	}
	m, err := NewManifest(RunRecord{Compiled: artifact, Binding: runtime.Binding, Results: result.Actors})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Verify(); err != nil {
		t.Fatal(err)
	}
}

// An import-only provenance list cannot hide an existing main source from the
// staleness and source-availability checks merely by omitting its entry.
func TestCompiledManifestRequiresMainSourceProvenance(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	dir := compiledGraph(t)
	bp, artifact := filepath.Join(dir, "instance/binding.yaml"), filepath.Join(dir, "artifacts/model.json")
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	var cm CompiledModel
	if err := json.Unmarshal(data, &cm); err != nil {
		t.Fatal(err)
	}
	if len(cm.Sources) < 2 {
		t.Fatal("fixture has no transitive source")
	}
	cm.Sources = cm.Sources[1:]
	data, err = json.Marshal(cm)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, data, 0644); err != nil {
		t.Fatal(err)
	}
	b, err := binding.Load(bp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewManifest(RunRecord{Compiled: artifact, Binding: b}); err == nil || !strings.Contains(err.Error(), "main source first") {
		t.Fatalf("main provenance omission accepted: %v", err)
	}
}

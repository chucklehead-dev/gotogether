package validate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func bundleState(t *testing.T, m *Manifest) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func rehashBundleFile(t *testing.T, f *File) {
	t.Helper()
	h, err := hashFile(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	f.SHA256 = h
}

func assertBundleFailure(t *testing.T, m *Manifest, out string, want string) {
	t.Helper()
	before := bundleState(t, m)
	if err := m.Bundle(out); err == nil || want != "" && !strings.Contains(err.Error(), want) {
		t.Fatalf("Bundle error=%v, want refusal containing %q", err, want)
	}
	if got := bundleState(t, m); got != before {
		t.Fatal("failed Bundle mutated source manifest")
	}
	if _, err := os.Lstat(out); !os.IsNotExist(err) {
		t.Fatalf("failed Bundle published output: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(out))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "."+filepath.Base(out)+".stage-") {
			t.Fatalf("failed Bundle leaked stage %s", e.Name())
		}
	}
}

func TestBundleRefusesIncompleteOrChangedEvidence(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"input changed", "input missing", "binding changed", "spec changed", "import changed", "missing hash", "artifact changed", "artifact missing", "artifact unhashed", "nonregular"} {
		t.Run(kind, func(t *testing.T) {
			source := t.TempDir()
			m := layoutManifest(t, source)
			write(t, source, map[string]string{"shot.png": "original"})
			if err := m.addArtifact("shot", filepath.Join(source, "shot.png")); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "input changed":
				write(t, source, map[string]string{"run/steps.jsonl": "changed"})
			case "input missing":
				if err := os.Remove(m.Inputs[0].Path); err != nil {
					t.Fatal(err)
				}
			case "binding changed":
				write(t, source, map[string]string{"run/b.yaml": "changed"})
			case "spec changed":
				write(t, source, map[string]string{"model/m.qnt": "changed"})
			case "import changed":
				write(t, source, map[string]string{"model/sub/base.qnt": "changed"})
			case "missing hash":
				m.Model.Spec.SHA256 = ""
			case "artifact changed":
				write(t, source, map[string]string{"shot.png": "changed"})
			case "artifact missing":
				if err := os.Remove(m.Artifacts[0].Path); err != nil {
					t.Fatal(err)
				}
			case "artifact unhashed":
				m.Artifacts[0].SHA256 = ""
			case "nonregular":
				m.Inputs[0].Path = source
			}
			assertBundleFailure(t, m, filepath.Join(t.TempDir(), "out"), "")
		})
	}
}

func TestBundleRefusesUnrecordedModelDependencies(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"absolute spec", "missing import", "absolute import", "binding defs", "unrecorded resolved spec", "imports without spec"} {
		t.Run(kind, func(t *testing.T) {
			source := t.TempDir()
			m := layoutManifest(t, source)
			switch kind {
			case "absolute spec":
				write(t, source, map[string]string{"run/b.yaml": "spec: " + m.Model.Spec.Path + "\nmodule: m\n"})
				rehashBundleFile(t, m.Model.Binding)
			case "missing import":
				b, err := os.ReadFile(m.Model.Spec.Path)
				if err != nil {
					t.Fatal(err)
				}
				b = append(b, []byte("\nmodule missingUser { import Missing.* from \"./not-recorded\" }\n")...)
				if err := os.WriteFile(m.Model.Spec.Path, b, 0600); err != nil {
					t.Fatal(err)
				}
				rehashBundleFile(t, m.Model.Spec)
			case "absolute import":
				write(t, source, map[string]string{"model/m.qnt": "module m { import External.* from \"" + filepath.ToSlash(filepath.Join(source, "other.qnt")) + "\" }\n"})
				rehashBundleFile(t, m.Model.Spec)
			case "binding defs":
				write(t, source, map[string]string{"run/b.yaml": layout["run/b.yaml"] + "defs: |\n  import Extra.* from \"./external\"\n"})
				rehashBundleFile(t, m.Model.Binding)
			case "unrecorded resolved spec":
				b, err := os.ReadFile(m.Model.Spec.Path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(source, "model", "copy.qnt"), b, 0600); err != nil {
					t.Fatal(err)
				}
				write(t, source, map[string]string{"run/b.yaml": "spec: ../model/copy.qnt\nmodule: m\n"})
				rehashBundleFile(t, m.Model.Binding)
			case "imports without spec":
				m.Model.Spec = nil
				m.Model.Binding = nil
			}
			assertBundleFailure(t, m, filepath.Join(t.TempDir(), "out"), "")
		})
	}
}

func TestBundlePublicationPreservesReceiverOnCopyFailure(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	m := layoutManifest(t, source)
	// The original basename fits a filesystem entry; the second copy needs a
	// collision prefix and cannot fit. The first copy already exists in staging.
	name := strings.Repeat("x", 255)
	for _, dir := range []string{"one", "two"} {
		p := filepath.Join(source, dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(dir), 0600); err != nil {
			t.Skipf("filesystem cannot create long control filename: %v", err)
		}
		f, err := hashed(p)
		if err != nil {
			t.Fatal(err)
		}
		m.Inputs = append(m.Inputs, Input{File: *f, Format: "steps"})
	}
	assertBundleFailure(t, m, filepath.Join(t.TempDir(), "out"), "")
}

func TestBundleNoOverwrite(t *testing.T) {
	t.Parallel()
	m := layoutManifest(t, t.TempDir())
	out := filepath.Join(t.TempDir(), "out")
	if err := os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}
	before := bundleState(t, m)
	if err := m.Bundle(out); err == nil {
		t.Fatal("overwrote existing empty directory")
	}
	if bundleState(t, m) != before {
		t.Fatal("nooverwrite refusal mutated receiver")
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 0 {
		t.Fatalf("existing directory changed: %v %v", entries, err)
	}
}

func TestBundleDuplicateNamesAndNestedModelsRelocate(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	m := layoutManifest(t, source)
	other := layoutManifest(t, filepath.Join(source, "other"))
	other.Model.Instance = "instance_module"
	m.Inputs = append(m.Inputs, Input{File: other.Inputs[0].File, Format: "steps", Model: &other.Model})
	write(t, source, map[string]string{"convert.yaml": "format: gotogether.convert/1\n"})
	f, err := hashed(filepath.Join(source, "convert.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m.Inputs[1].Model.Convert = f
	wanted := m.copy()
	out := filepath.Join(t.TempDir(), "bundle")
	if err := m.Bundle(out); err != nil {
		t.Fatal(err)
	}
	if m.Inputs[0].Path == m.Inputs[1].Path {
		t.Fatal("same-basename inputs collided")
	}
	if m.Inputs[1].Model.Instance != "instance_module" || !reflect.DeepEqual(m.Verdict, wanted.Verdict) || !reflect.DeepEqual(m.Check, wanted.Check) {
		t.Fatal("export changed semantics")
	}
	if err := m.Write(filepath.Join(out, "run.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(out, moved); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManifest(filepath.Join(moved, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := got.verifyBundle(moved); err != nil {
		t.Fatal(err)
	}
	for _, f := range got.bundleFiles() {
		if !within(moved, *f.path) {
			t.Fatalf("escaped relocated bundle: %s", *f.path)
		}
	}
}

func TestBundleMaterializesSymlinkBytes(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	m := layoutManifest(t, source)
	link := filepath.Join(source, "linked.jsonl")
	if err := os.Symlink(m.Inputs[0].Path, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	m.Inputs[0].Path = link
	out := filepath.Join(t.TempDir(), "bundle")
	if err := m.Bundle(out); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(m.Inputs[0].Path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("exported symlink instead of bytes: %v %v", info, err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if err := m.verifyBundle(out); err != nil {
		t.Fatal(err)
	}
}

func TestBundleRefusesUnboundITFDefsImports(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	m := layoutManifest(t, source)
	model := m.Model
	model.Binding = nil
	m.Model = RunModel{Invariants: []string{}}
	raw := `{"vars":[],"states":[{}],"#meta":{"gotogether":{"spec":"obsolete-provenance.qnt","module":"m","defs":"import External.* from \"./unrecorded\""}}}`
	write(t, source, map[string]string{"trace.itf.json": raw})
	f, err := hashed(filepath.Join(source, "trace.itf.json"))
	if err != nil {
		t.Fatal(err)
	}
	m.Inputs = []Input{{File: *f, Format: "itf", Model: &model}}
	assertBundleFailure(t, m, filepath.Join(t.TempDir(), "out"), "metadata defs")
	for _, tc := range []struct{ name, raw string }{
		{"empty first", `{"#meta":{},` + strings.TrimPrefix(raw, "{")},
		{"empty last", strings.TrimSuffix(raw, "}") + `,"#meta":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(m.Inputs[0].Path, []byte(tc.raw), 0600); err != nil {
				t.Fatal(err)
			}
			rehashBundleFile(t, &m.Inputs[0].File)
			assertBundleFailure(t, m, filepath.Join(t.TempDir(), "out"), "duplicate #meta")
		})
	}

}

func TestBundlePreservesBoundITFBigIntegers(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	m := layoutManifest(t, source)
	raw, err := os.ReadFile(filepath.Join("..", "itf", "testdata", "bignumber.rust.itf.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(source, "bound.itf.json")
	if err := os.WriteFile(p, raw, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := hashed(p)
	if err != nil {
		t.Fatal(err)
	}
	model := m.Model
	m.Inputs = []Input{{File: *f, Format: "itf", Model: &model}}
	out := filepath.Join(t.TempDir(), "bundle")
	if err := m.Bundle(out); err != nil {
		t.Fatalf("export reinterpreted already checked bound ITF bytes: %v", err)
	}
	got, err := os.ReadFile(m.Inputs[0].Path)
	if err != nil || string(got) != string(raw) {
		t.Fatalf("export changed ITF evidence: %v", err)
	}
}

func TestBundleRefusesUnresolvedInputFormats(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"", "auto", "unknown"} {
		t.Run(format, func(t *testing.T) {
			m := layoutManifest(t, t.TempDir())
			m.Inputs[0].Format = format
			assertBundleFailure(t, m, filepath.Join(t.TempDir(), "out"), "actual input format")
		})
	}
}

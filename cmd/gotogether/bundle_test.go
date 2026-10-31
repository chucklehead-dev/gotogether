package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/schemas"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The public handoff exports existing judgments, including failures and absent
// judgments. Removing the original files prevents a same-machine fallback from
// making an incomplete export appear portable.
func TestBundleCLI(t *testing.T) {
	skip.Quint(t)
	base := t.TempDir()
	cli := filepath.Join(base, "gotogether")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", cli, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	invoke := func(t *testing.T, cwd string, want int, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), cli, args...)
		cmd.Dir = cwd
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				t.Fatalf("start CLI: %v", err)
			}
		}
		if code != want {
			t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, &out, &stderr)
		}
		return out.Bytes()
	}
	source := filepath.Join(base, "original")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"account.yaml", "account.qnt"} {
		if err := os.WriteFile(filepath.Join(source, name), mustRead(t, filepath.Join("../../validate/testdata", name)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name, verdict string
		code          int
		steps         []qtrace.Step
	}{
		{"pass", "pass", 0, []qtrace.Step{{Actor: "account", Recorder: "capture", Thread: "client", Seq: 1, Action: "deposit", Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}}},
		{"fail", "fail", 1, []qtrace.Step{{Actor: "account", Recorder: "capture", Thread: "client", Seq: 1, Action: "withdraw", Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(-5)}}}},
		{"empty", "no verdict", 1, nil},
	}
	type exported struct {
		path string
		want *validate.Manifest
	}
	var exports []exported
	for _, tc := range cases {
		log := filepath.Join(source, tc.name+".jsonl")
		writeBundleSteps(t, log, tc.steps)
		manifest := filepath.Join(source, tc.name+".json")
		raw := invoke(t, base, tc.code, "validate", "-binding", filepath.Join(source, "account.yaml"), "-manifest", manifest, "-dir", filepath.Join(base, "work-"+tc.name), "-json", log)
		var document validate.Document
		if err := json.Unmarshal(raw, &document); err != nil || document.Verdict != tc.verdict {
			t.Fatalf("original %s judgment: %v\n%s", tc.name, err, raw)
		}
		original, err := validate.ReadManifest(manifest)
		if err != nil {
			t.Fatal(err)
		}
		before := bundleTree(t, source)
		out := filepath.Join(base, "export-"+tc.name)
		got := invoke(t, base, 0, "bundle", "-out", out, "-json", manifest)
		written := mustRead(t, filepath.Join(out, "run.json"))
		if !bytes.Equal(got, written) {
			t.Fatalf("%s: -json is not the exact native saved manifest\nstdout: %s\nsaved: %s", tc.name, got, written)
		}
		var stored validate.Manifest
		if err := json.Unmarshal(got, &stored); err != nil || stored.Format != validate.ManifestFormat {
			t.Fatalf("%s: bundle emitted another format: %v\n%s", tc.name, err, got)
		}
		for _, path := range bundleManifestPaths(&stored) {
			if !filepath.IsLocal(filepath.FromSlash(path)) {
				t.Fatalf("%s: export names a file outside itself: %q", tc.name, path)
			}
		}
		resolved, err := validate.ReadManifest(filepath.Join(out, "run.json"))
		if err != nil {
			t.Fatal(err)
		}
		if warnings, err := resolved.Verify(); err != nil || len(warnings) != 0 {
			t.Fatalf("%s export integrity: %v %v", tc.name, warnings, err)
		}
		if !reflect.DeepEqual(original.Receipt, resolved.Receipt) || !reflect.DeepEqual(original.Verdict, resolved.Verdict) || !reflect.DeepEqual(original.Check, resolved.Check) || original.Tools != resolved.Tools ||
			original.Model.Module != resolved.Model.Module || original.Model.Instance != resolved.Model.Instance || !reflect.DeepEqual(original.Model.Constants, resolved.Model.Constants) || !reflect.DeepEqual(original.Model.Invariants, resolved.Model.Invariants) {
			t.Fatalf("%s: export changed the recorded judgment, model instance or check scope", tc.name)
		}
		if original.Model.Binding.SHA256 != resolved.Model.Binding.SHA256 || original.Model.Spec.SHA256 != resolved.Model.Spec.SHA256 || original.Inputs[0].SHA256 != resolved.Inputs[0].SHA256 {
			t.Fatalf("%s: export rewrote evidence bytes", tc.name)
		}
		if !reflect.DeepEqual(before, bundleTree(t, source)) {
			t.Fatalf("%s: exporting changed original evidence", tc.name)
		}
		exports = append(exports, exported{out, original})
	}

	// Refusal paths return a process error, no judgment document and no export.
	for _, tc := range []struct {
		name string
		edit func(*testing.T, string, *validate.Manifest)
	}{
		{"changed input", func(t *testing.T, dir string, m *validate.Manifest) { bundleWrite(t, m.Inputs[0].Path, "changed\n") }},
		{"missing input", func(t *testing.T, dir string, m *validate.Manifest) {
			if err := os.Remove(m.Inputs[0].Path); err != nil {
				t.Fatal(err)
			}
		}},
		{"absolute binding spec", func(t *testing.T, dir string, m *validate.Manifest) {}},
		{"missing artifact", func(t *testing.T, dir string, m *validate.Manifest) {
			if err := os.Remove(m.Artifacts[0].Path); err != nil {
				t.Fatal(err)
			}
		}},
		{"changed artifact", func(t *testing.T, dir string, m *validate.Manifest) {
			bundleWrite(t, m.Artifacts[0].Path, "other screenshot")
		}},
		{"unhashed artifact", func(t *testing.T, dir string, m *validate.Manifest) {
			m.Artifacts[0].SHA256 = ""
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(base, strings.ReplaceAll(tc.name, " ", "-"))
			m := bundleRefusalFixture(t, dir, tc.name == "absolute binding spec")
			tc.edit(t, dir, m)
			manifest := filepath.Join(dir, "source.json")
			if err := m.Write(manifest); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(base, filepath.Base(dir)+"-export")
			before := bundleTree(t, base)
			if raw := invoke(t, base, 2, "bundle", "-out", out, "-json", manifest); len(raw) != 0 {
				t.Fatalf("refused export emitted success or judgment JSON: %s", raw)
			}
			if _, err := os.Lstat(out); !os.IsNotExist(err) {
				t.Fatalf("refused export left destination: %v", err)
			}
			if !reflect.DeepEqual(before, bundleTree(t, base)) {
				t.Fatal("refusal changed files or left staging debris")
			}
		})
	}

	// Existing destinations include empty directories; none is an overwrite grant.
	for _, empty := range []bool{true, false} {
		out := filepath.Join(base, "existing")
		if err := os.MkdirAll(out, 0o755); err != nil {
			t.Fatal(err)
		}
		if !empty {
			bundleWrite(t, filepath.Join(out, "keep.txt"), "retain this evidence")
		}
		before := bundleTree(t, base)
		invoke(t, base, 2, "bundle", "-out", out, filepath.Join(source, "fail.json"))
		if !reflect.DeepEqual(before, bundleTree(t, base)) {
			t.Fatal("existing destination was modified")
		}
	}

	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	for i, ex := range exports {
		moved := filepath.Join(base, "relocated-"+cases[i].name)
		if err := os.Rename(ex.path, moved); err != nil {
			t.Fatal(err)
		}
		manifest := filepath.Join(moved, "run.json")
		out := invoke(t, base, cases[i].code, "validate", "-replay", manifest, "-manifest", filepath.Join(moved, "replayed.json"), "-dir", filepath.Join(base, "replay-"+cases[i].name), "-json")
		if problems, err := schemas.Check(schemas.Report(), out); err != nil || len(problems) != 0 {
			t.Fatalf("replay native report: %v %v", problems, err)
		}
		var doc validate.Document
		if err := json.Unmarshal(out, &doc); err != nil || doc.Verdict != cases[i].verdict {
			t.Fatalf("relocated replay: %v\n%s", err, out)
		}
		replayed, err := validate.ReadManifest(filepath.Join(moved, "replayed.json"))
		if err != nil {
			t.Fatal(err)
		}
		if same, why := ex.want.Verdict.Same(replayed.Verdict); !same {
			t.Fatalf("relocated verdict changed: %s", why)
		}
		if cases[i].name == "fail" && (len(doc.Actors) != 1 || len(doc.Actors[0].Failures) != 1 || doc.Actors[0].Failures[0].Record == nil || doc.Actors[0].Failures[0].Record.Seq != 1 || doc.Actors[0].Failures[0].Record.Recorder != "capture") {
			t.Fatalf("replay lost captured failure identity: %+v", doc.Actors)
		}
	}
}

func writeBundleSteps(t *testing.T, path string, steps []qtrace.Step) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := qtrace.WriteJSONL(f, steps); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func bundleRefusalFixture(t *testing.T, dir string, absoluteSpec bool) *validate.Manifest {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"account.yaml", "account.qnt"} {
		bundleWrite(t, filepath.Join(dir, name), string(mustRead(t, filepath.Join("../../validate/testdata", name))))
	}
	if absoluteSpec {
		p := filepath.Join(dir, "account.yaml")
		bundleWrite(t, p, strings.Replace(string(mustRead(t, p)), "spec: account.qnt", "spec: "+filepath.Join(dir, "account.qnt"), 1))
	}
	log := filepath.Join(dir, "steps.jsonl")
	writeBundleSteps(t, log, nil)
	shot := filepath.Join(dir, "shot.txt")
	bundleWrite(t, shot, "captured screenshot")
	b, err := binding.Load(filepath.Join(dir, "account.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := validate.NewManifest(validate.RunRecord{Options: validate.Options{Quint: "no-such-quint"}, Binding: b, Inputs: []validate.RunInput{{Path: log, Format: "steps"}}, Artifacts: []validate.Artifact{{Name: "screenshot", Path: shot}}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func bundleWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func bundleTree(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			out[rel+"/"] = sha256.Sum256(nil)
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = sha256.Sum256(body)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func bundleManifestPaths(m *validate.Manifest) []string {
	var out []string
	model := func(md *validate.RunModel) {
		for _, f := range []*validate.File{md.Binding, md.Spec, md.Convert} {
			if f != nil {
				out = append(out, f.Path)
			}
		}
		for _, f := range md.Imports {
			out = append(out, f.Path)
		}
	}
	model(&m.Model)
	for _, input := range m.Inputs {
		out = append(out, input.Path)
		if input.Model != nil {
			model(input.Model)
		}
	}
	for _, artifact := range m.Artifacts {
		out = append(out, artifact.Path)
	}
	return out
}

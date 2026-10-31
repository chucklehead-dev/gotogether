package main

import (
	"bytes"
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

// Compile is the only phase allowed to need Quint. Each following public CLI
// command runs with forbidden Quint/Node executables on its PATH. Removing the
// original tree then moving the bundle prevents a machine-local fallback from
// making an incomplete export appear portable.
func TestCompiledManifestBundleCLI(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	base := t.TempDir()
	cli := filepath.Join(base, "gotogether")
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", cli, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	blocked := filepath.Join(base, "forbidden-tools")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	called := filepath.Join(base, "forbidden-tool-called")
	for _, name := range []string{"quint", "node", "npx"} {
		bundleWrite(t, filepath.Join(blocked, name), "#!/bin/sh\nprintf '%s\\n' \"$0\" >> \"$GTG_FORBIDDEN_TOOL_CALLS\"\nexit 91\n")
		if err := os.Chmod(filepath.Join(blocked, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	invoke := func(t *testing.T, noTools bool, want int, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), cli, args...)
		cmd.Dir = base
		if noTools {
			cmd.Env = append(os.Environ(), "PATH="+blocked, "GTG_FORBIDDEN_TOOL_CALLS="+called)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
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
			t.Fatalf("%v: exit %d want %d\nstdout: %s\nstderr: %s", args, code, want, &stdout, &stderr)
		}
		if noTools {
			if body, err := os.ReadFile(called); !os.IsNotExist(err) {
				t.Fatalf("compiled workflow invoked forbidden external tools: %v %s", err, body)
			}
		}
		return stdout.Bytes()
	}
	templates := map[string]string{}
	for _, model := range []string{"account", "rr"} {
		dir := filepath.Join(base, "template-"+model)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, ext := range []string{".yaml", ".qnt"} {
			bundleWrite(t, filepath.Join(dir, model+ext), string(mustRead(t, filepath.Join("../../validate/testdata", model+ext))))
		}
		invoke(t, false, 0, "compile", "-binding", filepath.Join(dir, model+".yaml"), "-o", filepath.Join(dir, model+".model.json"))
		templates[model] = dir
	}
	// Preserve lexical paths across binding, artifact, and absent source imports.
	// The compiled file names its binding relative to its own directory, while
	// its source paths are relative to the binding directory.
	nested := filepath.Join(base, "template-nested")
	for _, rel := range []string{"app/config/sub/deep/artifacts", "models", "lib"} {
		if err := os.MkdirAll(filepath.Join(nested, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bundleWrite(t, filepath.Join(nested, "app/config/sub/deep/account.yaml"), strings.Replace(string(mustRead(t, filepath.Join(templates["account"], "account.yaml"))), "spec: account.qnt", "spec: ../../../../models/main.qnt", 1))
	mainSource := string(mustRead(t, filepath.Join(templates["account"], "account.qnt")))
	mainSource = strings.Replace(mainSource, "module account {", "module account {\n  import helper.* from \"../lib/helper\"", 1)
	bundleWrite(t, filepath.Join(nested, "models/main.qnt"), mainSource)
	bundleWrite(t, filepath.Join(nested, "lib/helper.qnt"), "module helper { pure val marker = 1 }\n")
	invoke(t, false, 0, "compile", "-binding", filepath.Join(nested, "app/config/sub/deep/account.yaml"), "-o", filepath.Join(nested, "app/config/sub/deep/artifacts/account.model.json"))
	type savedRun struct {
		name, model, verdict string
		code                 int
		source, bundle       string
		original             *validate.Manifest
		artifact             []byte
	}
	var runs []savedRun
	for _, tc := range []struct {
		name, model, verdict string
		code                 int
		absentSource         bool
		nested               bool
		steps                []qtrace.Step
	}{
		{"pass", "account", "pass", 0, false, false, []qtrace.Step{{Actor: "account", Recorder: "capture", Thread: "client", Seq: 1, Action: "deposit", Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}}},
		{"fail", "account", "fail", 1, false, false, []qtrace.Step{{Actor: "account", Recorder: "capture", Thread: "client", Seq: 1, Action: "withdraw", Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(-5)}}}},
		{"unsupported", "rr", "no verdict", 1, false, false, []qtrace.Step{{Actor: "rr", Recorder: "capture", Thread: "client", Seq: 1, Action: "place", Obs: map[string]any{"s2": int64(0)}}}},
		{"source-absent", "account", "pass", 0, true, false, []qtrace.Step{{Actor: "account", Recorder: "capture", Thread: "client", Seq: 1, Action: "deposit", Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}}},
		{"empty", "account", "no verdict", 1, false, false, nil},
		{"invariant-subset", "account", "pass", 0, false, false, []qtrace.Step{{Actor: "account", Recorder: "capture", Thread: "client", Seq: 1, Action: "deposit", Args: map[string]any{"amount": int64(110)}, Obs: map[string]any{"balance": int64(110)}}}},
		{"nested-source-absent", "account", "pass", 0, true, true, []qtrace.Step{{Actor: "account", Recorder: "capture", Thread: "client", Seq: 1, Action: "deposit", Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := filepath.Join(base, "original-"+tc.name)
			if err := os.Mkdir(source, 0o755); err != nil {
				t.Fatal(err)
			}
			from := templates[tc.model]
			bindingRel, artifactRel := tc.model+".yaml", tc.model+".model.json"
			sourcePaths := []string{tc.model + ".qnt"}
			if tc.nested {
				from = nested
				bindingRel, artifactRel = "app/config/sub/deep/account.yaml", "app/config/sub/deep/artifacts/account.model.json"
				sourcePaths = []string{"models/main.qnt", "lib/helper.qnt"}
			}
			compiledManifestCopyTree(t, from, source)
			if tc.absentSource {
				for _, rel := range sourcePaths {
					if err := os.Remove(filepath.Join(source, rel)); err != nil {
						t.Fatal(err)
					}
				}
			}
			bind, artifact := filepath.Join(source, bindingRel), filepath.Join(source, artifactRel)
			log, manifest := filepath.Join(source, "steps.jsonl"), filepath.Join(source, "manifest.json")
			writeBundleSteps(t, log, tc.steps)
			args := []string{"validate", "-binding", bind, "-compiled", artifact, "-manifest", manifest, "-dir", filepath.Join(base, "judge-"+tc.name), "-json"}
			if tc.name == "invariant-subset" {
				args = append(args, "-invariants", "nonNegative")
			}
			raw := invoke(t, true, tc.code, append(args, log)...)
			var doc validate.Document
			if err := json.Unmarshal(raw, &doc); err != nil || doc.Verdict != tc.verdict {
				t.Fatalf("original judgment: %v\n%s", err, raw)
			}
			if tc.absentSource && !strings.Contains(string(raw), "unchecked") {
				t.Fatalf("source-free judgment hid unchecked source provenance: %s", raw)
			}
			for _, actor := range doc.Actors {
				if actor.JudgedBy != "evaluator" {
					t.Fatalf("compiled judgment lost evaluator-only provenance: %+v", actor)
				}
			}
			original, err := validate.ReadManifest(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "invariant-subset" && !reflect.DeepEqual(original.Model.Invariants, []string{"nonNegative"}) {
				t.Fatalf("manifest lost effective invariant selection: %+v", original.Model.Invariants)
			}
			if original.Model.Compiled == nil || original.Model.Compiled.SHA256 == "" || original.Tools.Quint != "" || original.Tools.Backend != "" {
				t.Fatalf("compiled replay mode/provenance missing or attributed to Quint: %+v", original)
			}
			before := bundleTree(t, source)
			out := filepath.Join(base, "export-"+tc.name)
			bundleJSON := invoke(t, true, 0, "bundle", "-out", out, "-json", manifest)
			if !bytes.Equal(bundleJSON, mustRead(t, filepath.Join(out, "run.json"))) {
				t.Fatal("bundle emitted a second representation rather than the saved native manifest")
			}
			var stored validate.Manifest
			if err := json.Unmarshal(bundleJSON, &stored); err != nil || stored.Format != validate.ManifestFormat || stored.Model.Compiled == nil || !filepath.IsLocal(filepath.FromSlash(stored.Model.Compiled.Path)) {
				t.Fatalf("portable compiled reference: %v\n%s", err, bundleJSON)
			}
			resolved, err := validate.ReadManifest(filepath.Join(out, "run.json"))
			if err != nil {
				t.Fatal(err)
			}
			if warnings, err := resolved.Verify(); err != nil || len(warnings) != 0 {
				t.Fatalf("export integrity: %v %v", warnings, err)
			}
			if !reflect.DeepEqual(original.Receipt, resolved.Receipt) || !reflect.DeepEqual(original.Verdict, resolved.Verdict) || !reflect.DeepEqual(original.Check, resolved.Check) || original.Tools != resolved.Tools || original.Model.Compiled.SHA256 != resolved.Model.Compiled.SHA256 {
				t.Fatal("bundle changed native verdict, receipt, replay scope or compiled digest")
			}
			if !bytes.Equal(mustRead(t, artifact), mustRead(t, resolved.Model.Compiled.Path)) || !reflect.DeepEqual(before, bundleTree(t, source)) {
				t.Fatal("bundle rewrote compiled bytes or original evidence")
			}
			runs = append(runs, savedRun{tc.name, tc.model, tc.verdict, tc.code, source, out, original, mustRead(t, artifact)})
		})
	}
	if len(runs) != 7 {
		t.Fatal("a compiled capture failed; no portable-replay claim can follow")
	}
	// A manifest's pinned artifact, binding and present source are evidence, not
	// optional rediscovery hints. Refusals must not produce a PASS document or an
	// export destination. Restore each file before the next independent control.
	for _, tc := range []struct{ name, path string }{
		{"compiled", runs[0].original.Model.Compiled.Path},
		{"binding", runs[0].original.Model.Binding.Path},
		{"source", runs[0].original.Model.Spec.Path},
		{"input", runs[0].original.Inputs[0].Path},
	} {
		t.Run("changed-"+tc.name, func(t *testing.T) {
			old := mustRead(t, tc.path)
			bundleWrite(t, tc.path, string(old)+"\n ")
			defer func() { bundleWrite(t, tc.path, string(old)) }()
			manifest := filepath.Join(runs[0].source, "manifest.json")
			out := invoke(t, true, 2, "validate", "-replay", manifest, "-json", "-dir", filepath.Join(base, "refused-"+tc.name))
			var doc validate.Document
			if json.Unmarshal(out, &doc) == nil && doc.Verdict == "pass" {
				t.Fatalf("changed evidence became PASS: %s", out)
			}
			dest := filepath.Join(base, "refused-export-"+tc.name)
			before := bundleTree(t, base)
			invoke(t, true, 2, "bundle", "-out", dest, manifest)
			if _, err := os.Lstat(dest); !os.IsNotExist(err) || !reflect.DeepEqual(before, bundleTree(t, base)) {
				t.Fatal("refused compiled export changed files or left destination/staging")
			}
		})
	}
	// A search is still unsupported by compiled validation; enabling manifests
	// must not introduce a source/Quint fallback or a spurious recorded success.
	invoke(t, true, 2, "validate", "-binding", runs[0].original.Model.Binding.Path, "-compiled", runs[0].original.Model.Compiled.Path, "-linearize", "-manifest", filepath.Join(base, "search.json"), "-json", runs[0].original.Inputs[0].Path)
	if _, err := os.Stat(filepath.Join(base, "search.json")); !os.IsNotExist(err) {
		t.Fatal("unsupported compiled search recorded a manifest")
	}
	itfPath, err := filepath.Abs("../../itf/testdata/bignumber.rust.itf.json")
	if err != nil {
		t.Fatal(err)
	}
	itfManifest := filepath.Join(base, "unsupported-itf.json")
	invoke(t, true, 2, "validate", "-binding", runs[0].original.Model.Binding.Path, "-compiled", runs[0].original.Model.Compiled.Path, "-manifest", itfManifest, "-json", itfPath)
	if _, err := os.Stat(itfManifest); !os.IsNotExist(err) {
		t.Fatal("unsupported compiled ITF recorded a manifest")
	}
	for _, run := range runs {
		if err := os.RemoveAll(run.source); err != nil {
			t.Fatal(err)
		}
	}
	for _, run := range runs {
		t.Run("relocated-"+run.name, func(t *testing.T) {
			moved := filepath.Join(base, "relocated-"+run.name)
			if err := os.Rename(run.bundle, moved); err != nil {
				t.Fatal(err)
			}
			manifest := filepath.Join(moved, "run.json")
			newManifest := filepath.Join(moved, "replayed.json")
			if strings.Contains(run.name, "source-absent") {
				loaded, err := validate.ReadManifest(manifest)
				if err != nil {
					t.Fatal(err)
				}
				b, err := binding.Load(loaded.Model.Binding.Path)
				if err != nil {
					t.Fatal(err)
				}
				// An absent source is an explicit unchecked boundary, not a
				// license to accept different source text that appears later.
				if err := os.MkdirAll(filepath.Dir(b.SpecPath()), 0o755); err != nil {
					t.Fatal(err)
				}
				bundleWrite(t, b.SpecPath(), "module unrelated {}\n")
				out := invoke(t, true, 2, "validate", "-replay", manifest, "-json", "-dir", filepath.Join(base, "late-stale-source"))
				var stale validate.Document
				if json.Unmarshal(out, &stale) == nil && stale.Verdict == "pass" {
					t.Fatal("new stale source became a pass")
				}
				if err := os.Remove(b.SpecPath()); err != nil {
					t.Fatal(err)
				}
			}
			raw := invoke(t, true, run.code, "validate", "-replay", manifest, "-manifest", newManifest, "-dir", filepath.Join(base, "replay-"+run.name), "-json")
			if problems, err := schemas.Check(schemas.Report(), raw); err != nil || len(problems) != 0 {
				t.Fatalf("replay report schema: %v %v", problems, err)
			}
			var doc validate.Document
			if err := json.Unmarshal(raw, &doc); err != nil || doc.Verdict != run.verdict {
				t.Fatalf("relocated compiled replay verdict: %v\n%s", err, raw)
			}
			if run.name == "source-absent" && !strings.Contains(string(raw), "unchecked") {
				t.Fatalf("portable replay hid absent-source limitation: %s", raw)
			}
			if run.name == "unsupported" && !strings.Contains(doc.Reason, "needs quint") {
				t.Fatalf("unsupported evaluation lost its no-verdict reason: %s", raw)
			}
			replayed, err := validate.ReadManifest(newManifest)
			if err != nil {
				t.Fatal(err)
			}
			if same, why := run.original.Verdict.Same(replayed.Verdict); !same {
				t.Fatalf("relocated native verdict changed: %s", why)
			}
			if replayed.Model.Compiled == nil || !bytes.Equal(run.artifact, mustRead(t, replayed.Model.Compiled.Path)) || replayed.Tools.Quint != "" || replayed.Tools.Backend != "" {
				t.Fatal("replay dropped compiled provenance or changed the selected evaluator")
			}
			if p := compiledPiece(replayed.Model.Compiled.Path); p.skew != "" {
				t.Fatalf("relocated compiled artifact's own binding reference is stale: %+v", p)
			}
			if run.name == "fail" && (len(doc.Actors) != 1 || len(doc.Actors[0].Failures) != 1 || doc.Actors[0].Failures[0].Record == nil || doc.Actors[0].Failures[0].Record.Recorder != "capture" || doc.Actors[0].Failures[0].Record.Seq != 1) {
				t.Fatalf("compiled replay lost captured failure identity: %+v", doc.Actors)
			}
		})
	}
}

func compiledManifestCopyTree(t *testing.T, from, to string) {
	t.Helper()
	if err := filepath.WalkDir(from, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(to, rel)), 0o755); err != nil {
			return err
		}
		bundleWrite(t, filepath.Join(to, rel), string(mustRead(t, path)))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

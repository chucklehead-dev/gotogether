package validate

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
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
		{"account.qnt", "is not the one the run was judged on", false},
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
		case !c.warn && (err == nil || !strings.Contains(err.Error(), c.says) || !strings.Contains(err.Error(), filepath.Join(dir, c.file)) || !strings.Contains(err.Error(), "restore it")):
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
	if v.Actors[2].Action != "deposit" || v.Actors[1].Action != "" {
		t.Errorf("the actors' failing actions: %+v", v.Actors)
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
	if same, note := v.Same(w); !same || note != "" {
		t.Errorf("a ref made another verdict: %v %q", same, note)
	}
	// reasons worded otherwise (a newer gotogether's) are the same kind
	w.Reason, w.Actors[2].Reason = "no verdict (steps lost)", "mismatch after the deposit"
	if same, note := v.Same(w); !same || !strings.Contains(note, "worded otherwise") {
		t.Errorf("a reworded reason: %v %q", same, note)
	}
	w.Reason = v.Reason // an actor's alone reworded
	if same, note := v.Same(w); !same || !strings.Contains(note, "worded otherwise") {
		t.Errorf("an actor's reworded reason: %v %q", same, note)
	}
	w.Actors[2].Reason = "rejects deposit"
	if same, _ := v.Same(w); same {
		t.Error("a rejection is the same verdict as a mismatch")
	}
	w = VerdictOf([]*Result{pass, none, fail}, nil)
	w.Failing.Seq = 3
	if same, _ := v.Same(w); same {
		t.Error("another failing step is the same verdict")
	}
	viol := func(inv string) RunVerdict {
		return RunVerdict{Verdict: Fail, Reason: "violates " + inv, Actors: []ActorVerdict{{Actor: "a", Verdict: Fail, Reason: "violates " + inv}}}
	}
	if same, _ := viol("underLimit").Same(viol("nonNegative")); same {
		t.Error("two invariants' violations are one verdict")
	}
}

// Two verdicts of one kind differ in the cause of incomplete or no
// verdict, and in the action any failing actor fails at, not only the
// first's (the G1 review: each replayed as reproduced).
func TestVerdictSameCauseAndActors(t *testing.T) {
	t.Parallel()
	one := func(verdict Verdict, reason string) RunVerdict {
		return RunVerdict{Verdict: verdict, Reason: reason, Actors: []ActorVerdict{{Actor: "a", Verdict: verdict, Reason: reason}}}
	}
	for _, c := range [][2]string{
		{"incomplete (no settle)", "incomplete (pending calls)"},
		{"incomplete (no eventual val)", "incomplete (eventual val false)"},
	} {
		if same, note := one(Fail, c[0]).Same(one(Fail, c[1])); same || !strings.Contains(note, c[1]) {
			t.Errorf("%s and %s: %v %q", c[0], c[1], same, note)
		}
	}
	for _, c := range [][2]string{
		{"no verdict (lost tail)", "no verdict (uncollected)"},
		{"no verdict (lost steps)", "no verdict (dropped steps)"},
		{"no verdict (linearize budget)", "no verdict (lost tail)"},
	} {
		if same, _ := one(NotEvaluated, c[0]).Same(one(NotEvaluated, c[1])); same {
			t.Errorf("%s and %s are one verdict", c[0], c[1])
		}
	}
	// every cause gotogether states is its own
	known := []string{"pending calls", "no settle", "no eventual val", "eventual val false", "lost tail", "uncollected",
		"dropped steps", "lost steps", "linearize budget", "snapshot overlap", "conformance n/a", "ITF trace"}
	seen := map[string]string{}
	for _, c := range known {
		k := causeKind("(" + c + ")")
		if o, ok := seen[k]; ok {
			t.Errorf("%q and %q are one cause, %s", o, c, k)
		}
		seen[k] = c
	}
	// a second failing actor's failing action
	two := func(first, second string) RunVerdict {
		return RunVerdict{Verdict: Fail, Reason: "mismatch after " + first,
			Failing: &FailingStep{Actor: "a", Recorder: "r", Seq: 1, Action: first},
			Actors:  []ActorVerdict{{"a", Fail, "mismatch after " + first, first}, {"b", Fail, "mismatch after " + second, second}}}
	}
	if same, note := two("deposit", "withdraw").Same(two("deposit", "transfer")); same || !strings.Contains(note, "actor b fails at withdraw") {
		t.Errorf("a second actor's failing action: %v %q", same, note)
	}
	// a manifest written before Action: the reason's action
	old := two("deposit", "withdraw")
	old.Actors[1].Action = ""
	if same, _ := old.Same(two("deposit", "withdraw")); !same {
		t.Error("an older manifest's actor, failing at the same action, made another verdict")
	}
	if same, _ := old.Same(two("deposit", "transfer")); same {
		t.Error("an older manifest's actor, failing at another action, is the same verdict")
	}
	// reworded, the action kept: the same verdict
	w := two("deposit", "withdraw")
	w.Actors[1].Reason = "mismatch after the withdraw step"
	if same, note := two("deposit", "withdraw").Same(w); !same || !strings.Contains(note, "worded otherwise") {
		t.Errorf("a reworded reason: %v %q", same, note)
	}
}

// write writes files (path relative to dir: content) under dir.
func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		p = filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A model of a binding whose spec is beside it in another directory,
// importing an instance with a string constant, a file by an import
// split over lines, and a file that imports another (and commented-out
// imports, and a string that reads like one, which import nothing).
var layout = map[string]string{
	"run/b.yaml": "spec: ../model/m.qnt\nmodule: m\nconstants: { N: '3' }\n",
	"model/m.qnt": "module m {\n  /*\n  import extra.* from \"./extra\"\n  */\n  // import extra.* from \"./extra\"\n" +
		"  import inst(N = Set(\"u\")) as I from \"./inst\"\n  import split.*\n    from\n    \"./split\"\n" +
		"  pure val s = \"import extra.* from \"\n  import lib.* from \"./lib\"\n}\n",
	"model/extra.qnt":    "module extra {}\n",
	"model/inst.qnt":     "module inst {\n  const N: Set[str]\n}\n",
	"model/split.qnt":    "module split {}\n",
	"model/lib.qnt":      "module lib {\n  import base.* from \"sub/base.qnt\"\n}\n",
	"model/sub/base.qnt": "module base {}\n",
	"run/steps.jsonl":    `{"quint.action":"a","quint.seq":1,"quint.recorder":"r"}` + "\n",
}

func layoutManifest(t *testing.T, dir string) *Manifest {
	t.Helper()
	write(t, dir, layout)
	b, err := binding.Load(filepath.Join(dir, "run", "b.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManifest(Options{Quint: "no-such-quint"})
	if err := m.SetBinding(b); err != nil {
		t.Fatal(err)
	}
	if err := m.AddInput(filepath.Join(dir, "run", "steps.jsonl"), "steps", nil); err != nil {
		t.Fatal(err)
	}
	return m
}

// The spec's imports are hashed, transitively, as quint resolves them; a
// replay checks the spec the binding resolves now: a binding moved
// (with its run) beside another model, or an import changed, is
// refused, naming it; the same model moved whole is the same.
func TestManifestVerifiesTheModelReplayed(t *testing.T) {
	t.Parallel()
	a := t.TempDir()
	m := layoutManifest(t, a)
	var imps []string
	for _, i := range m.Model.Imports {
		imps = append(imps, filepath.ToSlash(strings.TrimPrefix(i.Path, a+string(filepath.Separator))))
	}
	if strings.Join(imps, " ") != "model/inst.qnt model/split.qnt model/lib.qnt model/sub/base.qnt" {
		t.Fatalf("imports: %v", imps)
	}
	if err := m.Write(filepath.Join(a, "run", "run.json")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		edit map[string]string // the other place's files beside the copied run/
		says string
	}{
		{"the same model elsewhere", nil, ""},
		{"another spec", map[string]string{"model/m.qnt": "module m {}\n"}, "is not the one the run was judged on"},
		{"an import changed", map[string]string{"model/sub/base.qnt": "module base { val x = 1 }\n"}, "import sub/base.qnt (beside"},
		{"an instance's import changed", map[string]string{"model/inst.qnt": "module inst {\n  const N: Set[str]\n  val x = 1\n}\n"}, "import inst.qnt (beside"},
		{"a split import changed", map[string]string{"model/split.qnt": "module split { val x = 1 }\n"}, "import split.qnt (beside"},
		{"an import more", map[string]string{"model/lib.qnt": "module lib {\n  import base.* from \"sub/base.qnt\"\n  import more.* from \"more\"\n}\n", "model/more.qnt": "module more {}\n"}, "which the run's model did not"},
	} {
		b := t.TempDir()
		files := map[string]string{}
		for p, s := range layout {
			if !strings.HasPrefix(p, "run/") {
				files[p] = s
			}
		}
		for p, s := range c.edit {
			files[p] = s
		}
		write(t, b, files)
		run, _ := os.ReadFile(filepath.Join(a, "run", "run.json"))
		write(t, b, map[string]string{"run/run.json": string(run), "run/b.yaml": layout["run/b.yaml"], "run/steps.jsonl": layout["run/steps.jsonl"]})
		got, err := ReadManifest(filepath.Join(b, "run", "run.json"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = got.Verify()
		if c.says == "" && err != nil || c.says != "" && (err == nil || !strings.Contains(err.Error(), c.says)) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// Bundle copies a manifest's files beside it, the model at its places
// relative to the binding, so the directory replays anywhere: the
// originals gone, it verifies.
func TestManifestBundle(t *testing.T) {
	t.Parallel()
	a := t.TempDir()
	m := layoutManifest(t, a)
	write(t, a, map[string]string{"shot.webp": "pixels"})
	if err := m.AddArtifact("screenshot", filepath.Join(a, "shot.webp")); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "ev")
	if err := m.Bundle(filepath.Join(out, "run1")); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(filepath.Join(out, "run1.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(a); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(out, moved); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManifest(filepath.Join(moved, "run1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if w, err := got.Verify(); err != nil || len(w) > 0 {
		t.Errorf("a bundle moved: %v %v", w, err)
	}
	for _, f := range []string{"m.qnt", "inst.qnt", "split.qnt", "lib.qnt", "sub/base.qnt"} {
		if _, err := os.Stat(filepath.Join(moved, "run1", "model", "0", "model", filepath.FromSlash(f))); err != nil {
			t.Errorf("the bundle left out %s: %v", f, err)
		}
	}
	if !strings.HasPrefix(got.Model.Spec.Path, moved) || !strings.HasPrefix(got.Artifacts[0].Path, moved) || !strings.HasPrefix(got.Inputs[0].Path, moved) {
		t.Errorf("paths: %+v", got)
	}
}

// gotogether's version: a dependency's (a consumer's binary, its test
// binaries too), a replacement's, a stamped checkout's commit, else
// (devel).
func TestModuleVersion(t *testing.T) {
	t.Parallel()
	const path = "github.com/chucklehead-dev/gotogether"
	for _, c := range []struct {
		bi   debug.BuildInfo
		want string
	}{
		{debug.BuildInfo{Main: debug.Module{Path: "example.com/app"}, Deps: []*debug.Module{{Path: path, Version: "v0.1.0"}}}, "v0.1.0"},
		{debug.BuildInfo{Main: debug.Module{Path: "example.com/app"}, Deps: []*debug.Module{{Path: path, Version: "v0.1.0", Replace: &debug.Module{Path: "../.."}}}}, "(devel: ../..)"},
		{debug.BuildInfo{Main: debug.Module{Path: path, Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}}, "commit abc"},
		{debug.BuildInfo{Main: debug.Module{Path: path, Version: "v0.2.0"}}, "v0.2.0"},
		{debug.BuildInfo{Main: debug.Module{Path: path, Version: "(devel)"}}, "(devel)"},
	} {
		if got := moduleVersion(&c.bi); got != c.want {
			t.Errorf("%+v: %q, want %q", c.bi, got, c.want)
		}
	}
}

// A Quint source's imports are read by token: an instance's overrides
// (strings, parentheses), an import over several lines, a qualified
// alias; not a comment's, nor a string's, nor an import of a module of
// the same file, which runs into the next declaration.
func TestImportPaths(t *testing.T) {
	t.Parallel()
	src := "module m {\n" +
		"  // import c.* from \"./comment\"\n" +
		"  /* import c.* from \"./block\" */\n" +
		"  import a(N = Set(\"u\"), M = \"from\") as A from \"./a\"\n" +
		"  import b\n    .*\n    from \"b.qnt\"\n" +
		"  import local.*\n  val s = \"import x.* from ./str\"\n" +
		"  import local as L\n  import d.* from \"sub/d\"\n" +
		"  export e.*\n" +
		"  val url = \"http://x\" // import f.* from \"./f\"\n" +
		"}\n"
	if got := importPaths(src); strings.Join(got, " ") != "./a b.qnt sub/d" {
		t.Errorf("imports %q", got)
	}
}

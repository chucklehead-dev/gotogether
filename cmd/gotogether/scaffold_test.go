package main

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// scaffold -system prints a test that compiles as it is, and whose
// conformance test fails, saying what to do, until Run records steps;
// its mutant test skips until written, and fails under GOTOGETHER_STRICT=1.
func TestScaffoldSystem(t *testing.T) {
	skip.Quint(t)
	b, err := binding.Load("../../examples/bank/bank.binding.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := binding.LoadModel("quint", b.SpecPath(), b.Module)
	if err != nil {
		t.Fatal(err)
	}
	src := systemTest("scaffolded", "../bank/bank.binding.yaml", b, m)
	for _, want := range []string{
		`qobs.Record(ctx, "deposit", err, "account", account, "amount", amount, "obs.balance", observedBalance)`,
		`qtest.Some(qtest.Violates("noOverdraft"))`,
		`Expect(t, qtest.Conforms(), qtest.Exercised())`, // and what the runs exercised
		`s.Mutant = "Mutant"`, // a mutant is not the application in the evidence
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %s in:\n%s", want, src)
		}
	}
	// a package inside the module, so the test builds against it
	dir, err := os.MkdirTemp("../../examples", "_scaffolded") // _: no ./... pattern sees it
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "system_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "-count=1", "-v", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOTOGETHER_STRICT=") // (the last of a name wins)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "recorded a step") {
		t.Fatalf("want TestConforms to fail saying no run recorded a step, got (%v):\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- SKIP: TestMutantIsCaught") {
		t.Fatalf("want it built and the mutant test skipped:\n%s", out)
	}
	// under strict mode the unfinished mutant test fails: a scaffold left
	// as it is cannot pass a strict job
	cmd = exec.Command("go", "test", "-count=1", "-v", "-run", "TestMutantIsCaught", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOTOGETHER_STRICT=1")
	out, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "--- FAIL: TestMutantIsCaught") || !strings.Contains(string(out), "GOTOGETHER_STRICT is set") {
		t.Fatalf("want the mutant test failed under GOTOGETHER_STRICT=1, got (%v):\n%s", err, out)
	}
}

// A binding with eventual vals gets a Run that settles, and a claim that
// every run did: its eventual vals are judged only after a settle, and a
// scaffold without one passed with them unjudged.
func TestScaffoldSystemSettles(t *testing.T) {
	skip.Quint(t)
	b, err := binding.Load("../../examples/quorumkv/quorumkv.binding.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := binding.LoadModel("quint", b.SpecPath(), b.Module)
	if err != nil {
		t.Fatal(err)
	}
	src := systemTest("scaffolded", "../quorumkv/quorumkv.binding.yaml", b, m)
	for _, want := range []string{"return s.Settle(time.Minute)", "qtest.All(qtest.Settles())"} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %s in:\n%s", want, src)
		}
	}
	dir, err := os.MkdirTemp("../../examples", "_scaffolded")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "system_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "vet", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the scaffold does not build: %v\n%s", err, out)
	}
}

// -out writes the scaffold to a file, and never over one: a scaffold is
// filled in by hand.
func TestScaffoldOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system_test.go")
	if err := writeOut(path, []byte("package app\n")); err != nil {
		t.Fatal(err)
	}
	if err := checkOut(path); err == nil || !strings.Contains(err.Error(), "never overwrites") {
		t.Errorf("a second scaffold to %s: %v, want it refused", path, err)
	}
	if err := writeOut(path, []byte("package other\n")); err == nil || !strings.Contains(err.Error(), "never overwrites") || !errors.Is(err, fs.ErrExist) {
		t.Errorf("a second write to %s: %v, want it refused", path, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "package app\n" {
		t.Errorf("the file holds %q", b)
	}
}

// A scaffold that fails (here quint cannot run; a model quint rejects
// fails the same way) leaves no file: an empty one once made its retry
// fail with "never overwrites".
func TestScaffoldFailedLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "m.qnt")
	if err := os.WriteFile(spec, []byte("module m {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "m.binding.yaml")
	if err := scaffold([]string{"-spec", spec, "-module", "m", "-quint", filepath.Join(dir, "no-quint"), "-out", out}); err == nil {
		t.Fatal("scaffold without quint succeeded")
	}
	if _, err := os.Lstat(out); err == nil {
		t.Errorf("a failed scaffold left %s", out)
	}
}

// A scaffold's paths are relative to the file it writes, as the binding
// and the test read them: written relative to the working directory, a
// binding in a subdirectory named a spec that is not there.
func TestScaffoldPathsRelativeToOut(t *testing.T) {
	for _, c := range []struct{ out, path, want string }{
		{"sub/b.yaml", "model/stock.qnt", "../model/stock.qnt"},
		{"b.yaml", "model/stock.qnt", "model/stock.qnt"},
		{"sub/x_test.go", "stock.binding.yaml", "../stock.binding.yaml"},
		{"", "model/stock.qnt", "model/stock.qnt"}, // standard output: as given
		{"sub/b.yaml", "/abs/stock.qnt", "/abs/stock.qnt"},
	} {
		if got := relativeTo(c.out, c.path); got != c.want {
			t.Errorf("relativeTo(%q, %q) = %q, want %q", c.out, c.path, got, c.want)
		}
	}
}

// Through a symlink to a directory elsewhere, a path relative to out as
// named is not one relative to it as resolved: absolute (the binding
// once named ../funds.qnt, wrong from far/real). -out into a missing
// directory fails before quint runs, saying so (it once failed after,
// with "a scaffold never overwrites").
func TestScaffoldOutSymlinkAndMissingDir(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"proj", "far/real"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "proj", "funds.qnt"), []byte("module funds {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "far", "real"), filepath.Join(dir, "proj", "link")); err != nil {
		t.Skip("no symlinks:", err)
	}
	t.Chdir(filepath.Join(dir, "proj"))
	abs, _ := filepath.Abs("funds.qnt")
	if got := relativeTo("link/b.yaml", "funds.qnt"); got != abs {
		t.Errorf("relativeTo through a symlink = %q, want %q", got, abs)
	}
	if got := relativeTo("sub/b.yaml", "funds.qnt"); got != "../funds.qnt" {
		t.Errorf("relativeTo without a symlink = %q", got)
	}
	err := checkOut("nodir/b.yaml")
	if err == nil || !strings.Contains(err.Error(), "directory nodir does not exist") {
		t.Errorf("checkOut into a missing directory: %v", err)
	}
}

// A binding scaffolded into a subdirectory lints there: its spec is
// written relative to it.
func TestScaffoldIntoSubdirectory(t *testing.T) {
	skip.Quint(t)
	spec, err := filepath.Abs("../../validate/testdata/account.qnt")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "model"), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model", "account.qnt"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if err := scaffold([]string{"-spec", "model/account.qnt", "-module", "account", "-out", "sub/b.yaml"}); err == nil {
		t.Fatal("scaffold into a missing directory succeeded")
	}
	if err := os.Mkdir("sub", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := scaffold([]string{"-spec", "model/account.qnt", "-module", "account", "-out", "sub/b.yaml"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("sub/b.yaml")
	if err != nil || !strings.Contains(string(b), "spec: ../model/account.qnt") {
		t.Fatalf("the skeleton (%v):\n%s", err, b)
	}
	set := strings.Replace(string(b), "LIMIT: ''", "LIMIT: '100'", 1)
	if err := os.WriteFile("sub/b.yaml", []byte(set), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := capture(t, func() (int, error) { return lint([]string{"-binding", "sub/b.yaml"}) }); code != 0 {
		t.Errorf("lint from the working directory: exit %d:\n%s", code, out)
	}
}

// A binding whose spec is not where it says names the file, the spec as
// resolved, and, when the spec is relative to the working directory
// instead, the path to write: quint's error was a bare chdir failure.
func TestBindingSpecNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, d := range []string{"model", "sub"} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("model/stock.qnt", []byte("module stock {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("sub/b.yaml", []byte("spec: model/stock.qnt\nmodule: stock\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("run.jsonl", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	resolved := filepath.Join(dir, "sub", "model", "stock.qnt")
	for name, run := range map[string]func() (int, error){
		"lint":     func() (int, error) { return lint([]string{"-binding", "sub/b.yaml"}) },
		"simulate": func() (int, error) { return simulate([]string{"-binding", "sub/b.yaml"}) },
		"validate": func() (int, error) {
			code, _, err := validated([]string{"-binding", "sub/b.yaml", "run.jsonl"})
			return code, err
		},
	} {
		code, err := run()
		for _, want := range []string{"sub/b.yaml: spec model/stock.qnt resolves to " + resolved + ", which does not exist",
			"write spec: ../model/stock.qnt"} {
			if code != 2 || err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: exit %d, %v: want %q", name, code, err, want)
			}
		}
	}
	// what the model cache and every other caller of LoadModel see
	if _, err := binding.LoadModel("quint", resolved, "stock"); err == nil || !strings.Contains(err.Error(), "the spec "+resolved+" does not exist") || strings.Contains(err.Error(), "chdir") {
		t.Errorf("LoadModel: %v", err)
	}
}

// A scaffolded binding is checked as written: lint refuses its empty
// constants with the fix (they once linted and failed in quint's parser),
// it checks every boolean val and classifies them for simulate, and once
// its constants are set it lints and simulates.
func TestScaffoldBindingChecks(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	spec, err := filepath.Abs("../../validate/testdata/account.qnt")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "account.binding.yaml")
	if err := scaffold([]string{"-spec", spec, "-module", "account", "-out", p}); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(p)
	for _, want := range []string{"invariants: [nonNegative, underLimit]", "simulate:", "holds: [nonNegative, underLimit]"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("the skeleton lacks %q:\n%s", want, src)
		}
	}
	code, out := capture(t, func() (int, error) { return lint([]string{"-binding", p}) })
	if code != 1 || !strings.Contains(out, "constant LIMIT: no value (type int): give it a Quint expression (LIMIT: '3')") {
		t.Errorf("lint of the skeleton: exit %d:\n%s", code, out)
	}
	set := strings.Replace(string(src), "LIMIT: ''", "LIMIT: '100'", 1)
	if err := os.WriteFile(p, []byte(set), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := capture(t, func() (int, error) { return lint([]string{"-binding", p}) }); code != 0 {
		t.Errorf("lint with LIMIT set: exit %d:\n%s", code, out)
	}
	// underLimit can fail in the model (deposits have no cap): simulate
	// says to move it to allows, which is the classification's point
	code, out = capture(t, func() (int, error) { return simulate([]string{"-binding", p, "-samples", "100"}) })
	if code != 1 || !strings.Contains(out, "FAIL    underLimit: the model should guarantee it, and a trace violates it: fix the model if it must hold, or move it to simulate.allows") || strings.Contains(out, "FAIL    nonNegative") {
		t.Errorf("simulate: exit %d:\n%s", code, out)
	}
}

// capture runs f with its standard output captured.
func capture(t *testing.T, f func() (int, error)) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	code, err := f()
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	return code, string(out)
}

// scaffold -system takes the test's package from the Go files where it
// goes: its default "app" did not build in a package stock.
func TestScaffoldPackageIn(t *testing.T) {
	dir := t.TempDir()
	if got := packageIn(dir); got != "app" {
		t.Errorf("no Go files: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "stock_test.go"), []byte("package stock_test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := packageIn(dir); got != "stock" {
		t.Errorf("a stock_test file: %q", got)
	}
}

package main

import (
	"io"
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
	w, done, err := output(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("package app\n"))
	done()
	if _, _, err := output(path); err == nil || !strings.Contains(err.Error(), "never overwrites") {
		t.Errorf("a second scaffold to %s: %v, want it refused", path, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "package app\n" {
		t.Errorf("the file holds %q", b)
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

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
)

// scaffold -system prints a test that compiles as it is, and whose
// conformance test fails, saying what to do, until Run records steps.
func TestScaffoldSystem(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not installed")
	}
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
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "recorded a step") {
		t.Fatalf("want TestConforms to fail saying no run recorded a step, got (%v):\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- SKIP: TestMutantIsCaught") {
		t.Fatalf("want it built and the mutant test skipped:\n%s", out)
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

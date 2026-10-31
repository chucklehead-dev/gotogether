package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// lint -binding -src reports a literal qobs.Record action the model
// lacks, with the nearest action, and counts the ones it has as emitted.
func TestLintRecords(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	src := `package app

import (
	"context"

	"github.com/chucklehead-dev/gotogether/qobs"
)

func pay(ctx context.Context) {
	qobs.Record(ctx, "deposit", nil)
	qobs.Record(ctx, "withdraw", nil)
	qobs.Record(ctx, "transfr", nil)
}
`
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	code, err := lint([]string{"-binding", "../../examples/bank/bank.binding.yaml", "-src", dir})
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Errorf("exit %d, want 1:\n%s", code, out)
	}
	for _, want := range []string{
		"3 literal Record calls under",
		`app.go:12:19: pay: Record(ctx, "transfr", ...): transfr is not an action of bank (did you mean transfer?); its actions: deposit, transfer, withdraw`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "warning: action deposit") {
		t.Errorf("deposit is recorded, but warned as emitted by nothing:\n%s", out)
	}
}

// Internal-choice lint explains bounded finite-choice validation, its no-verdict
// limits, and how recording choices can reduce search and improve diagnostics.
func TestLintInternalChoice(t *testing.T) {
	skip.Quint(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	code, err := lint([]string{"-binding", "../../qtest/testdata/flip.yaml"})
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if err != nil || code != 0 {
		t.Fatalf("exit %d, %v:\n%s", code, err, out)
	}
	for _, want := range []string{
		"action flipit makes a choice of its own (any {} of 4 branches)",
		"expose the choice as a parameter of flipit",
		"supported finite choices use bounded search and original-action pinned replay",
		"unsupported shapes or exhaustion give an explicit no verdict",
		"[binding.action.internal_choice]",
		"trace conformance and application behavior were not checked",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// lintOut runs lint with args, its standard output captured.
func lintOut(t *testing.T, args ...string) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	code, err := lint(args)
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return code, string(out)
}

// lint checks a binding against its schema before it loads it, naming
// every problem at its line (the strict decoder named the first alone),
// and a rule file with -convert; neither needs quint.
func TestLintSchema(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "b.binding.yaml")
	os.WriteFile(bad, []byte("spec: m.qnt\nmodule: m\nactions:\n  pay:\n    arg: {}\ninvariants: ok\n"), 0o644)
	code, out := lintOut(t, "-binding", bad)
	for _, want := range []string{
		bad + ":5:5: actions.pay.arg: unknown key (did you mean args?)",
		bad + ":6:13: invariants: a string, want array",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	rules := filepath.Join(dir, "r.convert.yaml")
	os.WriteFile(rules, []byte("rules:\n  - span: GET /x\n"), 0o644)
	if code, out := lintOut(t, "-convert", rules); code != 1 || !strings.Contains(out, rules+":2:5: rules[0]: missing action (required)") {
		t.Errorf("exit %d:\n%s", code, out)
	}
	if code, out := lintOut(t, "-convert", "../../examples/kvstore/kv.convert.yaml"); code != 0 || !strings.Contains(out, "fits its schema and loads") {
		t.Errorf("kvstore's rules: exit %d:\n%s", code, out)
	}
}

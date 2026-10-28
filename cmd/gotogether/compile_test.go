package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// gotogether compile, then validate -compiled: the result line quint's
// check gives, with no quint (-quint names none there is), a search and a
// manifest refused (quint confirms or replays them), and a stale artifact
// refused with the fix, leaving no empty artifacts directory behind. Not
// parallel: it sets TMPDIR.
func TestCompileThenValidate(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	for _, f := range []string{"account.yaml", "account.qnt"} {
		b, err := os.ReadFile(filepath.Join("../../validate/testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bind, model := filepath.Join(dir, "account.yaml"), filepath.Join(dir, "account.model.json")
	var out bytes.Buffer
	if err := compileTo(&out, []string{"-binding", bind, "-o", model}); err != nil {
		t.Fatal(err)
	}
	step := func(actor string, action string, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: action, Actor: actor, Recorder: actor, Thread: actor, Seq: 1,
			Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	log := filepath.Join(dir, "run.jsonl")
	f, err := os.Create(log)
	if err != nil {
		t.Fatal(err)
	}
	if err := qtrace.WriteJSONL(f, []qtrace.Step{step("good", "deposit", 5, 5), step("off", "deposit", 5, 6), step("over", "withdraw", 5, -5)}); err != nil {
		t.Fatal(err)
	}
	f.Close()
	code, got, err := validated([]string{"-binding", bind, "-compiled", model, "-quint", "/nonexistent/quint", "-dir", filepath.Join(dir, "out"), log})
	if err != nil || code != 1 {
		t.Fatalf("exit %d, %v:\n%s", code, err, got)
	}
	for _, want := range []string{"RESULT: FAIL (2 of 3 actors: 1 mismatch after deposit, 1 rejects withdraw)", "judged by the Go evaluator alone"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	for flag, want := range map[string]string{"-linearize": "needs quint", "-manifest": "drop one of them"} {
		args := []string{"-binding", bind, "-compiled", model, "-dir", filepath.Join(dir, "out-"+flag[1:]), flag}
		if flag == "-manifest" {
			args = append(args, filepath.Join(dir, "m.json"))
		}
		code, got, err := validated(append(args, log))
		if code != 2 || err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: exit %d, %v, want 2 and %q:\n%s", flag, code, err, want, got)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "account.qnt"), append(mustRead(t, filepath.Join(dir, "account.qnt")), "\n// edited\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	code, got, err = validated([]string{"-binding", bind, "-compiled", model, "-dir", filepath.Join(dir, "out-stale"), log})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "is stale") || !strings.Contains(err.Error(), "gotogether compile -binding") {
		t.Errorf("a stale artifact: exit %d, %v:\n%s", code, err, got)
	}
	// without -dir, the refusal names no artifacts and leaves no
	// directory: nothing was written there
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	code, got, _ = validated([]string{"-binding", bind, "-compiled", model, log})
	if left, _ := os.ReadDir(tmp); code != 2 || strings.Contains(string(got), "artifacts") || len(left) > 0 {
		t.Errorf("a stale artifact without -dir: exit %d, %d entries left in TMPDIR:\n%s", code, len(left), got)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// doctor reports a binding's format and a compiled model's staleness:
// fresh, no skew; its model edited, a skew naming the fix.
func TestDoctorCompiled(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	dir := t.TempDir()
	for _, f := range []string{"account.yaml", "account.qnt"} {
		if err := os.WriteFile(filepath.Join(dir, f), mustRead(t, filepath.Join("../../validate/testdata", f)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bind, model := filepath.Join(dir, "account.yaml"), filepath.Join(dir, "account.model.json")
	if err := compileTo(&bytes.Buffer{}, []string{"-binding", bind, "-o", model}); err != nil {
		t.Fatal(err)
	}
	if p := compiledPiece(model); p.skew != "" || p.version != "gotogether.model/1" {
		t.Errorf("a fresh compiled model: %+v", p)
	}
	if p := bindingPiece(bind); p.skew != "" || p.version != "gotogether.binding/1" {
		t.Errorf("a binding: %+v", p)
	}
	if err := os.WriteFile(bind, append(mustRead(t, bind), "\n# edited\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := compiledPiece(model); !strings.Contains(p.skew, "is stale") || !strings.Contains(p.skew, "gotogether compile") {
		t.Errorf("a stale compiled model: %+v", p)
	}
	if err := os.WriteFile(bind, []byte("format: gotogether.binding/9\nspec: account.qnt\nmodule: account\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := bindingPiece(bind); !strings.Contains(p.skew, "upgrade") {
		t.Errorf("a later binding format: %+v", p)
	}
}

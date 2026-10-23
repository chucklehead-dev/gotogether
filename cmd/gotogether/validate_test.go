package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// validate ends with how many traces fail and why, in the claims' words:
// it said "VIOLATION" for any failure, a mismatch included.
func TestValidateResultLine(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not installed")
	}
	step := func(actor string, seq uint64, action string, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: action, Actor: actor, Recorder: actor, Thread: actor, Seq: seq,
			Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	steps := []qtrace.Step{
		step("good", 1, "deposit", 5, 5),
		step("off", 1, "deposit", 5, 6),    // a mismatch
		step("over", 1, "withdraw", 5, -5), // a step the model cannot take
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "run.jsonl")
	f, err := os.Create(log)
	if err != nil {
		t.Fatal(err)
	}
	if err := qtrace.WriteJSONL(f, steps); err != nil {
		t.Fatal(err)
	}
	f.Close()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	code, err := runValidate([]string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out"), log})
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if err != nil || code != 1 {
		t.Fatalf("exit %d, %v:\n%s", code, err, out)
	}
	if want := "RESULT: FAIL (2 of 3 traces: 1 mismatch after deposit, 1 rejects withdraw)"; !strings.Contains(string(out), want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
}

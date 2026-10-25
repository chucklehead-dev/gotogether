package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if want := "RESULT: FAIL (2 of 3 actors: 1 mismatch after deposit, 1 rejects withdraw)"; !strings.Contains(string(out), want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
	// a passing run says which invariants it checked, if any
	f, err = os.Create(log)
	if err != nil {
		t.Fatal(err)
	}
	if err := qtrace.WriteJSONL(f, steps[:1]); err != nil {
		t.Fatal(err)
	}
	f.Close()
	r, w, _ = os.Pipe()
	os.Stdout = w
	code, err = runValidate([]string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out2"), log})
	os.Stdout = stdout
	w.Close()
	out, _ = io.ReadAll(r)
	if want := "RESULT: PASS (1 actor conforms to the model and satisfies its 2 invariants)"; err != nil || code != 0 || !strings.Contains(string(out), want) {
		t.Errorf("exit %d, %v; output lacks %q:\n%s", code, err, want, out)
	}
}

// -linearize checks a client history in an order real time allows: a
// withdrawal that overlapped the deposit it depended on returned first,
// which recorded order rejects and the order the intervals allow accepts.
func TestValidateLinearize(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not installed")
	}
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "a", Process: "p", Seq: 1, Thread: "a", Invoked: at(0), Time: at(10),
			Args: map[string]any{"amount": int64(60)}, Obs: map[string]any{"balance": int64(60)}},
		{Action: "withdraw", Recorder: "b", Process: "p", Seq: 1, Thread: "b", Invoked: at(1), Time: at(5),
			Args: map[string]any{"amount": int64(50)}, Obs: map[string]any{"balance": int64(10)}},
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
	for _, c := range []struct {
		flags []string
		code  int
	}{{nil, 1}, {[]string{"-linearize"}, 0}, {[]string{"-history", filepath.Join(dir, "h.svg")}, 1}} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout := os.Stdout
		os.Stdout = w
		args := append(append([]string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out")}, c.flags...), log)
		code, err := runValidate(args)
		os.Stdout = stdout
		w.Close()
		out, _ := io.ReadAll(r)
		if err != nil || code != c.code {
			t.Errorf("%v: exit %d, %v, want %d:\n%s", c.flags, code, err, c.code, out)
		}
	}
	// -history drew the failing run: the withdrawal checked first, red
	svg, err := os.ReadFile(filepath.Join(dir, "h.svg"))
	if err != nil || !strings.Contains(string(svg), "<svg") || strings.Count(string(svg), `fill="#f6b5b5"`) != 1 {
		t.Errorf("-history: %v\n%s", err, svg)
	}
}

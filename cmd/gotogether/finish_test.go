package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// validateSteps writes steps to a log and validates it with args,
// returning the exit code, the error and the output.
func validateSteps(t *testing.T, steps []qtrace.Step, args ...string) (int, error, string) {
	t.Helper()
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
	code, out, verr := validated(append(append([]string{"-dir", filepath.Join(dir, "out")}, args...), log))
	return code, verr, string(out)
}

// -require-finish is qtest.Finishes' judgment: the CLI and the claim agree
// on a call -resolve-pending placed (unanswered: unfinished), name an
// actor whose only call is pending by its calls (not a settle it lacks),
// call a lost tail no verdict, refuse a binding with no eventual val with
// Finishes' fix, and never pass an ITF trace, which records no settle.
func TestValidateRequireFinish(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	ms := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
	funds := "../../validate/testdata/funds.binding.yaml"
	dep := func(actor string, seq uint64, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: "deposit", Actor: actor, Recorder: "r", Seq: seq, Time: ms(int(seq)), Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	withdraw := func(actor string, seq uint64) qtrace.Step {
		return qtrace.Step{Action: "withdraw", Actor: actor, Recorder: "r", Seq: seq, Time: ms(int(seq)), Invocation: true, Args: map[string]any{"amount": int64(3)}}
	}
	settle := func(seq uint64) qtrace.Step {
		return qtrace.Step{Action: "settled", Settled: true, Recorder: "r", Seq: seq, Time: ms(int(seq))}
	}
	for _, x := range []struct {
		name  string
		steps []qtrace.Step
		args  []string
		code  int
		says  []string
	}{
		{"a resolved call", []qtrace.Step{dep("a1", 1, 5, 5), withdraw("a1", 2), settle(3)}, []string{"-resolve-pending"}, 1,
			[]string{"actor a1: incomplete (pending calls)", "never got the answer"}},
		{"an actor whose only call is pending", []qtrace.Step{dep("a1", 1, 5, 5), withdraw("a2", 2), settle(3)}, []string{"-resolve-pending"}, 1,
			[]string{"actor a2: incomplete (pending calls)", "call withdraw"}},
		{"a lost tail", []qtrace.Step{dep("", 1, 5, 5)}, []string{"-lost-suffix", "r"}, 1,
			[]string{"no verdict (lost tail)", "check a log that kept its steps", "1 no verdict (lost tail)"}},
		{"finished", []qtrace.Step{dep("", 1, 5, 5), settle(2)}, nil, 0, []string{"RESULT: PASS"}},
	} {
		t.Run(x.name, func(t *testing.T) {
			t.Parallel()
			code, err, out := validateSteps(t, x.steps, append([]string{"-binding", funds, "-require-finish"}, x.args...)...)
			if err != nil || code != x.code {
				t.Errorf("exit %d, %v, want %d:\n%s", code, err, x.code, out)
			}
			for _, s := range x.says {
				if !strings.Contains(out, s) {
					t.Errorf("no %q in:\n%s", s, out)
				}
			}
		})
	}
	// the claim says the same of the resolved call
	steps := []qtrace.Step{dep("a1", 1, 5, 5), withdraw("a1", 2), settle(3)}
	if run := qtest.Validate(t, funds, steps, qtest.ResolvePending()); qtest.Finishes().Holds(run) {
		t.Errorf("Finishes holds of a run whose resolved call was never answered:\n%s", run.Report.Summary())
	}
	// no eventual val: refused with Finishes' fix
	_, err, out := validateSteps(t, []qtrace.Step{dep("", 1, 5, 5), settle(2)}, "-binding", "../../validate/testdata/account.yaml", "-require-finish")
	if err == nil || !strings.Contains(err.Error(), "add the journey's end as an eventual val on a ghost flag") {
		t.Errorf("a binding with no eventual val: %v:\n%s", err, out)
	}
	// -require-settle, the old name, still checks the finish, and says
	// it is deprecated for -require-finish
	code, err, out := validateSteps(t, []qtrace.Step{dep("", 1, 5, 5)}, "-binding", funds, "-require-settle")
	if err != nil || code != 1 || !strings.Contains(out, "warning: -require-settle is deprecated") || !strings.Contains(out, "say -require-finish") ||
		!strings.Contains(out, "incomplete (no settle) (-require-finish)") {
		t.Errorf("-require-settle: exit %d, %v:\n%s", code, err, out)
	}
	// an ITF trace: no verdict, never a pass
	dir := t.TempDir()
	log := filepath.Join(dir, "run.jsonl")
	f, _ := os.Create(log)
	_ = qtrace.WriteJSONL(f, []qtrace.Step{dep("", 1, 5, 5), settle(2)})
	f.Close()
	if err := itfTo(io.Discard, []string{"-binding", funds, "-out", dir, log}); err != nil {
		t.Fatal(err)
	}
	itfs, _ := filepath.Glob(filepath.Join(dir, "*.itf.json"))
	if len(itfs) != 1 {
		t.Fatalf("itf wrote %v", itfs)
	}
	for _, args := range [][]string{{"-binding", funds, "-require-finish"}, {"-binding", "../../validate/testdata/funds_complete.yaml"}} {
		code, out, err := validated(append(append([]string{"-dir", filepath.Join(t.TempDir(), "out")}, args...), itfs[0]))
		if err != nil || code != 1 || !strings.Contains(string(out), "a finished run can't be judged from an ITF trace") {
			t.Errorf("%v on an ITF trace: exit %d, %v:\n%s", args, code, err, fmt.Sprint(string(out)))
		}
	}
}

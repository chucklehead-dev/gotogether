package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// validate -mutate-steps prints a passing log's observation mutants by
// op, and its survivors: a deposit's balance off by one is killed (the
// expect compares it); the last withdrawal dropped survives (no later
// step observes the balance); a field the binding reads removed is
// malformed (refused, as a log without it is: a tool error), in no
// score. A failing log judges none; the narrowing
// flags need -mutate-steps, and an unknown op names the ops.
func TestValidateMutateSteps(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	t0 := time.Unix(10, 0)
	step := func(seq uint64, action string, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: action, Recorder: "acct", Thread: "acct", Seq: seq, Time: t0.Add(time.Duration(seq) * time.Millisecond),
			Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	dir := t.TempDir()
	write := func(name string, steps ...qtrace.Step) string {
		p := filepath.Join(dir, name)
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := qtrace.WriteJSONL(f, steps); err != nil {
			t.Fatal(err)
		}
		f.Close()
		return p
	}
	good := write("good.jsonl", step(1, "deposit", 5, 5), step(2, "withdraw", 2, 3))
	code, out, err := validated([]string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out"), "-mutate-steps", "-mutate-ops", "drop,corrupt", good})
	if err != nil || code != 0 {
		t.Fatalf("exit %d, %v:\n%s", code, err, out)
	}
	for _, want := range []string{
		"observation mutants: 14, 9 killed (90%), 1 survived, 4 malformed (refused before a verdict, by Reconstruct or the binding: in no score)",
		"  drop           2       1         1          0",
		"  corrupt       12       8         0          0 (4 malformed)",
		"malformed: corrupt obs.balance of deposit (recorder acct seq 1): 5 removed (refused by the binding: ",
		"survived: drop of withdraw (recorder acct seq 2)",
		"RESULT: PASS",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	bad := write("bad.jsonl", step(1, "deposit", 5, 6))
	code, out, err = validated([]string{"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out2"), "-mutate-steps", bad})
	if want := "mutate-steps: none judged: the run does not pass"; err != nil || code != 1 || !strings.Contains(string(out), want) {
		t.Errorf("exit %d, %v; output lacks %q:\n%s", code, err, want, out)
	}
	for _, x := range []struct {
		args []string
		says string
	}{
		{[]string{"-mutate-limit", "3"}, "-mutate-ops and -mutate-limit narrow -mutate-steps: add -mutate-steps"},
		{[]string{"-mutate-steps", "-mutate-ops", "drop,delete"}, `"delete" is no mutation; the ops are drop, dup, swap, corrupt, retime, reactor`},
	} {
		code, _, err := validated(append(append([]string{"-binding", "../../validate/testdata/account.yaml"}, x.args...), good))
		if code != 2 || err == nil || !strings.Contains(err.Error(), x.says) {
			t.Errorf("%v: exit %d, %v; want one saying %q", x.args, code, err, x.says)
		}
	}
}

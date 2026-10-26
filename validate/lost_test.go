package validate

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A recorder that lost its tail while others went on: a later step of
// another recorder may depend on what it lost, so a failure there is no
// verdict, not a FAIL (a withdrawal of a deposit the lost tail held); a
// failure before the cut stands; and the run is unsettled, its eventual
// vals not judged (a lost withdrawal would leave the account empty, which
// the recorded steps hide: no false PASS). Every check says so: Check,
// CheckBatch, Judge's linearization.
func TestLostSuffixOtherRecorder(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	at := func(ms int) time.Time { return time.Unix(0, int64(ms)*int64(time.Millisecond)) }
	acct := func(rec string, seq uint64, ms int, action string, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: action, Recorder: rec, Seq: seq, Time: at(ms), Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	lostA := func(steps ...qtrace.Step) []qtrace.Step {
		return qtrace.MarkLost(steps, qtrace.Lost{Recorder: "a", After: 1})
	}
	settle := qtrace.Step{Action: "settled", Recorder: "b", Seq: 1, Settled: true, Time: at(50)}
	cases := []struct {
		name   string
		steps  []qtrace.Step
		reason string // Result.Reason
		says   string // an issue of its result
	}{
		{"a dependent later step", lostA(acct("a", 1, 10, "deposit", 5, 5), acct("b", 1, 30, "withdraw", 10, 0)),
			"no verdict (lost steps)", "may depend on a's lost steps after seq 1 (recorder a lost its tail after seq 1)"},
		{"a later violation", lostA(acct("a", 1, 10, "deposit", 5, 5), acct("b", 1, 30, "deposit", 100, 105)),
			"no verdict (lost steps)", "underLimit not evaluated: its violation at step 2"},
		{"a failure before the cut", lostA(acct("b", 1, 5, "withdraw", 10, 0), acct("a", 1, 10, "deposit", 5, 5)),
			"rejects withdraw", "recorder a lost its tail after seq 1; the failure comes before it, and stands"},
		{"a lost withdrawal", lostA(acct("a", 1, 10, "deposit", 5, 5), settle),
			"", "eventual vals not judged (funded): recorder a lost its tail after seq 1"},
	}
	c, err := NewChecker("testdata/funds.binding.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	judge := func(name string, rep *Report, k int) {
		t.Helper()
		cs := cases[k]
		a := rep.Actors[0]
		if a.Reason() != cs.reason || !strings.Contains(a.Summary(), cs.says) {
			t.Errorf("%s, %s: %q, want %q and %q:\n%s", name, cs.name, a.Reason(), cs.reason, cs.says, rep.Summary())
		}
		for _, inv := range a.Invariants {
			if inv.Eventually {
				t.Errorf("%s, %s: %s judged (%s) of a run whose tail was lost", name, cs.name, inv.Name, inv.Verdict)
			}
		}
	}
	var runs [][]qtrace.Step
	for k, cs := range cases {
		runs = append(runs, cs.steps)
		rep, err := c.Check(ctx, cs.steps)
		if err != nil {
			t.Fatal(err)
		}
		judge("Check", rep, k)
		j, err := c.Judge(ctx, cs.steps, SearchOptions{Linearize: true})
		if err != nil {
			t.Fatal(err)
		}
		judge("Judge, linearized", j.Report, k)
	}
	reps, errs := c.CheckBatch(ctx, runs, len(runs))
	for k := range cases {
		if errs[k] != nil {
			t.Fatal(errs[k])
		}
		judge("CheckBatch", reps[k], k)
	}
	if reps[1].Actors[0].OK() { // no verdict is no pass
		t.Errorf("a violation that may be the lost steps' passed: %s", reps[1].Summary())
	}
	// the run whole: its settle judges funded, which the withdrawal broke
	whole := []qtrace.Step{acct("a", 1, 10, "deposit", 5, 5), acct("a", 2, 20, "withdraw", 5, 0), settle}
	rep, err := c.Check(ctx, whole)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Actors[0].Reason() != "violates eventually funded" {
		t.Errorf("the whole run: %s", rep.Summary())
	}
}

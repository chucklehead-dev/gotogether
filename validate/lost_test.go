package validate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
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
	skip.Quint(t)
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
		// 1ms before the cut, as close as two machines' clocks may be
		{"a step within the slack of the cut", lostA(acct("a", 1, 10, "deposit", 5, 5), acct("b", 1, 9, "withdraw", 10, 0)),
			"no verdict (lost steps)", "may depend on a's lost steps after seq 1"},
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
	if reps[2].Actors[0].OK() { // no verdict is no pass
		t.Errorf("a violation that may be the lost steps' passed: %s", reps[1].Summary())
	}
	// a search out of budget is no verdict of its own: the loss is said,
	// not that a failure stands
	j, err := c.Judge(ctx, cases[3].steps, SearchOptions{Linearize: true, Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if a := j.Report.Actors[0]; a.Reason() != "no verdict (linearize budget)" || strings.Contains(a.Summary(), "stands") || !strings.Contains(a.Summary(), "info lost-suffix: recorder a lost its tail after seq 1") {
		t.Errorf("out of budget: %q:\n%s", a.Reason(), j.Report.Summary())
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

// A bounded response whose window reaches a lost tail's cut is no
// verdict: the response may be among the lost steps (an elect a's tail
// held), however long the trace went on. One whose window closed before
// the cut stands.
func TestLostSuffixWithin(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/within.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	at := func(s int) time.Time { return time.Unix(0, 0).Add(time.Duration(s) * time.Second) }
	step := func(rec string, seq uint64, action string, s int) qtrace.Step {
		return qtrace.Step{Action: action, Recorder: rec, Seq: seq, Time: at(s)}
	}
	for _, x := range []struct {
		name   string
		steps  []qtrace.Step
		reason string
	}{
		// healed at 1s, the cut at 1s: a lost elect may have come by 4s
		{"the window reaches the cut", qtrace.MarkLost([]qtrace.Step{step("a", 1, "crash", 0), step("a", 2, "heal", 1), step("b", 1, "tick", 10)},
			qtrace.Lost{Recorder: "a", After: 2}), "no verdict (lost steps)"},
		// healed at 1s, the window closed at 4s, the cut at 5s
		{"the window closed before it", qtrace.MarkLost([]qtrace.Step{step("a", 1, "crash", 0), step("a", 2, "heal", 1), step("a", 3, "tick", 5), step("b", 1, "tick", 10)},
			qtrace.Lost{Recorder: "a", After: 3}), "violates " + c.Binding.Within[0].Name()},
	} {
		rep, err := c.Check(context.Background(), x.steps)
		if err != nil {
			t.Fatal(err)
		}
		if got := rep.Actors[0].Reason(); got != x.reason {
			t.Errorf("%s: %q, want %q:\n%s", x.name, got, x.reason, rep.Summary())
		}
	}
}

// The key of a run with a lost tail has its steps' times: a withdrawal
// after the cut is no verdict, one before it a rejection, and a cache
// keyed without them gave one the other's verdict.
func TestLostSuffixKey(t *testing.T) {
	c := &Checker{Binding: &binding.Binding{}}
	at := func(ms int) time.Time { return time.Unix(0, int64(ms)*int64(time.Millisecond)) }
	run := func(withdraw int) []qtrace.Step {
		return qtrace.MarkLost([]qtrace.Step{
			{Action: "deposit", Recorder: "a", Seq: 1, Time: at(10), Args: map[string]any{"amount": int64(5)}},
			{Action: "withdraw", Recorder: "b", Seq: 1, Time: at(withdraw), Args: map[string]any{"amount": int64(10)}},
		}, qtrace.Lost{Recorder: "a", After: 1})
	}
	if c.Key(run(30)) == c.Key(run(5)) {
		t.Errorf("a step after the cut and one before it share a key")
	}
}

// A settle settles nothing when a recorder lost its tail, for the bounds
// of pending calls too: the pending deposit took effect between two
// deposits recorded after the settle, which a settle that bounded it
// (before every step recorded after it: the settle's recorder's, the
// others quiet) forbade.
func TestLostSuffixSettleBoundsNothing(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/funds.binding.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	at := func(ms int) time.Time { return time.Unix(0, int64(ms)*int64(time.Millisecond)) }
	dep := func(rec string, seq uint64, ms int, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: "deposit", Recorder: rec, Seq: seq, Time: at(ms), Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	pending := qtrace.Step{Action: "deposit", Recorder: "p", Seq: 1, Invocation: true, Time: at(5), Args: map[string]any{"amount": int64(5)}}
	steps := qtrace.MarkLost([]qtrace.Step{dep("a", 1, 10, 5, 5), pending,
		{Action: "settled", Recorder: "s", Seq: 1, Settled: true, Time: at(20)},
		dep("s", 2, 30, 1, 6), dep("s", 3, 40, 1, 12)}, qtrace.Lost{Recorder: "a", After: 1})
	j, err := c.Judge(context.Background(), steps, SearchOptions{ResolvePending: true})
	if err != nil {
		t.Fatal(err)
	}
	if a := j.Report.Actors[0]; a.Conforms != Pass {
		t.Errorf("%s:\n%s", a.Reason(), j.Report.Summary())
	}
}

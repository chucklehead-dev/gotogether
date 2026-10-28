package validate

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// absentRun is one row of the absence matrix: steps, the absence its
// report must name with its cause, and the obligation's verdict among
// the invariants ("" for none there).
type absentRun struct {
	name    string
	steps   []qtrace.Step
	absent  string // the absence's name ("" for none at all)
	cause   Cause
	verdict Verdict
}

// checkAbsent checks the rows with the binding's checker, in one batch
// (CheckBatch: a few quint processes for them all), and returns the
// rows' reports. The rows are judged again through every search of
// searches, a batch each (JudgeBatch), and each search must agree with
// the plain check: the same absences with the same causes, the same
// verdict of the obligation. A search re-sequences each actor onto a
// recorder of its own, so what the steps as recorded say of collection
// must reach its order's check.
func checkAbsent(t *testing.T, bind string, rows []absentRun, searches ...SearchOptions) []*Report {
	t.Helper()
	c, err := NewChecker(bind, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	runs := make([][]qtrace.Step, len(rows))
	for i, x := range rows {
		runs[i] = x.steps
	}
	reps, errs := c.CheckBatch(context.Background(), runs, 0)
	for i, x := range rows {
		if errs[i] != nil {
			t.Fatalf("%s: %v", x.name, errs[i])
		}
		t.Run(x.name, func(t *testing.T) { judgeAbsentRow(t, x, reps[i]) })
	}
	for _, so := range searches {
		js, errs := c.JudgeBatch(context.Background(), runs, so, nil)
		for i, x := range rows {
			if errs[i] != nil {
				t.Fatalf("%s, %+v: %v", x.name, so, errs[i])
			}
			rep, j := reps[i], js[i]
			if a, b := absences(rep), absences(j.Report); a != b || obligationVerdict(rep, x.absent) != obligationVerdict(j.Report, x.absent) {
				t.Errorf("%s: searched (%+v): absent %s, %s %q; checked as recorded: absent %s, %q:\n%s", x.name, so, b, x.absent, obligationVerdict(j.Report, x.absent), a, obligationVerdict(rep, x.absent), j.Report.Summary())
			}
		}
	}
	return reps
}

// searches are the searches a matrix row is judged through again:
// ResolvePending, and under the full suite Linearize too (a slower
// search over the same re-sequencing and carry).
func searches() []SearchOptions {
	if testing.Short() {
		return []SearchOptions{{ResolvePending: true}}
	}
	return []SearchOptions{{ResolvePending: true}, {Linearize: true}}
}

// absences are rep's absences' kinds, names and causes.
func absences(rep *Report) string {
	var out []string
	for _, a := range rep.Absent() {
		out = append(out, a.Kind+" "+a.Name+": "+string(a.Cause))
	}
	return strings.Join(out, "; ")
}

// obligationVerdict is the verdict of rep's first actor's invariant name.
func obligationVerdict(rep *Report, name string) Verdict {
	for _, inv := range rep.Actors[0].Invariants {
		if inv.Name == name {
			return inv.Verdict
		}
	}
	return ""
}

// judgeAbsentRow checks rep against row x.
func judgeAbsentRow(t *testing.T, x absentRun, rep *Report) {
	t.Helper()
	var got []Absence
	for _, a := range rep.Absent() {
		if x.absent == "" || a.Name == x.absent {
			got = append(got, a)
		}
	}
	switch {
	case x.absent == "" && len(got) > 0:
		t.Errorf("absent %v, want nothing absent:\n%s", got, rep.Summary())
	case x.absent != "" && (len(got) == 0 || got[0].Cause != x.cause):
		t.Errorf("absent %v, want %s %s:\n%s", got, x.absent, x.cause, rep.Summary())
	case x.absent != "" && !strings.Contains(rep.Summary(), "\n    "+x.absent+": "+x.cause.label()+": "):
		t.Errorf("the report's absent section does not say it:\n%s", rep.Summary())
	}
	if v := obligationVerdict(rep, x.absent); (strings.HasPrefix(x.absent, "within") || strings.HasPrefix(x.absent, "eventually")) && v != x.verdict {
		t.Errorf("%s %q, want %q:\n%s", x.absent, v, x.verdict, rep.Summary())
	}
	// only a defect is a FAIL: a dropped or uncollected step makes no
	// verdict, never a pass
	if x.absent != "" && x.cause != Defect && x.verdict == NotEvaluated && rep.OK() {
		t.Errorf("a run with no verdict passes:\n%s", rep.Summary())
	}
}

// atSec is t seconds after the epoch.
func atSec(t int) time.Time { return time.Unix(0, 0).Add(time.Duration(t) * time.Second) }

// The absence matrix, bounded responses: a leader due 3s after a heal at
// 1s, never elected. Dropped where a gap or a lost tail of the actor's
// recorder falls in the window (its failure no verdict); uncollected
// where the watermark ends before the window and nothing else says the
// steps there arrived (no verdict, which the trace going on once made a
// FAIL), or the trace ends first (open); a defect, a FAIL, where the
// trace settled, went on past the window with no watermark, or its
// watermark reaches past the window although its steps end before it:
// what lets a production trace, which never settles, be judged. A
// recorder's dense seqs past a lagging watermark say its steps there
// arrived: a FAIL still; steps without seqs (converted spans) have the
// watermark alone. A late response under a short watermark is no
// verdict: an earlier one may not be collected yet. Every row is judged
// again through ResolvePending (and Linearize: searches), which must agree.
// Ablations: with the watermark ignored the "watermark past the window"
// rows are open (uncollected) and the "no seqs" rows FAILs; with the
// dense-seq raise off the "lagging watermark" rows are no verdict; with
// the gaps ignored the gap row is a FAIL; with the carry exact no more
// (a search's order's own last step bounding it, every recorder taken
// as stated) the slow call's row is a FAIL through the searches.
func TestAbsentWithin(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	steps := func(actions ...any) []qtrace.Step {
		var out []qtrace.Step
		for i := 0; i < len(actions); i += 2 {
			out = append(out, qtrace.Step{Action: actions[i].(string), Recorder: "n", Seq: uint64(i/2 + 1), Time: atSec(actions[i+1].(int))})
		}
		return out
	}
	spans := func(actions ...any) []qtrace.Step { // no seqs: converted spans
		out := steps(actions...)
		for i := range out {
			out[i].Seq = 0
		}
		return out
	}
	settled := func(h []qtrace.Step) []qtrace.Step {
		last := h[len(h)-1]
		return append(h, qtrace.Step{Action: "settled", Record: qtrace.Record{Settled: true}, Recorder: "n", Seq: last.Seq + 1, Time: last.Time})
	}
	gapped := steps("crash", 0, "heal", 1, "tick", 6)
	gapped[2].Seq = 4 // seq 3, recorded between 1s and 6s, was lost
	// a slow call (invoked at 2s, before the window's end, completed at
	// 6s, after it) on a recorder with a watermark past the window, the
	// crash on another taken whole, whose log ends before the window
	slow := qtrace.MarkComplete([]qtrace.Step{
		{Action: "crash", Recorder: "m", Seq: 1, Time: atSec(0)},
		{Action: "heal", Recorder: "n", Seq: 1, Time: atSec(1)},
		{Action: "tick", Recorder: "n", Seq: 2, Time: atSec(2), Record: qtrace.Record{Invocation: true}},
		{Action: "tick", Recorder: "n", Seq: 3, Time: atSec(6), Call: 2},
	}, "n", atSec(10))
	name := "within 3s after healed: hasLeader"
	checkAbsent(t, "testdata/within.yaml", []absentRun{
		{"met", steps("crash", 0, "heal", 1, "elect", 2), "", "", ""},
		{"met, watermark past the window", qtrace.MarkComplete(steps("crash", 0, "heal", 1, "elect", 2), "n", atSec(10)), "", "", ""},
		{"settled", settled(steps("crash", 0, "heal", 1, "tick", 2)), name, Defect, Fail},
		{"went on past the window", steps("crash", 0, "heal", 1, "tick", 6), name, Defect, Fail},
		{"watermark past the window", qtrace.MarkComplete(steps("crash", 0, "heal", 1, "tick", 2), "n", atSec(10)), name, Defect, Fail},
		{"watermark past the window, no seqs", qtrace.MarkComplete(spans("crash", 0, "heal", 1, "tick", 2), "n", atSec(10)), name, Defect, Fail},
		{"every recorder's watermark", qtrace.MarkComplete(steps("crash", 0, "heal", 1, "tick", 2), qtrace.AllRecorders, atSec(10)), name, Defect, Fail},
		{"late, a lagging watermark", qtrace.MarkComplete(steps("crash", 0, "heal", 1, "elect", 6), "n", atSec(3)), name, Defect, Fail},
		{"late, a watermark, no seqs", qtrace.MarkComplete(spans("crash", 0, "heal", 1, "elect", 6), "n", atSec(3)), name, Uncollected, NotEvaluated},
		{"a lagging watermark", qtrace.MarkComplete(steps("crash", 0, "heal", 1, "tick", 6, "tick", 20), "n", atSec(3)), name, Defect, Fail},
		{"ended before the window", steps("crash", 0, "heal", 1, "tick", 2), name, Uncollected, ""},
		{"watermark before the window, no seqs", qtrace.MarkComplete(spans("crash", 0, "heal", 1, "tick", 6), "n", atSec(3)), name, Uncollected, NotEvaluated},
		{"watermark before the window, ended", qtrace.MarkComplete(steps("crash", 0, "heal", 1, "tick", 2), "n", atSec(3)), name, Uncollected, ""},
		{"a slow call, a whole log", slow, name, Uncollected, ""},
		{"a gap in the window", settled(gapped), name, Dropped, NotEvaluated},
		{"a lost tail", qtrace.MarkLost(steps("crash", 0, "heal", 1, "tick", 2, "tick", 2), qtrace.Lost{Recorder: "n", After: 3}), name, Dropped, ""},
	}, searches()...)
}

// The absence matrix, eventual vals: funded (money left) false at the
// settle is a defect, a FAIL; with a gap before it, dropped (the lost
// step may be the deposit that funds it: no verdict); with a lost tail,
// dropped (not judged: the tail may hold the settle); with no settle, or
// a harness's settle that the account recorder's watermark (and its
// dense seqs) does not reach, uncollected (not judged). A recorder's own
// settle, its seqs dense, reaches it whatever its watermark says.
// Ablation: with the watermark ignored the "before the settle" row
// settles, and funded is a FAIL; with the dense-seq raise off, the
// "lagging watermark" row is uncollected.
func TestAbsentEventually(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	op := func(action string, seq uint64, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: action, Recorder: "r", Seq: seq, Time: atSec(int(seq)), Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	settle := func(seq uint64) qtrace.Step {
		return qtrace.Step{Action: "settled", Record: qtrace.Record{Settled: true}, Recorder: "r", Seq: seq, Time: atSec(int(seq))}
	}
	name := EventuallyName("funded")
	checkAbsent(t, "testdata/funds.binding.yaml", []absentRun{
		{"held", []qtrace.Step{op("deposit", 1, 5, 5), settle(2)}, "", "", ""},
		{"false at the settle", []qtrace.Step{op("deposit", 1, 5, 5), op("withdraw", 2, 5, 0), settle(3)}, name, Defect, Fail},
		{"a gap before the settle", []qtrace.Step{op("deposit", 1, 5, 5), op("withdraw", 3, 5, 0), settle(4)}, name, Dropped, NotEvaluated},
		{"a lost tail", qtrace.MarkLost([]qtrace.Step{op("deposit", 1, 5, 5), op("withdraw", 2, 5, 0)}, qtrace.Lost{Recorder: "r", After: 2}), name, Dropped, ""},
		{"no settle", []qtrace.Step{op("deposit", 1, 5, 5), op("withdraw", 2, 5, 0)}, name, Uncollected, ""},
		{"a watermark before the settle", qtrace.MarkComplete([]qtrace.Step{op("deposit", 1, 5, 5), op("withdraw", 2, 5, 0),
			{Action: "settled", Record: qtrace.Record{Settled: true}, Recorder: "h", Seq: 1, Time: atSec(3)}}, "r", atSec(2)), name, Uncollected, ""},
		{"a lagging watermark, dense seqs to the settle", qtrace.MarkComplete([]qtrace.Step{op("deposit", 1, 5, 5), op("withdraw", 2, 5, 0), settle(3)}, "r", atSec(1)), name, Defect, Fail},
		{"a watermark at the settle", qtrace.MarkComplete([]qtrace.Step{op("deposit", 1, 5, 5), op("withdraw", 2, 5, 0), settle(3)}, "r", atSec(3)), name, Defect, Fail},
	}, searches()...)
}

// The absence matrix, pending calls: a withdrawal invoked and never
// completed is a defect where the run settled (or, as before
// watermarks, where its log is taken whole), dropped where a gap after
// it or a lost tail may hold its completion, uncollected where its
// recorder states a watermark and no settle reaches it. Only a gap on
// the call's own recorder after its seq may hold the completion (a
// completion follows its invocation in its recorder's sequence): one
// before it, though at the same instant, or on another recorder of the
// actor, leaves it a defect. Finished agrees: pending calls are missed
// for a defect, no verdict otherwise. Every row is judged again through
// ResolvePending and Linearize, which must agree. Ablations: with the
// watermark ignored the uncollected row is a defect (Finished missed);
// with the gaps ignored the gap row is; with gaps judged by time the
// "before it" and "another recorder" rows are dropped.
func TestAbsentPending(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	depOn := func(rec string, seq uint64, at int, balance int64) qtrace.Step {
		return qtrace.Step{Action: "deposit", Recorder: rec, Seq: seq, Time: atSec(at), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": balance}}
	}
	dep := func(seq uint64, balance int64) qtrace.Step { return depOn("r", seq, int(seq), balance) }
	invAt := func(seq uint64, at int) qtrace.Step {
		return qtrace.Step{Action: "withdraw", Recorder: "r", Seq: seq, Time: atSec(at), Record: qtrace.Record{Invocation: true}, Args: map[string]any{"amount": int64(3)}}
	}
	inv := func(seq uint64) qtrace.Step { return invAt(seq, int(seq)) }
	settleAt := func(seq uint64, at int) qtrace.Step {
		return qtrace.Step{Action: "settled", Record: qtrace.Record{Settled: true}, Recorder: "r", Seq: seq, Time: atSec(at)}
	}
	settle := func(seq uint64) qtrace.Step { return settleAt(seq, int(seq)) }
	rows := []struct {
		absentRun
		finished Verdict
	}{
		{absentRun{"settled", []qtrace.Step{dep(1, 5), inv(2), dep(3, 10), settle(4)}, "withdraw", Defect, ""}, Fail},
		{absentRun{"whole, no settle", []qtrace.Step{dep(1, 5), inv(2), dep(3, 10)}, "withdraw", Defect, ""}, Fail},
		{absentRun{"a gap after it", []qtrace.Step{dep(1, 5), inv(2), dep(4, 10), settle(5)}, "withdraw", Dropped, ""}, NotEvaluated},
		{absentRun{"a gap before it, at its instant", []qtrace.Step{depOn("r", 1, 1, 5), invAt(3, 3), depOn("r", 4, 3, 10), settleAt(5, 4)}, "withdraw", Defect, ""}, Fail},
		{absentRun{"a gap on another recorder", []qtrace.Step{dep(1, 5), inv(2), dep(3, 10), depOn("q", 1, 4, 15), depOn("q", 3, 5, 20), settleAt(4, 6)}, "withdraw", Defect, ""}, Fail},
		{absentRun{"a lost tail", qtrace.MarkLost([]qtrace.Step{dep(1, 5), inv(2), dep(3, 10)}, qtrace.Lost{Recorder: "r", After: 3}), "withdraw", Dropped, ""}, NotEvaluated},
		{absentRun{"a watermark, no settle", qtrace.MarkComplete([]qtrace.Step{dep(1, 5), inv(2), dep(3, 10)}, "r", atSec(3)), "withdraw", Uncollected, ""}, NotEvaluated},
		{absentRun{"a watermark at the settle", qtrace.MarkComplete([]qtrace.Step{dep(1, 5), inv(2), dep(3, 10), settle(4)}, "r", atSec(4)), "withdraw", Defect, ""}, Fail},
	}
	var ars []absentRun
	for _, x := range rows {
		ars = append(ars, x.absentRun)
	}
	reps := checkAbsent(t, "testdata/funds.binding.yaml", ars, searches()...)
	for i, x := range rows {
		rep := reps[i]
		if f := rep.Finished(); f.Verdict != x.finished {
			t.Errorf("%s: finished %v (%s: %s), want %v:\n%s", x.name, f.Verdict, f.Reason, f.Why, x.finished, rep.Summary())
		}
		a := rep.Actors[0]
		if got := a.PendingCause(a.Pending[0]); got != x.cause {
			t.Errorf("%s: PendingCause %s, want %s", x.name, got, x.cause)
		}
	}
}

// Reconstruct keeps watermarks per recorder (the later of its own and
// every recorder's) and gaps with their times, and a settle a watermark
// does not reach settles nothing; JSONL keeps the record, and Filter
// keeps it, no later than its window's end.
func TestWatermarkRecords(t *testing.T) {
	t.Parallel()
	steps := []qtrace.Step{
		{Action: "a", Recorder: "x", Seq: 1, Time: atSec(1)},
		{Action: "b", Recorder: "x", Seq: 3, Time: atSec(5)},
		{Action: "settled", Recorder: "x", Seq: 4, Time: atSec(6), Record: qtrace.Record{Settled: true}},
	}
	steps = qtrace.MarkComplete(qtrace.MarkComplete(steps, "x", atSec(2)), qtrace.AllRecorders, atSec(4))
	trs, issues := qtrace.Reconstruct(steps)
	tr := trs[0]
	if through, every := tr.CompleteThrough(); !through.Equal(atSec(4)) || !every || tr.Settled {
		t.Errorf("complete through %v (every %v), settled %v; want 4s, every, unsettled: %v", through, every, tr.Settled, issues)
	}
	if len(tr.Gaps) != 1 || tr.Gaps[0].First != 2 || tr.Gaps[0].Last != 2 || !tr.Gaps[0].After.Equal(atSec(1)) || !tr.Gaps[0].Before.Equal(atSec(5)) {
		t.Errorf("gaps %v, want seq 2 between 1s and 5s", tr.Gaps)
	}
	var b strings.Builder
	if err := qtrace.WriteJSONL(&b, steps); err != nil {
		t.Fatal(err)
	}
	back, err := qtrace.ReadJSONL(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if trs, _ := qtrace.Reconstruct(back); len(trs[0].Collected) != 1 || !trs[0].Collected[0].Through.Equal(atSec(4)) {
		t.Errorf("read back: %v", trs[0].Collected)
	}
	f := qtrace.Filter{Until: atSec(3)}.Apply(back)
	for _, s := range f {
		if !s.CompleteThrough.IsZero() && !s.CompleteThrough.Before(atSec(3)) {
			t.Errorf("filtered until 3s: a watermark at %v", s.CompleteThrough)
		}
	}
	if trs, _ := qtrace.Reconstruct(f); len(trs) != 1 || len(trs[0].Collected) != 1 {
		t.Errorf("filtered: the watermark records are gone: %v", f)
	}
	// reached: the settle stands
	ok := qtrace.MarkComplete(steps[:3:3], "x", atSec(6))
	ok[1].Seq = 2
	if trs, _ := qtrace.Reconstruct(ok); !trs[0].Settled {
		t.Errorf("a watermark at the settle: unsettled")
	}
}

// A watermark within the clocks' slack of a settle reaches it, in
// Reconstruct and in a search's settles alike (the slack a step after a
// settle is given); one before it by more does not. A watermark of a
// recorder with no step counts in neither.
func TestWatermarkReachesSettle(t *testing.T) {
	t.Parallel()
	settle := atSec(3)
	steps := func(w time.Time, rec string) []qtrace.Step {
		return qtrace.MarkComplete([]qtrace.Step{
			{Action: "inc", Recorder: "r", Time: atSec(1)}, // no seqs: the watermark alone
			{Action: "settled", Record: qtrace.Record{Settled: true}, Recorder: "h", Seq: 1, Time: settle},
		}, rec, w)
	}
	for _, x := range []struct {
		name    string
		steps   []qtrace.Step
		settled bool
	}{
		{"within the slack", steps(settle.Add(-qtrace.RealTimeSlack/2), "r"), true},
		{"before it", steps(settle.Add(-2*qtrace.RealTimeSlack), "r"), false},
		{"a recorder with no step", steps(atSec(1), "nobody"), true},
	} {
		trs, _ := qtrace.Reconstruct(x.steps)
		if trs[0].Settled != x.settled || (len(settlesOf(x.steps)) > 0) != x.settled {
			t.Errorf("%s: Reconstruct settled %v, a search's settles %v; want %v", x.name, trs[0].Settled, settlesOf(x.steps), x.settled)
		}
	}
}

// A failure at or after a gap of the actor's recorders is no verdict
// (dropped steps), as one after a lost tail's cut is: the dropped step
// may explain it (the review's log, deposits seq 1, 2, 4 and a settle at
// 5, was "mismatch after deposit", a FAIL). A failure before every gap
// stands; after it means a later seq on the gap's recorder, a time the
// lost steps' may precede on another. Recorded steps that pass beside a
// gap are no verdict too, and Finished agrees with Reason in every row.
// Every row is judged again through ResolvePending and Linearize (a
// search's order re-sequences the steps: its failure is placed by its
// original), which agree but where a linearization may put the dropped
// step, its invocation and thread unknown, before another thread's step
// (linearized). Ablation: with judgeGaps off the "after" rows are FAILs;
// with steps on the gap's recorder placed by time alone, "before the
// gap, at its instant" is no verdict; with originals not looked up, the
// linearized FAILs are.
// TestGapFailureWins: a failure before every gap or cut is a FAIL,
// whatever another check's failure after it (no verdict) says, by
// Reason, the run's verdict and Finished alike. gapinv.yaml's first
// invariant, fewOps, fails after the gap (or the cut), its second,
// underLimit, before it: Reason once returned at the first invariant
// with no verdict, and the CLI printed "invariant underLimit: FAIL"
// above "RESULT: NO VERDICT".
func TestGapFailureWins(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/gapinv.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	dep := func(rec string, seq uint64, at int, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: "deposit", Recorder: rec, Thread: rec, Seq: seq, Time: atSec(at), Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	rows := []struct {
		name, nv string
		steps    []qtrace.Step
	}{
		{"a gap", "no verdict (dropped steps)", []qtrace.Step{dep("r", 1, 1, 60, 60), dep("r", 2, 2, 60, 120), dep("r", 4, 4, 1, 121)}},
		// q's step at 5s is after r's cut, its fewOps no verdict; r's
		// underLimit at seq 2 is before it
		{"a lost tail", "no verdict (lost steps)", qtrace.MarkLost([]qtrace.Step{dep("r", 1, 1, 60, 60), dep("r", 2, 2, 60, 120), dep("q", 1, 5, 1, 121)}, qtrace.Lost{Recorder: "r", After: 2})},
	}
	for _, x := range rows {
		rep, err := c.Check(context.Background(), x.steps)
		if err != nil {
			t.Fatalf("%s: %v", x.name, err)
		}
		if len(rep.Actors) != 1 {
			t.Fatalf("%s: %d actors", x.name, len(rep.Actors))
		}
		a := rep.Actors[0]
		if got := a.Reason(); got != "violates underLimit" {
			t.Errorf("%s: %q, want violates underLimit:\n%s", x.name, got, rep.Summary())
		}
		// the failing step is underLimit's (seq 2), not fewOps's after it
		if v := rep.Verdict(); v.Verdict != Fail || v.Failing == nil || v.Failing.Recorder != "r" || v.Failing.Seq != 2 {
			t.Errorf("%s: run verdict %v (%s) at %+v, want fail at seq 2 of r", x.name, v.Verdict, v.Reason, v.Failing)
		}
		if f := rep.Finished(); f.Verdict != Fail || f.Reason != "violates underLimit" {
			t.Errorf("%s: finished %v (%s), want fail (violates underLimit)", x.name, f.Verdict, f.Reason)
		}
		if !strings.Contains(rep.Summary(), "invariant fewOps: "+x.nv) {
			t.Errorf("%s: want fewOps %s:\n%s", x.name, x.nv, rep.Summary())
		}
	}
}

// TestGapLinearizedOtherThread: a linearization judges a gapped log no
// verdict though its recorded steps are one thread: the dropped step's
// thread may be one no recorded step has. Here t2's deposit (seq 3),
// invoked before t1's seq 2 completed, explains seq 2's balance, and the
// complete log conforms; with it dropped the log was once a FAIL.
func TestGapLinearizedOtherThread(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/funds.binding.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ms := func(n int) time.Time { return time.Unix(0, 0).Add(time.Duration(n) * time.Millisecond) }
	st := func(seq uint64, th string, inv, at int, balance int64) qtrace.Step {
		return qtrace.Step{Action: "deposit", Recorder: "r", Thread: th, Seq: seq, Invoked: ms(inv), Time: ms(at), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": balance}}
	}
	full := []qtrace.Step{st(1, "t1", 0, 1000, 5), st(2, "t1", 2000, 3000, 15), st(3, "t2", 1500, 4000, 10), st(4, "t1", 4500, 5000, 20)}
	for _, x := range []struct {
		name   string
		steps  []qtrace.Step
		reason string
	}{{"complete", full, ""}, {"seq 3 dropped", []qtrace.Step{full[0], full[1], full[3]}, "no verdict (dropped steps)"}} {
		j, err := c.Judge(context.Background(), x.steps, SearchOptions{Linearize: true})
		if err != nil {
			t.Fatal(err)
		}
		if got := j.Report.Actors[0].Reason(); got != x.reason {
			t.Errorf("%s: %q, want %q:\n%s", x.name, got, x.reason, j.Report.Summary())
		}
		// a passing actor's module is not named: the CLI removes its
		// temporary directory (it once printed a path that was gone)
		sum := j.Report.Summary()
		if named, want := strings.Contains(sum, "module: "), x.reason != ""; named != want {
			t.Errorf("%s: module named %v, want %v:\n%s", x.name, named, want, sum)
		}
	}
}

func TestGapFailures(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/funds.binding.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	depAt := func(rec string, seq uint64, at int, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: "deposit", Recorder: rec, Thread: rec, Seq: seq, Time: atSec(at), Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	dep := func(seq uint64, amount, balance int64) qtrace.Step { return depAt("r", seq, int(seq), amount, balance) }
	settle := func(rec string, seq uint64, at int) qtrace.Step {
		return qtrace.Step{Action: "settled", Record: qtrace.Record{Settled: true}, Recorder: rec, Thread: rec, Seq: seq, Time: atSec(at)}
	}
	wd := func(seq uint64, amount, balance int64) qtrace.Step {
		s := dep(seq, amount, balance)
		s.Action = "withdraw"
		return s
	}
	// clocked steps: one process's, ordered by a batch clock, not by seq
	clocked := func(seq uint64, batch int64, balance int64) qtrace.Step {
		s := dep(seq, 5, balance)
		s.Thread, s.Process, s.Order = "", "p", map[string]int64{"batch": batch}
		return s
	}
	dropped := "no verdict (dropped steps)"
	rows := []struct {
		name   string
		steps  []qtrace.Step
		reason string
		finish Verdict
		// linearized, if set, is the reason through Linearize (its
		// Finished no verdict)
		linearized string
	}{
		{"a mismatch after the gap", []qtrace.Step{dep(1, 5, 5), dep(2, 5, 10), dep(4, 5, 20), settle("r", 5, 5)}, dropped, NotEvaluated, ""},
		{"a violation after the gap", []qtrace.Step{dep(1, 50, 50), dep(2, 40, 90), dep(4, 20, 110), settle("r", 5, 5)}, dropped, NotEvaluated, ""},
		{"passing beside the gap", []qtrace.Step{dep(1, 5, 5), dep(2, 5, 10), dep(4, 5, 15), settle("r", 5, 5)}, dropped, NotEvaluated, ""},
		// a linearization may place the dropped step, of a thread and an
		// invocation unknown, before any recorded one
		{"a mismatch before the gap", []qtrace.Step{dep(1, 5, 5), dep(2, 5, 11), dep(4, 5, 20), settle("r", 5, 5)}, "mismatch after deposit", Fail, dropped},
		{"a violation before the gap", []qtrace.Step{dep(1, 50, 50), dep(2, 60, 110), dep(4, 5, 115), settle("r", 5, 5)}, "violates underLimit", Fail, dropped},
		// one instant: the seqs order them
		{"a mismatch before the gap, at its instant", []qtrace.Step{depAt("r", 1, 1, 5, 5), depAt("r", 2, 1, 5, 11), depAt("r", 4, 1, 5, 20), settle("r", 5, 1)}, "mismatch after deposit", Fail, dropped},
		// the label is the gap's, as Finished's (it once said lost steps)
		{"an eventual val false after the gap", []qtrace.Step{dep(1, 5, 5), wd(3, 5, 0), settle("r", 4, 4)}, dropped, NotEvaluated, ""},
		// batch 4 (seq 2) is checked last, after seq 4's batch 2; the
		// dropped seq 3 may be batch 3, the deposit that explains it, or
		// batch 0, before every recorded step: under a domain clock no
		// failure stands after a gap (it once was a mismatch, a FAIL)
		{"a mismatch a clock orders after the gap", []qtrace.Step{clocked(1, 1, 5), clocked(2, 4, 20), clocked(4, 2, 10), settle("r", 5, 5)}, dropped, NotEvaluated, ""},
		{"a mismatch a clock orders first", []qtrace.Step{clocked(1, 1, 6), clocked(2, 2, 10), clocked(4, 3, 15), settle("r", 5, 5)}, dropped, NotEvaluated, ""},
		// recorder q lost seq 2 between 10s and 12s: r's mismatch at 3s
		// precedes it, at 11s it may follow it
		{"a mismatch before another recorder's gap", []qtrace.Step{dep(1, 5, 5), depAt("r", 2, 3, 5, 11), depAt("q", 1, 10, 5, 15), depAt("q", 3, 12, 5, 20), settle("q", 4, 13)}, "mismatch after deposit", Fail, dropped},
		{"a mismatch after another recorder's gap", []qtrace.Step{dep(1, 5, 5), depAt("q", 1, 10, 5, 10), depAt("r", 2, 11, 5, 99), depAt("q", 3, 12, 5, 20), settle("q", 4, 13)}, dropped, NotEvaluated, ""},
	}
	runs := make([][]qtrace.Step, len(rows))
	for i, x := range rows {
		runs[i] = x.steps
	}
	judge := func(how string, linearized bool, reps []*Report) {
		for i, x := range rows {
			rep := reps[i]
			if linearized && x.linearized != "" {
				x.reason, x.finish = x.linearized, NotEvaluated
			}
			if len(rep.Actors) != 1 {
				t.Fatalf("%s (%s): %d actors", x.name, how, len(rep.Actors))
			}
			if got := rep.Actors[0].Reason(); got != x.reason {
				t.Errorf("%s (%s): %q, want %q:\n%s", x.name, how, got, x.reason, rep.Summary())
			}
			if f := rep.Finished(); f.Verdict != x.finish {
				t.Errorf("%s (%s): finished %v (%s), want %v:\n%s", x.name, how, f.Verdict, f.Reason, x.finish, rep.Summary())
			}
			if v := rep.Verdict(); x.reason == dropped && v.Verdict != NotEvaluated {
				t.Errorf("%s (%s): run verdict %v, want no verdict", x.name, how, v.Verdict)
			}
		}
	}
	reps, errs := c.CheckBatch(context.Background(), runs, 0)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("%s: %v", rows[i].name, err)
		}
	}
	judge("as recorded", false, reps)
	for _, so := range searches() {
		js, errs := c.JudgeBatch(context.Background(), runs, so, nil)
		reps := make([]*Report, len(js))
		for i, j := range js {
			if errs[i] != nil {
				t.Fatalf("%s, %+v: %v", rows[i].name, so, errs[i])
			}
			reps[i] = j.Report
		}
		judge(fmt.Sprintf("%+v", so), so.Linearize, reps)
	}
}

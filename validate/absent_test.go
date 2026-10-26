package validate

import (
	"context"
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

// checkAbsent checks each row with the binding's checker, and returns
// the rows' reports.
func checkAbsent(t *testing.T, bind string, rows []absentRun) []*Report {
	t.Helper()
	c, err := NewChecker(bind, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var reps []*Report
	for _, x := range rows {
		rep, err := c.Check(context.Background(), x.steps)
		if err != nil {
			t.Fatalf("%s: %v", x.name, err)
		}
		reps = append(reps, rep)
		var got []Absence
		for _, a := range rep.Absent() {
			if x.absent == "" || a.Name == x.absent {
				got = append(got, a)
			}
		}
		switch {
		case x.absent == "" && len(got) > 0:
			t.Errorf("%s: absent %v, want nothing absent:\n%s", x.name, got, rep.Summary())
		case x.absent != "" && (len(got) == 0 || got[0].Cause != x.cause):
			t.Errorf("%s: absent %v, want %s %s:\n%s", x.name, got, x.absent, x.cause, rep.Summary())
		case x.absent != "" && !strings.Contains(rep.Summary(), "\n    "+x.absent+": "+x.cause.label()+": "):
			t.Errorf("%s: the report's absent section does not say it:\n%s", x.name, rep.Summary())
		}
		var v Verdict
		for _, inv := range rep.Actors[0].Invariants {
			if inv.Name == x.absent {
				v = inv.Verdict
			}
		}
		if strings.HasPrefix(x.absent, "within") || strings.HasPrefix(x.absent, "eventually") {
			if v != x.verdict {
				t.Errorf("%s: %s %q, want %q:\n%s", x.name, x.absent, v, x.verdict, rep.Summary())
			}
		}
		// only a defect is a FAIL: a dropped or uncollected step makes no
		// verdict, never a pass
		if x.absent != "" && x.cause != Defect && x.verdict == NotEvaluated && rep.OK() {
			t.Errorf("%s: a run with no verdict passes:\n%s", x.name, rep.Summary())
		}
	}
	return reps
}

// atSec is t seconds after the epoch.
func atSec(t int) time.Time { return time.Unix(0, 0).Add(time.Duration(t) * time.Second) }

// The absence matrix, bounded responses: a leader due 3s after a heal at
// 1s, never elected. Dropped where a gap or a lost tail of the actor's
// recorder falls in the window (its failure no verdict); uncollected
// where the watermark ends before the window (no verdict, which the
// trace going on once made a FAIL) or the trace does (open); a defect,
// a FAIL, where the trace settled, went on past the window with no
// watermark, or its watermark is past the window although its steps
// end before it: what lets a production trace, which never settles, be
// judged. Ablations: with the watermark ignored the "watermark past the
// window" row is open (uncollected, not a defect) and the "watermark
// before the window" row a FAIL (a defect); with the gaps ignored the
// gap row is a FAIL.
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
	settled := func(h []qtrace.Step) []qtrace.Step {
		last := h[len(h)-1]
		return append(h, qtrace.Step{Action: "settled", Settled: true, Recorder: "n", Seq: last.Seq + 1, Time: last.Time})
	}
	gapped := steps("crash", 0, "heal", 1, "tick", 6)
	gapped[2].Seq = 4 // seq 3, recorded between 1s and 6s, was lost
	name := "within 3s after healed: hasLeader"
	checkAbsent(t, "testdata/within.yaml", []absentRun{
		{"met", steps("crash", 0, "heal", 1, "elect", 2), "", "", ""},
		{"met, watermark past the window", qtrace.MarkComplete(steps("crash", 0, "heal", 1, "elect", 2), "n", atSec(10)), "", "", ""},
		{"settled", settled(steps("crash", 0, "heal", 1, "tick", 2)), name, Defect, Fail},
		{"went on past the window", steps("crash", 0, "heal", 1, "tick", 6), name, Defect, Fail},
		{"watermark past the window", qtrace.MarkComplete(steps("crash", 0, "heal", 1, "tick", 2), "n", atSec(10)), name, Defect, Fail},
		{"every recorder's watermark", qtrace.MarkComplete(steps("crash", 0, "heal", 1, "tick", 2), qtrace.AllRecorders, atSec(10)), name, Defect, Fail},
		{"late", qtrace.MarkComplete(steps("crash", 0, "heal", 1, "elect", 6), "n", atSec(3)), name, Defect, Fail},
		{"ended before the window", steps("crash", 0, "heal", 1, "tick", 2), name, Uncollected, ""},
		{"watermark before the window", qtrace.MarkComplete(steps("crash", 0, "heal", 1, "tick", 6), "n", atSec(3)), name, Uncollected, NotEvaluated},
		{"watermark before the window, ended", qtrace.MarkComplete(steps("crash", 0, "heal", 1, "tick", 2), "n", atSec(3)), name, Uncollected, ""},
		{"a gap in the window", settled(gapped), name, Dropped, NotEvaluated},
		{"a lost tail", qtrace.MarkLost(steps("crash", 0, "heal", 1, "tick", 2, "tick", 2), qtrace.Lost{Recorder: "n", After: 3}), name, Dropped, ""},
	})
}

// The absence matrix, eventual vals: funded (money left) false at the
// settle is a defect, a FAIL; with a gap before it, dropped (the lost
// step may be the deposit that funds it: no verdict); with a lost tail,
// dropped (not judged: the tail may hold the settle); with no settle, or
// a settle the recorder's watermark does not reach, uncollected (not
// judged). Ablation: with the watermark ignored the last row settles,
// and funded is a FAIL.
func TestAbsentEventually(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	op := func(action string, seq uint64, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: action, Recorder: "r", Seq: seq, Time: atSec(int(seq)), Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	settle := func(seq uint64) qtrace.Step {
		return qtrace.Step{Action: "settled", Settled: true, Recorder: "r", Seq: seq, Time: atSec(int(seq))}
	}
	name := EventuallyName("funded")
	checkAbsent(t, "testdata/funds.binding.yaml", []absentRun{
		{"held", []qtrace.Step{op("deposit", 1, 5, 5), settle(2)}, "", "", ""},
		{"false at the settle", []qtrace.Step{op("deposit", 1, 5, 5), op("withdraw", 2, 5, 0), settle(3)}, name, Defect, Fail},
		{"a gap before the settle", []qtrace.Step{op("deposit", 1, 5, 5), op("withdraw", 3, 5, 0), settle(4)}, name, Dropped, NotEvaluated},
		{"a lost tail", qtrace.MarkLost([]qtrace.Step{op("deposit", 1, 5, 5), op("withdraw", 2, 5, 0)}, qtrace.Lost{Recorder: "r", After: 2}), name, Dropped, ""},
		{"no settle", []qtrace.Step{op("deposit", 1, 5, 5), op("withdraw", 2, 5, 0)}, name, Uncollected, ""},
		{"a watermark before the settle", qtrace.MarkComplete([]qtrace.Step{op("deposit", 1, 5, 5), op("withdraw", 2, 5, 0), settle(3)}, "r", atSec(2)), name, Uncollected, ""},
		{"a watermark at the settle", qtrace.MarkComplete([]qtrace.Step{op("deposit", 1, 5, 5), op("withdraw", 2, 5, 0), settle(3)}, "r", atSec(3)), name, Defect, Fail},
	})
}

// The absence matrix, pending calls: a withdrawal invoked and never
// completed is a defect where the run settled (or, as before
// watermarks, where its log is taken whole), dropped where a gap after
// it or a lost tail may hold its completion, uncollected where its
// recorder states a watermark and no settle reaches it. Finished agrees:
// pending calls are missed for a defect, no verdict otherwise.
// Ablations: with the watermark ignored the uncollected row is a defect
// (Finished missed); with the gaps ignored the gap row is.
func TestAbsentPending(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	dep := func(seq uint64, balance int64) qtrace.Step {
		return qtrace.Step{Action: "deposit", Recorder: "r", Seq: seq, Time: atSec(int(seq)), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": balance}}
	}
	inv := func(seq uint64) qtrace.Step {
		return qtrace.Step{Action: "withdraw", Recorder: "r", Seq: seq, Time: atSec(int(seq)), Invocation: true, Args: map[string]any{"amount": int64(3)}}
	}
	settle := func(seq uint64) qtrace.Step {
		return qtrace.Step{Action: "settled", Settled: true, Recorder: "r", Seq: seq, Time: atSec(int(seq))}
	}
	rows := []struct {
		absentRun
		finished Verdict
	}{
		{absentRun{"settled", []qtrace.Step{dep(1, 5), inv(2), dep(3, 10), settle(4)}, "withdraw", Defect, ""}, Fail},
		{absentRun{"whole, no settle", []qtrace.Step{dep(1, 5), inv(2), dep(3, 10)}, "withdraw", Defect, ""}, Fail},
		{absentRun{"a gap after it", []qtrace.Step{dep(1, 5), inv(2), dep(4, 10), settle(5)}, "withdraw", Dropped, ""}, Fail}, // (the gap is an issue: missed as such)
		{absentRun{"a lost tail", qtrace.MarkLost([]qtrace.Step{dep(1, 5), inv(2), dep(3, 10)}, qtrace.Lost{Recorder: "r", After: 3}), "withdraw", Dropped, ""}, NotEvaluated},
		{absentRun{"a watermark, no settle", qtrace.MarkComplete([]qtrace.Step{dep(1, 5), inv(2), dep(3, 10)}, "r", atSec(3)), "withdraw", Uncollected, ""}, NotEvaluated},
		{absentRun{"a watermark at the settle", qtrace.MarkComplete([]qtrace.Step{dep(1, 5), inv(2), dep(3, 10), settle(4)}, "r", atSec(4)), "withdraw", Defect, ""}, Fail},
	}
	var ars []absentRun
	for _, x := range rows {
		ars = append(ars, x.absentRun)
	}
	reps := checkAbsent(t, "testdata/funds.binding.yaml", ars)
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

// A search's order carries what the recorded steps say of collection: a
// pending call resolved with -resolve-pending under a watermark is still
// uncollected, not a defect (the re-sequenced recorder would otherwise
// be a log taken whole), and a gap of the recorded steps still drops it.
func TestAbsentThroughASearch(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/funds.binding.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	dep := func(seq uint64, balance int64) qtrace.Step {
		return qtrace.Step{Action: "deposit", Recorder: "r", Seq: seq, Time: atSec(int(seq)), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": balance}}
	}
	inv := qtrace.Step{Action: "withdraw", Recorder: "r", Seq: 2, Time: atSec(2), Invocation: true, Args: map[string]any{"amount": int64(3)}}
	for _, x := range []struct {
		name  string
		steps []qtrace.Step
		cause Cause
	}{
		{"a watermark", qtrace.MarkComplete([]qtrace.Step{dep(1, 5), inv, dep(3, 10)}, "r", atSec(3)), Uncollected},
		{"a gap", []qtrace.Step{dep(1, 5), inv, dep(4, 10)}, Dropped},
		{"whole", []qtrace.Step{dep(1, 5), inv, dep(3, 10)}, Defect},
	} {
		j, err := c.Judge(context.Background(), x.steps, SearchOptions{ResolvePending: true})
		if err != nil {
			t.Fatal(err)
		}
		ab := j.Report.Absent()
		if len(ab) == 0 || ab[len(ab)-1].Kind != "pending" || ab[len(ab)-1].Cause != x.cause {
			t.Errorf("%s: absent %v, want the call %s:\n%s", x.name, ab, x.cause, j.Report.Summary())
		}
	}
}

// A bounded response judged on a search's order (one that resolves
// pending calls re-sequences every actor onto a recorder of its own)
// keeps the watermark the steps stated: before the window, no verdict;
// without the record the order carries, its one recorder's log would be
// taken whole, and the trace going on past the window a FAIL.
func TestWithinThroughASearch(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/within.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var steps []qtrace.Step
	for i, a := range []string{"crash", "heal", "tick"} {
		steps = append(steps, qtrace.Step{Action: a, Recorder: "n", Seq: uint64(i + 1), Time: atSec([]int{0, 1, 6}[i])})
	}
	name := c.Binding.Within[0].Name()
	for _, x := range []struct {
		name  string
		steps []qtrace.Step
		want  Verdict
		cause Cause
	}{
		{"a watermark before the window", qtrace.MarkComplete(steps, "n", atSec(3)), NotEvaluated, Uncollected},
		{"past it", qtrace.MarkComplete(steps, "n", atSec(10)), Fail, Defect},
	} {
		j, err := c.Judge(context.Background(), x.steps, SearchOptions{ResolvePending: true})
		if err != nil {
			t.Fatal(err)
		}
		var got Verdict
		for _, inv := range j.Report.Actors[0].Invariants {
			if inv.Name == name {
				got = inv.Verdict
			}
		}
		ab := j.Report.Absent()
		if got != x.want || len(ab) != 1 || ab[0].Cause != x.cause {
			t.Errorf("%s: %s %q, absent %v; want %q, %s:\n%s", x.name, name, got, ab, x.want, x.cause, j.Report.Summary())
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
		{Action: "settled", Recorder: "x", Seq: 4, Time: atSec(6), Settled: true},
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

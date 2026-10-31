package validate

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// snapshotRuns are runs of snapshot.yaml: a log that lost seqs 1..2 and
// starts with the state restore recorded (balance 50), then a deposit of
// 5 that saw balance (the passing run 55, the failing run 5: what a
// replay from init would see); and a run with no snapshot. The deposit
// is on a client of its own, recorded after the snapshot, its invocation
// unknown: a linearization that put it first would start from init.
func snapshotRuns() (pass, fail, plain []qtrace.Step) {
	ms := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
	run := func(balance int64) []qtrace.Step {
		return []qtrace.Step{
			{Action: "restore", Recorder: "r", Seq: 3, Invoked: ms(0), Time: ms(10), Args: map[string]any{"b": int64(50), "n": int64(3)}},
			{Action: "deposit", Recorder: "c", Seq: 1, Time: ms(20), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": balance}},
		}
	}
	plain = []qtrace.Step{{Action: "deposit", Recorder: "c", Seq: 1, Time: ms(1), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}}
	return run(55), run(5), plain
}

// A trace whose first step is the binding's snapshot is judged from the
// state it recorded, not init, by every check: Check, CheckBatch (a run
// starting from init beside it), and Judge's linearization, which keeps
// the snapshot first in every order (with the Go evaluator, quint alone,
// or both, compared). The lost prefix of its log is what the snapshot recorded, said
// so; the verdict's key says the binding starts there, so a checker
// without the snapshot (whose verdict is the lost prefix's error) shares
// no cached verdict with it.
func TestSnapshotStart(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	pass, fail, plain := snapshotRuns()
	c, err := NewChecker("testdata/snapshot.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	verdicts := func(name string, reps ...*Report) {
		t.Helper()
		for i, want := range []string{"", "mismatch after deposit", ""}[:len(reps)] {
			if got := reps[i].Actors[0].Reason(); got != want {
				t.Errorf("%s, run %d: %q, want %q:\n%s", name, i, got, want, reps[i].Summary())
			}
		}
		if s := reps[0].Summary(); !strings.Contains(s, "info lost-prefix: judged from the state recorded by restore at seq 3 (recorder r lost seqs 1..2)") {
			t.Errorf("%s: the report does not say where the trace starts:\n%s", name, s)
		}
	}
	var reps []*Report
	for _, steps := range [][]qtrace.Step{pass, fail} {
		rep, err := c.Check(ctx, steps)
		if err != nil {
			t.Fatal(err)
		}
		reps = append(reps, rep)
	}
	verdicts("Check", reps...)
	reps, errs := c.CheckBatch(ctx, [][]qtrace.Step{pass, fail, plain}, 3) // one module: a start per run
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	verdicts("CheckBatch", reps...)
	// shadowed, the Go evaluator's verdicts are quint's (it starts from the
	// snapshot too)
	for _, eval := range []string{"go", "quint", "shadow"} {
		cc := *c
		cc.Options.Eval = eval
		reps = nil
		for _, steps := range [][]qtrace.Step{pass, fail} {
			j, err := cc.Judge(ctx, steps, SearchOptions{Linearize: true})
			if err != nil {
				t.Fatal(err)
			}
			reps = append(reps, j.Report)
		}
		verdicts("Judge, linearized, eval "+eval, reps...)
	}
	// a deposit invoked before the snapshot was recorded may be in the
	// state it recorded (55 = 50 + 5, then the deposit saw 55): the replay
	// cannot say so, and its failure is no verdict; a later step's that
	// conforms after it stands (the passing run, overlapping too)
	overlap := slices.Clone(pass)
	overlap[0].Args = map[string]any{"b": int64(55), "n": int64(3)}
	overlap[1].Invoked = time.Unix(0, int64(5*time.Millisecond))
	passing := slices.Clone(pass)
	passing[1].Invoked = overlap[1].Invoked
	for _, x := range []struct {
		steps  []qtrace.Step
		reason string
	}{{overlap, "no verdict (snapshot overlap)"}, {passing, ""}} {
		j, err := c.Judge(ctx, x.steps, SearchOptions{Linearize: true})
		if err != nil {
			t.Fatal(err)
		}
		if got := j.Report.Actors[0].Reason(); got != x.reason {
			t.Errorf("an overlapping deposit: %q, want %q:\n%s", got, x.reason, j.Report.Summary())
		}
	}
	// a history by key starts every key's from the snapshot (it has no key:
	// the whole state)
	if j, err := c.Judge(ctx, pass, SearchOptions{Linearize: true, Key: "amount"}); err != nil || !j.Report.OK() {
		t.Errorf("by key: %v\n%v", err, j)
	}
	// a pending deposit invoked before the snapshot was recorded takes
	// effect after it, if at all: the deposit that saw 10 (init's 5, and
	// 5) is no balance from 50
	pendingRun := append([]qtrace.Step{
		{Action: "deposit", Recorder: "p", Seq: 1, Record: qtrace.Record{Invocation: true}, Time: time.Unix(0, int64(time.Millisecond)), Args: map[string]any{"amount": int64(5)}}}, fail...)
	pendingRun[2].Obs = map[string]any{"balance": int64(10)}
	pendingRun[2].Invoked = time.Unix(0, int64(15*time.Millisecond)) // after the snapshot: no overlap
	if j, err := c.Judge(ctx, pendingRun, SearchOptions{ResolvePending: true}); err != nil || j.Report.OK() {
		t.Errorf("a pending call before the snapshot: %v\n%s", err, j.Report.Summary())
	}
	// the same steps from init: the lost prefix an error, another verdict
	fromInit, err := checkerFixtureVariant(t, "testdata/snapshot.yaml", map[string]any{"snapshot": nil})
	if err != nil {
		t.Fatal(err)
	}
	defer fromInit.Close()
	if c.Key(pass) == fromInit.Key(pass) {
		t.Errorf("the key does not say the trace starts from the snapshot")
	}
	rep, err := fromInit.Check(ctx, pass)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() || !strings.Contains(rep.Summary(), "error lost-prefix") {
		t.Errorf("from init:\n%s", rep.Summary())
	}
}

// The search over hidden actions starts from the snapshot's state too: a
// read at c of what a knew at the log's head conforms (two rounds of
// gossip), which from init it cannot.
func TestSnapshotStartHidden(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/gossip_snapshot.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	restore := qtrace.Step{Action: "restoreA", Recorder: "r", Seq: 4, Actor: "g", Args: map[string]any{"a": "[1]"}}
	for _, c2 := range []struct {
		seen []int
		ok   bool
	}{{[]int{1}, true}, {[]int{2}, false}} {
		rep, err := c.Check(context.Background(), []qtrace.Step{restore, gossipStep(5, "c", 0, c2.seen)})
		if err != nil {
			t.Fatal(err)
		}
		if rep.OK() != c2.ok || c2.ok && !strings.Contains(rep.Summary(), "judged from the state recorded by restoreA at seq 4") {
			t.Errorf("c saw %v: OK %v, want %v:\n%s", c2.seen, rep.OK(), c2.ok, rep.Summary())
		}
	}
}

// A trace of its snapshot alone is rejected when the snapshot is no state
// of the model (restore's balance negative), as any rejection: by its
// action, not as quint's error at init; and the snapshot is a step.
func TestSnapshotAlone(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/snapshot.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []int64{-1, 5} {
		rep, err := c.Check(context.Background(), []qtrace.Step{{Action: "restore", Recorder: "r", Seq: 1, Args: map[string]any{"b": b, "n": int64(0)}}})
		if err != nil {
			t.Fatal(err)
		}
		a := rep.Actors[0]
		want := ""
		if b < 0 {
			want = "rejects restore"
		}
		if a.Reason() != want || a.Steps != 1 || b < 0 && !strings.Contains(a.Summary(), "the snapshot step the trace starts from is no state of the model") {
			t.Errorf("restore(%d): %q, %d steps, want %q:\n%s", b, a.Reason(), a.Steps, want, a.Summary())
		}
	}
}

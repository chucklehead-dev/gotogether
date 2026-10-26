package qnemesis

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// recordedTarget is a fake target that names its incarnations'
// recorders and logs.
type recordedTarget struct {
	*fakeTarget
	recorders func(int) []string
	logs      func(int) []string
	started   time.Time
}

func (r *recordedTarget) RecordedBy(inc int) ([]string, []string, time.Time) {
	var logs []string
	if r.logs != nil {
		logs = r.logs(inc)
	}
	return r.recorders(inc), logs, r.started
}

// A server that replied (count 1) and was killed before it recorded its
// inc leaves a log that ends cleanly; the client recorded what it saw.
// The crash declares the incarnation's tail lost, so the client's step,
// which depends on the lost one, is no verdict rather than a FAIL: the
// model (testdata/ctr.qnt) cannot take saw(1) from count 0.
func TestCrashDeclaresTheTailLost(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	c := &calls{start: time.Now()}
	started := time.Now()
	mem := &qobs.MemorySink{}
	client := qobs.NewRecorder(mem, qobs.WithID("client"), qobs.WithProcess("sys"))
	client.Record(context.Background(), "saw", nil, "n", 1) // the reply came; its record never did
	srv := &recordedTarget{fakeTarget: &fakeTarget{name: "srv", c: c, alive: true, inc: 1}, started: started,
		recorders: func(inc int) []string { return []string{"srv-1"} }}
	rc := Recording{"crash": func(Event) (string, []any) { return "crash", nil }, "restart": func(Event) (string, []any) { return "restart", nil }}
	n := &Nemesis{Targets: map[string]Target{"srv": srv}, Rec: qobs.NewRecorder(mem, qobs.WithID(RecorderID), qobs.WithProcess("sys")), Record: rc}
	if err := n.Run(context.Background(), Schedule{{Kind: "nemesis.crash.srv"}}); err != nil {
		t.Fatal(err)
	}
	steps := mem.Steps()
	if len(steps) != 4 || steps[1].Lost != "suffix" || steps[1].Recorder != "srv-1" || steps[1].Actor != "default" || !steps[1].Time.Equal(started) {
		t.Fatalf("steps %+v; want saw, srv-1's tail lost from its start, crash, restart", steps)
	}
	r := qtest.Validate(t, "testdata/ctr.yaml", steps)
	for _, a := range r.Report.Actors {
		if a.Conforms == validate.Fail {
			t.Fatalf("a step that depends on the killed incarnation's lost tail failed:\n%s", r.Report.Summary())
		}
	}
	if len(r.Report.Actors) == 0 || len(r.Report.Actors[0].Lost) == 0 {
		t.Fatalf("no loss reached the actor:\n%s", r.Report.Summary())
	}
}

// A real incarnation's loss records name each recorder of its logs,
// cut at its last step there.
func TestCrashLossRecordsFromTheLogs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := child(t, dir, "n1")
	p.Logs = func(inc int) []string { return []string{filepath.Join(dir, "n1."+itoa(inc)+".jsonl")} }
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	a := addrOf(t, dir, "n1", 1)
	if _, err := get(a, "/inc", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	mem := &qobs.MemorySink{}
	n := &Nemesis{Targets: map[string]Target{"n1": p}, Rec: qobs.NewRecorder(mem, qobs.WithID(RecorderID))}
	if err := n.Run(ctx, Schedule{{Kind: "nemesis.crash.n1"}}); err != nil {
		t.Fatal(err)
	}
	logged, _ := readLog(t, filepath.Join(dir, "n1.1.jsonl"))
	last := logged[len(logged)-1]
	steps := mem.Steps()
	if len(steps) != 2 || steps[0].Lost != "suffix" || steps[0].Recorder != "n1-1" || steps[0].LostAfter != last.Seq || !steps[0].Time.Equal(last.Time) || steps[1].Action != "crash" {
		t.Fatalf("nemesis steps %+v; want n1-1's tail lost after seq %d (%v), then the crash", steps, last.Seq, last.Time)
	}
	// and Reconstruct reads it as the incarnation's lost tail
	trs, _ := qtrace.Reconstruct(append(logged, steps...))
	if len(trs) != 1 || len(trs[0].Lost) == 0 || trs[0].Lost[0].Recorder != "n1-1" {
		t.Fatalf("no loss of n1-1 in the reconstructed trace: %+v", trs)
	}
}

func itoa(n int) string { return string(rune('0' + n)) }

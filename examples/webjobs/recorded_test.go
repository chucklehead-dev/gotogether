package harness

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The journey recorded with the real Bombadil (testdata/runs/faithful):
// a finished one (it conforms, the server drained and settled, the
// within holds, every click was answered on the page), and the CLI's
// manifest of it replays.
func TestRecorded(t *testing.T) {
	if mutant != "" {
		t.Skip("webjobs as it is; a mutant's recorded run is TestMutant's")
	}
	t.Parallel()
	qtest.RequireQuint(t)
	recorded(t, "faithful").Expect(t, qtest.Finishes())
	t.Run("replays", func(t *testing.T) { replays(t, filepath.Join("testdata", "runs", "faithful")) })
}

// edited copies the recorded faithful journey into a directory of the
// test's, with its trace.jsonl's lines passed through edit (nil: dropped).
func edited(t *testing.T, edit func(n int, line string) *string) string {
	t.Helper()
	from, dir := filepath.Join("testdata", "runs", "faithful"), t.TempDir()
	for _, f := range []string{ServerFile, "run-info.json"} {
		copyFile(t, filepath.Join(from, f), filepath.Join(dir, f))
	}
	b, err := os.ReadFile(filepath.Join(from, TraceFile))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i, l := range strings.SplitAfter(string(b), "\n") {
		if l == "" {
			continue
		}
		if e := edit(i+1, l); e != nil {
			out = append(out, *e)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, TraceFile), []byte(strings.Join(out, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A click whose outcome the page never showed is pending: the recorded
// journey with its first submission's answer taken out of the trace
// (the page shows what it showed before, from that line on) does not
// finish, whatever the server did.
func TestClickWithNoOutcome(t *testing.T) {
	if mutant != "" {
		t.Skip("webjobs as it is")
	}
	t.Parallel()
	qtest.RequireQuint(t)
	var before json.RawMessage
	cut := false
	dir := edited(t, func(n int, l string) *string {
		if cut {
			return nil
		}
		var e map[string]json.RawMessage
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(e["action"]), `"ui.submit"`) {
			e["snapshots"], cut = before, true // the click, and no answer seen
			b, _ := json.Marshal(e)
			s := string(b) + "\n"
			return &s
		}
		before = e["snapshots"]
		return &l
	})
	steps, err := Steps(dir)
	if err != nil {
		t.Fatal(err)
	}
	run := qtest.Validate(t, "webjobs.binding.yaml", steps)
	run.Expect(t, qtest.Pending("submit"))
	qtest.Runs(run).Expect(t, qtest.None(qtest.Finishes()))
	if f := run.Report.Finished(); f.Verdict != validate.Fail || !strings.Contains(f.Reason, "pending") {
		t.Errorf("finished: %+v, want a miss for the pending click", f)
	}
}

// A trace whose last line was cut off (Bombadil killed while writing it)
// is a lost tail: the steps before it are judged, and the run has no
// verdict for its finish, since the lost line may hold an answer.
func TestTornTrace(t *testing.T) {
	if mutant != "" {
		t.Skip("webjobs as it is")
	}
	t.Parallel()
	qtest.RequireQuint(t)
	b, err := os.ReadFile(filepath.Join("testdata", "runs", "faithful", TraceFile))
	if err != nil {
		t.Fatal(err)
	}
	last := strings.Count(string(b), "\n")
	dir := edited(t, func(n int, l string) *string {
		if n == last {
			l = l[:len(l)/2]
		}
		return &l
	})
	steps, err := Steps(dir)
	var torn *qtrace.TornTail
	if !errors.As(err, &torn) {
		t.Fatalf("err %v, want a torn tail", err)
	}
	run := qtest.Validate(t, "webjobs.binding.yaml", steps)
	run.Expect(t, qtest.Conforms())
	if f := run.Report.Finished(); f.Verdict != validate.NotEvaluated || !strings.Contains(f.Reason, "lost tail") {
		t.Errorf("finished: %+v, want no verdict (lost tail)", f)
	}
}

// rerecorded is what a journey's page steps become when recorded again
// through a qobs.Recorder at validation time, instead of written as the
// trace has them: each stamped with the time of the conversion and
// numbered in the recorder's own sequence.
func rerecorded(t *testing.T, steps []qtrace.Step) []qtrace.Step {
	t.Helper()
	mem := &qobs.MemorySink{}
	rec := qobs.NewRecorder(mem, qobs.WithID("ui"), qobs.WithActor("webjobs"), qobs.WithProcess("webjobs"))
	ctx := t.Context()
	calls := map[uint64]*qobs.Call{}
	var out []qtrace.Step
	for _, s := range steps {
		if s.Recorder != "ui" {
			out = append(out, s)
			continue
		}
		var kv []any
		for k, v := range s.Args {
			kv = append(kv, k, v)
		}
		switch {
		case s.Invocation:
			calls[s.Seq] = rec.Invoke(ctx, s.Action, kv...)
		case s.Call != 0:
			calls[s.Call].Done(nil, kv...)
		default:
			rec.Record(ctx, s.Action, nil, kv...)
		}
	}
	return append(out, mem.Steps()...)
}

// The page's steps keep the trace's own times (goo's point): recorded
// again through a Recorder, stamped with validation's time, they all
// follow the server's, so readyEarly's page shows ready only after the
// commit it preceded (the bug hidden: the run conforms), and the
// faithful journey's page acts after the server's settle (which then
// settles nothing: no finish).
func TestTimesKept(t *testing.T) {
	if mutant != "" {
		t.Skip("webjobs as it is")
	}
	t.Parallel()
	qtest.RequireQuint(t)
	steps, err := Steps(filepath.Join("testdata", "runs", "readyEarly"))
	if err != nil {
		t.Fatal(err)
	}
	qtest.Validate(t, "webjobs.binding.yaml", steps).Expect(t, qtest.Violates("readyIsCommitted"))
	qtest.Validate(t, "webjobs.binding.yaml", rerecorded(t, steps)).Expect(t, qtest.Conforms())

	steps, err = Steps(filepath.Join("testdata", "runs", "faithful"))
	if err != nil {
		t.Fatal(err)
	}
	qtest.Runs(qtest.Validate(t, "webjobs.binding.yaml", rerecorded(t, steps))).Expect(t, qtest.None(qtest.Finishes()))
}

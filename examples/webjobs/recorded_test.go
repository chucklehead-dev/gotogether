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
	// its manifest and replay, as the CLI makes them (cli.Check,
	// cli.Replay): two more checks, no CLI to build
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

// A request whose answer the page never showed is pending: the recorded
// journey cut at its first click, the requests it sent left unanswered
// in the log, does not finish, whatever the server did.
func TestClickWithNoOutcome(t *testing.T) {
	if mutant != "" {
		t.Skip("webjobs as it is")
	}
	if testing.Short() {
		t.Skip("a pending click's claim, per push in qdriver/bombadil's TestOnlyItsOwnAnswerCompletesACall and TestProbes' cancel never answered: full mode")
	}
	t.Parallel()
	qtest.RequireQuint(t)
	cut := false
	dir := edited(t, func(n int, l string) *string {
		if cut {
			return nil
		}
		if !strings.Contains(l, `"ui.submit"`) {
			return &l
		}
		var e map[string]any
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatal(err)
		}
		for _, sn := range e["snapshots"].([]any) {
			v := sn.(map[string]any)["value"].(map[string]any)
			for _, r := range v["log"].([]any) {
				r.(map[string]any)["code"], r.(map[string]any)["job"] = nil, nil // sent, no answer seen
			}
			v["id"], v["status"] = "", ""
		}
		cut = true
		b, _ := json.Marshal(e)
		s := string(b) + "\n"
		return &s
	})
	steps, err := Steps(dir)
	if err != nil {
		t.Fatal(err)
	}
	run := qtest.Validate(t, "webjobs.binding.yaml", steps)
	run.Expect(t, qtest.Pending("submit"))
	qtest.Runs(run).Expect(t, qtest.None(qtest.Finishes()))
}

// A trace whose last line was cut off (Bombadil killed while writing it)
// is a lost tail: the steps before it are judged, and the run has no
// verdict for its finish, since the lost line may hold an answer.
func TestTornTrace(t *testing.T) {
	if mutant != "" {
		t.Skip("webjobs as it is")
	}
	if testing.Short() {
		t.Skip("a torn trace's claim, per push in qdriver/bombadil's TestTornTail: full mode")
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
	qtest.Validate(t, "webjobs.binding.yaml", steps).Expect(t, qtest.Conforms(), qtest.Undecided(qtest.Finishes(), "lost its tail"))
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
	// readyEarly's run as written violates readyIsCommitted
	// (TestRecordedMutants); re-recorded, it conforms
	t.Run("readyEarly", func(t *testing.T) {
		t.Parallel()
		steps, err := Steps(filepath.Join("testdata", "runs", "readyEarly"))
		if err != nil {
			t.Fatal(err)
		}
		qtest.Validate(t, "webjobs.binding.yaml", rerecorded(t, steps)).Expect(t, qtest.Conforms())
	})
	t.Run("faithful", func(t *testing.T) {
		if testing.Short() {
			t.Skip("the faithful journey re-recorded: full mode (readyEarly's states the point)")
		}
		t.Parallel()
		steps, err := Steps(filepath.Join("testdata", "runs", "faithful"))
		if err != nil {
			t.Fatal(err)
		}
		qtest.Runs(qtest.Validate(t, "webjobs.binding.yaml", rerecorded(t, steps))).Expect(t, qtest.None(qtest.Finishes()))
	})
}

// Each mutant's recorded Bombadil run (testdata/runs/<mutant>) violates
// its invariant: a recorded trace needs no overlay, so these run here,
// side by side. Full mode only.
func TestRecordedMutants(t *testing.T) {
	if mutant != "" {
		t.Skip("webjobs as it is")
	}
	if testing.Short() {
		// per push each mutant's server is caught by the client
		// (TestMutants), and the real trace format is TestRecorded's
		t.Skip("the mutants' recorded runs, regression tests of the adapter and model: full mode")
	}
	t.Parallel()
	qtest.RequireQuint(t)
	for m, inv := range mutants {
		if pageless[m] {
			continue
		}
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			recorded(t, m).Expect(t, qtest.Violates(inv))
		})
	}
}

// The recorded journey's observation mutants (qtest.MutateSteps), four
// of each op spread over its 59 steps: which recorded fields the model
// and the binding constrain. Each of the four steps dropped is killed (a job no
// longer created, finished or shown, an answer no longer seen), and so
// are a job created twice (createdOnce) and a submit answered 199
// (noFailedRequest): both survived before the model stated them. What
// survives is read in the README: the page's answers and acks are sets,
// so a submit or a cancel recorded twice changes nothing; no within
// reads a submit's or a cancel's time. Full mode only: each mutant is a
// check of the whole journey (about 3 s each).
func TestObservationMutants(t *testing.T) {
	if mutant != "" {
		t.Skip("webjobs as it is")
	}
	if testing.Short() {
		t.Skip("a check of the whole journey per mutant: full mode")
	}
	t.Parallel()
	qtest.RequireQuint(t)
	steps, err := Steps(filepath.Join("testdata", "runs", "faithful"))
	if err != nil {
		t.Fatal(err)
	}
	rep := qtest.MutateSteps(t, "webjobs.binding.yaml", steps, qtest.StepMutations(qtrace.MutateLimit(4)))
	rep.Expect(t,
		qtest.Killed(qtest.StepOp("drop")),
		qtest.Survived(qtest.StepOp("dup").Action("submit")),
		qtest.Survived(qtest.StepOp("dup").Action("cancel")),
		qtest.Killed(qtest.StepOp("dup").Action("created")),
		qtest.Killed(qtest.StepOp("corrupt").Action("submit").Field("code").Change("-1")),
		qtest.Survived(qtest.StepOp("retime")), // within the actor's next step: the order the model reads is kept
	)
	for _, m := range rep.Killed() {
		t.Log(m)
	}
}

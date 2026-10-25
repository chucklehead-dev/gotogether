package validate

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A bounded-response obligation is judged on the replay's timed states:
// a leader 1s after the heal passes, 4s after fails (late); never, it
// fails if the trace settled or went on past the bound, and is no verdict
// (not among the invariants, an issue says so) if it ended before; a
// second heal answered late fails though the first was answered in time.
func TestWithin(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	c, err := NewChecker("testdata/within.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	name := c.Binding.Within[0].Name()
	history := func(steps ...any) []qtrace.Step {
		var out []qtrace.Step
		for i := 0; i < len(steps); i += 2 {
			out = append(out, qtrace.Step{Action: steps[i].(string), Recorder: "n", Seq: uint64(i/2 + 1), Time: time.Unix(0, 0).Add(time.Duration(steps[i+1].(int)) * time.Second)})
		}
		return out
	}
	settled := func(h []qtrace.Step) []qtrace.Step {
		last := h[len(h)-1]
		return append(h, qtrace.Step{Action: "settled", Settled: true, Recorder: "n", Seq: last.Seq + 1, Time: last.Time})
	}
	for _, x := range []struct {
		name  string
		steps []qtrace.Step
		want  Verdict // "" for not judged
	}{
		{"in time", history("crash", 0, "heal", 1, "elect", 2), Pass},
		{"late", history("crash", 0, "heal", 1, "elect", 5), Fail},
		{"cut short", history("crash", 0, "heal", 1, "tick", 2), ""},
		{"never, settled", settled(history("crash", 0, "heal", 1, "tick", 2)), Fail},
		{"never, past the bound", history("crash", 0, "heal", 1, "tick", 5), Fail},
		{"the second heal late", history("crash", 0, "heal", 1, "elect", 2, "crash", 3, "heal", 4, "elect", 9), Fail},
		// the bound is inclusive, and a run that ends at it is not past it
		{"at the bound", history("crash", 0, "heal", 1, "elect", 4), Pass},
		{"cut at the bound", history("crash", 0, "heal", 1, "tick", 4), ""},
		// a trigger is a rising edge: deposed while up opens no obligation
		{"deposed, no heal", history("crash", 0, "heal", 1, "elect", 2, "depose", 3, "elect", 9), Pass},
		// a response in the trigger's own state
		{"recovered", settled(history("crash", 0, "recover", 1)), Pass},
		// times that go back along the order: not judged
		{"time back", settled(history("crash", 0, "heal", 10, "tick", 2)), ""},
	} {
		rep, err := c.Check(context.Background(), x.steps)
		if err != nil {
			t.Fatalf("%s: %v", x.name, err)
		}
		a := rep.Actors[0]
		var got Verdict
		for _, inv := range a.Invariants {
			if inv.Name == name {
				got = inv.Verdict
				if inv.Verdict == Fail && (inv.Failure == nil || inv.Failure.Note == "" || !strings.Contains(rep.Summary(), inv.Failure.Note)) {
					t.Errorf("%s: a failure the report does not say: %s", x.name, rep.Summary())
				}
			}
		}
		if a.Conforms != Pass || got != x.want {
			t.Errorf("%s: conforms %v, %s %q, want %q\n%s", x.name, a.Conforms, name, got, x.want, rep.Summary())
		}
		if x.want == "" && !rep.OK() {
			t.Errorf("%s: a run cut short fails: %s", x.name, rep.Summary())
		}
	}
	// a val the evaluator does not compute: not judged, said, no failure
	rc, err := NewChecker("testdata/within_refused.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := rc.Check(context.Background(), settled(history("crash", 0, "heal", 1, "elect", 2)))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() || !strings.Contains(fmt.Sprint(rep.Actors[0].Issues), "bounded-response obligations not judged") {
		t.Errorf("a refused val: %s %v", rep.Summary(), rep.Actors[0].Issues)
	}
	// a batch judges them as one check does
	reps, errs := c.CheckBatch(context.Background(), [][]qtrace.Step{history("crash", 0, "heal", 1, "elect", 2), history("crash", 0, "heal", 1, "elect", 5)}, 0)
	if errs[0] != nil || errs[1] != nil || !reps[0].OK() || reps[1].OK() {
		t.Errorf("batch: %v %v, OK %v %v", errs[0], errs[1], reps[0] != nil && reps[0].OK(), reps[1] != nil && reps[1].OK())
	}
}

// Calls recorded with their invocations are judged on their intervals: a
// heal over [1s, 10s] and an election over [11s, 14s] may be 1s apart or
// 13s, which a bound of 3s neither passes nor fails (it once failed, "4s
// later", on their completions alone); an election over [11s, 12.5s]
// after a heal over [1s, 10s] once passed, 2.5s, though they may be 11.5s
// apart. A heal and an election both within one second pass; an election
// whose invocation is past the bound fails.
func TestWithinIntervals(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	c, err := NewChecker("testdata/within.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	name := c.Binding.Within[0].Name()
	sec := func(f float64) time.Time { return time.Unix(0, 0).Add(time.Duration(f * float64(time.Second))) }
	history := func(calls ...[3]any) []qtrace.Step {
		var out []qtrace.Step
		seq := uint64(1)
		for _, cl := range calls {
			out = append(out,
				qtrace.Step{Action: cl[0].(string), Recorder: "n", Seq: seq, Thread: "n", Invocation: true, Time: sec(cl[1].(float64))},
				qtrace.Step{Action: cl[0].(string), Recorder: "n", Seq: seq + 1, Thread: "n", Call: seq, Time: sec(cl[2].(float64))})
			seq += 2
		}
		return append(out, qtrace.Step{Action: "settled", Settled: true, Recorder: "n", Seq: seq, Time: out[len(out)-1].Time})
	}
	for _, x := range []struct {
		name  string
		steps []qtrace.Step
		want  Verdict // "" for not judged
	}{
		{"either way", history([3]any{"crash", 0.0, 0.5}, [3]any{"heal", 1.0, 10.0}, [3]any{"elect", 11.0, 14.0}), ""},
		{"possibly late", history([3]any{"crash", 0.0, 0.5}, [3]any{"heal", 1.0, 10.0}, [3]any{"elect", 11.0, 12.5}), ""},
		{"in time however placed", history([3]any{"crash", 0.0, 0.5}, [3]any{"heal", 1.0, 1.5}, [3]any{"elect", 2.0, 2.5}), Pass},
		{"late however placed", history([3]any{"crash", 0.0, 0.5}, [3]any{"heal", 1.0, 1.5}, [3]any{"elect", 6.0, 7.0}), Fail},
	} {
		rep, err := c.Check(context.Background(), x.steps)
		if err != nil {
			t.Fatalf("%s: %v", x.name, err)
		}
		var got Verdict
		for _, inv := range rep.Actors[0].Invariants {
			if inv.Name == name {
				got = inv.Verdict
			}
		}
		if got != x.want || x.want == "" && !strings.Contains(fmt.Sprint(rep.Actors[0].Issues), "as the calls' intervals place their effects") {
			t.Errorf("%s: %q, want %q\n%s", x.name, got, x.want, rep.Summary())
		}
	}
}

// A verdict's cache key: the steps' relative times when the binding has
// bounded-response obligations (a slow run and a fast one once shared a
// verdict), not otherwise; and the evaluator's mode. It was tested only
// from qhegel, which CI tests on an older core.
func TestKeyCoversWhatJudges(t *testing.T) {
	withBound, err := NewChecker("testdata/within.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	without, err := NewChecker("testdata/account.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	run := func(apart time.Duration) []qtrace.Step {
		t0 := time.Unix(100, 0)
		return []qtrace.Step{{Action: "heal", Recorder: "n", Seq: 1, Time: t0}, {Action: "elect", Recorder: "n", Seq: 2, Time: t0.Add(apart)}}
	}
	later := func(steps []qtrace.Step) []qtrace.Step { // the same run started an hour later
		out := slices.Clone(steps)
		for i := range out {
			out[i].Time = out[i].Time.Add(time.Hour)
		}
		return out
	}
	if withBound.Key(run(time.Second)) == withBound.Key(run(5*time.Second)) {
		t.Errorf("a within binding: a fast run and a slow one share a key")
	}
	if withBound.Key(run(time.Second)) != withBound.Key(later(run(time.Second))) {
		t.Errorf("a within binding: one run started later has another key")
	}
	if without.Key(run(time.Second)) != without.Key(run(5*time.Second)) {
		t.Errorf("no within: times change the key")
	}
	k := without.Key(run(time.Second))
	t.Setenv("GOTOGETHER_EVAL", "quint")
	if without.Key(run(time.Second)) == k {
		t.Errorf("the evaluator's mode is not in the key")
	}
}

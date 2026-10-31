package qtest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Regression tests for review 11's silent wrong answers: each input once
// got a verdict quint does not give.

// A fold whose order matters (a round robin over a set) agreed folded
// sorted and reversed, so the Go evaluator passed a step quint's replay
// rejects: the verdict is quint's, a Mismatch, by default as under
// GOTOGETHER_EVAL=quint (F1).
func TestSoundFoldOrderIsQuints(t *testing.T) {
	RequireQuint(t)
	t.Parallel()
	steps := []qtrace.Step{{Action: "place", Recorder: "c", Seq: 1, Time: time.Unix(1, 0), Obs: map[string]any{"s2": int64(1)}}}
	run := Validate(t, "testdata/rr.yaml", steps)
	run.Expect(t, Mismatch("place"))
	if run.Report.Actors[0].JudgedBy() != "quint" {
		t.Errorf("judged by %s, want quint:\n%s", run.Report.Actors[0].JudgedBy(), run.Report.Actors[0].Summary())
	}
}

// A pending inc invoked before finish: placed nowhere (the first
// resolution that conforms) it violates ok, placed before finish it
// keeps it. ResolvePending once judged the invariants on the first and
// said Violates, where with Linearize too the same history kept them
// (F3). Both now take the placement that keeps them.
func TestSoundResolveKeepsInvariants(t *testing.T) {
	RequireQuint(t)
	t.Parallel()
	at := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
	steps := []qtrace.Step{
		{Action: "inc", Recorder: "c", Seq: 1, Time: at(1), Record: qtrace.Record{Invocation: true}},
		{Action: "finish", Recorder: "c", Seq: 2, Time: at(2)},
	}
	for says, opts := range map[string][]Option{"inc() took effect first": {ResolvePending()}, "inc() at step 1": {ResolvePending(), Linearize()}} {
		run := Validate(t, "testdata/flag.yaml", steps, opts...)
		run.Expect(t, Conforms())
		if !strings.Contains(fmt.Sprint(run.Report.Issues), says) {
			t.Errorf("%d options: issues %v, want %q", len(opts), run.Report.Issues, says)
		}
	}
	// finished before inc was invoked: no placement keeps ok
	late := []qtrace.Step{
		{Action: "finish", Recorder: "c", Seq: 1, Time: at(1)},
		{Action: "inc", Recorder: "c", Seq: 2, Time: at(2), Record: qtrace.Record{Invocation: true}},
	}
	Validate(t, "testdata/flag.yaml", late, ResolvePending()).Expect(t, Violates("ok"))
}

// Finite internal choices must explain the entire observed trace with one
// original-action witness, confirmed by Quint. Repeated checks cannot depend
// on which branch a random replay happens to draw.
func TestSoundInternalChoiceIsPinned(t *testing.T) {
	RequireQuint(t)
	t.Parallel()
	var flips []qtrace.Step
	for j := 1; j <= 3; j++ {
		flips = append(flips, qtrace.Step{Action: "flipit", Recorder: "c", Seq: uint64(j), Time: time.Unix(int64(j), 0), Obs: map[string]any{"x": int64(4)}})
	}
	roll := []qtrace.Step{{Action: "roll", Recorder: "c", Seq: 1, Time: time.Unix(1, 0), Obs: map[string]any{"x": int64(3)}}}
	for _, c := range []struct {
		binding string
		steps   []qtrace.Step
	}{{"testdata/flip.yaml", flips}, {"testdata/die.yaml", roll}} {
		var witness string
		for range 3 {
			run := Validate(t, c.binding, c.steps)
			a := run.Report.Actors[0]
			if !run.Report.OK() || a.Conforms != validate.Pass || a.Choices == nil || a.Choices.Confirmation != "path_confirmed" || !a.Choices.Exhaustive || len(a.Choices.Witness) != len(c.steps) {
				t.Fatalf("%s: missing confirmed finite witness: %+v\n%s", c.binding, a.Choices, a.Summary())
			}
			got := fmt.Sprint(a.Choices.Witness)
			if witness != "" && got != witness {
				t.Fatalf("%s: repeated validation changed its pinned witness: %s then %s", c.binding, witness, got)
			}
			witness = got
		}
		bounded := Validate(t, c.binding, c.steps, Quint(validate.Options{ChoiceLimit: 1}))
		a := bounded.Report.Actors[0]
		if a.Conforms != validate.NotEvaluated || a.Failure != nil || a.Choices == nil || a.Choices.Status != "exhausted" || a.Choices.Confirmation != "not_performed" || decidedFailing([]*Run{bounded}) {
			t.Fatalf("%s: a bounded search manufactured a failure: %+v", c.binding, a)
		}
	}
	// No finite successor gives seven. Exhaustive Go search proves this
	// rejection; no sampled Quint path is presented as a nonexistence proof.
	roll[0].Obs["x"] = int64(7)
	rejected := Validate(t, "testdata/die.yaml", roll)
	rejected.Expect(t, Mismatch("roll"))
	choices := rejected.Report.Actors[0].Choices
	if choices == nil || choices.Status != "rejected" || !choices.Exhaustive || choices.Confirmation != "not_performed" || !decidedFailing([]*Run{rejected}) {
		t.Fatalf("impossible die observation lacks exhaustive rejection: %+v", choices)
	}
}

// Two hosts: a write recorded by one completes at 1 ms, a read recorded
// by the other is invoked at 3 ms (2 ms after) and misses it. Real time across them
// is their clocks' word: with clocks within 1 ms the read is stale, and
// the verdict now says it rests on that (it once said nothing of it);
// with a skew of 5 ms (two hosts under NTP) the read may precede the
// write, and the history is linearizable (F5).
func TestSoundClockSkewNamed(t *testing.T) {
	RequireQuint(t)
	t.Parallel()
	at := func(ms int) time.Time { return time.Unix(100, 0).Add(time.Duration(ms) * time.Millisecond) }
	steps := []qtrace.Step{
		{Action: "write", Recorder: "host-a", Seq: 1, Invoked: at(0), Time: at(1), Args: map[string]any{"key": "a", "value": int64(1)}},
		{Action: "read", Recorder: "host-b", Seq: 1, Invoked: at(3), Time: at(4), Args: map[string]any{"key": "a"}, Obs: map[string]any{"value": int64(0)}},
	}
	stale := Validate(t, "testdata/registers.yaml", steps, Linearize())
	stale.Expect(t, Mismatch("read"))
	if s := fmt.Sprint(stale.Report.Issues); !strings.Contains(s, "host-a, host-b orders a pair of steps by their clocks, the closest 2ms apart: it assumes the clocks within 1ms") ||
		!strings.Contains(s, "rests on") {
		t.Errorf("the clock assumption unnamed: %s", s)
	}
	Validate(t, "testdata/registers.yaml", steps, ClockSkew(5*time.Millisecond)).Expect(t, Conforms())
}

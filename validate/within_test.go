package validate

import (
	"context"
	"os/exec"
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
	// a batch judges them as one check does
	reps, errs := c.CheckBatch(context.Background(), [][]qtrace.Step{history("crash", 0, "heal", 1, "elect", 2), history("crash", 0, "heal", 1, "elect", 5)}, 0)
	if errs[0] != nil || errs[1] != nil || !reps[0].OK() || reps[1].OK() {
		t.Errorf("batch: %v %v, OK %v %v", errs[0], errs[1], reps[0] != nil && reps[0].OK(), reps[1] != nil && reps[1].OK())
	}
}

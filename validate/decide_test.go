package validate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// countingQuint is a quint that logs each process's subcommand to the
// file it returns, then runs the real one.
func countingQuint(t *testing.T) (bin, log string) {
	t.Helper()
	real, err := exec.LookPath("quint")
	if err != nil {
		t.Skip("quint not on PATH")
	}
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "quint"), filepath.Join(dir, "processes")
	script := "#!/bin/sh\necho \"$1\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

// replays counts the `quint test` processes log records since the last
// count.
func replays(t *testing.T, log string) int {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	os.Remove(log)
	return strings.Count(string(data), "test\n")
}

func accountSteps(withdrawn, balance int64) []qtrace.Step {
	ms := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
	return []qtrace.Step{
		{Action: "deposit", Recorder: "c", Seq: 1, Time: ms(1), Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(10)}},
		{Action: "withdraw", Recorder: "c", Seq: 2, Time: ms(2), Args: map[string]any{"amount": withdrawn}, Obs: map[string]any{"balance": balance}},
	}
}

// A run the Go evaluator passes is a pass with no quint replay, by Check
// and CheckBatch alike, and says so; a run it rejects is quint's to
// report (its module, quint's message and the model's state at the
// failure), from the detail replay alone; GOTOGETHER_EVAL=quint replays
// every run with quint.
func TestEvaluatorDecidesPasses(t *testing.T) {
	skip.Quint(t)
	bin, log := countingQuint(t)
	pass, fail := accountSteps(3, 7), accountSteps(3, 8)
	ctx := context.Background()
	for _, eval := range []string{"go", "quint"} {
		c, err := NewChecker("testdata/account.yaml", Options{Quint: bin, Dir: t.TempDir(), Eval: eval})
		if err != nil {
			t.Fatal(err)
		}
		c.Check(ctx, pass) // the evaluator's compile, once
		replays(t, log)
		rep, err := c.Check(ctx, pass)
		if err != nil || !rep.OK() {
			t.Fatalf("%s: a passing run: %v\n%v", eval, err, rep)
		}
		n := replays(t, log)
		byEval := strings.Contains(rep.Summary(), "judged by the Go evaluator")
		switch {
		case eval == "go" && (n != 0 || !byEval || rep.Actors[0].Module != ""):
			t.Errorf("go: a passing run started %d quint replays (want none), says by the evaluator %v:\n%s", n, byEval, rep.Summary())
		case eval == "quint" && (n == 0 || byEval):
			t.Errorf("quint: a passing run started no quint replay, or says it was the evaluator's:\n%s", rep.Summary())
		}
		if a := rep.Actors[0]; a.Steps != 2 || len(a.Invariants) != 2 || a.ITF == "" {
			t.Errorf("%s: a passing run's result: %d steps, %v, itf %q", eval, a.Steps, a.Invariants, a.ITF)
		}
		rep, err = c.Check(ctx, fail)
		if err != nil || rep.OK() {
			t.Fatalf("%s: a failing run: %v\n%v", eval, err, rep)
		}
		a := rep.Actors[0]
		if n := replays(t, log); eval == "go" && n != 1 || n == 0 {
			t.Errorf("%s: a failing run started %d quint replays (go: the detail replay alone)", eval, n)
		}
		// (the model's state: Rust's, which TypeScript writes no ITF for)
		noState := a.Failure != nil && a.Failure.State == nil && os.Getenv("QUINT_BACKEND") != "typescript"
		if a.Module == "" || a.Failure == nil || !strings.Contains(a.Failure.Quint, "QNT") || noState || a.Failure.Index != 1 ||
			strings.Contains(rep.Summary(), "judged by the Go evaluator") {
			t.Errorf("%s: a failing run's report is not quint's:\n%s", eval, rep.Summary())
		}
		reps, errs := c.CheckBatch(ctx, [][]qtrace.Step{pass, pass, fail}, 0)
		if errors.Join(errs...) != nil || !reps[0].OK() || !reps[1].OK() || reps[2].OK() || reps[2].Actors[0].Module == "" {
			t.Fatalf("%s: a batch: %v %v", eval, errs, reps)
		}
		if n := replays(t, log); eval == "go" && n != 1 || n == 0 {
			t.Errorf("%s: a batch of two passing runs and one failing started %d quint replays (go: the failing one's detail)", eval, n)
		}
	}
}

// The evaluator's ITF of a run it passes holds quint's states: the
// same variables, values and actions as quint's replay writes.
func TestEvaluatorITFIsQuints(t *testing.T) {
	skip.Quint(t)
	t.Parallel()
	ctx := context.Background()
	itfOf := func(eval string, steps []qtrace.Step) string {
		c, err := NewChecker("testdata/account.yaml", Options{Dir: t.TempDir(), Eval: eval})
		if err != nil {
			t.Fatal(err)
		}
		rep, err := c.Check(ctx, steps)
		if err != nil || !rep.OK() || rep.Actors[0].ITF == "" {
			t.Fatalf("%s: %v %v", eval, err, rep)
		}
		return rep.Actors[0].ITF
	}
	steps := accountSteps(3, 7)
	if err := sameStates(itfOf("go", steps), itfOf("quint", steps)); err != nil {
		t.Error(err)
	}
}

// Under shadow quint judges every run the evaluator judges, and a verdict
// they disagree on is the run's error: an evaluator that passes every run
// (TamperEval 6, the unsound one) is caught on a failing run, by Check
// and CheckBatch; a right one checks as quint does. Without shadow the
// tampered evaluator's pass stands, which is what the shadow guards.
func TestShadowCatchesAWrongPass(t *testing.T) {
	skip.Quint(t)
	t.Setenv("GOTOGETHER_SHADOW_LOG", "") // its disagreements are made: not the nightly's to count
	defer hooks.TamperEval.Store(0)
	ctx := context.Background()
	fail := accountSteps(3, 8)
	for _, eval := range []string{"shadow", "go"} {
		c, err := NewChecker("testdata/account.yaml", Options{Dir: t.TempDir(), Eval: eval, NoITF: true})
		if err != nil {
			t.Fatal(err)
		}
		hooks.TamperEval.Store(0)
		if rep, err := c.Check(ctx, fail); err != nil || rep.OK() {
			t.Fatalf("%s, a right evaluator: %v %v", eval, err, rep)
		}
		hooks.TamperEval.Store(6)
		rep, err := c.Check(ctx, fail)
		_, errs := c.CheckBatch(ctx, [][]qtrace.Step{accountSteps(3, 7), fail}, 0)
		switch {
		case eval == "shadow" && (err == nil || !strings.Contains(err.Error(), "GOTOGETHER_EVAL=shadow") || !strings.Contains(err.Error(), "it passes")):
			t.Errorf("shadow, Check: %v", err)
		case eval == "shadow" && (errs[0] != nil || errs[1] == nil || !strings.Contains(errs[1].Error(), "GOTOGETHER_EVAL=shadow")):
			t.Errorf("shadow, CheckBatch: %v", errs)
		case eval == "go" && (err != nil || !rep.OK()):
			t.Errorf("go: the tampered evaluator's pass is the verdict (the shadow's to catch): %v %v", err, rep)
		}
	}
}

func withinSteps(electAt int) []qtrace.Step {
	at := func(s int) time.Time { return time.Unix(0, 0).Add(time.Duration(s) * time.Second) }
	return []qtrace.Step{
		{Action: "crash", Recorder: "n", Seq: 1, Time: at(0)},
		{Action: "heal", Recorder: "n", Seq: 2, Time: at(1)},
		{Action: "elect", Recorder: "n", Seq: 3, Time: at(electAt)},
	}
}

// A binding's bounded responses are judged on the evaluator's states of a
// run it passes: an obligation met is a pass with no quint replay; one
// missed (late) is quint's to report, its module and its failure note
// judged on quint's states; GOTOGETHER_EVAL=quint replays every run.
func TestEvaluatorDecidesWithin(t *testing.T) {
	skip.Quint(t)
	bin, log := countingQuint(t)
	ctx := context.Background()
	for _, eval := range []string{"go", "quint"} {
		c, err := NewChecker("testdata/within.yaml", Options{Quint: bin, Dir: t.TempDir(), Eval: eval})
		if err != nil {
			t.Fatal(err)
		}
		name := c.Binding.Within[0].Name()
		c.Check(ctx, withinSteps(2)) // the evaluator's compile, once
		replays(t, log)
		verdict := func(rep *Report) *InvariantResult {
			for i, inv := range rep.Actors[0].Invariants {
				if inv.Name == name {
					return &rep.Actors[0].Invariants[i]
				}
			}
			return nil
		}
		for _, batch := range []bool{false, true} {
			check := func(steps []qtrace.Step) *Report {
				if !batch {
					rep, err := c.Check(ctx, steps)
					if err != nil {
						t.Fatal(err)
					}
					return rep
				}
				reps, errs := c.CheckBatch(ctx, [][]qtrace.Step{steps}, 0)
				if errs[0] != nil {
					t.Fatal(errs[0])
				}
				return reps[0]
			}
			rep := check(withinSteps(2))
			n := replays(t, log)
			if w := verdict(rep); !rep.OK() || w == nil || w.Verdict != Pass {
				t.Fatalf("%s, batch %v: an obligation met: %s", eval, batch, rep.Summary())
			}
			byEval := strings.Contains(rep.Summary(), "judged by the Go evaluator")
			switch {
			case eval == "go" && (n != 0 || !byEval):
				t.Errorf("go, batch %v: an obligation met started %d quint replays (want none), by the evaluator %v", batch, n, byEval)
			case eval == "quint" && (n == 0 || byEval):
				t.Errorf("quint, batch %v: an obligation met started no quint replay", batch)
			}
			rep = check(withinSteps(5))
			n = replays(t, log)
			w := verdict(rep)
			if rep.OK() || w == nil || w.Verdict != Fail || w.Failure == nil || !strings.Contains(rep.Summary(), w.Failure.Note) {
				t.Fatalf("%s, batch %v: an obligation missed: %s", eval, batch, rep.Summary())
			}
			if a := rep.Actors[0]; n == 0 || a.Module == "" || strings.Contains(rep.Summary(), "judged by the Go evaluator") {
				t.Errorf("%s, batch %v: a missed obligation's report is not quint's (%d replays):\n%s", eval, batch, n, rep.Summary())
			}
		}
	}
}

// An evaluator whose states are wrong (TamperEval 7: every state its
// last, so an obligation is met at once) passes a run whose obligation was
// missed, and the shadow catches it, by Check and CheckBatch.
func TestShadowCatchesAWrongWithin(t *testing.T) {
	skip.Quint(t)
	t.Setenv("GOTOGETHER_SHADOW_LOG", "") // its disagreements are made: not the nightly's to count
	defer hooks.TamperEval.Store(0)
	ctx := context.Background()
	late := withinSteps(5)
	for _, eval := range []string{"go", "shadow"} {
		c, err := NewChecker("testdata/within.yaml", Options{Dir: t.TempDir(), Eval: eval})
		if err != nil {
			t.Fatal(err)
		}
		hooks.TamperEval.Store(0)
		if rep, err := c.Check(ctx, late); err != nil || rep.OK() {
			t.Fatalf("%s, a right evaluator: %v %v", eval, err, rep)
		}
		hooks.TamperEval.Store(7)
		rep, err := c.Check(ctx, late)
		_, errs := c.CheckBatch(ctx, [][]qtrace.Step{late}, 0)
		switch {
		case eval == "go" && (err != nil || !rep.OK()):
			t.Errorf("go: the tampered evaluator's pass is the verdict (the shadow's to catch): %v\n%v", err, rep)
		case eval == "shadow" && (err == nil || !strings.Contains(err.Error(), "GOTOGETHER_EVAL=shadow")):
			t.Errorf("shadow, Check: %v", err)
		case eval == "shadow" && (errs[0] == nil || !strings.Contains(errs[0].Error(), "GOTOGETHER_EVAL=shadow")):
			t.Errorf("shadow, CheckBatch: %v", errs)
		}
	}
}

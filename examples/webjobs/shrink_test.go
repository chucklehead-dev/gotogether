package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/webjobs/journey"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// shrinks are what each mutant's client plan shrinks to: its claim
// stays mutants', and at most this many actions remain.
var shrinks = map[string]int{
	"doubleJob":         1, // one double-clicked submission
	"cancelIgnored":     2, // a submission, its cancel (answered cancelled, never made)
	"cancelBroken":      2, // a submission, its cancel
	"commitAfterCancel": 2, // a submission, its cancel: the commit comes with the drain
	"readyEarly":        1, // a submission: the page, left open, polls it to its end
	"statusStale":       1, // a submission: the page, left open, never sees its job end
	"rerunFinished":     3, // a submission, its end, a resubmission
}

// rerunOf reruns actions against a fresh mutant server (the program
// bin) each time, drained before its steps come back: Reset is nil. A
// rerun of no actions records the server's drain and settle alone, the
// same each time, as the shrinker's reset check wants.
func rerunOf(t *testing.T, bin string) func(ctx context.Context, actions []journey.Action) ([]qtrace.Step, error) {
	return func(ctx context.Context, actions []journey.Action) ([]qtrace.Step, error) {
		steps, err := journey.Rerun(ctx, journey.Program{Bin: bin, Dir: t.TempDir(), Seed: 1, MinWork: 100 * time.Millisecond, MaxWork: 300 * time.Millisecond}, client(), actions)
		if errors.Is(err, journey.ErrStuck) {
			// an await never ended and actions after it were not played:
			// the rerun did not do its actions
			return nil, fmt.Errorf("%w: %v", qtest.ErrDiverged, err)
		}
		return steps, err
	}
}

// Each mutant's client plan (seed 1: 12 alternating journeys under
// -short, 8 at random out of it) shrinks, by removing actions and
// rerunning the rest against a fresh server, to the few actions that
// still show its failure (qtest.ShrinkActions keeps the first failing
// rerun's failure). Full mode: each candidate is a rerun and a check.
func TestShrink(t *testing.T) {
	t.Parallel()
	if qtest.Mutant() != "" {
		t.Skip("already a mutant's binary")
	}
	qtest.RequireQuint(t)
	for m, most := range shrinks {
		t.Run(m, func(t *testing.T) {
			if testing.Short() {
				t.Skip("a shrink of each mutant: full mode and nightly (the shrinker is tested per push in qtest)")
			}
			t.Parallel()
			bin := filepath.Join(t.TempDir(), "webjobs")
			if out, err := exec.Command("go", "build", "-overlay", planted.Overlay(t, m), "-o", bin, "./cmd/webjobs").CombinedOutput(); err != nil {
				t.Fatalf("mutant %s: go build: %v\n%s", m, err, out)
			}
			c := client()
			original := c.Plan(1)
			if m == "statusStale" {
				// its page never sees a job end: no play gets past the
				// first await (the rest would be ErrStuck, a divergence),
				// so the sequence is what a page can do, up to there
				original = upToFirstAwait(original)
			}
			s := qtest.ShrinkActions(t, original, qtest.ActionShrink[journey.Action]{
				Binding: "webjobs.binding.yaml", Rerun: rerunOf(t, bin),
			})
			s.Expect(t, qtest.Violates(mutants[m]), qtest.AtMostActions(most))
			faithful(t, s.Actions)
		})
	}
}

// faithful checks that a shrunk sequence passes on the faithful server:
// what it shows is the mutant's bug, not the harness's (a rerun that
// stopped short of a job's end once "showed" a bound past it).
func faithful(t *testing.T, actions []journey.Action) {
	t.Helper()
	for range 3 {
		steps, err := journey.Rerun(t.Context(), journey.InProcess{Seed: 1, MinWork: 100 * time.Millisecond, MaxWork: 300 * time.Millisecond}, client(), actions)
		if err != nil {
			t.Fatal(err)
		}
		qtest.Validate(t, "webjobs.binding.yaml", steps).Expect(t, qtest.Finishes())
	}
}

// A failure Bombadil found shrinks in Go: a mutant's recorded journey
// becomes the page's actions (journey.FromTrace), played against a
// fresh mutant server each rerun and shrunk as the client's plans are.
// Full mode.
func TestShrinkRecorded(t *testing.T) {
	t.Parallel()
	if qtest.Mutant() != "" {
		t.Skip("already a mutant's binary")
	}
	if testing.Short() {
		t.Skip("a recorded journey shrunk: full mode")
	}
	qtest.RequireQuint(t)
	for _, m := range []string{"doubleJob", "cancelIgnored"} {
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			f, err := os.Open(filepath.Join("testdata", "runs", m, TraceFile))
			if err != nil {
				t.Fatal(err)
			}
			original, err := journey.FromTrace(f)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(t.TempDir(), "webjobs")
			if out, err := exec.Command("go", "build", "-overlay", planted.Overlay(t, m), "-o", bin, "./cmd/webjobs").CombinedOutput(); err != nil {
				t.Fatalf("mutant %s: go build: %v\n%s", m, err, out)
			}
			s := qtest.ShrinkActions(t, original, qtest.ActionShrink[journey.Action]{Binding: "webjobs.binding.yaml", Rerun: rerunOf(t, bin)})
			s.Expect(t, qtest.Violates(mutants[m]), qtest.AtMostActions(shrinks[m]))
			faithful(t, s.Actions)
		})
	}
}

// upToFirstAwait is actions up to and with the first AwaitEnd.
func upToFirstAwait(actions []journey.Action) []journey.Action {
	for i, a := range actions {
		if _, ok := a.(journey.AwaitEnd); ok {
			return actions[:i+1]
		}
	}
	return actions
}

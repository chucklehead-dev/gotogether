package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/webjobs/bombadiltrace"
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
	"readyEarly":        2, // a submission, its end
	"statusStale":       1, // a submission: the drain commits it while the page shows it running
	"rerunFinished":     3, // a submission, its end, a resubmission
}

// rerunOf reruns actions against a fresh mutant server (the program
// bin) each time, drained before its steps come back: Reset is nil. A
// rerun of no actions plays nothing and returns no steps of the server's
// (its drain and settle, which it records at every shutdown, are no work
// a rerun left behind: each rerun's server is new).
func rerunOf(t *testing.T, bin string) func(ctx context.Context, actions []journey.Action) ([]qtrace.Step, error) {
	return func(ctx context.Context, actions []journey.Action) ([]qtrace.Step, error) {
		steps, err := journey.Rerun(ctx, journey.Program{Bin: bin, Dir: t.TempDir(), Seed: 1, MinWork: 100 * time.Millisecond, MaxWork: 300 * time.Millisecond}, client(), actions)
		if err != nil || len(actions) > 0 {
			return steps, err
		}
		var none []qtrace.Step
		for _, s := range steps {
			if s.Action != "drained" && !s.Settled {
				none = append(none, s)
			}
		}
		return none, nil
	}
}

// Each mutant's client plan (seed 1: 12 alternating journeys under
// -short, 8 at random out of it) shrinks, by removing actions and
// rerunning the rest against a fresh server, to the few actions that
// still show its failure (qtest.ShrinkActions keeps the first failing
// rerun's failure). Full mode: each candidate is a rerun and a check.
func TestShrink(t *testing.T) {
	t.Parallel()
	if mutant != "" {
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
			if out, err := exec.Command("go", "build", "-overlay", Overlay(t, m, t.TempDir()), "-o", bin, "./cmd/webjobs").CombinedOutput(); err != nil {
				t.Fatalf("mutant %s: go build: %v\n%s", m, err, out)
			}
			c := client()
			s := qtest.ShrinkActions(t, c.Plan(1), qtest.ActionShrink[journey.Action]{
				Binding: "webjobs.binding.yaml", Rerun: rerunOf(t, bin),
			})
			s.Expect(t, qtest.Violates(mutants[m]), qtest.AtMostActions(most))
		})
	}
}

// A failure Bombadil found shrinks in Go: a mutant's recorded journey
// becomes the page's actions (bombadiltrace.Actions), played against a
// fresh mutant server each rerun and shrunk as the client's plans are.
// Full mode.
func TestShrinkRecorded(t *testing.T) {
	t.Parallel()
	if mutant != "" {
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
			original, err := bombadiltrace.Actions(f, bombadiltrace.Options{})
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(t.TempDir(), "webjobs")
			if out, err := exec.Command("go", "build", "-overlay", Overlay(t, m, t.TempDir()), "-o", bin, "./cmd/webjobs").CombinedOutput(); err != nil {
				t.Fatalf("mutant %s: go build: %v\n%s", m, err, out)
			}
			s := qtest.ShrinkActions(t, original, qtest.ActionShrink[journey.Action]{Binding: "webjobs.binding.yaml", Rerun: rerunOf(t, bin)})
			s.Expect(t, qtest.Violates(mutants[m]), qtest.AtMostActions(shrinks[m]))
		})
	}
}

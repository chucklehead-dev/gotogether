package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// simulate -graph judges as the simulate section classifies: graph.yaml
// fails on its deadlock (exit 1) and passes once simulate.terminal says
// it is the model's end; vending passes; a binding the evaluator cannot
// follow to the end exits 2, never complete; -graph with -verify is
// refused. -out's module (in a directory it makes, and at a path
// relative to the working directory) replays the counterexamples in
// quint test.
func TestSimulateGraph(t *testing.T) {
	skip.Quint(t)
	out := filepath.Join(t.TempDir(), "replays", "cex.qnt") // a directory -out makes
	for _, c := range []struct {
		args []string
		want int
	}{
		{[]string{"-binding", "../../validate/testdata/graph.yaml", "-out", out}, 1},
		{[]string{"-binding", "../../validate/testdata/graph_ends.yaml"}, 0},
		{[]string{"-binding", "../../validate/testdata/vending.yaml"}, 0},
	} {
		if code, err := simulate(append([]string{"-graph"}, c.args...)); code != c.want || err != nil {
			t.Errorf("%v: exit %d, %v; want %d", c.args, code, err, c.want)
		}
	}
	src, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"run gtg_violates_small = ", "run gtg_deadlock = "} {
		if !strings.Contains(string(src), want) {
			t.Errorf("-out's module has no %q:\n%s", want, src)
		}
	}
	replayInQuint(t, out)
	// -out relative to the working directory (the module's import is
	// relative to its own), and a step around its branches replayed as
	// step: graph_wrap allows small's violation and ends at n = 3
	wd, _ := os.Getwd()
	rel, err := filepath.Rel(wd, filepath.Join(t.TempDir(), "wrap.qnt"))
	if err != nil {
		t.Fatal(err)
	}
	code, got := capture(t, func() (int, error) {
		return simulate([]string{"-graph", "-binding", "../../validate/testdata/graph_wrap.yaml", "-out", rel})
	})
	if code != 0 {
		t.Errorf("graph_wrap -out %s: exit %d; want 0", rel, code)
	}
	// the replay's command names the module as given, which runs from
	// here (its base name ran only from its directory)
	if want := "quint test --main simulate --match 'gtg_' " + rel + " replays"; !strings.Contains(got, want) {
		t.Errorf("want %q in\n%s", want, got)
	}
	replayInQuint(t, must(filepath.Abs(rel)))
	// what the report says beside the verdicts: an invariant of a graph a
	// bound cut is bounded, never holds; a state only a stutter leaves is
	// quiet; an action of the binding's that step never calls is outside
	for _, c := range []struct {
		args         []string
		want, absent string
	}{
		{[]string{"-binding", "../../validate/testdata/graph.yaml", "-states", "5"}, "  bounded positive: none of the 5 states explored", "  holds   positive"},
		{[]string{"-binding", "../../validate/testdata/graph_partial.yaml"}, "  quiet   1 reachable state only a stutter leaves", ""},
		{[]string{"-binding", "../../validate/testdata/graph_wrap.yaml"}, "  outside reset: the binding's, never called by step", ""},
	} {
		_, got := capture(t, func() (int, error) { return simulate(append([]string{"-graph"}, c.args...)) })
		if !strings.Contains(got, c.want) || c.absent != "" && strings.Contains(got, c.absent) {
			t.Errorf("%v: want %q (and no %q) in\n%s", c.args, c.want, c.absent, got)
		}
	}
	if code, err := simulate([]string{"-graph", "-verify", "3", "-binding", "../../validate/testdata/graph.yaml"}); code != 2 || err == nil {
		t.Errorf("-graph -verify: exit %d, %v; want refused", code, err)
	}
	dir := t.TempDir()
	b := filepath.Join(dir, "grow.yaml")
	spec := must(filepath.Abs("../../internal/qeval/testdata/grow.qnt"))
	if err := os.WriteFile(b, []byte("spec: "+spec+"\nmodule: grow\nactions: {step: {}}\ninvariants: [positive]\nsimulate: {holds: [positive]}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, err := simulate([]string{"-graph", "-binding", b}); code != 2 || err != nil {
		t.Errorf("grow: exit %d, %v; want 2 (stopped, not complete)", code, err)
	}
}

// replayInQuint runs quint test on a module of gtg_ runs: each must
// pass.
func replayInQuint(t *testing.T, file string) {
	t.Helper()
	cmd := exec.Command("quint", "test", "--main", "simulate", "--match", "gtg_", "--max-samples", "20", file)
	cmd.Dir = filepath.Dir(file)
	if b, err := cmd.CombinedOutput(); err != nil {
		src, _ := os.ReadFile(file)
		t.Errorf("quint test of the counterexamples: %v\n%s\n%s", err, b, src)
	}
}

// simulate -graph judges eventual vals as cycles, as the simulate
// section classifies them (live.go): hot.yaml allows its queue to stay
// busy forever (exit 0, the lasso on one line); hot_stops proves it
// drains once submissions stop, the retry loops unfair; hot_storm's
// retry storm fails drained, which it holds, with the lasso, whose
// replay quint passes (-out), the val expected false after each of the
// cycle's steps; hot_ends' giving up, an end simulate.terminal declares
// with its val false, fails it as a lasso that stays there. An
// eventual-only binding is judged, not skipped as plain simulate skips
// it.
func TestSimulateGraphEventually(t *testing.T) {
	skip.Quint(t)
	out := filepath.Join(t.TempDir(), "hot.qnt")
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"-binding", "../../validate/testdata/hot.yaml"}, 0, "  allows  drained: a fair cycle keeps it false forever (the code must leave it): init, submit, then (submit, process, retry, retry) forever"},
		{[]string{"-binding", "../../validate/testdata/hot_stops.yaml"}, 0, "  live    drained: no fair cycle keeps it false (the whole graph, 6 states); fairness rules out 2 hot cycles (the nearest leaves process enabled throughout, never taken: if it is an input that may stop, simulate.unfair; a fault, simulate.stops)"},
		{[]string{"-binding", "../../validate/testdata/hot_storm.yaml", "-out", out}, 1, "    then this cycle, forever, drained false in each of its states (2 steps of a component of 2; taking retry, every weakly fair action enabled throughout it taken: retry):"},
		{[]string{"-binding", "../../validate/testdata/hot_ends.yaml"}, 1, "    then nothing, forever: the step cannot leave that state (a deadlock, or an end simulate.terminal declares), and finished is false there"},
	} {
		code, got := capture(t, func() (int, error) { return simulate(append([]string{"-graph"}, c.args...)) })
		if code != c.code || !strings.Contains(got, c.want) {
			t.Errorf("%v: exit %d, want %d and %q in\n%s", c.args, code, c.code, c.want, got)
		}
	}
	src, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(src), "run gtg_hot_drained = init.then(submit).then(retry).expect(not(drained)).then(retry).expect(") {
		t.Fatalf("-out: %v\n%s", err, src)
	}
	replayInQuint(t, out)
}

// A complete graph's verdicts are checked against quint's sampling: every
// state quint reaches must be in the graph. With the evaluator made to
// lack one (TamperEval 8, as a fold folded in the wrong order made it:
// review 11, O1) the run fails loudly as an evaluator bug, its "holds"
// void; untampered it says the sample agreed.
func TestSimulateGraphCheckedByQuint(t *testing.T) {
	skip.Quint(t)
	code, got := capture(t, func() (int, error) {
		return simulate([]string{"-graph", "-binding", "../../validate/testdata/vending.yaml"})
	})
	if code != 0 || !strings.Contains(got, "every state they reach is in the graph") {
		t.Errorf("exit %d, want 0 and the sample's agreement in\n%s", code, got)
	}
	hooks.TamperEval.Store(8)
	defer hooks.TamperEval.Store(0)
	code, got = capture(t, func() (int, error) {
		return simulate([]string{"-graph", "-binding", "../../validate/testdata/vending.yaml"})
	})
	if code != 1 || !strings.Contains(got, "a state the Go evaluator's complete graph lacks") || strings.Contains(got, "  holds ") {
		t.Errorf("tampered: exit %d, want 1, the evaluator bug named and no holds, in\n%s", code, got)
	}
}

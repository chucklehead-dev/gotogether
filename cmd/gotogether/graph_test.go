package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

// simulate -graph judges as the simulate section classifies: graph.yaml
// fails on its deadlock (exit 1) and passes once simulate.terminal says
// it is the model's end; vending passes; a binding the evaluator cannot
// follow to the end exits 2, never complete; -graph with -verify is
// refused. -out's module (a path relative to the working directory too)
// replays the counterexamples in quint test.
func TestSimulateGraph(t *testing.T) {
	skip.Quint(t)
	out := filepath.Join(t.TempDir(), "cex.qnt")
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
	if code, err := simulate([]string{"-graph", "-binding", "../../validate/testdata/graph_wrap.yaml", "-out", rel}); code != 0 || err != nil {
		t.Errorf("graph_wrap -out %s: exit %d, %v; want 0", rel, code, err)
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

// graphAgreement are the bindings whose graphs TestGraphAgreesWithQuint
// holds to quint: the repository's small models, with invariants each
// way (held, violated), deadlocks and choices inside actions; each with
// the graph's state bound and the depth both search (0: 5000 states, the
// binding's steps up to 12).
var graphAgreement = []struct {
	path          string
	states, depth int
}{
	{"../../validate/testdata/graph.yaml", 0, 0},
	{"../../validate/testdata/vending.yaml", 0, 0},
	{"../../validate/testdata/limiter.yaml", 0, 0},
	// account's amounts make a hundred transitions a state: 4 steps to
	// underLimit's violation
	{"../../validate/testdata/account.yaml", 0, 4},
	{"../../examples/hello/hello.binding.yaml", 0, 0},
	// lease's stale write is 11 steps deep, past its first 5000 states
	{"../../examples/lease/lease.binding.yaml", 20000, 0},
	{"../../examples/jobqueue/jobqueue.binding.yaml", 0, 0},
	{"../../examples/twophase/twophase.binding.yaml", 0, 0},
}

// The explorer against quint, on the repository's small models: each
// invariant quint's sampling violates within the binding's steps is one
// the explorer reports violated (its graph as deep, and not cut by the
// state bound), so an explorer's "holds" is never one quint refutes;
// and every counterexample it reports (each violation's, a deadlock's)
// replays in quint test, its last state violating the invariant or
// enabling no step. A counterexample whose steps choose inside their
// action is replayed too: quint draws those choices, so it passes only
// if each draw of a sample reaches the path's states (it is logged when
// it does not, not failed).
func TestGraphAgreesWithQuint(t *testing.T) {
	skip.Quint(t)
	if testing.Short() {
		t.Skip("a quint run per invariant and a quint test per model")
	}
	var agreed atomic.Int32
	t.Run("models", func(t *testing.T) {
		for _, a := range graphAgreement {
			t.Run(strings.TrimPrefix(a.path, "../../"), func(t *testing.T) {
				t.Parallel()
				agreed.Add(int32(agreeGraph(t, a.path, or(a.states, 5000), a.depth)))
			})
		}
	})
	// graph's small, lease's noStaleWrite and account's underLimit at least:
	// a test where neither finds a violation holds nothing to account
	if n := agreed.Load(); n < 3 {
		t.Errorf("%d violations found by both quint and the explorer, want at least 3", n)
	}
}

// agreeGraph holds one binding's graph to quint, and returns how many
// invariants both found violated.
func agreeGraph(t *testing.T, path string, states, depth int) int {
	c, err := validate.NewChecker(path, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b := c.Binding
	steps, samples := 12, 300
	var overrides []string
	if sim := b.Simulate; sim != nil {
		steps = or(min(sim.Steps, 12), steps)
		for k, v := range sim.Constants {
			overrides = append(overrides, k+"="+v)
		}
	}
	if depth > 0 {
		steps = depth
	}
	r, err := c.Graph(validate.GraphSearch{Constants: overrides, States: states, Depth: steps})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s, %.2fs", r.Extent(), r.Elapsed.Seconds())
	if r.Cut == "unsupported" || r.Cut == "error" {
		t.Skipf("the evaluator stopped: %s", r.Stopped)
		return 0
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "simulate.qnt")
	if err := writeReplays(b, overrides, file, nil); err != nil {
		t.Fatal(err)
	}
	exact, both := true, 0
	for _, v := range r.Invariants {
		argv := backendArgs([]string{"run", file, "--main", "simulate", "--invariant", v.Name,
			"--max-steps", strconv.Itoa(steps), "--max-samples", strconv.Itoa(samples), "--seed", "1"})
		cmd := exec.Command("quint", argv...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		violated := strings.Contains(string(out), "[violation]")
		if err != nil && !violated {
			t.Fatalf("quint run --invariant %s: %v\n%s", v.Name, err, out)
		}
		switch {
		case violated && !v.Violated && r.Cut != "states":
			t.Errorf("%s: quint's sampling violates it within %d steps, the explorer says %+v (%s)", v.Name, steps, v, r.Extent())
		case violated && !v.Violated:
			t.Logf("%s: quint violates it, beyond the explorer's %d states", v.Name, r.States)
		}
		if violated && v.Violated {
			both++
		}
		t.Logf("%s: the explorer's violation %v, quint's %v", v.Name, v.Violated, violated)
		if v.Violated && len(v.Counterexample.Sampled) > 0 || r.Deadlock != nil && len(r.Deadlock.Sampled) > 0 {
			exact = false
		}
	}
	runs := replayRuns(r)
	if len(runs) == 0 {
		return both
	}
	if err := writeReplays(b, overrides, file, runs); err != nil {
		t.Fatal(err)
	}
	if !exact {
		// a path whose actions choose inside: quint's draws are its own
		cmd := exec.Command("quint", "test", "--main", "simulate", "--match", "gtg_", "--max-samples", "20", file)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("a counterexample choosing inside its actions does not replay in every sample of quint's: %s", out)
		}
		return both
	}
	replayInQuint(t, file)
	return both
}

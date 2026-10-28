package model

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The simulation module imports the binding's instance, with its
// constants overridden, relative to where it is written.
func TestSimulationModule(t *testing.T) {
	b := &binding.Binding{Spec: "model/m.qnt", Module: "m", Dir: "/repo/ex",
		Constants: map[string]string{"N": "{{ set \"node\" }}", "K": "2"}, Defs: "def two = 2"}
	if _, err := simulationModule(b, nil, "/tmp/s/simulate.qnt"); err == nil || !strings.Contains(err.Error(), "-const N=expr") {
		t.Errorf("a template constant: %v, want the fix", err)
	}
	src, err := simulationModule(b, []string{`N=Set("a", "b")`}, "/repo/ex/out/simulate.qnt")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`import m(K = 2, N = Set("a", "b")).* from "../model/m"`, "  def two = 2"} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q in:\n%s", want, src)
		}
	}
}

// A calls binding's model stutters where nothing is called: quint names
// no action there, and a witness lists none.
func TestActionsOfDropsStutters(t *testing.T) {
	tr := &itf.Trace{States: []itf.State{{}, {Action: "join"}, {}, {Action: "borrow"}, {Action: ""}}}
	if got := strings.Join(actionsOf(tr), ", "); got != "join, borrow" {
		t.Errorf("actionsOf: %q, want join, borrow", got)
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
	{"../testdata/graph.yaml", 0, 0},
	{"../testdata/vending.yaml", 0, 0},
	{"../testdata/limiter.yaml", 0, 0},
	// account's amounts make a hundred transitions a state: 4 steps to
	// underLimit's violation
	{"../testdata/account.yaml", 0, 4},
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
			t.Run(strings.TrimPrefix(a.path, "../"), func(t *testing.T) {
				t.Parallel()
				agreed.Add(int32(agreeGraph(t, a.path, orInt(a.states, 5000), a.depth)))
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
	c, err := newChecker(path, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b := c.Binding
	steps, samples := 12, 300
	var overrides []string
	if sim := b.Simulate; sim != nil {
		steps = orInt(min(sim.Steps, 12), steps)
		for k, v := range sim.Constants {
			overrides = append(overrides, k+"="+v)
		}
	}
	if depth > 0 {
		steps = depth
	}
	r, err := c.Graph(GraphSearch{Constants: overrides, States: states, Depth: steps})
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

// A binding's possibles: vending's are reached (exit 0); its strict
// variant's are not (exit 1), the model forbidding each. Each possible
// quint can check (a predicate, a sequence of names) gets the same
// answer from quint's sampling and the Go evaluator's search, and a
// sequence naming arguments is the evaluator's alone.
func TestSimulatePossible(t *testing.T) {
	skip.Quint(t)
	for b, want := range map[string]int{"vending.yaml": 0, "vending_strict.yaml": 1} {
		path := filepath.Join("../testdata", b)
		if rep, err := Simulate(path, SimulateOptions{}); err != nil || rep.Code != want {
			t.Errorf("%s: %v; want code %d", b, err, want)
		}
		c, err := newChecker(path, validate.Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		cs, err := resolvePossibles(c, c.Binding.Simulate.Possible)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		file := filepath.Join(dir, "simulate.qnt")
		src, err := simulationModule(c.Binding, nil, file)
		if err != nil {
			t.Fatal(err)
		}
		src = strings.TrimSuffix(src, "}\n") + possibleVals(cs) + "}\n"
		if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := quintPossibles("quint", dir, file, cs, 12, 300, "1", 0, nil); err != nil {
			t.Fatal(err)
		}
		rs, err := c.Possible(c.Binding.Simulate.Possible, PossibleSearch{Depth: 12})
		if err != nil {
			t.Fatal(err)
		}
		for i, pc := range cs {
			r := rs[i]
			switch {
			case pc.quintSkip != "":
				if pc.when != "" || len(pc.steps[0].Params) == 0 && len(pc.steps) == 1 {
					t.Errorf("%s: %s: quint skipped it (%s)", b, pc.p.Text(), pc.quintSkip)
				}
			case pc.quintFound != r.Found:
				t.Errorf("%s: %s: quint found %v, the evaluator %v (%s)", b, pc.p.Text(), pc.quintFound, r.Found, r.Verdict())
			}
			if want == 1 && (r.Found || !r.Complete) {
				t.Errorf("%s: %s: %s, want impossible", b, pc.p.Text(), r.Verdict())
			}
			if want == 0 && !r.Found {
				t.Errorf("%s: %s: %s, want reached", b, pc.p.Text(), r.Verdict())
			}
		}
	}
}

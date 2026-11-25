package qeval

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/itf"
)

// transitionKey is a transition as a string: its labels and its state,
// equal exactly when both are.
func transitionKey(m *Model, t Transition) string {
	return string(appendEvents(nil, t.Events)) + m.Key(t.State)
}

// The sampler against brute force: in every state of sample.qnt's
// complete graph (and from no state, for init), each of 300 seeded
// samples is one of Transitions' (its labels and state), the action is
// enabled exactly where a transition is, and every successor state is
// drawn. The model's rare action has one enabling value in 1000 and its
// bump one in six: a sampler that did not backtrack would be disabled
// where they are the only successor, and miss their states.
func TestSampleAgainstBruteForce(t *testing.T) {
	m := compile(t, "sample.qnt", "sample", "init", "step")
	g, err := m.Explore(nil, "init", "step", Bound{States: 1000}, nil)
	if err != nil || !g.Complete || len(g.States) != 24 {
		t.Fatalf("the graph: %v, complete %v, %d states, want 24", err, g != nil && g.Complete, len(g.States))
	}
	check := func(from State, action string, seed uint64) {
		t.Helper()
		ts, err := m.Transitions(nil, from, action)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]bool{}
		states := map[string]bool{}
		for _, x := range ts {
			want[transitionKey(m, x)] = true
			states[m.Key(x.State)] = false
		}
		r := rand.New(rand.NewPCG(seed, 1))
		for range 300 {
			x, ok, err := m.Sample(nil, from, action, r)
			if err != nil {
				t.Fatal(err)
			}
			if ok != (len(ts) > 0) {
				t.Fatalf("%s from %s: the sample is enabled=%v, with %d transitions", action, showState(from), ok, len(ts))
			}
			if !ok {
				return
			}
			if !want[transitionKey(m, x)] {
				t.Fatalf("%s from %s: the sample %v -> %s is none of Transitions'", action, showState(from), x.Events, showState(x.State))
			}
			states[m.Key(x.State)] = true
		}
		for k, drawn := range states {
			if !drawn {
				t.Errorf("%s from %s: a successor never drawn in 300 samples (%q)", action, showState(from), k)
			}
		}
	}
	check(nil, "init", 0)
	for i, s := range g.States {
		check(s, "step", uint64(i+1))
	}
	// the rare action from its one enabled state: one value in 1000
	start := State{}
	for _, v := range m.Vars {
		start[v] = Int(0)
	}
	if _, ok, err := m.Sample(nil, start, "rare", rand.New(rand.NewPCG(9, 9))); err != nil || !ok {
		t.Fatalf("rare from x = 0, y = 0: enabled %v, %v; one of its 1000 values enables it", ok, err)
	}
}

// Simulate's traces reach the complete graph's every state and no other,
// stop at a violated invariant, and name the action quint's --mbt would.
func TestSimulateReachesTheGraph(t *testing.T) {
	m := compile(t, "sample.qnt", "sample", "init", "step")
	g, err := m.Explore(nil, "init", "step", Bound{States: 1000}, nil)
	if err != nil || !g.Complete {
		t.Fatalf("the graph: %v", err)
	}
	nodes := map[string]bool{}
	for _, s := range g.States {
		nodes[m.Key(s)] = true
	}
	seen := map[string]bool{}
	taken := map[string]bool{}
	for i := range 200 {
		tr, err := m.Simulate(nil, SimOptions{Steps: 20}, rand.New(rand.NewPCG(uint64(i), 2)))
		if err != nil {
			t.Fatal(err)
		}
		if tr.Deadlock || tr.Violated != "" || len(tr.Steps) != 20 {
			t.Fatalf("trace %d: deadlock %v, violated %q, %d steps: the model has neither, every trace takes 20", i, tr.Deadlock, tr.Violated, len(tr.Steps))
		}
		for _, s := range tr.States() {
			k := m.Key(s)
			if !nodes[k] {
				t.Fatalf("trace %d: %s is no state of the graph", i, showState(s))
			}
			seen[k] = true
		}
		prev := tr.Init.State
		for _, st := range tr.Steps {
			a, err := m.ActionTaken(nil, prev, "step", st)
			if err != nil {
				t.Fatal(err)
			}
			// a graph's path, which carries no action taken, has it again
			again, err := m.ActionTaken(nil, prev, "step", Transition{Events: st.Events, State: st.State})
			if err != nil || again != a {
				t.Fatalf("trace %d: the action taken computed again is %q (%v), the sample's %q", i, again, err, a)
			}
			taken[a] = true
			prev = st.State
		}
	}
	if len(seen) != len(nodes) {
		t.Errorf("200 traces reached %d of the graph's %d states", len(seen), len(nodes))
	}
	// quint names the branch the last any {} took: again (not the reset
	// it wraps), and swap's own anonymous branch (not swap), as ""
	want := map[string]bool{"bump": true, "again": true, "rare": true, "": true}
	if !maps.Equal(taken, want) {
		t.Errorf("the actions taken are %v, want %v", taken, want)
	}
	tr, err := m.Simulate(nil, SimOptions{Steps: 50, Invariants: []string{"small", "boundedY"}}, rand.New(rand.NewPCG(1, 2)))
	if err != nil {
		t.Fatal(err)
	}
	last := tr.States()[len(tr.States())-1]
	if tr.Violated != "boundedY" || Compare(last["y"], Int(3)) != 0 {
		t.Fatalf("the trace ends violating %q at %s, want boundedY at y = 3", tr.Violated, showState(last))
	}
}

// Parity by meaning, both ways, on sample.qnt: every trace the sampler
// makes replays in quint (each transition pinned, PinAction, and each
// state expected, on both backends), and every state of quint's own
// traces (quint run --mbt) is a successor of the one before by the
// evaluator, its action taken one of a transition's to it; both reach
// the graph's every state.
func TestSimulationParityWithQuint(t *testing.T) {
	m := compile(t, "sample.qnt", "sample", "init", "step")
	g, err := m.Explore(nil, "init", "step", Bound{States: 1000}, nil)
	if err != nil || !g.Complete {
		t.Fatalf("the graph: %v", err)
	}
	nodes := map[string]bool{}
	for _, s := range g.States {
		nodes[m.Key(s)] = true
	}
	vars := map[string]string{}
	for _, v := range m.Vars {
		vars[v] = m.Display(v)
	}
	expect := func(s State) string {
		var eqs []string
		for _, v := range m.Vars {
			eqs = append(eqs, vars[v]+" == "+String(s[v]))
		}
		return "and { " + strings.Join(eqs, ", ") + " }"
	}
	dir := t.TempDir()
	src, _ := filepath.Abs(filepath.Join("testdata", "sample.qnt"))
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample.qnt"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	// Go's traces, as runs of pinned transitions; the last trace's last
	// state expected wrong, a control that the expects are judged
	var body strings.Builder
	const traces = 12
	for i := range traces {
		tr, err := m.Simulate(nil, SimOptions{Steps: 10}, rand.New(rand.NewPCG(uint64(i), 3)))
		if err != nil {
			t.Fatal(err)
		}
		pin, err := m.PinAction(nil, nil, "init", nil, tr.Init.State, vars)
		if err != nil {
			t.Fatalf("trace %d: pinning init: %v", i, err)
		}
		fmt.Fprintf(&body, "  action t%d_0 = %s\n", i, pin)
		run := fmt.Sprintf("t%d_0.expect(%s)", i, expect(tr.Init.State))
		prev := tr.Init.State
		for j, st := range tr.Steps {
			pin, err := m.PinAction(nil, prev, "step", nil, st.State, vars)
			if err != nil {
				t.Fatalf("trace %d step %d: pinning: %v", i, j+1, err)
			}
			fmt.Fprintf(&body, "  action t%d_%d = %s\n", i, j+1, pin)
			run += fmt.Sprintf(".then(t%d_%d).expect(%s)", i, j+1, expect(st.State))
			prev = st.State
		}
		fmt.Fprintf(&body, "  run go%d = %s\n", i, run)
		if i == traces-1 {
			wrong := strings.TrimSuffix(run, ".expect("+expect(prev)+")") + ".expect(" + expect(perturbed(m, prev)) + ")"
			fmt.Fprintf(&body, "  run control = %s\n", wrong)
		}
	}
	path := filepath.Join(dir, "replay.qnt")
	if err := os.WriteFile(path, []byte("module replay {\n  import sample.* from \"./sample\"\n"+body.String()+"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, backend := range []string{"rust", "typescript"} {
		for _, c := range []struct {
			match string
			pass  bool
		}{{"^go[0-9]+$", true}, {"^control$", false}} {
			cmd := quintcmd.Command(t.Context(), "quint", dir, "test", path, "--main", "replay", "--match", c.match, "--max-samples", "1", "--backend", backend)
			out, err := cmd.CombinedOutput()
			if (err == nil) != c.pass {
				t.Fatalf("quint test (%s) %s: pass %v, want %v: %v\n%s", backend, c.match, err == nil, c.pass, err, out)
			}
			if c.pass && !strings.Contains(string(out), fmt.Sprintf("%d passing", traces)) {
				t.Fatalf("quint test (%s): want %d runs passing:\n%s", backend, traces, out)
			}
			if !c.pass && !strings.Contains(string(out), "1 failed") {
				t.Fatalf("quint test (%s): the control failed otherwise than its expect:\n%s", backend, out)
			}
		}
	}
	// quint's traces, as paths of the evaluator's
	run := quintcmd.Command(t.Context(), "quint", dir, "run", filepath.Join(dir, "sample.qnt"), "--mbt", "--seed", "11", "--max-samples", "100", "--n-traces", "100",
		"--max-steps", "20", "--out-itf", filepath.Join(dir, "q_{seq}.itf.json"), "--verbosity", "0")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("quint run: %v\n%s", err, out)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "q_*.itf.json"))
	if len(files) != 100 {
		t.Fatalf("quint run wrote %d traces, want 100", len(files))
	}
	reached := map[string]bool{}
	for _, f := range files {
		tr, err := itf.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var prev State
		for i, st := range tr.States {
			s, err := stateOf(m, st)
			if err != nil {
				t.Fatal(err)
			}
			action := "step"
			if i == 0 {
				action = "init"
			}
			ts, err := m.Transitions(nil, prev, action)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, x := range ts {
				if sameState(x.State, s) {
					a, err := m.ActionTaken(nil, prev, action, x)
					if err != nil {
						t.Fatal(err)
					}
					names = append(names, a)
				}
			}
			if len(names) == 0 {
				t.Fatalf("%s state %d: quint's %s is no successor of the evaluator's", filepath.Base(f), i, showState(s))
			}
			if i > 0 && !slices.Contains(names, st.Action) {
				t.Fatalf("%s state %d: quint took %q, the evaluator's transitions to it name %v", filepath.Base(f), i, st.Action, names)
			}
			reached[m.Key(s)] = true
			prev = s
		}
	}
	if len(reached) != len(nodes) {
		t.Errorf("quint's 100 traces reached %d of the graph's %d states", len(reached), len(nodes))
	}
}

// perturbed is s with each integer one more: a state no trace is in.
func perturbed(m *Model, s State) State {
	out := State{}
	for _, v := range m.Vars {
		if i, ok := s[v].(Int); ok {
			out[v] = i + 1
		} else {
			out[v] = s[v]
		}
	}
	return out
}

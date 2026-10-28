package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/validate"
)

// simulateGraph is `simulate -graph`: the binding's instance explored by
// the Go evaluator (validate.Checker.Graph), each invariant judged as
// the simulate section classifies it over every state the graph holds,
// then its deadlocks, the actions it never took, and the possibles
// (searched by the evaluator as deep as the graph, what it did not find
// sampled by quint as without -graph: the only quint process but the
// cached compile). A violation prints its shortest
// counterexample and the Quint run that replays it (all of them in -out's
// module). Exit 1 for a verdict that fails, 2 for a graph the evaluator
// could not finish or an invariant it could not evaluate, with nothing
// failing: it never says complete then. checker is the binding's, nil
// to make one; possibles its simulation's, resolved.
func simulateGraph(bpath string, b *binding.Binding, checker *validate.Checker, possibles []*possibleCheck, overrides, invariants []string, fair fairness,
	quint string, states, depth int, out string, steps, samples int, seed string) (int, error) {
	sim := b.Simulate
	if sim == nil {
		sim = &binding.Simulation{}
	}
	if out != "" {
		// the module imports the model by a path relative to its own
		abs, err := filepath.Abs(out)
		if err != nil {
			return 2, err
		}
		out = abs
	}
	if checker == nil {
		c, err := validate.NewChecker(bpath, validate.Options{Quint: quint})
		if err != nil {
			return 2, err
		}
		defer c.Close()
		checker = c
	}
	r, err := checker.Graph(validate.GraphSearch{Constants: overrides, Invariants: invariants, States: states, Depth: depth, Unfair: fair.unfair, Stops: fair.stops})
	if err != nil {
		return 2, fmt.Errorf("simulate -graph: %w", err)
	}
	// a complete graph's verdicts are the evaluator's alone: quint samples
	// the instance, and every state it reaches must be in the graph
	var sampledLine string
	evalBug := false
	if r.Complete {
		n, lack, err := crossCheck(b, overrides, quint, r, steps, samples, seed)
		switch {
		case err != nil:
			return 2, err
		case lack != "":
			evalBug = true
			r.Complete, r.Cut, r.Stopped = false, "error", "quint reached a state the graph lacks: "+lack
			sampledLine = fmt.Sprintf("  FAIL    quint's sampling reached a state the Go evaluator's complete graph lacks (%s): an evaluator bug (internal/qeval), so the graph's verdicts are void. "+
				"Report it with the binding; meanwhile check the model with simulate without -graph (quint's sampling)\n", lack)
		default:
			sampledLine = fmt.Sprintf("  sampled quint's %s of up to %d steps: every state they reach is in the graph (the evaluator's verdicts checked against quint's)\n", plural(n, "trace"), steps)
		}
	}
	bounds := fmt.Sprintf("at most %d states", r.Bound.States)
	if r.Bound.Depth > 0 {
		bounds += fmt.Sprintf(", depth %d", r.Bound.Depth)
	}
	simPrintf("simulate %s: %s(%s), its state graph by the Go evaluator (%s)\n", bpath, b.Module, strings.Join(constantList(b, overrides), ", "), bounds)
	simPrintf("  graph   %s, %.1fs\n", r.Extent(), r.Elapsed.Seconds())
	simPrint(sampledLine)
	where := "no reachable state"
	if !r.Complete {
		where = fmt.Sprintf("none of the %d states explored", r.States)
	}
	raise := "raise -states or -depth (simulate.graphStates, simulate.graphDepth) for a proof"
	if r.Cut == "unsupported" || r.Cut == "error" {
		raise = "simulate without -graph samples the model with quint, bounded too"
	}
	holds, allows := set(sim.Holds), set(sim.Allows)
	code, unchecked := 0, false
	for _, v := range r.Invariants {
		inv := v.Name
		switch {
		case v.Unsupported != "" && !v.Violated:
			unchecked = true
			simPrintf("  unchecked %s: the Go evaluator cannot evaluate it in %s (%s), and it holds in the %d it could; simulate without -graph samples it with quint\n",
				inv, plural(v.Unchecked, "state"), v.Unsupported, v.Checked)
			continue
		case v.Error != "":
			code = 1
			simPrintf("  FAIL    %s: evaluating it fails in a reachable state (%s): the model's own error, fix the model. The path:\n%s", inv, v.Error, cexText(v.Counterexample))
			continue
		}
		switch {
		case holds[inv] && v.Held() && r.Complete:
			simPrintf("  holds   %s: no reachable state violates it (the whole graph, %d states%s)\n", inv, r.States, depthNote(r))
		case holds[inv] && v.Held():
			simPrintf("  bounded %s: %s violates it, which proves nothing past them (the graph is truncated: %s)\n", inv, where, raise)
		case allows[inv] && v.Violated:
			simPrintf("  allows  %s: a state %d steps from init violates it (the code must not): %s\n", inv, len(v.Counterexample.Steps)-1, callsOf(v.Counterexample))
		case holds[inv]:
			code = 1
			simPrintf("  FAIL    %s: the model should guarantee it, and a reachable state violates it: fix the model if it must hold, "+
				"or move it to simulate.allows if it is a requirement on the code. The shortest counterexample:\n%s", inv, cexText(v.Counterexample))
		case allows[inv] && r.Complete:
			code = 1
			simPrintf("  FAIL    %s: the model should allow its violation, and no reachable state violates it: checking the code against it proves nothing. "+
				"Move it to simulate.holds (the graph proves it for this instance), or loosen the model\n", inv)
		case allows[inv]:
			code = 1
			simPrintf("  FAIL    %s: the model should allow its violation, and %s violates it: checking the code against it proves nothing. "+
				"%s, or move it to simulate.holds\n", inv, where, capitalize(raise))
		case v.Violated:
			code = 1
			simPrintf("  FAIL    %s: not classified; the model allows its violation: add it to simulate.allows if the code must prevent it, else fix the model. "+
				"The shortest counterexample:\n%s", inv, cexText(v.Counterexample))
		case r.Complete:
			simPrintf("  holds   %s: no reachable state violates it (unclassified: add it to simulate.holds to keep it so)\n", inv)
		default:
			simPrintf("  bounded %s: %s violates it, which proves nothing past them (unclassified, the graph truncated)\n", inv, where)
		}
	}
	switch {
	case r.Deadlocks > 0:
		code = 1
		simPrintf("  FAIL    deadlock: %s the step cannot leave%s. If the model means to stop there, say where with simulate.terminal "+
			"(a predicate over the state); else find the guard that disables every action (gotogether nav -binding %s). The nearest:\n%s",
			plural(r.Deadlocks, "reachable state"), terminalNote(r), bpath, cexText(r.Deadlock))
	case r.Terminals > 0:
		simPrintf("  ends    %s the step cannot leave, each terminal (simulate.terminal)\n", plural(r.Terminals, "reachable state"))
	}
	if r.Quiescent > 0 {
		// not deadlocks: a model whose step can always stutter (a read, a
		// grist query) never deadlocks, so say where it stops
		simPrintf("  quiet   %s only a stutter leaves (every edge back to itself), so no deadlock there\n", plural(r.Quiescent, "reachable state"))
	}
	if never := r.NeverEnabled(); len(never) > 0 {
		if r.Complete {
			simPrintf("  never   %s: no reachable state enables it (dead in this instance: a guard too strict, or constants too small to reach it)\n", strings.Join(never, ", "))
		} else {
			simPrintf("  never   %s: no state explored enables it (within the bound)\n", strings.Join(never, ", "))
		}
	}
	// the binding's actions the step never calls: no state of the graph
	// comes from them, so an invariant about what they do (an audit's, a
	// settle's) holds in it vacuously
	inStep := map[string]bool{b.Restart: true}
	for _, a := range r.Actions {
		inStep[a.Action] = true
	}
	var outside []string
	for _, a := range slices.Sorted(maps.Keys(b.Actions)) {
		if !inStep[a] {
			outside = append(outside, a)
		}
	}
	if len(outside) > 0 {
		simPrintf("  outside %s: the binding's, never called by step, so no state of the graph comes from them (an invariant about their effect holds vacuously)\n",
			strings.Join(outside, ", "))
	}
	var cov []string
	for _, a := range r.Actions {
		if a.Transitions > 0 {
			cov = append(cov, fmt.Sprintf("%s %d in %s", a.Action, a.Transitions, plural(a.States, "state")))
		}
	}
	if len(cov) > 0 {
		simPrintf("  covered %s\n", strings.Join(cov, ", "))
	}
	ec, eu := reportEventually(r, holds, allows)
	code, unchecked = max(code, ec), unchecked || eu
	if len(possibles) > 0 {
		// the evaluator's search first, as deep as the graph; quint samples
		// what it did not find, as simulate does without -graph (a deep
		// scenario the graph's breadth cannot reach)
		depthP := r.Bound.Depth
		if depthP == 0 {
			depthP = 1 << 30 // none: the state bound bounds it
		}
		rs, evalErr := checker.Possible(sim.Possible, validate.PossibleSearch{Constants: overrides, States: r.Bound.States, Depth: depthP})
		var left []*possibleCheck
		for i, c := range possibles {
			if evalErr == nil && rs[i].Found {
				c.quintSkip = "found by the Go evaluator, not sampled"
				continue
			}
			left = append(left, c)
		}
		if len(left) > 0 {
			// the one quint process -graph runs: say so, the header
			// having promised the evaluator's graph
			simPrintf("  sampling %s the Go evaluator did not find with quint (%d traces of %d steps): not part of the graph's verdicts\n",
				plural(len(left), "possible"), samples, steps)
			dir, err := os.MkdirTemp("", "gotogether-simulate-*")
			if err != nil {
				return 2, err
			}
			defer os.RemoveAll(dir)
			file := filepath.Join(dir, "simulate.qnt")
			src, err := simulationModule(b, overrides, file)
			if err != nil {
				return 2, err
			}
			src = strings.TrimSuffix(src, "}\n") + possibleVals(possibles) + "}\n"
			if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
				return 2, err
			}
			if err := quintPossibles(quint, dir, file, left, steps, samples, seed, 0, nil); err != nil {
				return 2, err
			}
		}
		pc, err := reportPossibles(bpath, possibles, rs, evalErr, steps, samples, 0)
		if err != nil {
			return 2, err
		}
		code = max(code, pc)
	}
	if runs := replayRuns(r); out != "" && len(runs) > 0 {
		if err := writeReplays(b, overrides, out, runs); err != nil {
			return 2, err
		}
		simPrintf("  wrote   %s: quint test --main simulate --match 'gtg_' %s replays each counterexample\n", out, filepath.Base(out))
	}
	if evalBug {
		return 1, nil
	}
	if code == 0 && (unchecked || r.Cut == "unsupported" || r.Cut == "error") {
		if r.Cut == "unsupported" || r.Cut == "error" {
			simPrintf("  stopped %s\n", r.Stopped)
		}
		return 2, nil
	}
	return code, nil
}

// fairness is -unfair's and -stops' actions (nil: the binding's).
type fairness struct {
	unfair, stops []string
}

// replayRuns are the Quint runs of a graph's counterexamples: one per
// violated invariant (gtg_violates_<name>), the nearest deadlock's
// (gtg_deadlock), and each hot eventual val's lasso (gtg_hot_<name>),
// each a declaration of the simulation module.
func replayRuns(r *validate.GraphResult) []string {
	var runs []string
	for _, v := range r.Invariants {
		if v.Violated {
			runs = append(runs, fmt.Sprintf("  // %s violated\n  run gtg_violates_%s = %s", v.Name, ident(v.Name), v.Counterexample.Replay))
		}
	}
	if r.Deadlock != nil {
		runs = append(runs, "  // the nearest deadlock\n  run gtg_deadlock = "+r.Deadlock.Replay)
	}
	for _, v := range r.Eventually {
		if v.Hot {
			runs = append(runs, fmt.Sprintf("  // %s kept false forever: the path to a fair cycle and one turn of it (no run checks the forever)\n  run gtg_hot_%s = %s", v.Name, ident(v.Name), v.Lasso.Replay))
		}
	}
	return runs
}

// writeReplays writes the simulation module of b's instance to path,
// with runs.
func writeReplays(b *binding.Binding, overrides []string, path string, runs []string) error {
	src, err := simulationModule(b, overrides, path)
	if err != nil {
		return err
	}
	src = strings.TrimSuffix(src, "}\n") + strings.Join(runs, "\n") + "\n}\n"
	// a directory -out names that does not exist yet is made: the report
	// was printed before the write once failed on it
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(src), 0o644)
}

// cexText is a counterexample as a report prints it: each step's call,
// the choices it made itself and the state after it, then its replay.
func cexText(cx *validate.Counterexample) string {
	if cx == nil {
		return ""
	}
	var b strings.Builder
	for i, st := range cx.Steps {
		fmt.Fprintf(&b, "    %d. %s", i, st.Call)
		if len(st.Picks) > 0 {
			var ps []string
			for _, p := range st.Picks {
				ps = append(ps, p.Name+" = "+p.Value)
			}
			fmt.Fprintf(&b, " choosing %s", strings.Join(ps, ", "))
		}
		var vs []string
		for _, v := range st.State {
			vs = append(vs, v.Name+" = "+v.Value)
		}
		fmt.Fprintf(&b, "\n         %s\n", strings.Join(vs, ", "))
	}
	fmt.Fprintf(&b, "    replay: run r = %s\n", cx.Replay)
	if len(cx.Sampled) > 0 {
		fmt.Fprintf(&b, "    (steps %v have other successors than the path's: quint's replay draws among them, so it may not reproduce the path; the path above is the evaluator's)\n", cx.Sampled)
	}
	return b.String()
}

// callsOf is a counterexample's steps as one line.
func callsOf(cx *validate.Counterexample) string {
	var cs []string
	for _, st := range cx.Steps {
		cs = append(cs, st.Call)
	}
	return strings.Join(cs, ", ")
}

func depthNote(r *validate.GraphResult) string {
	if r.Bound.Depth > 0 {
		return fmt.Sprintf(" within depth %d", r.Bound.Depth)
	}
	return ""
}

func terminalNote(r *validate.GraphResult) string {
	if r.Terminals > 0 {
		return fmt.Sprintf(" where simulate.terminal does not hold (%d more where it does)", r.Terminals)
	}
	return ""
}

// ident is an invariant's text as a Quint identifier's tail.
func ident(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// crossChecked bounds the traces crossCheck samples: the check is a
// membership test per state, and a few dozen traces find a state the
// graph lacks where one is reachable often (a fold's order: in every one).
const crossChecked = 50

// crossCheck samples b's instance with quint (min(samples, crossChecked)
// traces of up to steps steps, seed's) and looks for a state the graph r
// lacks: how many traces it read, and the first such state, with where
// it was, or "". The one quint process of a -graph run without possibles
// (review 11, O1).
func crossCheck(b *binding.Binding, overrides []string, quint string, r *validate.GraphResult, steps, samples int, seed string) (int, string, error) {
	n := min(max(samples, 1), crossChecked)
	dir, err := os.MkdirTemp("", "gotogether-graph-*")
	if err != nil {
		return 0, "", err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "simulate.qnt")
	src, err := simulationModule(b, overrides, file)
	if err != nil {
		return 0, "", err
	}
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		return 0, "", err
	}
	argv := backendArgs([]string{"run", "--main", "simulate", "--out-itf", filepath.Join(dir, "t_{seq}.itf.json"), "--n-traces", fmt.Sprint(n),
		"--max-steps", fmt.Sprint(steps), "--max-samples", fmt.Sprint(n)})
	if seed != "" {
		argv = append(argv, "--seed", seed)
	}
	out, qerr := runQuint(quint, dir, append(argv, file))
	files, _ := filepath.Glob(filepath.Join(dir, "t_*.itf.json"))
	if len(files) == 0 {
		return 0, "", fmt.Errorf("simulate -graph: quint's sampling of the instance wrote no trace to check the graph against: %v\n%s", qerr, out)
	}
	slices.Sort(files)
	// by the module's types where quint's Rust backend's integers need them
	traces := &binding.TraceReader{Quint: quint, Spec: file, Module: "simulate"}
	for _, f := range files {
		tr, err := traces.Read(f)
		if err != nil {
			return 0, "", fmt.Errorf("simulate -graph: %s: %w", f, err)
		}
		i, st, err := r.Lacks(tr)
		if err != nil {
			return 0, "", fmt.Errorf("simulate -graph: %w", err)
		}
		if i >= 0 {
			return len(files), fmt.Sprintf("%s, state %d of a sampled trace", st, i), nil
		}
	}
	return len(files), "", nil
}

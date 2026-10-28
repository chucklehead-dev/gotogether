package validate

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
)

// graph is Simulate with Graph (`simulate -graph`): the binding's
// instance explored by the Go evaluator (Checker.Graph), each invariant
// judged as the simulate section classifies it over every state the
// graph holds, then its deadlocks, the actions it never took, the
// eventual vals as cycles, and the possibles (searched by the evaluator
// as deep as the graph, what it did not find sampled by quint as
// without Graph: with the cross-check, the only quint processes but the
// cached compile). A violation prints its shortest counterexample and
// the Quint run that replays it (all of them in Out's module). Code 1
// for a verdict that fails, 2 for a graph the evaluator could not finish
// or an invariant it could not evaluate, with nothing failing: it never
// says complete then.
func (s *sim) graph(bpath string, b *binding.Binding, checker *Checker, possibles []*possibleCheck, overrides, invariants, eventually []string, opt SimulateOptions) error {
	sec := b.Simulate
	if sec == nil {
		sec = &binding.Simulation{}
	}
	out, steps, samples := opt.Out, opt.Steps, opt.Samples
	given := out // as the caller named it: the replay's command says it so
	if out != "" {
		// the module imports the model by a path relative to its own
		abs, err := filepath.Abs(out)
		if err != nil {
			return err
		}
		out = abs
	}
	evs := eventually
	if evs == nil {
		evs = []string{}
	}
	r, err := checker.Graph(GraphSearch{Constants: overrides, Invariants: invariants, Eventually: evs, States: opt.States, Depth: opt.Depth, Unfair: opt.Unfair, Stops: opt.Stops})
	if err != nil {
		return fmt.Errorf("simulate -graph: %w", err)
	}
	s.rep.Graph = r
	// a complete graph's verdicts are the evaluator's alone: quint samples
	// the instance, and every state it reaches must be in the graph
	var sampledLine string
	evalBug := false
	if r.Complete {
		n, lack, err := crossCheck(b, overrides, opt.Quint, r, steps, samples, opt.Seed)
		switch {
		case err != nil:
			return err
		case lack != "":
			evalBug = true
			s.rep.EvaluatorBug = lack
			r.Complete, r.Cut, r.Stopped = false, "error", "quint reached a state the graph lacks: "+lack
			sampledLine = fmt.Sprintf("  FAIL    quint's sampling reached a state the Go evaluator's complete graph lacks (%s): an evaluator bug (internal/qeval), so the graph's verdicts are void. "+
				"Report it with the binding; meanwhile check the model with simulate without -graph (quint's sampling)\n", lack)
		default:
			sampledLine = fmt.Sprintf("  sampled quint's %s of up to %d steps: every state they reach is in the graph (the evaluator's verdicts checked against quint's)\n", nOf(n, "trace"), steps)
		}
	}
	bound := graphBound(r)
	bounds := fmt.Sprintf("at most %d states", r.Bound.States)
	if r.Bound.Depth > 0 {
		bounds += fmt.Sprintf(", depth %d", r.Bound.Depth)
	}
	s.printf("simulate %s: %s(%s), its state graph by the Go evaluator (%s)\n", bpath, b.Module, strings.Join(constantList(b, overrides), ", "), bounds)
	s.printf("  graph   %s, %.1fs\n", r.Extent(), r.Elapsed.Seconds())
	s.printf("%s", sampledLine)
	where := "no reachable state"
	if !r.Complete {
		where = fmt.Sprintf("none of the %d states explored", r.States)
	}
	raise := "raise -states or -depth (simulate.graphStates, simulate.graphDepth) for a proof"
	if r.Cut == "unsupported" || r.Cut == "error" {
		raise = "simulate without -graph samples the model with quint, bounded too"
	}
	holds, allows := nameSet(sec.Holds), nameSet(sec.Allows)
	code, unchecked := 0, false
	for _, v := range r.Invariants {
		inv := v.Name
		mv := ModelVerdict{Name: inv, Class: classOf(inv, holds, allows), Held: v.Held(), Violated: v.Violated, Bound: bound,
			Counterexample: v.Counterexample, Error: v.Error, Unchecked: v.Unsupported}
		s.rep.Invariants = append(s.rep.Invariants, mv)
		switch {
		case v.Unsupported != "" && !v.Violated:
			unchecked = true
			s.printf("  unchecked %s: the Go evaluator cannot evaluate it in %s (%s), and it holds in the %d it could; simulate without -graph samples it with quint\n",
				inv, nOf(v.Unchecked, "state"), v.Unsupported, v.Checked)
			continue
		case v.Error != "":
			code = 1
			s.printf("  FAIL    %s: evaluating it fails in a reachable state (%s): the model's own error, fix the model. The path:\n%s", inv, v.Error, cexText(v.Counterexample))
			continue
		}
		switch {
		case holds[inv] && v.Held() && r.Complete:
			s.printf("  holds   %s: no reachable state violates it (the whole graph, %d states%s)\n", inv, r.States, depthNote(r))
		case holds[inv] && v.Held():
			s.printf("  bounded %s: %s violates it, which proves nothing past them (the graph is truncated: %s)\n", inv, where, raise)
		case allows[inv] && v.Violated:
			s.printf("  allows  %s: a state %d steps from init violates it (the code must not): %s\n", inv, len(v.Counterexample.Steps)-1, callsOf(v.Counterexample))
		case holds[inv]:
			code = 1
			s.printf("  FAIL    %s: the model should guarantee it, and a reachable state violates it: fix the model if it must hold, "+
				"or move it to simulate.allows if it is a requirement on the code. The shortest counterexample:\n%s", inv, cexText(v.Counterexample))
		case allows[inv] && r.Complete:
			code = 1
			s.printf("  FAIL    %s: the model should allow its violation, and no reachable state violates it: checking the code against it proves nothing. "+
				"Move it to simulate.holds (the graph proves it for this instance), or loosen the model\n", inv)
		case allows[inv]:
			code = 1
			s.printf("  FAIL    %s: the model should allow its violation, and %s violates it: checking the code against it proves nothing. "+
				"%s, or move it to simulate.holds\n", inv, where, capitalize(raise))
		case v.Violated:
			code = 1
			s.printf("  FAIL    %s: not classified; the model allows its violation: add it to simulate.allows if the code must prevent it, else fix the model. "+
				"The shortest counterexample:\n%s", inv, cexText(v.Counterexample))
		case r.Complete:
			s.printf("  holds   %s: no reachable state violates it (unclassified: add it to simulate.holds to keep it so)\n", inv)
		default:
			s.printf("  bounded %s: %s violates it, which proves nothing past them (unclassified, the graph truncated)\n", inv, where)
		}
	}
	s.rep.Deadlocks = &ModelVerdict{Name: "deadlock", Held: r.Deadlocks == 0, Violated: r.Deadlocks > 0, Bound: bound, Counterexample: r.Deadlock}
	switch {
	case r.Deadlocks > 0:
		code = 1
		s.printf("  FAIL    deadlock: %s the step cannot leave%s. If the model means to stop there, say where with simulate.terminal "+
			"(a predicate over the state); else find the guard that disables every action (gotogether nav -binding %s). The nearest:\n%s",
			nOf(r.Deadlocks, "reachable state"), terminalNote(r), bpath, cexText(r.Deadlock))
	case r.Terminals > 0:
		s.printf("  ends    %s the step cannot leave, each terminal (simulate.terminal)\n", nOf(r.Terminals, "reachable state"))
	}
	if r.Quiescent > 0 {
		// not deadlocks: a model whose step can always stutter (a read, a
		// grist query) never deadlocks, so say where it stops
		s.printf("  quiet   %s only a stutter leaves (every edge back to itself), so no deadlock there\n", nOf(r.Quiescent, "reachable state"))
	}
	if never := r.NeverEnabled(); len(never) > 0 {
		if r.Complete {
			s.printf("  never   %s: no reachable state enables it (dead in this instance: a guard too strict, or constants too small to reach it)\n", strings.Join(never, ", "))
		} else {
			s.printf("  never   %s: no state explored enables it (within the bound)\n", strings.Join(never, ", "))
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
		s.printf("  outside %s: the binding's, never called by step, so no state of the graph comes from them (an invariant about their effect holds vacuously)\n",
			strings.Join(outside, ", "))
	}
	var cov []string
	for _, a := range r.Actions {
		if a.Transitions > 0 {
			cov = append(cov, fmt.Sprintf("%s %d in %s", a.Action, a.Transitions, nOf(a.States, "state")))
		}
	}
	if len(cov) > 0 {
		s.printf("  covered %s\n", strings.Join(cov, ", "))
	}
	ec, eu := s.reportEventually(r, holds, allows, bound)
	code, unchecked = max(code, ec), unchecked || eu
	if len(possibles) > 0 {
		// the evaluator's search first, within the graph's bound; quint
		// samples what it did not find, as simulate does without -graph (a
		// deep scenario the graph's breadth cannot reach)
		ps := make([]binding.Possible, len(possibles))
		for i, c := range possibles {
			ps[i] = c.p
		}
		depthP := r.Bound.Depth
		if depthP == 0 {
			depthP = 1 << 30 // none: the state bound bounds it
		}
		rs, evalErr := checker.Possible(ps, PossibleSearch{Constants: overrides, States: r.Bound.States, Depth: depthP})
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
			s.printf("  sampling %s the Go evaluator did not find with quint (%d traces of %d steps): not part of the graph's verdicts\n",
				nOf(len(left), "possible"), samples, steps)
			dir, err := os.MkdirTemp("", "gotogether-simulate-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(dir)
			file := filepath.Join(dir, "simulate.qnt")
			src, err := simulationModule(b, overrides, file)
			if err != nil {
				return err
			}
			src = strings.TrimSuffix(src, "}\n") + possibleVals(possibles) + "}\n"
			if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
				return err
			}
			if err := quintPossibles(opt.Quint, dir, file, left, steps, samples, opt.Seed, 0, nil); err != nil {
				return err
			}
		}
		pc, err := s.reportPossibles(bpath, possibles, rs, evalErr, steps, samples, 0)
		if err != nil {
			return err
		}
		code = max(code, pc)
	}
	if runs := replayRuns(r); out != "" && len(runs) > 0 {
		if err := writeReplays(b, overrides, out, runs); err != nil {
			return err
		}
		s.printf("  wrote   %s: quint test --main simulate --match 'gtg_' %s replays each counterexample\n", out, given)
	}
	if opt.Replay {
		if err := s.replay(b, overrides, opt.Quint); err != nil {
			return err
		}
	}
	switch {
	case evalBug:
		s.rep.Code = 1
	case code == 0 && (unchecked || r.Cut == "unsupported" || r.Cut == "error"):
		if r.Cut == "unsupported" || r.Cut == "error" {
			s.printf("  stopped %s\n", r.Stopped)
		}
		s.rep.Code = 2
	default:
		s.rep.Code = code
	}
	return nil
}

// graphBound is what r's verdicts cover.
func graphBound(r *GraphResult) ModelBound {
	b := ModelBound{States: r.States, Depth: r.Bound.Depth}
	switch {
	case r.Complete:
		b.Kind = "complete"
	case r.Cut == "depth":
		b.Kind = "depth"
	case r.Cut == "states":
		b.Kind = "states"
	default:
		b.Kind, b.Stopped = "stopped", r.Stopped
	}
	return b
}

// replay replays each counterexample of the report's verdicts with quint
// test, one run at a time, setting each verdict's Replayed. A path whose
// steps have other successors is not replayed: quint's draw among them
// would not reproduce it.
func (s *sim) replay(b *binding.Binding, overrides []string, quint string) error {
	type one struct {
		v      *ModelVerdict
		name   string
		replay string
		drawn  []int
	}
	var todo []one
	for i := range s.rep.Invariants {
		if v := &s.rep.Invariants[i]; v.Violated && v.Counterexample != nil {
			todo = append(todo, one{v, "gtg_violates_" + ident(v.Name), v.Counterexample.Replay, v.Counterexample.Sampled})
		}
	}
	for i := range s.rep.Eventually {
		if v := &s.rep.Eventually[i]; v.Violated && v.Lasso != nil {
			todo = append(todo, one{v, "gtg_hot_" + ident(v.Name), v.Lasso.Replay, v.Lasso.Sampled})
		}
	}
	if v := s.rep.Deadlocks; v != nil && v.Violated && v.Counterexample != nil {
		todo = append(todo, one{v, "gtg_deadlock", v.Counterexample.Replay, v.Counterexample.Sampled})
	}
	if len(todo) == 0 {
		return nil
	}
	dir, err := os.MkdirTemp("", "gotogether-replay-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "replay.qnt")
	var runs []string
	for _, x := range todo {
		runs = append(runs, fmt.Sprintf("  run %s = %s", x.name, x.replay))
	}
	if err := writeReplays(b, overrides, file, runs); err != nil {
		return err
	}
	for _, x := range todo {
		if len(x.drawn) > 0 {
			x.v.Replayed = fmt.Sprintf("not replayed: steps %v have other successors, which quint's replay may draw instead", x.drawn)
			continue
		}
		out, err := runQuint(quint, dir, []string{"test", "--main", "simulate", "--match", "^" + x.name + "$", "--max-samples", "1", file})
		switch {
		case err == nil:
			x.v.Replayed = "replayed"
		case strings.Contains(out, "error") || strings.Contains(out, "failed"):
			x.v.Replayed = "not replayed: quint test fails the path the Go evaluator found (an evaluator bug, internal/qeval: report it with the binding):\n" + out
		default:
			return fmt.Errorf("simulate: quint test of %s: %w\n%s", x.name, err, out)
		}
	}
	return nil
}

// replayRuns are the Quint runs of a graph's counterexamples: one per
// violated invariant (gtg_violates_<name>), the nearest deadlock's
// (gtg_deadlock), and each hot eventual val's lasso (gtg_hot_<name>),
// each a declaration of the simulation module.
func replayRuns(r *GraphResult) []string {
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
func cexText(cx *Counterexample) string {
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
func callsOf(cx *Counterexample) string {
	var cs []string
	for _, st := range cx.Steps {
		cs = append(cs, st.Call)
	}
	return strings.Join(cs, ", ")
}

func depthNote(r *GraphResult) string {
	if r.Bound.Depth > 0 {
		return fmt.Sprintf(" within depth %d", r.Bound.Depth)
	}
	return ""
}

func terminalNote(r *GraphResult) string {
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
func crossCheck(b *binding.Binding, overrides []string, quint string, r *GraphResult, steps, samples int, seed string) (int, string, error) {
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

// reportEventually writes simulate -graph's verdicts on the eventual vals
// (EventualVerdict, DESIGN §62), each as the simulate section
// classifies it: in holds, a fair cycle keeping it false fails; in
// allows, one is what the model allows (the code's to leave); else it is
// reported, not failed. A val no fair cycle keeps false is live: proved
// for the instance, under per-action weak fairness, in a complete graph
// (true infinitely often in every fair behaviour), bounded in a
// truncated one.
// It returns the exit code its verdicts make (1 for a failure) and
// whether one was unchecked (the evaluator refused it somewhere, and no
// cycle was found).
func (s *sim) reportEventually(r *GraphResult, holds, allows map[string]bool, bound ModelBound) (code int, unchecked bool) {
	if len(r.Eventually) == 0 {
		return 0, false
	}
	fairness := "every action weakly fair"
	var but []string
	if len(r.Unfair) > 0 {
		but = append(but, "unfair: "+strings.Join(r.Unfair, ", "))
	}
	if len(r.Stops) > 0 {
		but = append(but, "stopping: "+strings.Join(r.Stops, ", "))
	}
	if len(but) > 0 {
		fairness += " but " + strings.Join(but, "; ") + " (simulate.unfair, simulate.stops)"
	}
	// what is checked, said as it is (review 11, O2): true infinitely
	// often (at quiescence, for a run that settles), not true once, and
	// under per-action weak fairness, not the most fairness there is
	s.printf("  cycles  %s judged as true infinitely often (at quiescence, for a run that settles): a fair cycle of states where one is false refutes it; "+
		"%s, fairness weak and per action (per-call or strong fairness would rule out more cycles, so a lasso may be no behaviour of a system that has them, "+
		"and live is so only for a system at least this fair); a stutter is no step\n", nOf(len(r.Eventually), "eventual val"), fairness)
	for _, v := range r.Eventually {
		ev := v.Name
		s.rep.Eventually = append(s.rep.Eventually, ModelVerdict{Name: ev, Class: classOf(ev, holds, allows), Held: v.Live(), Violated: v.Hot, Bound: bound,
			Lasso: v.Lasso, Counterexample: v.Counterexample, Error: v.Error, Unchecked: v.Unsupported})
		if v.Error != "" {
			code = 1
			s.printf("  FAIL    %s: evaluating it fails in a reachable state (%s): the model's own error, fix the model. The path:\n%s", ev, v.Error, cexText(v.Counterexample))
			continue
		}
		if v.Hot {
			switch {
			case holds[ev]:
				code = 1
				s.printf("  FAIL    %s: the model should drive it, and a fair cycle keeps it false forever: fix the model if it must, move it to simulate.allows if it is the code's "+
					"to reach, or name the actions a run stops before it settles (simulate.stops) or need not take (simulate.unfair). The lasso:\n%s", ev, lassoText(ev, v))
			case allows[ev]:
				s.printf("  allows  %s: a fair cycle keeps it false forever (the code must leave it): %s, then %s forever\n", ev, callsOf(v.Lasso.Prefix), cycleOf(v.Lasso))
			default:
				s.printf("  hot     %s: a fair cycle keeps it false forever (unclassified: simulate.holds if the model must drive it, which then fails; simulate.allows if it is the code's to reach). The lasso:\n%s",
					ev, lassoText(ev, v))
			}
			continue
		}
		note := ""
		if r := ruledOut(v); r != "" {
			note = "; " + r
		}
		switch {
		case v.Unsupported != "":
			unchecked = true
			s.printf("  unchecked %s: the Go evaluator cannot evaluate it in %s (%s), which are in no cycle, and no fair cycle of the others keeps it false%s\n",
				ev, nOf(v.Unchecked, "state"), v.Unsupported, note)
		case r.Complete && allows[ev]:
			s.printf("  live    %s: no fair cycle keeps it false: the model drives it itself (the whole graph, %d states%s; it could be in simulate.holds)%s\n", ev, r.States, depthNote(r), note)
		case r.Complete:
			s.printf("  live    %s: no fair cycle keeps it false (the whole graph, %d states%s)%s\n", ev, r.States, depthNote(r), note)
		default:
			s.printf("  bounded %s: no fair cycle among the %d states explored keeps it false, which proves nothing past them%s\n", ev, r.States, note)
		}
	}
	return code, unchecked
}

// lassoText is a lasso as a report prints it: the path to the cycle,
// the cycle, its replay.
func lassoText(ev string, v EventualVerdict) string {
	l := v.Lasso
	var b strings.Builder
	pre := cexText(l.Prefix)
	pre = pre[:strings.LastIndex(pre, "    replay: ")]
	b.WriteString(pre)
	switch {
	case l.Ends:
		fmt.Fprintf(&b, "    then nothing, forever: the step cannot leave that state (a deadlock, or an end simulate.terminal declares), and %s is false there: a run that ends there ends with it false\n", ev)
	case l.Stutter:
		fmt.Fprintf(&b, "    then nothing, forever: no weakly fair action is enabled in that state (%s false there; only stutters, unfair actions or those that stop leave it)\n", ev)
	default:
		fmt.Fprintf(&b, "    then this cycle, forever, %s false in each of its states (%s of a component of %d; taking %s",
			ev, nOf(len(l.Cycle), "step"), l.Component, strings.Join(l.Takes, ", "))
		if len(l.Obliged) > 0 {
			fmt.Fprintf(&b, ", every weakly fair action enabled throughout it taken: %s", strings.Join(l.Obliged, ", "))
		}
		b.WriteString("):\n")
		n := len(l.Prefix.Steps)
		for i, st := range l.Cycle {
			fmt.Fprintf(&b, "    %d. %s", n+i, st.Call)
			if len(st.Picks) > 0 {
				var ps []string
				for _, p := range st.Picks {
					ps = append(ps, p.Name+" = "+p.Value)
				}
				fmt.Fprintf(&b, " choosing %s", strings.Join(ps, ", "))
			}
			var vs []string
			for _, x := range st.State {
				vs = append(vs, x.Name+" = "+x.Value)
			}
			again := ""
			if i == len(l.Cycle)-1 {
				again = fmt.Sprintf(" (step %d's state again)", n-1)
			}
			fmt.Fprintf(&b, "\n         %s%s\n", strings.Join(vs, ", "), again)
		}
	}
	fmt.Fprintf(&b, "    replay: run r = %s\n", l.Replay)
	if l.Stutter {
		b.WriteString("    (quint replays the path to the state; no quint run can check that the model may stay there forever: the Go evaluator's search does)\n")
	} else {
		b.WriteString("    (quint replays the path and one turn of the cycle back to its state, the val false throughout; no quint run can check that it repeats forever, nor its fairness: the Go evaluator's search does)\n")
	}
	if len(l.Sampled) > 0 {
		fmt.Fprintf(&b, "    (steps %v have other successors than the path's: quint's replay draws among them, so it may not reproduce the path)\n", l.Sampled)
	}
	if r := ruledOut(v); r != "" {
		fmt.Fprintf(&b, "    %s\n", r)
	}
	return b.String()
}

// ruledOut says what fairness rules out of a val's hot components, ""
// for none: the cycles, and the single states it is false in whose only
// cycle, a stutter, leaves a weakly fair action enabled (in a model whose
// state only grows, every hot state: an input left fair hides a processor
// stuck behind it there, so the actions are named).
func ruledOut(v EventualVerdict) string {
	var parts []string
	if v.Unfair > 0 {
		parts = append(parts, nOf(v.Unfair, "hot cycle"))
	}
	if v.Stutters > 0 {
		parts = append(parts, nOf(v.Stutters, "hot state")+" stuttering")
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("fairness rules out %s (the nearest leaves %s enabled throughout, never taken: if it is an input that may stop, simulate.unfair; a fault, simulate.stops)",
		strings.Join(parts, " and "), v.UnfairBy)
}

// cycleOf is a lasso's cycle as one line.
func cycleOf(l *HotLasso) string {
	if l.Ends {
		return "nothing (the step cannot leave its state)"
	}
	if l.Stutter {
		return "nothing (no weakly fair action enabled)"
	}
	var cs []string
	for _, st := range l.Cycle {
		cs = append(cs, st.Call)
	}
	return "(" + strings.Join(cs, ", ") + ")"
}

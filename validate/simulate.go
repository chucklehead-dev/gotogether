package validate

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/confidence"
)

// Simulate is the one engine for a model on its own (I17): the binding's
// instance (its constants and defs, its simulate section's overrides)
// run without any code, each invariant, eventual val and possible judged
// as the simulate section classifies it. `gotogether simulate` prints its
// report; qtest.Model judges claims by it.
//
// Three ways to check every invariant, chosen by the options:
//   - by default, quint samples (`quint run`, Samples traces of Steps
//     steps, one invariant at a time): "holds" is a sample, no proof;
//   - with Verify N, quint verify (Apalache) checks every trace of up to
//     N steps: "holds" is a bounded proof, "allows" a shortest
//     counterexample. The section's verifySteps caps N, and verifySkip
//     says why a model is not verified;
//   - with Graph, the Go evaluator explores the state graph from init
//     within the section's one search bound (Checker.Graph): "holds" in a
//     complete graph is a proof for the instance (cross-checked by quint's
//     sampling of the instance), a violation the shortest counterexample;
//     deadlocks, the actions never enabled, and each eventual val judged
//     as cycles (a fair cycle keeping it false is a lasso, DESIGN §62).
//
// The possibles (the section's, or Possibles) are searched for by the Go
// evaluator within the same search bound, and what it did not find is
// sampled by quint, in the first invariant's run where there is one.
// Each is reached with its witness, or fails: forbidden when the
// evaluator's search saw the whole state graph, else not found in the
// samples or within the bound.
//
// An error says why there is no report: a binding that does not load, a
// simulate section that names an invariant it lacks, options that
// contradict each other, a quint that fails other than by a violation, a
// model with no step.
func Simulate(bindingPath string, opt SimulateOptions) (*SimulateReport, error) {
	b, err := binding.Load(bindingPath)
	if err != nil {
		return nil, err
	}
	return simulate(bindingPath, b, nil, opt)
}

// Simulate is the package's Simulate of c's binding, with c's model and
// evaluator: one compile for a test that checks runs and the model alone.
func (c *Checker) Simulate(opt SimulateOptions) (*SimulateReport, error) {
	if opt.Quint == "" {
		opt.Quint = c.Options.Quint
	}
	return simulate(c.Binding.Path, c.Binding, c, opt)
}

// SimulateOptions choose Simulate's checks and bounds. Every zero value
// is the binding's simulate section's, else its default.
type SimulateOptions struct {
	// Quint is the quint binary ("": quint).
	Quint string
	// Seed is quint run's --seed ("": 1), fixed so the verdicts repeat.
	Seed string
	// Steps and Samples bound quint's sampling: traces of Steps steps,
	// Samples of them (0: simulate.steps and simulate.samples, else 30
	// and 1000).
	Steps, Samples int
	// Constants are NAME=expr, over the binding's and its section's.
	Constants []string
	// Invariants are the invariants checked (nil: the binding's; empty:
	// none), Eventually the eventual vals judged as cycles with Graph
	// (nil: the binding's; empty: none), each classified as the section
	// says. Possibles are what the model must be able to do (nil: the
	// section's possibles; empty: none).
	Invariants, Eventually []string
	Possibles              []binding.Possible
	// Verify checks each invariant in every trace of up to Verify steps
	// with quint verify (Apalache) instead of sampling.
	Verify int
	// Graph explores the model's state graph with the Go evaluator
	// instead of sampling with quint.
	Graph bool
	// States bounds the Go evaluator's search for the possibles (0:
	// simulate.searchStates, else 2000; its depth is Steps, or Verify) and,
	// with Graph, the graph's states (0: simulate.graphStates, else
	// DefaultGraphStates); Depth the graph's depth (0: simulate.graphDepth,
	// else none), the possibles' with it.
	States, Depth int
	// Unfair and Stops are the actions the liveness search does not hold
	// to weak fairness (nil: simulate.unfair and simulate.stops).
	Unfair, Stops []string
	// Out, with Graph, is where to write a Quint module of the instance
	// with a run replaying each counterexample (quint test --match 'gtg_').
	Out string
	// Replay, with Graph, replays each counterexample with quint test
	// (InvariantVerdict's Replayed): a violation the Go evaluator found is
	// then quint's too.
	Replay bool
}

// A SimulateReport is Simulate's: every verdict, with what bounds it,
// and the report as `gotogether simulate` prints it.
type SimulateReport struct {
	// Binding is the binding's path as given.
	Binding string
	// Skipped, if set, says why nothing was checked: the section's skip,
	// or verifySkip with Verify, or eventual vals alone without Graph.
	Skipped string
	// Graph is the evaluator's graph (with Graph).
	Graph *GraphResult
	// Invariants and Eventually are each one's verdict, in the order
	// checked; Possibles each possible's.
	Invariants, Eventually []ModelVerdict
	Possibles              []PossibleVerdict
	// Deadlocks is the graph's verdict on the states the step cannot
	// leave (with Graph): Held when none is a deadlock.
	Deadlocks *ModelVerdict
	// EvaluatorBug, if set, is quint reaching a state the evaluator's
	// complete graph lacks: the graph's verdicts are void.
	EvaluatorBug string
	// Code is the verdict as the CLI exits: 0 for every verdict as the
	// section says, 1 for one that fails, 2 for none failing and one with
	// no verdict (the evaluator refused an invariant, or stopped).
	Code int
	// Text is the report as `gotogether simulate` prints it.
	Text string
}

// A ModelVerdict is one invariant's or eventual val's verdict in the
// model alone (Name "deadlock" for the graph's deadlocks). Three-valued:
// Held (no violation within Bound), Violated (a witness), or neither (no
// verdict: Error or Unchecked says why). Held is a proof only as far as
// Bound goes (ModelBound.Proves).
type ModelVerdict struct {
	Name string
	// Class is the simulate section's: "holds", "allows", or "" for
	// unclassified.
	Class            string
	Held, Violated   bool
	Bound            ModelBound
	Counterexample   *Counterexample
	Lasso            *HotLasso
	Error, Unchecked string
	// Trace is quint's output for a sampled or verified violation.
	Trace string
	// Replayed is, with Replay, whether quint test replayed the
	// counterexample: "replayed", "not replayed: <why>", or "" (not
	// asked, or no counterexample).
	Replayed string
}

// A ModelBound is what a model verdict covers. Kind is "complete" (every
// reachable state of the instance), "depth" (every state within Depth
// steps: every trace of up to Depth steps), "states" (the first States
// states breadth first, which proves nothing past them), "stopped" (the
// evaluator could not go on: Stopped), "verified" (quint verify, every
// trace of up to Steps steps), or "sampled" (quint run, Traces traces of
// up to Steps steps).
type ModelBound struct {
	Kind                         string
	States, Depth, Traces, Steps int
	Stopped                      string
}

// Proves reports whether a verdict held within b is a proof as far as b
// says: every reachable state, or every trace within a depth.
func (b ModelBound) Proves() bool {
	return b.Kind == "complete" || b.Kind == "depth" || b.Kind == "verified"
}

func (b ModelBound) String() string {
	switch b.Kind {
	case "complete":
		return fmt.Sprintf("the whole state graph, %d states", b.States)
	case "depth":
		return fmt.Sprintf("every state within %d steps of init, %d states (a bounded proof: every trace of up to %d steps)", b.Depth, b.States, b.Depth)
	case "states":
		return fmt.Sprintf("the first %d states breadth first, truncated by the state bound: no proof past them (raise simulate.graphStates, or set graphDepth for a bounded proof)", b.States)
	case "stopped":
		return fmt.Sprintf("%d states, then the Go evaluator stopped (%s)", b.States, b.Stopped)
	case "verified":
		return fmt.Sprintf("every trace of up to %d steps (quint verify)", b.Steps)
	case "sampled":
		return fmt.Sprintf("%d sampled traces of up to %d steps (a sample, no proof: %s)", b.Traces, b.Steps, confidence.Sentence(b.Traces, "trace", "a violation"))
	}
	return b.Kind
}

// A PossibleVerdict is one possible's: Found (with a witness, the
// evaluator's Eval.Path or quint's Quint), Impossible (the evaluator's
// complete search reached it nowhere, and quint did not either), or
// neither: not found within the bound or the samples, no verdict.
type PossibleVerdict struct {
	Possible   binding.Possible
	Found      bool
	Impossible bool
	// Eval is the evaluator's search (nil: none, EvalErr says why).
	Eval    *PossibleResult
	EvalErr string
	// Quint is what quint's sampling says of it.
	Quint string
	// Verdict says what was found and how, Line is its line of the
	// report.
	Verdict, Line string
}

// sim is one Simulate: its report as it is written.
type sim struct {
	b   strings.Builder
	rep *SimulateReport
}

func (s *sim) printf(format string, a ...any) { fmt.Fprintf(&s.b, format, a...) }

func simulate(bpath string, b *binding.Binding, checker *Checker, opt SimulateOptions) (rep *SimulateReport, err error) {
	s := &sim{rep: &SimulateReport{Binding: bpath}}
	defer func() {
		if rep != nil {
			rep.Text = s.b.String()
		}
	}()
	if opt.Quint == "" {
		opt.Quint = "quint"
	}
	if opt.Seed == "" {
		opt.Seed = "1"
	}
	if opt.Graph && opt.Verify > 0 {
		return nil, fmt.Errorf("simulate: -graph and -verify are two ways to check every trace: pick one")
	}
	if !opt.Graph && (opt.Depth > 0 || opt.Out != "" || opt.Unfair != nil || opt.Stops != nil || opt.Replay) {
		return nil, fmt.Errorf("simulate: -depth, -out, -unfair and -stops are -graph's: add -graph")
	}
	if err := b.CheckSpec(); err != nil {
		return nil, err
	}
	sec := b.Simulate
	if sec == nil {
		sec = &binding.Simulation{}
	}
	if sec.Skip != "" {
		s.rep.Skipped = sec.Skip
		s.printf("simulate %s: skipped: %s\n", bpath, sec.Skip)
		return s.rep, nil
	}
	capped := ""
	if opt.Verify > 0 && sec.VerifySkip != "" {
		s.rep.Skipped = sec.VerifySkip
		s.printf("simulate %s: not verified: %s\n", bpath, sec.VerifySkip)
		return s.rep, nil
	}
	if sec.VerifySteps > 0 && opt.Verify >= sec.VerifySteps {
		opt.Verify, capped = sec.VerifySteps, ", the binding's verifySteps"
	}
	overrides := []string{}
	for _, k := range slices.Sorted(maps.Keys(sec.Constants)) {
		overrides = append(overrides, k+"="+sec.Constants[k])
	}
	overrides = append(overrides, opt.Constants...)
	invariants := b.Invariants
	if opt.Invariants != nil {
		invariants = opt.Invariants
	}
	eventually := b.Eventually
	if opt.Eventually != nil {
		eventually = opt.Eventually
	}
	possibleList := sec.Possible
	if opt.Possibles != nil {
		possibleList = opt.Possibles
	}
	if len(invariants) == 0 && len(eventually) > 0 && len(possibleList) == 0 && !opt.Graph {
		// an eventual val holds when a run settles, which a sampled trace of
		// the model never says: there is nothing to sample (-graph judges
		// it as cycles)
		s.rep.Skipped = "eventual vals alone, which a sampled trace cannot judge"
		s.printf("simulate %s: no invariants, and its eventual vals (%s) are judged after a run settles, not on sampled traces: nothing to simulate (-graph judges them as cycles)\n",
			bpath, strings.Join(eventually, ", "))
		return s.rep, nil
	}
	if len(invariants) == 0 && len(possibleList) == 0 && !(opt.Graph && len(eventually) > 0) {
		return nil, fmt.Errorf("simulate: %s names no invariants (or pass -invariants a,b) and no possibles", bpath)
	}
	holds, allows := nameSet(sec.Holds), nameSet(sec.Allows)
	// an eventual val is classified too, for -graph's cycles
	known := nameSet(append(slices.Clone(b.Invariants), b.Eventually...))
	for _, n := range append(append([]string(nil), sec.Holds...), sec.Allows...) {
		if !known[n] {
			return nil, fmt.Errorf("simulate: %s's simulate section names %s, which is not one of its invariants (%s) nor eventual vals (%s)", bpath, n,
				strings.Join(b.Invariants, ", "), strings.Join(b.Eventually, ", "))
		}
		if holds[n] && allows[n] {
			return nil, fmt.Errorf("simulate: %s's simulate section lists %s in both holds and allows", bpath, n)
		}
	}
	var possibles []*possibleCheck
	if len(possibleList) > 0 || opt.Graph {
		// the possibles need the model's actions (a name alone is a step
		// if it is one) and its evaluator, as the graph does
		if checker == nil {
			c, err := NewChecker(bpath, Options{Quint: opt.Quint})
			if err != nil {
				return nil, err
			}
			defer c.Close()
			checker = c
		}
		if possibles, err = resolvePossibles(checker, possibleList); err != nil {
			return nil, err
		}
	}
	opt.Steps = orInt(opt.Steps, orInt(sec.Steps, 30))
	opt.Samples = orInt(opt.Samples, orInt(sec.Samples, 1000))
	if opt.Graph {
		if err := s.graph(bpath, b, checker, possibles, overrides, invariants, eventually, opt); err != nil {
			return nil, err
		}
		return s.rep, nil
	}
	dir, err := os.MkdirTemp("", "gotogether-simulate-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "simulate.qnt")
	src, err := simulationModule(b, overrides, file)
	if err != nil {
		return nil, err
	}
	if vals := possibleVals(possibles); vals != "" {
		src = strings.TrimSuffix(src, "}\n") + vals + "}\n"
	}
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		return nil, err
	}
	steps, samples, verify := opt.Steps, opt.Samples, opt.Verify
	if verify > 0 {
		s.printf("simulate %s: %s(%s), every trace of up to %d steps (quint verify%s)\n", bpath, b.Module, strings.Join(constantList(b, overrides), ", "), verify, capped)
	} else {
		s.printf("simulate %s: %s(%s), %d traces of %d steps, seed %s\n", bpath, b.Module, strings.Join(constantList(b, overrides), ", "), samples, steps, opt.Seed)
	}
	// the Go evaluator's search for the possibles first (milliseconds a
	// state, the shortest witness with its arguments); quint samples what
	// it did not find, in the first invariant's run (hostArgs) where
	// there is one, the classified held one if any: a violated one's
	// samples stop at the violation, and its possibles get runs of their
	// own after the loop
	var rs []PossibleResult
	var evalErr error
	var left []*possibleCheck
	var host *hostedSample
	hostInv := ""
	if len(possibles) > 0 {
		depth := steps
		if verify > 0 {
			depth = verify
		}
		rs, evalErr = checker.Possible(possibleList, PossibleSearch{Constants: overrides, States: orInt(opt.States, sec.SearchStates), Depth: depth})
		for i, c := range possibles {
			if evalErr == nil && rs[i].Found {
				c.quintSkip = "found by the Go evaluator, not sampled"
				continue
			}
			left = append(left, c)
		}
		for _, inv := range invariants {
			if hostInv == "" || holds[inv] && !holds[hostInv] {
				hostInv = inv
			}
		}
	}
	bound := ModelBound{Kind: "sampled", Traces: samples, Steps: steps}
	if verify > 0 {
		bound = ModelBound{Kind: "verified", Steps: verify}
	}
	code := 0
	for _, inv := range invariants {
		// the file first: --witnesses, if hostArgs adds it, takes every
		// argument after it
		argv := []string{"run", file, "--main", "simulate", "--invariant", inv,
			"--max-steps", fmt.Sprint(steps), "--max-samples", fmt.Sprint(samples), "--seed", opt.Seed}
		var hosting *hostedSample
		if verify > 0 {
			argv = []string{"verify", "--main", "simulate", "--invariant", inv, "--max-steps", fmt.Sprint(verify), file}
		} else {
			argv = backendArgs(argv)
			if inv == hostInv {
				traces := filepath.Join(dir, "traces-host")
				if err := os.MkdirAll(traces, 0o755); err != nil {
					return nil, err
				}
				extra, n := hostArgs(left, verify, samples, traces)
				if extra != nil {
					argv, hosting = append(argv, extra...), &hostedSample{traces: traces, n: n}
				}
			}
		}
		out, err := runQuint(opt.Quint, dir, argv)
		violated := strings.Contains(out, "[violation]")
		if hosting != nil && err == nil && !violated {
			hosting.out, host = out, hosting
		}
		if err != nil && !violated {
			if strings.Contains(out, "Name 'step' not found") {
				return nil, fmt.Errorf("simulate: %s has no step action to run on its own: add one that picks among its actions, "+
					"or say why not (simulate.skip: a replay-only model)", b.Module)
			}
			return nil, fmt.Errorf("simulate: quint %s --invariant %s: %w\n%s", argv[0], inv, err, out)
		}
		v := ModelVerdict{Name: inv, Class: classOf(inv, holds, allows), Held: !violated, Violated: violated, Bound: bound}
		if violated {
			v.Trace = out
		}
		s.rep.Invariants = append(s.rep.Invariants, v)
		switch {
		case holds[inv] && !violated && verify > 0:
			s.printf("  holds   %s: no trace of up to %d steps violates it\n", inv, verify)
		case holds[inv] && !violated:
			s.printf("  holds   %s: none of %d sampled traces of up to %d steps violates it (a sample, no proof: %s; -graph searches every state)\n", inv, samples, steps, confidence.Sentence(samples, "trace", "a violation"))
		case allows[inv] && violated:
			s.printf("  allows  %s: a trace violates it (the code must not)\n", inv)
		case holds[inv]:
			code = 1
			s.printf("  FAIL    %s: the model should guarantee it, and a trace violates it: fix the model if it must hold, "+
				"or move it to simulate.allows if it is a requirement on the code (the model lets it fail, the code must prevent it). The trace:\n%s\n", inv, out)
		case allows[inv] && verify > 0:
			code = 1
			raise := "Raise -verify"
			if capped != "" { // the binding caps the depth: a deeper search is the binding's to allow
				raise = "Raise the binding's simulate.verifySteps if quint verify finishes deeper, run without -verify for longer sampled traces"
			}
			s.printf("  FAIL    %s: the model should allow its violation, and no trace of up to %d steps violates it: "+
				"checking the code against it proves nothing. %s, or move it to simulate.holds\n", inv, verify, raise)
		case allows[inv]:
			code = 1
			s.printf("  FAIL    %s: the model should allow its violation, and none of %d sampled traces of up to %d steps violates it: "+
				"checking the code against it proves nothing. Raise -samples or -steps, or move it to simulate.holds\n", inv, samples, steps)
		case violated:
			code = 1
			s.printf("  FAIL    %s: not classified; the model allows its violation: add it to simulate.allows if the code must prevent it, else fix the model\n", inv)
		case verify > 0: // held: not wrong, only unsaid
			s.printf("  holds   %s: no trace of up to %d steps violates it (unclassified: add it to simulate.holds to keep it so)\n", inv, verify)
		default:
			s.printf("  holds   %s: none of %d sampled traces of up to %d steps violates it (unclassified: add it to simulate.holds to keep it so; a sample, no proof)\n", inv, samples, steps)
		}
	}
	if len(possibles) > 0 {
		if err := quintPossibles(opt.Quint, dir, file, left, steps, samples, opt.Seed, verify, host); err != nil {
			return nil, err
		}
		pc, err := s.reportPossibles(bpath, possibles, rs, evalErr, steps, samples, verify)
		if err != nil {
			return nil, err
		}
		code = max(code, pc)
	}
	s.rep.Code = code
	return s.rep, nil
}

// classOf is name's class in the simulate section.
func classOf(name string, holds, allows map[string]bool) string {
	switch {
	case holds[name]:
		return "holds"
	case allows[name]:
		return "allows"
	}
	return ""
}

func orInt(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func nameSet(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// nOf is n things: "1 state", "3 states".
func nOf(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// simulationModule imports the binding's instance from file's directory,
// with its defs, so its invariants (and init and step) are the main
// module's.
func simulationModule(b *binding.Binding, overrides []string, file string) (string, error) {
	cs := constantList(b, overrides)
	for _, c := range cs {
		if strings.Contains(c, "{{") {
			return "", fmt.Errorf("simulate: constant %s is a template, filled from a run: set it with -const %s=expr", c, strings.SplitN(c, " =", 2)[0])
		}
	}
	rel, err := filepath.Rel(filepath.Dir(file), b.SpecPath())
	if err != nil {
		return "", err
	}
	rel = strings.TrimSuffix(filepath.ToSlash(rel), ".qnt")
	var s strings.Builder
	s.WriteString("// Generated by gotogether simulate: the binding's instance, run on its own.\nmodule simulate {\n")
	if len(cs) == 0 {
		fmt.Fprintf(&s, "  import %s.* from %q\n", b.Module, rel)
	} else {
		fmt.Fprintf(&s, "  import %s(%s).* from %q\n", b.Module, strings.Join(cs, ", "), rel)
	}
	if d := strings.TrimSpace(b.Defs); d != "" {
		for l := range strings.SplitSeq(d, "\n") {
			s.WriteString("  " + l + "\n")
		}
	}
	s.WriteString("}\n")
	return s.String(), nil
}

// constantList is the binding's constants as NAME = expr, overridden by
// NAME=expr entries, sorted by name.
func constantList(b *binding.Binding, overrides []string) []string {
	m := map[string]string{}
	for k, v := range b.Constants {
		m[k] = v
	}
	for _, o := range overrides {
		if k, v, ok := strings.Cut(o, "="); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	var out []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, k+" = "+m[k])
	}
	sort.Strings(out)
	return out
}

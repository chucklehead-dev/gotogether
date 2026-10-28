package main

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/validate"
)

// simulate runs the binding's model on its own: `quint run` of the
// instance the binding checks against (its constants and defs, a
// simulation section's overrides), one invariant at a time. The binding's
// simulate section says what each must show: an invariant the model holds
// (a property of the design) has no violating trace; one it allows (a
// requirement on the code) has one, or checking the code against it
// proves nothing. An invariant in neither list fails, saying which it is.
//
// With -verify N, quint verify (Apalache) checks each invariant in every
// trace of up to N steps instead of sampling: "holds" is then a bounded
// proof, and "allows" a shortest counterexample. The binding's
// simulate.verifySteps caps N for its model, and simulate.verifySkip
// says why a model is not verified (one Apalache cannot encode, a
// nondet over an infinite set or Choreo's recursion, fails with its
// error without it).
//
// The section's possibles (binding.Possible: a predicate, an action, a
// sequence of steps) are what the model must be able to do: each is
// searched for by the Go evaluator (validate.Checker.Possible, bounded by
// -states and the steps), and what it did not find sampled by quint
// (possible.go). Each is reported reached with its witness, or fails:
// forbidden, when the evaluator's search saw the whole state graph, else
// not found in the samples or within the bound.
func simulate(args []string) (int, error) {
	fset := commandFlags("simulate")
	bpath := fset.String("binding", "", "binding file: its spec, module, constants, defs, invariants and simulate section")
	steps := fset.Int("steps", 0, "steps per trace (default: the binding's simulate.steps, else 30)")
	samples := fset.Int("samples", 0, "traces per invariant (default: the binding's simulate.samples, else 1000)")
	seed := fset.String("seed", "1", "quint run --seed: fixed, so the verdicts repeat")
	consts := fset.String("const", "", "constants to set, NAME=expr,... (over the binding's and its simulate section's)")
	invs := fset.String("invariants", "", "comma-separated invariants instead of the binding's (classified as the binding says)")
	verify := fset.Int("verify", 0, "check each invariant in every trace of up to this many steps with quint verify (Apalache), instead of sampling with quint run")
	states := fset.Int("states", 0, "states the Go evaluator's search for the possibles visits at most (default: the binding's simulate.searchStates, else 2000)")
	quint := fset.String("quint", "quint", "quint binary")
	fset.Parse(args)
	if *bpath == "" {
		return 2, fmt.Errorf("simulate needs -binding b.yaml")
	}
	b, err := binding.Load(*bpath)
	if err != nil {
		return 2, err
	}
	if err := b.CheckSpec(); err != nil {
		return 2, err
	}
	sim := b.Simulate
	if sim == nil {
		sim = &binding.Simulation{}
	}
	if sim.Skip != "" {
		fmt.Printf("simulate %s: skipped: %s\n", *bpath, sim.Skip)
		return 0, nil
	}
	capped := ""
	if *verify > 0 && sim.VerifySkip != "" {
		fmt.Printf("simulate %s: not verified: %s\n", *bpath, sim.VerifySkip)
		return 0, nil
	}
	if sim.VerifySteps > 0 && *verify >= sim.VerifySteps {
		*verify, capped = sim.VerifySteps, ", the binding's verifySteps"
	}
	overrides := []string{}
	for _, k := range slices.Sorted(maps.Keys(sim.Constants)) {
		overrides = append(overrides, k+"="+sim.Constants[k])
	}
	overrides = append(overrides, splitTop(*consts)...)
	invariants := b.Invariants
	if *invs != "" {
		invariants = splitList(*invs)
	}
	if len(invariants) == 0 && len(b.Eventually) > 0 && len(sim.Possible) == 0 {
		// an eventual val holds when a run settles, which a sampled trace of
		// the model never says: there is nothing to sample
		fmt.Printf("simulate %s: no invariants, and its eventual vals (%s) are judged after a run settles, not on sampled traces: nothing to simulate\n",
			*bpath, strings.Join(b.Eventually, ", "))
		return 0, nil
	}
	if len(invariants) == 0 && len(sim.Possible) == 0 {
		return 2, fmt.Errorf("simulate: %s names no invariants (or pass -invariants a,b) and no possibles", *bpath)
	}
	var possibles []*possibleCheck
	var checker *validate.Checker
	if len(sim.Possible) > 0 {
		// the possibles need the model's actions (a name alone is a step
		// if it is one) and its evaluator
		checker, err = validate.NewChecker(*bpath, validate.Options{Quint: *quint})
		if err != nil {
			return 2, err
		}
		defer checker.Close()
		if possibles, err = resolvePossibles(checker, sim.Possible); err != nil {
			return 2, err
		}
	}
	if *steps == 0 {
		*steps = or(sim.Steps, 30)
	}
	if *samples == 0 {
		*samples = or(sim.Samples, 1000)
	}
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
	if vals := possibleVals(possibles); vals != "" {
		src = strings.TrimSuffix(src, "}\n") + vals + "}\n"
	}
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		return 2, err
	}
	if *verify > 0 {
		fmt.Printf("simulate %s: %s(%s), every trace of up to %d steps (quint verify%s)\n", *bpath, b.Module, strings.Join(constantList(b, overrides), ", "), *verify, capped)
	} else {
		fmt.Printf("simulate %s: %s(%s), %d traces of %d steps, seed %s\n", *bpath, b.Module, strings.Join(constantList(b, overrides), ", "), *samples, *steps, *seed)
	}
	holds, allows := set(sim.Holds), set(sim.Allows)
	known := set(b.Invariants)
	for _, n := range append(append([]string(nil), sim.Holds...), sim.Allows...) {
		if !known[n] {
			return 2, fmt.Errorf("simulate: %s's simulate section names %s, which is not one of its invariants (%s)", *bpath, n, strings.Join(b.Invariants, ", "))
		}
		if holds[n] && allows[n] {
			return 2, fmt.Errorf("simulate: %s's simulate section lists %s in both holds and allows", *bpath, n)
		}
	}
	code := 0
	for _, inv := range invariants {
		argv := []string{"run", "--main", "simulate", "--invariant", inv,
			"--max-steps", fmt.Sprint(*steps), "--max-samples", fmt.Sprint(*samples), "--seed", *seed}
		if *verify > 0 {
			argv = []string{"verify", "--main", "simulate", "--invariant", inv, "--max-steps", fmt.Sprint(*verify)}
		} else if be := os.Getenv("QUINT_BACKEND"); be != "" {
			argv = append(argv, "--backend", be)
		}
		cmd := exec.Command(*quint, append(argv, file)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		violated := strings.Contains(string(out), "[violation]")
		if err != nil && !violated {
			if strings.Contains(string(out), "Name 'step' not found") {
				return 2, fmt.Errorf("simulate: %s has no step action to run on its own: add one that picks among its actions, "+
					"or say why not (simulate.skip: a replay-only model)", b.Module)
			}
			return 2, fmt.Errorf("simulate: quint %s --invariant %s: %w\n%s", argv[0], inv, err, out)
		}
		switch {
		case holds[inv] && !violated && *verify > 0:
			fmt.Printf("  holds   %s: no trace of up to %d steps violates it\n", inv, *verify)
		case holds[inv] && !violated:
			fmt.Printf("  holds   %s: none of %d sampled traces of up to %d steps violates it\n", inv, *samples, *steps)
		case allows[inv] && violated:
			fmt.Printf("  allows  %s: a trace violates it (the code must not)\n", inv)
		case holds[inv]:
			code = 1
			fmt.Printf("  FAIL    %s: the model should guarantee it, and a trace violates it: fix the model if it must hold, "+
				"or move it to simulate.allows if it is a requirement on the code (the model lets it fail, the code must prevent it). The trace:\n%s\n", inv, out)
		case allows[inv] && *verify > 0:
			code = 1
			raise := "Raise -verify"
			if capped != "" { // the binding caps the depth: a deeper search is the binding's to allow
				raise = "Raise the binding's simulate.verifySteps if quint verify finishes deeper, run without -verify for longer sampled traces"
			}
			fmt.Printf("  FAIL    %s: the model should allow its violation, and no trace of up to %d steps violates it: "+
				"checking the code against it proves nothing. %s, or move it to simulate.holds\n", inv, *verify, raise)
		case allows[inv]:
			code = 1
			fmt.Printf("  FAIL    %s: the model should allow its violation, and none of %d sampled traces of up to %d steps violates it: "+
				"checking the code against it proves nothing. Raise -samples or -steps, or move it to simulate.holds\n", inv, *samples, *steps)
		case violated:
			code = 1
			fmt.Printf("  FAIL    %s: not classified; the model allows its violation: add it to simulate.allows if the code must prevent it, else fix the model\n", inv)
		default: // held: not wrong, only unsaid
			fmt.Printf("  holds   %s (unclassified: add it to simulate.holds to keep it so)\n", inv)
		}
	}
	if len(possibles) > 0 {
		// the Go evaluator's search first (milliseconds a state, the
		// shortest witness with its arguments), quint's sampling for what
		// it did not find
		depth := *steps
		if *verify > 0 {
			depth = *verify
		}
		rs, evalErr := checker.Possible(sim.Possible, validate.PossibleSearch{Constants: overrides, States: or(*states, sim.SearchStates), Depth: depth})
		var left []*possibleCheck
		for i, c := range possibles {
			if evalErr == nil && rs[i].Found {
				c.quintSkip = "found by the Go evaluator, not sampled"
				continue
			}
			left = append(left, c)
		}
		if err := quintPossibles(*quint, dir, file, left, *steps, *samples, *seed, *verify); err != nil {
			return 2, err
		}
		pc, err := reportPossibles(possibles, rs, evalErr, *steps, *samples, *verify)
		if err != nil {
			return 2, err
		}
		code = max(code, pc)
	}
	return code, nil
}

func or(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func set(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
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

func splitList(s string) []string {
	var out []string
	for x := range strings.SplitSeq(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// splitTop splits NAME=expr,NAME=expr at the commas outside brackets:
// Set(1, 2) is one value.
func splitTop(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if rest := strings.TrimSpace(s[start:]); rest != "" {
		out = append(out, rest)
	}
	return out
}

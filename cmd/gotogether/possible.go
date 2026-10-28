package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The possibles of a binding's simulation (P3): what the model on its
// own must be able to do, checked by quint (a predicate's witness is its
// counterexample to not(P); a sequence of actions is looked for in
// sampled traces' mbt::actionTaken) and by the Go evaluator's bounded
// search where the model is in its fragment (validate.Checker.Possible).
// Either finding it reaches it; neither fails simulate, saying what to
// do. The evaluator calling impossible what quint found is its bug.

// possibleCheck is one possible as simulate checks it.
type possibleCheck struct {
	p     binding.Possible
	when  string // its predicate, or "" for steps
	steps []binding.StepPattern
	// val and not are the simulation module's vals of when and not(when)
	val, not string
	// quint's verdict: found (witness its actions), or why it did not
	// look ("" when it did)
	quintFound   bool
	quintWitness []string
	quintSkip    string
}

// possibleVals are the simulation module's vals of the predicates' negations.
func possibleVals(cs []*possibleCheck) string {
	var b strings.Builder
	for i, c := range cs {
		if c.when != "" {
			c.val, c.not = fmt.Sprintf("__gtg_possible_%d", i), fmt.Sprintf("__gtg_not_possible_%d", i)
			fmt.Fprintf(&b, "  val %s = %s\n  val %s = not(%s)\n", c.val, c.when, c.not, c.val)
		}
	}
	return b.String()
}

// resolvePossibles reads the binding's possibles against its model.
func resolvePossibles(c *validate.Checker, ps []binding.Possible) ([]*possibleCheck, error) {
	var out []*possibleCheck
	for _, p := range ps {
		when, steps, err := p.Resolve(c.Model)
		if err != nil {
			return nil, fmt.Errorf("simulate: %w", err)
		}
		out = append(out, &possibleCheck{p: p, when: when, steps: steps})
	}
	return out, nil
}

// quintPossibles checks each possible with quint: a predicate by
// `quint run --invariant not(P)`, its violation the witness; steps that
// name no parameters in sampled traces (quint's mbt::actionTaken), in
// batches until every one is found or the samples are spent. Steps that
// name a parameter's value are the evaluator's alone (quint does not
// record an action's arguments).
func quintPossibles(quint, dir, file string, cs []*possibleCheck, steps, samples int, seed string, verify int) error {
	var seqs, preds []*possibleCheck
	for _, c := range cs {
		switch {
		case c.when != "" && verify > 0:
			argv := []string{"verify", "--main", "simulate", "--invariant", c.not, "--max-steps", fmt.Sprint(verify), file}
			out, err := runQuint(quint, dir, argv)
			v := strings.Contains(out, "[violation]")
			if err != nil && !v {
				return fmt.Errorf("simulate: quint verify of possible %q: %w\n%s", c.p.Text(), err, out)
			}
			c.quintFound = v
		case c.when != "":
			preds = append(preds, c)
		case verify > 0:
			c.quintSkip = "quint verify checks predicates; the Go evaluator searched the steps"
		default:
			plain := true
			for _, s := range c.steps {
				plain = plain && len(s.Params) == 0
			}
			if !plain {
				c.quintSkip = "quint does not record an action's arguments: the Go evaluator searched it"
				continue
			}
			seqs = append(seqs, c)
		}
	}
	if err := quintPredicates(quint, dir, file, preds, steps, samples, seed); err != nil {
		return err
	}
	if len(seqs) == 0 {
		return nil
	}
	// sampled traces, a batch at a time (each its own ITF file), each
	// looked through for every sequence not yet found
	const batch = 100
	base, seedErr := strconv.ParseInt(seed, 0, 64)
	for done := 0; done < samples; done += batch {
		n := min(batch, samples-done)
		s := seed
		if done > 0 {
			if seedErr != nil {
				break // a seed that is no integer: one batch, as given
			}
			s = fmt.Sprint(base + int64(done))
		}
		bdir := filepath.Join(dir, fmt.Sprintf("traces-%d", done))
		if err := os.MkdirAll(bdir, 0o755); err != nil {
			return err
		}
		argv := []string{"run", "--main", "simulate", "--mbt", "--out-itf", filepath.Join(bdir, "t_{seq}.itf.json"), "--n-traces", fmt.Sprint(n),
			"--max-steps", fmt.Sprint(steps), "--max-samples", fmt.Sprint(n), "--seed", s}
		if be := os.Getenv("QUINT_BACKEND"); be != "" {
			argv = append(argv, "--backend", be)
		}
		if out, err := runQuint(quint, dir, append(argv, file)); err != nil {
			return fmt.Errorf("simulate: quint run of the possibles' traces: %w\n%s", err, out)
		}
		files, _ := filepath.Glob(filepath.Join(bdir, "*.itf.json"))
		left := 0
		for _, f := range files {
			tr, err := itf.ReadFile(f)
			if err != nil {
				return fmt.Errorf("simulate: %s: %w", f, err)
			}
			acts := actionsOf(tr)
			for _, c := range seqs {
				if c.quintFound {
					continue
				}
				if end := takesInTurn(acts, c.steps); end >= 0 {
					c.quintFound, c.quintWitness = true, acts[:end]
				}
			}
		}
		os.RemoveAll(bdir)
		for _, c := range seqs {
			if !c.quintFound {
				left++
			}
		}
		if left == 0 {
			break
		}
	}
	return nil
}

// quintPredicates checks the predicates' possibles in one sampling, each
// a witness (quint run --witnesses: in how many traces it held), then
// takes a witness trace of each one found from a run that stops at it
// (--invariant not(P)).
func quintPredicates(quint, dir, file string, preds []*possibleCheck, steps, samples int, seed string) error {
	if len(preds) == 0 {
		return nil
	}
	backend := func(argv []string) []string {
		if be := os.Getenv("QUINT_BACKEND"); be != "" {
			return append(argv, "--backend", be)
		}
		return argv
	}
	// the file first: --witnesses takes every argument after it
	argv := backend([]string{"run", file, "--main", "simulate", "--max-steps", fmt.Sprint(steps), "--max-samples", fmt.Sprint(samples), "--seed", seed})
	argv = append(argv, "--witnesses")
	for _, c := range preds {
		argv = append(argv, c.val)
	}
	out, err := runQuint(quint, dir, argv)
	if err != nil {
		return fmt.Errorf("simulate: quint run of the possibles' predicates: %w\n%s", err, out)
	}
	for _, c := range preds {
		var n, of int
		found := false
		for line := range strings.SplitSeq(out, "\n") {
			if _, err := fmt.Sscanf(strings.TrimSpace(line), c.val+" was witnessed in %d trace(s) out of %d", &n, &of); err == nil {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("simulate: quint's output names no count of witness %s (possible %q):\n%s", c.val, c.p.Text(), out)
		}
		if n == 0 {
			continue
		}
		c.quintFound = true
		itfPath := filepath.Join(dir, c.val+".itf.json")
		argv := []string{"run", "--main", "simulate", "--invariant", c.not, "--mbt", "--out-itf", itfPath,
			"--max-steps", fmt.Sprint(steps), "--max-samples", fmt.Sprint(samples), "--seed", seed}
		if out, err := runQuint(quint, dir, append(backend(argv), file)); err == nil || !strings.Contains(out, "[violation]") {
			continue // witnessed, its trace not taken again: reached all the same
		}
		if tr, err := itf.ReadFile(itfPath); err == nil {
			c.quintWitness = actionsOf(tr)
		}
	}
	return nil
}

func runQuint(quint, dir string, argv []string) (string, error) {
	cmd := exec.Command(quint, argv...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// actionsOf are a trace's actions after init (quint's mbt::actionTaken).
func actionsOf(tr *itf.Trace) []string {
	var out []string
	for i, s := range tr.States {
		if i > 0 {
			out = append(out, s.Action)
		}
	}
	return out
}

// takesInTurn is the length of acts' shortest prefix taking each of
// steps in turn, or -1.
func takesInTurn(acts []string, steps []binding.StepPattern) int {
	k := 0
	for i, a := range acts {
		if k < len(steps) && a == steps[k].Action {
			k++
			if k == len(steps) {
				return i + 1
			}
		}
	}
	return -1
}

// reportPossibles prints each possible's verdict, quint's and the
// evaluator's (rs, nil when it had no search: why in evalErr), and
// returns 1 if one is reached by neither. An evaluator's "impossible"
// against quint's witness is an error: the evaluator's bug.
func reportPossibles(cs []*possibleCheck, rs []validate.PossibleResult, evalErr error, steps, samples, verify int) (int, error) {
	code := 0
	for i, c := range cs {
		var r *validate.PossibleResult
		if rs != nil {
			r = &rs[i]
		}
		if r != nil && !r.Found && r.Complete && c.quintFound {
			return 2, fmt.Errorf("simulate: possible %q: quint reached it (%s) and the Go evaluator's complete search says no trace of up to %d steps does: "+
				"a bug of the evaluator (internal/qeval); report it with the binding", c.p.Text(), strings.Join(c.quintWitness, ", "), r.Depth)
		}
		var quintSays string
		switch {
		case c.quintSkip != "":
			quintSays = c.quintSkip
		case c.quintFound && len(c.quintWitness) > 0:
			quintSays = "quint's sampled trace: " + strings.Join(c.quintWitness, ", ")
		case c.quintFound && verify > 0:
			quintSays = fmt.Sprintf("quint verify: a trace of up to %d steps", verify)
		case c.quintFound:
			quintSays = "quint: reached in the initial state"
		case verify > 0:
			quintSays = fmt.Sprintf("quint verify: no trace of up to %d steps (a bounded proof)", verify)
		default:
			quintSays = fmt.Sprintf("quint: not reached in %d sampled traces of up to %d steps", samples, steps)
		}
		evalSays := "the Go evaluator: " + possibleEval(r, evalErr)
		switch {
		case r != nil && r.Found:
			fmt.Printf("  possible %s: %s (the Go evaluator's search)\n", c.p.Text(), r.Verdict())
		case c.quintFound:
			fmt.Printf("  possible %s: reached (%s; %s)\n", c.p.Text(), quintSays, evalSays)
		case r != nil && r.Complete:
			code = 1
			fmt.Printf("  FAIL    possible %s: %s; %s. The model forbids it: if a conforming run must be able to show it, find the guard that forbids it "+
				"(gotogether nav -binding shows each disabled action with why) and loosen it; if it must be impossible, drop it from simulate.possible\n", c.p.Text(), evalSays, quintSays)
		default:
			code = 1
			fmt.Printf("  FAIL    possible %s: %s; %s. The model may be too strict for any conforming run to show it: "+
				"find the guard that forbids it (gotogether nav -binding shows each disabled action with why) and loosen it if the requirement allows it, "+
				"or, if it is only rare, raise -samples, -steps or simulate.searchStates\n", c.p.Text(), quintSays, evalSays)
		}
	}
	return code, nil
}

func possibleEval(r *validate.PossibleResult, err error) string {
	switch {
	case err != nil:
		return "no search (" + err.Error() + ")"
	case r == nil:
		return "no search"
	}
	return r.Verdict()
}

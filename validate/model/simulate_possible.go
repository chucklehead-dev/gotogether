package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/confidence"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/itf"
)

// The possibles of a binding's simulation (P3): what the model on its
// own must be able to do, checked by quint (a predicate's witness is its
// counterexample to not(P); a sequence of actions is looked for in
// sampled traces' mbt::actionTaken) and by the Go evaluator's bounded
// search where the model is in its fragment (Analyzer.Possible).
// Either finding it reaches it; neither fails simulate, saying what to
// do. The evaluator calling impossible what quint found is its bug.

// possibleCheck is one possible as simulate checks it.
type possibleCheck struct {
	p     binding.Possible
	when  string // its predicate, or "" for steps
	steps []binding.StepPattern
	// val and not are the simulation module's vals of when and not(when)
	val, not string
	// quint's verdict: found (witness: a sequence's actions, or a
	// predicate's count of the sampled traces it held in), or why it did
	// not look ("" when it did)
	quintFound    bool
	quintWitness  []string
	quintWitnessN int
	quintSkip     string
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
func resolvePossibles(c *modelChecker, ps []binding.Possible) ([]*possibleCheck, error) {
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

// quintShare splits the possibles quint checks by how: predicates by
// quint verify (with -verify) or as witnesses of sampled traces,
// sequences of names in sampled traces' mbt::actionTaken. The rest are
// the evaluator's alone, each with why (quintSkip).
func quintShare(cs []*possibleCheck, verify int) (verified, preds, seqs []*possibleCheck) {
	for _, c := range cs {
		switch {
		case c.quintSkip != "":
		case c.when != "" && verify > 0:
			verified = append(verified, c)
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
	return verified, preds, seqs
}

// traceBatch is how many sampled traces quint writes at a time for the
// sequences: each an ITF file, looked through for every sequence not yet
// found.
const traceBatch = 100

// A hostedSample is quint's sampling of the possibles inside an
// invariant's run (hostArgs): the run's output, whose witness counts
// are the predicates', and the directory of the traces it wrote for the
// sequences, how many.
type hostedSample struct {
	out    string
	traces string
	n      int
}

// hostArgs are what an invariant's `quint run` adds to sample the
// possibles quint checks in the same traces (nil: none): the predicates
// as --witnesses (last: it takes every argument after it), and, for the
// sequences, the first batch of its traces written to tracesDir with
// mbt::actionTaken. One process for the invariant and the possibles,
// where one of their own cost as much again (a grist binding's 10-20 s).
func hostArgs(cs []*possibleCheck, verify, samples int, tracesDir string) (argv []string, n int) {
	if verify > 0 {
		return nil, 0
	}
	_, preds, seqs := quintShare(cs, verify)
	if len(seqs) > 0 {
		n = min(traceBatch, samples)
		argv = append(argv, "--mbt", "--out-itf", filepath.Join(tracesDir, "t_{seq}.itf.json"), "--n-traces", fmt.Sprint(n))
	}
	if len(preds) > 0 {
		argv = append(argv, "--witnesses")
		for _, c := range preds {
			argv = append(argv, c.val)
		}
	}
	return argv, n
}

// quintPossibles checks each possible with quint: with -verify a
// predicate by `quint verify --invariant not(P)`; else a predicate as a
// witness of sampled traces (in how many it held), and steps that name
// no parameters in sampled traces (quint's mbt::actionTaken), in batches
// until every one is found or the samples are spent. Steps that name a
// parameter's value are the evaluator's alone (quint does not record an
// action's arguments). host, if set, is a sampling an invariant's run
// made already (hostArgs): its witnesses are the predicates' verdicts
// and its traces the sequences' first batch.
func quintPossibles(quint, dir, file string, cs []*possibleCheck, steps, samples int, seed string, verify int, host *hostedSample) error {
	verified, preds, seqs := quintShare(cs, verify)
	for _, c := range verified {
		argv := []string{"verify", "--main", "simulate", "--invariant", c.not, "--max-steps", fmt.Sprint(verify), file}
		out, err := runQuint(quint, dir, argv)
		v := strings.Contains(out, "[violation]")
		if err != nil && !v {
			return fmt.Errorf("simulate: quint verify of possible %q: %w\n%s", c.p.Text(), err, out)
		}
		c.quintFound = v
	}
	if len(preds) > 0 {
		out := ""
		if host != nil {
			out = host.out
		} else {
			// the file first: --witnesses takes every argument after it
			argv := backendArgs([]string{"run", file, "--main", "simulate", "--max-steps", fmt.Sprint(steps), "--max-samples", fmt.Sprint(samples), "--seed", seed})
			argv = append(argv, "--witnesses")
			for _, c := range preds {
				argv = append(argv, c.val)
			}
			o, err := runQuint(quint, dir, argv)
			if err != nil {
				return fmt.Errorf("simulate: quint run of the possibles' predicates: %w\n%s", err, o)
			}
			out = o
		}
		if err := readWitnesses(out, preds); err != nil {
			return err
		}
	}
	if len(seqs) == 0 {
		return nil
	}
	look := func(bdir string) (left int, err error) {
		files, _ := filepath.Glob(filepath.Join(bdir, "*.itf.json"))
		// by the module's types where quint's Rust backend's integers need them
		traces := &binding.TraceReader{Quint: quint, Spec: file, Module: "simulate"}
		for _, f := range files {
			tr, err := traces.Read(f)
			if err != nil {
				return 0, fmt.Errorf("simulate: %s: %w", f, err)
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
		return left, nil
	}
	// sampled traces, a batch at a time (each its own ITF file), each
	// looked through for every sequence not yet found
	start := 0
	if host != nil {
		left, err := look(host.traces)
		if err != nil || left == 0 {
			return err
		}
		start = host.n
	}
	base, seedErr := strconv.ParseInt(seed, 0, 64)
	for done := start; done < samples; done += traceBatch {
		n := min(traceBatch, samples-done)
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
		argv := backendArgs([]string{"run", "--main", "simulate", "--mbt", "--out-itf", filepath.Join(bdir, "t_{seq}.itf.json"), "--n-traces", fmt.Sprint(n),
			"--max-steps", fmt.Sprint(steps), "--max-samples", fmt.Sprint(n), "--seed", s})
		if out, err := runQuint(quint, dir, append(argv, file)); err != nil {
			return fmt.Errorf("simulate: quint run of the possibles' traces: %w\n%s", err, out)
		}
		left, err := look(bdir)
		if err != nil {
			return err
		}
		if left == 0 {
			break
		}
	}
	return nil
}

// backendArgs is argv with quint's backend, if QUINT_BACKEND names one.
func backendArgs(argv []string) []string {
	if be := os.Getenv("QUINT_BACKEND"); be != "" {
		return append(argv, "--backend", be)
	}
	return argv
}

// readWitnesses reads each predicate's count of the traces it held in
// from a `quint run --witnesses` output: found if any.
func readWitnesses(out string, preds []*possibleCheck) error {
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
		c.quintFound, c.quintWitnessN = n > 0, n
	}
	return nil
}

func runQuint(quint, dir string, argv []string) (string, error) {
	cmd := quintcmd.Command(context.Background(), quint, dir, argv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// actionsOf are a trace's actions after init (quint's mbt::actionTaken),
// but its stutters: a state whose action quint names "" (a step that
// took none of the model's actions, as a calls binding's model takes
// where nothing is called) is no action of a witness.
func actionsOf(tr *itf.Trace) []string {
	var out []string
	for i, s := range tr.States {
		if i > 0 && s.Action != "" {
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
// against quint's witness is an error: the evaluator's bug. A failure
// says what the possible asks (a named predicate's text) and how to
// find the guard in bpath's model.
func (s *sim) reportPossibles(bpath string, cs []*possibleCheck, rs []PossibleResult, evalErr error, steps, samples, verify int) (int, error) {
	code := 0
	for i, c := range cs {
		var r *PossibleResult
		if rs != nil {
			r = &rs[i]
		}
		var quintSays string
		switch {
		case c.quintSkip != "":
			quintSays = c.quintSkip
		case c.quintFound && len(c.quintWitness) > 0:
			quintSays = "quint's sampled trace: " + strings.Join(c.quintWitness, ", ")
		case c.quintFound && verify > 0:
			quintSays = fmt.Sprintf("quint verify: a trace of up to %d steps", verify)
		case c.quintFound && c.quintWitnessN > 0:
			quintSays = fmt.Sprintf("quint: it held in %d of %d sampled traces of up to %d steps", c.quintWitnessN, samples, steps)
		case c.quintFound:
			quintSays = "quint: reached in the initial state"
		case verify > 0:
			quintSays = fmt.Sprintf("quint verify: no trace of up to %d steps (a bounded proof)", verify)
		default:
			quintSays = fmt.Sprintf("quint: not reached in %d sampled traces of up to %d steps (%s)", samples, steps, confidence.Sentence(samples, "trace", "a state"))
		}
		if r != nil && !r.Found && r.Complete && c.quintFound {
			return 2, fmt.Errorf("simulate: possible %q: quint reached it (%s) and the Go evaluator's complete search says no trace of up to %d steps does: "+
				"a bug of the evaluator (internal/qeval); report it with the binding", c.p.Text(), quintSays, r.Depth)
		}
		evalSays := "the Go evaluator: " + possibleEval(r, evalErr)
		what := c.p.Text()
		if c.p.Name != "" && c.when != "" {
			what += " (when " + c.when + ")"
		} else if c.p.Name != "" {
			what += " (steps " + strings.Join(c.p.Steps, ", ") + ")"
		}
		nav := fmt.Sprintf("gotogether nav -binding %s shows each action disabled in a state, with why", bpath)
		v := PossibleVerdict{Possible: c.p, Eval: r, Quint: quintSays}
		if evalErr != nil {
			v.EvalErr = evalErr.Error()
		}
		switch {
		case r != nil && r.Found:
			v.Found = true
			v.Verdict = r.Verdict() + " (the Go evaluator's search)"
			v.Line = fmt.Sprintf("  possible %s: %s\n", c.p.Text(), v.Verdict)
		case c.quintFound:
			v.Found = true
			v.Verdict = fmt.Sprintf("reached (%s; %s)", quintSays, evalSays)
			v.Line = fmt.Sprintf("  possible %s: %s\n", c.p.Text(), v.Verdict)
		case r != nil && r.Complete:
			code = 1
			v.Impossible = true
			v.Verdict = fmt.Sprintf("%s; %s. The model forbids it: if a conforming run must be able to show it, find the guard that forbids it "+
				"(%s) and loosen it; if it must be impossible, drop it from simulate.possible", evalSays, quintSays, nav)
			v.Line = fmt.Sprintf("  FAIL    possible %s: %s\n", what, v.Verdict)
		default:
			code = 1
			v.Verdict = fmt.Sprintf("%s; %s. The model may be too strict for any conforming run to show it: "+
				"find the guard that forbids it (%s) and loosen it if the requirement allows it; "+
				"if it is only rare or deep, raise simulate.search.states (or -states) for the evaluator's search, -samples or -steps for quint's", quintSays, evalSays, nav)
			v.Line = fmt.Sprintf("  FAIL    possible %s: %s\n", what, v.Verdict)
		}
		s.printf("%s", v.Line)
		s.rep.Possibles = append(s.rep.Possibles, v)
	}
	return code, nil
}

func possibleEval(r *PossibleResult, err error) string {
	switch {
	case err != nil:
		return "no search (" + err.Error() + ")"
	case r == nil:
		return "no search"
	}
	return r.Verdict()
}

package model

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/confidence"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/validate/internal/evalshare"
)

// A model analysed by its compiled model (SimulateOptions.Compiled,
// DESIGN §110) has no quint: the graph, the possibles' search and the
// sampling are the Go evaluator's (qeval.Model.Simulate's traces, each
// choice drawn uniformly among those that enable the step: not quint's
// distribution, the same meaning), and what only quint does is reported
// not done (Report.NotDone), never passed: the graph's cross-check
// against quint's samples, the witnesses' replays by quint test. quint
// verify is refused.

// instanceEval is the evaluator of c's instance under overrides (the
// simulate section's constants, then NAME=expr), with a val for each of
// preds, and its step: an error, prefixed what, where there is none.
func (c *modelChecker) instanceEval(ctx context.Context, overrides, preds []string, what string) (*evalshare.Eval, string, error) {
	cc := c.simulationInstance(overrides)
	original := evalshare.EvaluatorOf(ctx, cc.Checker, func(path string) (*binding.EvalModule, error) {
		return cc.Binding.PossibleModule(c.Model, path, preds)
	})
	e := analysisEvaluator(ctx, original)
	if e.Err != nil {
		return nil, "", fmt.Errorf("%s: the Go evaluator has no evaluation module for %s: %w", what, c.Binding.SpecPath(), e.Err)
	}
	step, ok := e.Module.Actions["step"]
	if !ok {
		return nil, "", fmt.Errorf("simulate: %s has no step action to run on its own: add one that picks among its actions, "+
			"or say why not (simulate.skip: a replay-only model)", c.Binding.Module)
	}
	if left := unvalued(e); len(left) > 0 {
		return nil, "", fmt.Errorf("%s: the binding computes constants from a run's steps (%s): give them values (the simulate section's constants, or -const)",
			what, strings.Join(left, ", "))
	}
	return e, step, nil
}

// seedOf is a simulation's seed as the sampler's: the number it names,
// else a hash of its text (quint takes either; the traces are not
// quint's whatever the seed).
func seedOf(seed string) uint64 {
	if n, err := strconv.ParseInt(seed, 0, 64); err == nil {
		return uint64(n)
	}
	h := fnv.New64a()
	h.Write([]byte(seed))
	return h.Sum64()
}

// sampler is trace i of a simulation seeded seed: one generator per
// trace, so a trace is drawn again alone.
func sampler(seed uint64, i int) *rand.Rand { return rand.New(rand.NewPCG(seed, uint64(i))) }

// possiblePreds are the predicates of cs, for an evaluation module's vals.
func possiblePreds(cs []*possibleCheck) []string {
	var out []string
	for _, c := range cs {
		if c.when != "" {
			out = append(out, c.when)
		}
	}
	return out
}

// sampled is Simulate without Graph or Verify by a compiled model: the
// possibles searched by the evaluator, what it did not find sampled by its
// sampler, and each invariant judged in Samples traces of up to Steps
// steps, as quint run --invariant judges it (the first false state ends a
// trace), each as the section classifies it.
func (s *sim) sampled(ctx context.Context, bpath string, b *binding.Binding, checker *modelChecker, possibles []*possibleCheck, possibleList []binding.Possible,
	overrides, invariants []string, holds, allows map[string]bool, opt SimulateOptions) error {
	steps, samples := opt.Steps, opt.Samples
	s.printf("simulate %s: %s(%s), %d traces of %d steps, seed %s, sampled by the Go evaluator (the compiled model %s: no quint)\n",
		bpath, b.Module, strings.Join(constantList(b, overrides), ", "), samples, steps, opt.Seed, s.compiled)
	progress := progressOf(ctx)
	var rs []PossibleResult
	var evalErr error
	var left []*possibleCheck
	if len(possibles) > 0 {
		done := progress.phase("go-possible")
		rs, evalErr = checker.Possible(ctx, possibleList, PossibleSearch{Constants: overrides, States: opt.States, Depth: opt.Depth})
		done(phaseStatus(ctx, evalErr))
		if isAnalysisCancellation(evalErr) {
			return evalErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		for i, c := range possibles {
			if evalErr == nil && rs[i].Found {
				c.quintSkip = "found by the Go evaluator, not sampled"
				continue
			}
			left = append(left, c)
		}
	}
	e, step, err := checker.instanceEval(ctx, overrides, append(append([]string(nil), invariants...), possiblePreds(left)...), "simulate")
	if err != nil {
		return err
	}
	seed := seedOf(opt.Seed)
	bound := Bound{Kind: "sampled", Traces: samples, Steps: steps}
	code := 0
	for _, inv := range invariants {
		done := progress.phase("invariant " + inv)
		v := Verdict{Name: inv, Class: classOf(inv, holds, allows), Bound: bound}
		var trace string
		for i := range samples {
			if err := ctx.Err(); err != nil {
				done(phaseStatus(ctx, err))
				return err
			}
			tr, err := e.Model.Simulate(e.Consts, qeval.SimOptions{Init: e.Module.Init, Step: step, Steps: steps, Invariants: []string{e.Module.Possible[inv]}}, sampler(seed, i))
			var u *qeval.Unsupported
			var ee *qeval.EvalError
			switch {
			case errors.As(err, &u):
				v.Unchecked = fmt.Sprintf("%s: %s", u.Op, u.Why)
			case errors.As(err, &ee):
				v.Error, trace = ee.Msg, sampledText(e, tr, i, opt.Seed)
			case err != nil:
				done(phaseStatus(ctx, err))
				return fmt.Errorf("simulate: the Go evaluator's sample %d of %s: %w", i, inv, err)
			case tr.Violated != "":
				v.Violated, trace = true, sampledText(e, tr, i, opt.Seed)
			}
			if err != nil || v.Violated {
				break
			}
		}
		done("completed")
		v.Held = !v.Violated && v.Error == "" && v.Unchecked == ""
		if v.Violated {
			v.Trace = trace
		}
		s.rep.Invariants = append(s.rep.Invariants, v)
		sample := fmt.Sprintf("%d traces the Go evaluator sampled, of up to %d steps", samples, steps)
		switch {
		case v.Unchecked != "":
			code = max(code, 2)
			s.printf("  unchecked %s: the Go evaluator cannot evaluate it (%s): simulate it where quint is installed (GOTOGETHER_COMPILED=0)\n", inv, v.Unchecked)
		case v.Error != "":
			code = 1
			s.printf("  FAIL    %s: evaluating it fails in a sampled state (%s): the model's own error, fix the model. The trace:\n%s", inv, v.Error, trace)
		case holds[inv] && !v.Violated:
			s.printf("  holds   %s: none of %s violates it (a sample, no proof: %s; -graph searches every state)\n", inv, sample, confidence.Sentence(samples, "trace", "a violation"))
		case allows[inv] && v.Violated:
			s.printf("  allows  %s: a trace violates it (the code must not)\n", inv)
		case holds[inv]:
			code = 1
			s.printf("  FAIL    %s: the model should guarantee it, and a trace violates it: fix the model if it must hold, "+
				"or move it to simulate.allows if it is a requirement on the code (the model lets it fail, the code must prevent it). The trace:\n%s", inv, trace)
		case allows[inv]:
			code = 1
			s.printf("  FAIL    %s: the model should allow its violation, and none of %s violates it: "+
				"checking the code against it proves nothing. Raise -samples or -steps, or move it to simulate.holds\n", inv, sample)
		case v.Violated:
			code = 1
			s.printf("  FAIL    %s: not classified; the model allows its violation: add it to simulate.allows if the code must prevent it, else fix the model\n", inv)
		default:
			s.printf("  holds   %s: none of %s violates it (unclassified: add it to simulate.holds to keep it so; a sample, no proof)\n", inv, sample)
		}
	}
	if len(possibles) > 0 {
		if err := goPossibles(ctx, e, step, checker.Model, left, steps, samples, opt.Seed); err != nil {
			return err
		}
		pc, err := s.reportPossibles(bpath, possibles, rs, evalErr, steps, samples, 0)
		if err != nil {
			return err
		}
		code = max(code, pc)
	}
	s.rep.Code = code
	return nil
}

// sampledText is trace tr (sample i of seed) as a failure shows it: each
// state after the action that reached it.
func sampledText(e *evalshare.Eval, tr *qeval.SimTrace, i int, seed string) string {
	if tr == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "    the Go evaluator's trace %d (seed %s):\n", i, seed)
	fmt.Fprintf(&b, "    0. init\n         %s\n", stateText(e, tr.Init.State))
	for k, t := range tr.Steps {
		call := "step"
		if a, ok := firstAction(t); ok {
			call = a.String()
		}
		fmt.Fprintf(&b, "    %d. %s\n         %s\n", k+1, call, stateText(e, t.State))
	}
	return b.String()
}

// firstAction is the first event of t that names an action of the
// model's own (not the evaluation module's step).
func firstAction(t qeval.Transition) (qeval.Event, bool) {
	for _, a := range t.Actions() {
		if a.Action != "step" && !strings.HasPrefix(a.Action, "__gtg_") {
			return a, true
		}
	}
	return qeval.Event{}, false
}

// goSampler names the Go evaluator's sampler in a possible's verdict.
const goSampler = "the Go evaluator's sampler"

// goPossibles looks for each of cs in samples traces of up to steps steps
// of the Go evaluator's sampler (a compiled model's, in place of quint's
// sampling): a predicate in any state, steps in turn among a trace's
// transitions, each with the arguments it names (which quint's actionTaken
// cannot show). A trace the evaluator cannot draw ends the sampling of
// what is left, saying why.
func goPossibles(ctx context.Context, e *evalshare.Eval, step string, m *binding.Model, cs []*possibleCheck, steps, samples int, seed string) error {
	type want struct {
		c       *possibleCheck
		match   []func(qeval.Transition) bool
		k       int // the steps taken in turn in the current trace
		taken   []string
		found   bool
		inTrace int
	}
	var ws []*want
	for _, c := range cs {
		if c.quintSkip != "" {
			continue
		}
		w := &want{c: c}
		for _, sp := range c.steps {
			f, err := stepMatcher(e, m, sp)
			if err != nil {
				return fmt.Errorf("simulate: possible %q: %w", c.p.Text(), err)
			}
			w.match = append(w.match, f)
		}
		ws = append(ws, w)
	}
	if len(ws) == 0 {
		return nil
	}
	rng := seedOf(seed)
	left := len(ws)
	drawn := 0
	var stopped string
	for i := 0; i < samples && left > 0 && stopped == ""; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		tr, err := e.Model.Simulate(e.Consts, qeval.SimOptions{Init: e.Module.Init, Step: step, Steps: steps}, sampler(rng, i))
		if err != nil {
			var u *qeval.Unsupported
			var ee *qeval.EvalError
			switch {
			case errors.As(err, &u):
				stopped = fmt.Sprintf("%s is outside the Go evaluator's fragment (%s)", u.Op, u.Why)
			case errors.As(err, &ee):
				stopped = "the model's own error: " + ee.Msg
			default:
				return fmt.Errorf("simulate: the Go evaluator's sample %d of the possibles: %w", i, err)
			}
			break
		}
		drawn++
		states := tr.States()
		for _, w := range ws {
			if w.found {
				continue
			}
			if w.c.when != "" {
				for _, st := range states {
					x, err := e.Model.Eval(e.Consts, st, e.Module.Possible[w.c.when])
					if err != nil {
						return fmt.Errorf("simulate: possible %q: %w", w.c.p.Text(), err)
					}
					if x == qeval.Bool(true) {
						w.found, w.inTrace = true, i
						break
					}
				}
			} else {
				w.k, w.taken = 0, nil
				for _, t := range tr.Steps {
					name := "step"
					if a, ok := evalshare.BranchEvent(m, t); ok {
						name = a.String()
					}
					w.taken = append(w.taken, name)
					if w.match[w.k](t) {
						if w.k++; w.k == len(w.match) {
							w.found, w.inTrace = true, i
							break
						}
					}
				}
			}
			if w.found {
				left--
			}
		}
	}
	for _, w := range ws {
		c := w.c
		switch {
		case w.found && c.when != "":
			c.quintFound = true
			c.sampled = fmt.Sprintf("%s: it holds in a state of trace %d (seed %s)", goSampler, w.inTrace, seed)
		case w.found:
			c.quintFound, c.quintWitness = true, w.taken
			c.sampled = fmt.Sprintf("%s's trace %d (seed %s): %s", goSampler, w.inTrace, seed, strings.Join(w.taken, ", "))
		case stopped != "":
			c.sampled = fmt.Sprintf("%s: not reached in %d traces of up to %d steps, then it stopped (%s)", goSampler, drawn, steps, stopped)
		default:
			c.sampled = fmt.Sprintf("%s: not reached in %d traces of up to %d steps (%s)", goSampler, drawn, steps, confidence.Sentence(drawn, "trace", "a state"))
		}
	}
	return nil
}

// witnesses is n witnesses: "1 witness", "2 witnesses".
func witnesses(n int) string {
	if n == 1 {
		return "1 witness"
	}
	return fmt.Sprintf("%d witnesses", n)
}

// notReplayed marks each witness of the report, which Replay would have
// had quint test replay, not done: no quint (a compiled model's). The
// witness is the Go evaluator's alone, said so, never "replayed".
func (s *sim) notReplayed() {
	why := "not done: quint test would replay it; the model is analysed by its compiled model " + s.compiled + ", the witness the Go evaluator's alone"
	n := 0
	mark := func(v *Verdict) {
		v.Replayed = why
		n++
	}
	for i := range s.rep.Invariants {
		if v := &s.rep.Invariants[i]; v.Violated && v.Counterexample != nil {
			mark(v)
		}
	}
	for i := range s.rep.Eventually {
		if v := &s.rep.Eventually[i]; v.Violated && v.Lasso != nil {
			mark(v)
		}
	}
	if v := s.rep.Deadlocks; v != nil && v.Violated && v.Counterexample != nil {
		mark(v)
	}
	if n > 0 {
		s.rep.NotDone = append(s.rep.NotDone, fmt.Sprintf("the replay of %s by quint test: no quint (the compiled model %s)", witnesses(n), s.compiled))
		s.printf("  not done the replay of %s by quint test: no quint, the compiled model %s (each witness is the Go evaluator's alone)\n", witnesses(n), s.compiled)
	}
}

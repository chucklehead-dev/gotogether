package validate

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/memo"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Checker validates steps against one binding. Create it once (it typechecks
// the model) and call Check per run: this is the entry point for Go tests.
type Checker struct {
	Binding *binding.Binding
	Model   *binding.Model
	Options Options

	// mode is how its checks are made: a search's and a shadow's
	// checkers set it on their copies
	mode mode
	// memo is a search's states of the candidates the Go evaluator
	// accepted, by their calls: an extension is judged from its prefix's
	memo *stateMemo
	// planner plans history's candidates, its instance computed once
	planner *binding.Planner
	// scratch is its temporary directory, with no Options.Dir (Close)
	scratch *scratch
	// compiled, if set, is the compiled model it judges by, with no
	// quint (NewCompiledChecker)
	compiled *compiledState
}

// mode is how a checker's checks are made, set on the copies a search
// and a shadow make: one value, so a copy carries all of it.
type mode struct {
	// history, if set, is the history a search's candidates order: they
	// are planned in its instance, with its interned identifiers
	// (binding.Planner)
	history *qtrace.Trace
	// quintOnly: a search's candidates are checked by quint alone (a
	// search again after quint contradicted the Go evaluator's)
	quintOnly bool
	// byCheck: an order the Go evaluator found is left to the check of the
	// search's Steps to confirm (Judge)
	byCheck bool
	// ctx is the Judge's or MutateSteps' context, which a search's quint
	// checks run under (cancelling it kills them); nil: Background
	ctx context.Context
	// skew is how far apart recorders' clocks may be for a search's real
	// time (SearchOptions.Skew; 0: qtrace.RealTimeSlack)
	skew time.Duration
}

// context is the context of c's search's quint checks.
func (m mode) context() context.Context {
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

// NewChecker loads a binding file, typechecks its model and lints the
// binding against it.
func NewChecker(bindingPath string, opt Options) (*Checker, error) {
	b, err := binding.Load(bindingPath)
	if err != nil {
		return nil, err
	}
	m, err := loadModel(opt.Quint, b.SpecPath(), b.Module)
	if err != nil {
		return nil, err
	}
	if errs := b.Lint(m); len(errs) > 0 {
		return nil, fmt.Errorf("binding %s does not fit %s: %v", bindingPath, b.Module, errs)
	}
	return &Checker{Binding: b, Model: m, Options: opt, scratch: &scratch{}}, nil
}

// scratch is a checker's own temporary directory, for checks with no
// Options.Dir: each check a directory under it (the modules and ITF its
// report names), all removed by Close. Each check once made a temporary
// directory of its own, which nothing removed.
type scratch struct {
	once sync.Once
	root string
	err  error
}

// dirFor is o with a directory of its own for one check: under o.Dir
// when it names one, else under c's scratch directory. Checks with one
// Dir once wrote the same files (observed_<actor>.qnt and its ITF), so
// two at once replayed each other's modules and took each other's
// verdicts.
func (c *Checker) dirFor(o Options) (Options, error) {
	if o.Dir != "" {
		d, err := checkDir(o.Dir)
		if err != nil {
			return o, err
		}
		o.Dir = d
		return o, nil
	}
	if c.scratch == nil {
		return o, nil
	}
	c.scratch.once.Do(func() { c.scratch.root, c.scratch.err = os.MkdirTemp("", "gotogether-") })
	if c.scratch.err != nil {
		return o, c.scratch.err
	}
	d, err := checkDir(c.scratch.root)
	if err != nil {
		return o, err
	}
	o.Dir = d
	return o, nil
}

// Process-wide: the checks' directories numbered so far.
var checks atomic.Int64

// checkDir is a new directory under dir, check-<n> by the process's
// count: a rerun with the same Dir (GOTOGETHER_KEEP, the CLI's -dir)
// writes into the directories of the run before in the same order,
// where random names piled a new generation up beside the old.
func checkDir(dir string) (string, error) {
	d := filepath.Join(dir, fmt.Sprintf("check-%d", checks.Add(1)))
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, nil
}

// Close removes the temporary directory c's checks wrote to when it was
// made with no Options.Dir (and the modules and ITF its reports name). A
// checker given a Dir leaves it. Its copies share it: close the one made.
func (c *Checker) Close() error {
	if c.scratch == nil || c.scratch.root == "" {
		return nil
	}
	return os.RemoveAll(c.scratch.root)
}

// Process-wide: the checkers For built.
var cached memo.Map[string, *Checker] // process-wide: absolute binding path, quint, backend -> *Checker

// Cached is NewChecker once per binding, quint binary and backend in a
// process (the model is typechecked and the binding linted once), with
// o's other options (Dir, NoITF) on the copy it returns: qtest and
// qhegel share it.
func Cached(bindingPath string, o Options) (*Checker, error) {
	abs, err := filepath.Abs(bindingPath)
	if err != nil {
		return nil, err
	}
	key := abs + "|" + o.Quint + "|" + o.Backend
	c, err := cached.Do(key, func() (*Checker, error) { return NewChecker(abs, o) })
	if err != nil {
		return nil, err
	}
	cc := *c
	cc.Options = o
	return &cc, nil
}

// WithConstants returns a checker for another instance of the same model:
// the binding's constants with these overridden (Quint expressions, or
// templates as in the binding). One binding then serves runs of different
// sizes, such as a load run's stock and a small exploration's.
func (c *Checker) WithConstants(constants map[string]string) (*Checker, error) {
	b := *c.Binding
	b.Constants = map[string]string{}
	for k, v := range c.Binding.Constants {
		b.Constants[k] = v
	}
	for k, v := range constants {
		if _, ok := c.Model.Consts[k]; !ok {
			return nil, fmt.Errorf("constant %s: not a const of %s (it has %v)", k, c.Model.Module, slices.Sorted(maps.Keys(c.Model.Consts)))
		}
		b.Constants[k] = v
	}
	cc := *c
	cc.Binding = &b
	return &cc, nil
}

// Report is the result of Check: global reconstruction issues and one
// Result per actor.
type Report struct {
	Issues []qtrace.Issue
	Actors []*Result
	// Unsettled, if set, is why the run's system did not settle when the
	// run had it settle (qenv.Settle): a run that never reached quiescence
	// is not OK, whatever its steps. Set by the caller that ran it (qtest).
	Unsettled string
}

// OK is true when every actor's trace conforms and satisfies the
// invariants, and the system settled if it was asked to.
func (r *Report) OK() bool {
	if r.Unsettled != "" {
		return false
	}
	for _, a := range r.Actors {
		if !a.OK() {
			return false
		}
	}
	return len(r.Actors) > 0
}

// Reason is why a report that is not OK fails, as validate's result line
// says it: its verdict's reason, else "unsettled" for a run whose system
// did not settle, else "fails". (Of an OK report it says nothing: ask OK
// first.)
func (r *Report) Reason() string {
	if v := r.Verdict(); v.Reason != "" {
		return v.Reason
	}
	if r.Unsettled != "" {
		return "unsettled"
	}
	return "fails"
}

// Summary renders a report for humans: its issues (but the info naming
// the actors its binding leaves out, which is in Issues), each actor's
// result.
func (r *Report) Summary() string { return r.summary(false) }

// VerboseSummary is Summary with every actor's VerboseSummary: each
// failing record's other indices, and the model's whole state.
func (r *Report) VerboseSummary() string { return r.summary(true) }

func (r *Report) summary(verbose bool) string {
	s := ""
	if r.Unsettled != "" {
		s += r.Unsettled + "\n"
	}
	for _, i := range r.Issues {
		if i.Severity == "info" && i.Kind == "actors" {
			// the actors the binding's actors: patterns leave out are
			// another binding's by its own declaration: in Issues (the
			// data), not in every run's report (a grist run's clients, on
			// each store-side report)
			continue
		}
		s += fmt.Sprintf("issue: %s\n", i)
	}
	folded, actors := foldPartitions(r.Actors)
	for _, l := range folded {
		s += l + "\n"
	}
	for _, a := range actors {
		sum := a.summary(verbose)
		if len(r.Actors) == 1 && a.Actor == "default" {
			// one actor, unnamed: "actor default" on every report read as
			// a setting to change
			sum = "the run" + strings.TrimPrefix(sum, "actor default")
		}
		s += sum
	}
	return s
}

// reconstruct is the binding's Reconstruct: the traces of the actors b
// checks.
func reconstruct(b *binding.Binding, steps []qtrace.Step) ([]*qtrace.Trace, []qtrace.Issue) {
	return b.Reconstruct(steps)
}

// Check reconstructs per-actor traces from steps (in any order, from any
// number of recorders) and validates each.
func (c *Checker) Check(ctx context.Context, steps []qtrace.Step) (*Report, error) {
	if c.compiled != nil {
		return c.checkCompiled(ctx, steps)
	}
	if c.partitionedChecks() {
		// each entity's trace an item of a batch: judged in parallel, the
		// failures' quint replays shared
		reps, errs := c.CheckBatch(ctx, [][]qtrace.Step{steps}, 0)
		return reps[0], errs[0]
	}
	traces, issues := reconstruct(c.Binding, steps)
	rep := &Report{Issues: issues}
	re := c.runEvaluator()
	var shadowErr error // the first actor the evaluator and quint disagree on
	for _, t := range traces {
		if (len(c.Binding.Hidden) > 0 || c.resumes(t)) && c.mode.history == nil {
			r, err := c.checkHidden(ctx, t)
			if err != nil {
				return rep, fmt.Errorf("actor %s: %w", t.Actor, err)
			}
			judgeLost(c.Binding, r, t.Lost)
			judgeAbsent(c.Binding, r, t.Lost, t.Gaps, nil, nil)
			rep.Actors = append(rep.Actors, r)
			continue
		}
		o, err := c.dirFor(c.withinITF(c.Options))
		if err != nil {
			return nil, err
		}
		if o, err = o.resolve(); err != nil {
			return nil, err
		}
		p, err := c.Binding.Plan(c.Model, t)
		if err != nil {
			return rep, fmt.Errorf("actor %s: %w", t.Actor, err)
		}
		r, serr, err := c.checkPlan(ctx, re, t, p, newResult(c.Binding, t, p), o)
		if err != nil {
			return rep, fmt.Errorf("actor %s: %w", t.Actor, err)
		}
		if shadowErr == nil {
			shadowErr = serr
		}
		c.explainResult(p, r, o)
		c.judgeWithin(t, r)
		judgeLost(c.Binding, r, t.Lost)
		judgeAbsent(c.Binding, r, t.Lost, t.Gaps, nil, nil)
		rep.Actors = append(rep.Actors, r)
	}
	if shadowErr != nil {
		return rep, shadowErr
	}
	return rep, nil
}

// WithInvariants returns a copy of the checker that checks these model
// invariants instead of the binding's, e.g. a property the design does not
// guarantee, to see whether (and where) a recorded run violates it.
func (c *Checker) WithInvariants(invariants ...string) (*Checker, error) {
	for _, inv := range invariants {
		if !c.Model.Vals[inv] {
			return nil, fmt.Errorf("invariant %s: not a boolean val of %s", inv, c.Binding.Module)
		}
	}
	b := *c.Binding
	b.Invariants = invariants
	cc := *c
	cc.Binding = &b
	return &cc, nil
}

// WithoutRefs is r with its failing steps' refs (qtrace.Step.Ref) left
// out, on a copy: what a verdict cached for every run of the same steps
// says (its Summary), since each run's refs are its own and no verdict
// reads them.
func (r *Report) WithoutRefs() *Report {
	fix := func(f *Failure) *Failure {
		if f == nil || f.Step == nil || f.Step.Ref == "" {
			return f
		}
		c, s := *f, *f.Step
		s.Ref = ""
		c.Step = &s
		return &c
	}
	out := *r
	out.Actors = make([]*Result, len(r.Actors))
	for i, a := range r.Actors {
		c := *a
		c.Failure = fix(a.Failure)
		c.Invariants = slices.Clone(a.Invariants)
		for j := range c.Invariants {
			c.Invariants[j].Failure = fix(c.Invariants[j].Failure)
		}
		out.Actors[i] = &c
	}
	return &out
}

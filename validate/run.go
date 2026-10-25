package validate

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"path/filepath"
	"sync"
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
	// planner plans within's candidates, its instance computed once
	planner *binding.Planner
	// scratch is its temporary directory, with no Options.Dir (Close)
	scratch *scratch
}

// mode is how a checker's checks are made, set on the copies a search
// and a shadow make: one value, so a copy carries all of it.
type mode struct {
	// history, if set, is the history a search's candidates order: they
	// are planned in its instance, with its interned identifiers
	// (binding.PlanWithin)
	history *qtrace.Trace
	// quintOnly: a search's candidates are checked by quint alone (a
	// search again after quint contradicted the Go evaluator's)
	quintOnly bool
	// byCheck: an order the Go evaluator found is left to the check of the
	// search's Steps to confirm (Judge)
	byCheck bool
	// shadowed: the checks of a shadow's (GOTOGETHER_EVAL=shadow), not
	// shadowed again
	shadowed bool
}

// NewChecker loads a binding file, typechecks its model and lints the
// binding against it.
func NewChecker(bindingPath string, opt Options) (*Checker, error) {
	b, err := binding.Load(bindingPath)
	if err != nil {
		return nil, err
	}
	m, err := binding.LoadModel(opt.Quint, b.SpecPath(), b.Module)
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

// dirFor is o with a directory of its own under c's scratch directory
// when o names none.
func (c *Checker) dirFor(o Options) (Options, error) {
	if o.Dir != "" || c.scratch == nil {
		return o, nil
	}
	c.scratch.once.Do(func() { c.scratch.root, c.scratch.err = os.MkdirTemp("", "gotogether-") })
	if c.scratch.err != nil {
		return o, c.scratch.err
	}
	d, err := os.MkdirTemp(c.scratch.root, "check-")
	if err != nil {
		return o, err
	}
	o.Dir = d
	return o, nil
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
var cached sync.Map // absolute binding path, quint, backend -> *Checker

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
	c, ok := cached.Load(key)
	if !ok {
		nc, err := NewChecker(abs, o)
		if err != nil {
			return nil, err
		}
		c, _ = cached.LoadOrStore(key, nc)
	}
	cc := *c.(*Checker)
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
			return nil, fmt.Errorf("constant %s: not a const of %s (it has %v)", k, c.Model.Module, sortedConsts(c.Model.Consts))
		}
		b.Constants[k] = v
	}
	cc := *c
	cc.Binding = &b
	return &cc, nil
}

func sortedConsts(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
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

func (r *Report) Summary() string {
	s := ""
	if r.Unsettled != "" {
		s += r.Unsettled + "\n"
	}
	for _, i := range r.Issues {
		s += fmt.Sprintf("issue: %s\n", i)
	}
	for _, a := range r.Actors {
		s += a.Summary()
	}
	return s
}

// Check reconstructs per-actor traces from steps (in any order, from any
// number of recorders) and validates each.
func (c *Checker) Check(ctx context.Context, steps []qtrace.Step) (*Report, error) {
	traces, issues := qtrace.Reconstruct(steps)
	rep := &Report{Issues: issues}
	for _, t := range traces {
		if len(c.Binding.Hidden) > 0 && c.mode.history == nil {
			r, err := c.checkHidden(t)
			if err != nil {
				return rep, fmt.Errorf("actor %s: %w", t.Actor, err)
			}
			rep.Actors = append(rep.Actors, r)
			continue
		}
		o, err := c.dirFor(c.withinITF(c.Options))
		if err != nil {
			return nil, err
		}
		r, err := validateTrace(ctx, c.Binding, c.Model, t, o)
		if err != nil {
			return rep, fmt.Errorf("actor %s: %w", t.Actor, err)
		}
		c.judgeWithin(t, r)
		rep.Actors = append(rep.Actors, r)
	}
	if err := c.shadowRuns([][]qtrace.Step{steps})[0]; err != nil {
		return rep, err
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

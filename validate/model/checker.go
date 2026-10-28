// Package model analyses a binding's model on its own, before any code
// runs (DESIGN §69, §70): its state graph with the Go evaluator (Graph:
// invariants in every reachable state, deadlocks, eventual vals as
// cycles), the possibles' search, and Simulate, the one engine of
// `gotogether simulate` and qtest.Model's claims (quint's sampling,
// quint verify, or the graph, with the possibles searched then sampled).
// Package validate judges runs; this package builds on its Checker.
package model

import (
	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/validate"
)

// An Analyzer is a binding's model on its own, with a validate.Checker's
// compiled model and evaluator.
type Analyzer struct {
	c   *modelChecker
	own bool // New made the checker: Close closes it
}

// New is an Analyzer of the binding file at bindingPath, with its own
// checker (validate.NewChecker's options); Close it.
func New(bindingPath string, opt validate.Options) (*Analyzer, error) {
	c, err := validate.NewChecker(bindingPath, opt)
	if err != nil {
		return nil, err
	}
	return &Analyzer{c: &modelChecker{c}, own: true}, nil
}

// For is an Analyzer of c's binding with c's compiled model: one compile
// for a test that judges runs and the model alone. Closing it leaves c
// open.
func For(c *validate.Checker) *Analyzer { return &Analyzer{c: &modelChecker{c}} }

// Checker is the analyzer's checker.
func (a *Analyzer) Checker() *validate.Checker { return a.c.Checker }

// Close closes the checker New made (For's is its caller's).
func (a *Analyzer) Close() error {
	if a.own {
		return a.c.Close()
	}
	return nil
}

// Graph explores the analyzer's binding's model with the Go evaluator, from init by
// its step, within cfg's bounds, and checks each invariant in every state
// it reaches. An error says why there is no graph: the binding has no
// evaluation module or no step action, computes constants from a run's
// steps with no value given, or names an invariant or terminal predicate
// quint refuses. A construct the evaluator does not take, met during the
// search, is no error: the result is cut "unsupported", naming it, and
// never complete.
func (a *Analyzer) Graph(cfg GraphSearch) (*GraphResult, error) { return a.c.Graph(cfg) }

// Possible searches the analyzer's binding's model for each of ps with the Go
// evaluator, from init by the model's step. An error says why there is
// no search: the model outside the evaluator's fragment, no step action,
// a constant computed from a run's steps with no value given, a possible
// that names no action or parameter of the model.
func (a *Analyzer) Possible(ps []binding.Possible, cfg PossibleSearch) ([]PossibleResult, error) {
	return a.c.Possible(ps, cfg)
}

// Simulate is the package's Simulate of the analyzer's binding, with its
// checker's compiled model.
func (a *Analyzer) Simulate(opt SimulateOptions) (*Report, error) { return a.c.Simulate(opt) }

// modelChecker is a validate.Checker with the model analyses as its methods.
type modelChecker struct{ *validate.Checker }

// newChecker is validate.NewChecker's checker with the analyses.
func newChecker(bindingPath string, opt validate.Options) (*modelChecker, error) {
	c, err := validate.NewChecker(bindingPath, opt)
	if err != nil {
		return nil, err
	}
	return &modelChecker{c}, nil
}

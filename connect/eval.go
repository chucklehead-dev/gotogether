package connect

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/itf"
)

// evaluator is a run's model for Step.Eval: compiled by quint and loaded
// by the Go evaluator the first time a driver asks, once per Run or Test,
// or handed the model Config.Require compiled. quint keeps only the
// definitions init, step and the names it is given use (compileModel), so
// a name the model lacks is compiled for once, with every name asked
// before it: a few compiles per run, each kept for every later step.
type evaluator struct {
	ctx     context.Context
	c       Config
	mu      sync.Mutex
	m       *qeval.Model
	err     error    // the last compile's failure, when m is nil
	keep    []string // the names m was compiled to keep
	missing map[string]bool
	// bound: a bound driver's binding was checked against the model
	// (checkBound), once per run
	bound bool
}

// model is the run's model with a definition name, compiled again for
// it when the one at hand lacks it. A model that has none of that name
// in quint's scope is returned as it is: Eval's lookup then says so.
func (e *evaluator) model(name string) (*qeval.Model, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.m == nil && e.err == nil {
		e.m, e.err = compileModel(e.ctx, e.c, e.keep)
	}
	if e.m == nil {
		return nil, e.err
	}
	if _, ok := e.m.Decl(name); ok || e.missing[name] {
		return e.m, nil
	}
	keep := append(slices.Clone(e.keep), name)
	m, err := compileModel(e.ctx, e.c, keep)
	if _, ok := errors.AsType[*notFound](err); ok {
		if e.missing == nil {
			e.missing = map[string]bool{}
		}
		e.missing[name] = true
		return e.m, nil
	}
	if err != nil {
		return nil, err
	}
	e.m, e.keep = m, keep
	return m, nil
}

// modelWith is the run's model with every one of names it has, in one
// compile when none is at hand yet (a bound driver's check, before the
// first step, asks for its Returns' defs so that their Evals compile
// nothing more). A name quint refuses is left out, and the compile tried
// again without it.
func (e *evaluator) modelWith(names []string) (*qeval.Model, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for {
		var want []string
		for _, n := range names {
			if e.missing[n] || slices.Contains(e.keep, n) {
				continue
			}
			if e.m != nil {
				if _, ok := e.m.Decl(n); ok {
					continue
				}
			}
			want = append(want, n)
		}
		if e.m != nil && len(want) == 0 {
			return e.m, nil
		}
		keep := append(slices.Clone(e.keep), want...)
		m, err := compileModel(e.ctx, e.c, keep)
		if nf, ok := errors.AsType[*notFound](err); ok && slices.Contains(want, nf.name) {
			if e.missing == nil {
				e.missing = map[string]bool{}
			}
			for _, n := range append([]string{nf.name}, nf.others...) {
				if slices.Contains(want, n) {
					e.missing[n] = true
				}
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		e.m, e.keep, e.err = m, keep, nil
	}
}

// Eval is the value of the model's definition name in the step's
// post-state (After, whole: Config.StatePath does not narrow it): a val,
// or a def applied to args, each a Go value as itf.Encode encodes it.
// A driver's projection can then compare with the model's own definition
// (s.Eval("balanceOf", "alice")) instead of a copy of it in Go.
//
// The Go evaluator computes it, on the model connect.Run or connect.Test
// compiled with quint once (the compile Config.Require makes, when it is
// set; else on the first Eval of the run). A step that Replay made
// without them has no model, and Eval says so. Constants are the
// instance's (Config.Main). A name, its arity and what the evaluator
// cannot compute are errors naming the step and the fix; prefer the
// typed Eval[T] / MustEval[T].
func (s *Step) Eval(name string, args ...any) (itf.Value, error) {
	v, _, err := s.evalValue(name, args...)
	if err != nil {
		return itf.Value{}, err
	}
	out, err := qeval.ToITF(v)
	if err != nil {
		return itf.Value{}, fmt.Errorf("connect: Eval(%q) at step %d (%s): %s's value %s has no ITF form (%v): name a definition whose value is data", name, s.Index, s.Action, name, qeval.String(v), err)
	}
	return out, nil
}

// evalValue is Eval's value as the evaluator has it, with the model it
// was computed on: an effects def's elements print as the evaluator
// prints values, for a comparison text for text (effects.go).
func (s *Step) evalValue(name string, args ...any) (qeval.Value, *qeval.Model, error) {
	fail := func(format string, a ...any) error {
		return fmt.Errorf("connect: Eval(%q) at step %d (%s): %s", name, s.Index, s.Action, fmt.Sprintf(format, a...))
	}
	if s.eval == nil {
		return nil, nil, fail("this step has no model to evaluate in: Replay compiles none; replay through connect.Run or connect.Test, which compile the model once for Eval")
	}
	m, err := s.eval.model(name)
	if err != nil {
		return nil, nil, fail("%v", err)
	}
	spec := s.eval.c.Spec
	d, ok := m.Decl(name)
	switch {
	case ok && d.Kind == "var":
		return nil, nil, fail("%s is a variable of %s, not a definition: read it with s.Get(%q)", name, spec, name)
	case !ok || d.Kind != "def":
		return nil, nil, fail("%s is no definition of %s: name a val or a def of the model", name, spec)
	}
	switch d.Qualifier {
	case "val", "def", "pureval", "puredef", "invariant", "observation": // the last two: quint 0.34's named vals
	default:
		return nil, nil, fail("%s is %s %s of %s, not a value: name a val or a def of the model", name, article(d.Qualifier), d.Qualifier, spec)
	}
	var params []string
	if d.Expr != nil && d.Expr.Kind == "lambda" {
		for _, p := range d.Expr.Params {
			params = append(params, m.Display(p.Name))
		}
	}
	if len(params) != len(args) {
		takes := "no arguments"
		if len(params) > 0 {
			takes = fmt.Sprintf("%d argument%s (%s)", len(params), plural(len(params)), strings.Join(params, ", "))
		}
		return nil, nil, fail("%s takes %s, not %d: pass one Go value per parameter", name, takes, len(args))
	}
	vals := make([]qeval.Value, len(args))
	for i, a := range args {
		x, err := itf.Encode(a)
		if err == nil {
			vals[i], err = qeval.FromITF(x)
		}
		if err != nil {
			return nil, nil, fail("argument %s (%T): %v: pass a value itf.Encode takes, as the parameter's Quint type", params[i], a, err)
		}
	}
	if s.state == nil || s.stateOf != m {
		st, err := qeval.StateOf(m, itf.State{Vars: s.After})
		if err != nil {
			return nil, nil, fail("the step's state is not %s's: %v: set Config.Main to the module the traces come from", spec, err)
		}
		s.state, s.stateOf = st, m
	}
	v, err := m.Eval(nil, s.state, name, vals...)
	if u, ok := errors.AsType[*qeval.Unsupported](err); ok {
		return nil, nil, fail("%s is outside the Go evaluator's fragment (%s: %s): compute this one in the driver", name, u.Op, u.Why)
	}
	if ee, ok := errors.AsType[*qeval.EvalError](err); ok {
		return nil, nil, fail("%s is undefined in this state (%s, the model's own error, as quint would report it): call it only where the model defines it", name, strings.TrimPrefix(ee.Error(), "qeval: "))
	}
	if err != nil {
		return nil, nil, fail("%v", err)
	}
	return v, m, nil
}

// Eval is Step.Eval decoded into T (see itf.Decode).
func Eval[T any](s *Step, name string, args ...any) (T, error) {
	var t T
	v, err := s.Eval(name, args...)
	if err != nil {
		return t, err
	}
	if err := itf.Decode(v, &t); err != nil {
		return t, fmt.Errorf("connect: Eval(%q) at step %d (%s): decoding %s into %T: %w: decode into a type of the definition's value", name, s.Index, s.Action, v, t, err)
	}
	return t, nil
}

// MustEval is Eval, panicking on error. Replay turns a handler's panic
// into a step failure, so handlers and CheckStep can use it freely.
func MustEval[T any](s *Step, name string, args ...any) T {
	t, err := Eval[T](s, name, args...)
	if err != nil {
		panic(err)
	}
	return t
}

func article(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}

// choose is a if cond, else b: a message's word that depends on a count.
func choose(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

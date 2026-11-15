package connect

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/itf"
)

// A Binding maps a model's actions onto an implementation's methods: each
// action to a method, each of its picks to a parameter by name, the
// model's state to a projection of the implementation (ROADMAP CW5). It
// replaces a Switch of handlers that each decode their picks by hand, and
// the compiler checks it: a renamed method or a changed parameter type is
// a build error or a binding error at its line, never a stale generated
// file. Bind's constructor makes the implementation at the model's init.
//
//	b := connect.Bind(func(testing.TB) *Bank { return NewBank(accounts) }).
//		Action("deposit", (*Bank).Deposit, "account", "amount").
//		Action("withdraw", (*Bank).Withdraw, "account", "amount").
//		State(func(b *Bank) any { return bankState{Balances: b.Balances()} })
//	connect.Run(t, cfg, b.Driver)
//
// A Binding is built once, before Run, and is not changed after: Driver
// makes each trace's driver from it.
type Binding[T any] struct {
	init    func(testing.TB) T
	actions map[string]*boundAction
	order   []string // the actions' names, in the order bound
	project func(T) any
	checks  []func(T, *Step) error
}

// boundAction is one action's method and the picks that are its
// parameters, with what its results are compared with.
type boundAction struct {
	name    string
	fn      reflect.Value
	ctx     bool     // the method's first parameter after the receiver is a context.Context
	params  []string // the picks, in parameter order
	types   []reflect.Type
	errOut  bool           // its last result is an error
	results []reflect.Type // its results besides the error
	handler func(T any, s *Step) error
	// returns, if set, is the model def its first result must equal,
	// evaluated with these picks as its arguments (Step.Eval)
	returns     string
	returnsArgs []string
}

var (
	errorType   = reflect.TypeFor[error]()
	contextType = reflect.TypeFor[context.Context]()
)

// Bind starts a Binding whose implementation newT makes at the model's
// init step, once per trace (tb is the trace's test).
func Bind[T any](newT func(tb testing.TB) T) *Binding[T] {
	if newT == nil {
		panic("connect.Bind: nil constructor: give the function that makes the implementation at init")
	}
	return &Binding[T]{init: newT, actions: map[string]*boundAction{}}
}

// Action binds the model's action name to method, a method expression of
// the implementation's type ((*Bank).Deposit) or any func taking it first.
// params name the step's picks that are the method's parameters, in its
// order: Go cannot read parameter names, so they are given, and their
// number must be the method's. A leading context.Context parameter is not
// named: it is given the trace's context. A pointer parameter is optional,
// nil when the model did not pick it. The method's last result, if an
// error, fails the step; other results are compared with a model def
// (Returns) or are an error at the binding, since a result left unchecked
// would read as a pass.
//
// A malformed binding (method of another type, a count of params not the
// method's, an action bound twice) panics here, naming the fix, as
// regexp.MustCompile does: it is the test's code, wrong before any trace
// runs.
func (b *Binding[T]) Action(name string, method any, params ...string) *Binding[T] {
	where := fmt.Sprintf("connect.Bind: action %s", name)
	if _, dup := b.actions[name]; dup || name == "init" {
		panic(fmt.Sprintf("%s: bound twice (init is Bind's constructor): bind each action once", where))
	}
	fn := reflect.ValueOf(method)
	ft := fn.Type()
	if fn.Kind() != reflect.Func || ft.NumIn() == 0 {
		panic(fmt.Sprintf("%s: %T is no func of the implementation: give a method expression, like (*Bank).Deposit", where, method))
	}
	var t T
	recv := reflect.TypeOf(&t).Elem()
	if !recv.AssignableTo(ft.In(0)) {
		panic(fmt.Sprintf("%s: %s takes %s first, not the implementation's %s: give a method of %s", where, ft, ft.In(0), recv, recv))
	}
	a := &boundAction{name: name, fn: fn, params: params}
	first := 1
	if ft.NumIn() > 1 && ft.In(1) == contextType {
		a.ctx, first = true, 2
	}
	if n := ft.NumIn() - first; n != len(params) {
		panic(fmt.Sprintf("%s: %s takes %d parameter%s besides the receiver%s, but %s named (%s): name one pick per parameter, in order",
			where, ft, n, plural(n), map[bool]string{true: " and a context", false: ""}[a.ctx], map[bool]string{true: "1 pick is", false: fmt.Sprintf("%d picks are", len(params))}[len(params) == 1], strings.Join(params, ", ")))
	}
	for i := first; i < ft.NumIn(); i++ {
		a.types = append(a.types, ft.In(i))
	}
	for i := range ft.NumOut() {
		if i == ft.NumOut()-1 && ft.Out(i) == errorType {
			a.errOut = true
			continue
		}
		a.results = append(a.results, ft.Out(i))
	}
	b.actions[name] = a
	b.order = append(b.order, name)
	return b
}

// Returns says what action name's first result must equal: the model's
// def evaluated in the step's state with the named picks as its arguments
// (Step.Eval, so in connect.Run or connect.Test), compared after decoding
// into the result's type. An action whose results Returns does not check
// is refused when its driver is made.
func (b *Binding[T]) Returns(name, def string, args ...string) *Binding[T] {
	a, ok := b.actions[name]
	if !ok {
		panic(fmt.Sprintf("connect.Bind: Returns(%q): no such action: bind it with Action first", name))
	}
	if len(a.results) != 1 {
		panic(fmt.Sprintf("connect.Bind: Returns(%q): its method returns %d result%s besides an error: Returns checks one", name, len(a.results), plural(len(a.results))))
	}
	a.returns, a.returnsArgs = def, args
	return b
}

// Handle binds action name to a handler of its own, for an action no
// method fits (one the implementation does in two calls, or a fault the
// test injects).
func (b *Binding[T]) Handle(name string, h func(impl T, s *Step) error) *Binding[T] {
	if _, dup := b.actions[name]; dup || name == "init" {
		panic(fmt.Sprintf("connect.Bind: action %s: bound twice (init is Bind's constructor): bind each action once", name))
	}
	b.actions[name] = &boundAction{name: name, handler: func(impl any, s *Step) error { return h(impl.(T), s) }}
	b.order = append(b.order, name)
	return b
}

// Skip binds actions the implementation does not have (another
// component's, a stutter): their steps do nothing.
func (b *Binding[T]) Skip(names ...string) *Binding[T] {
	for _, n := range names {
		b.Handle(n, func(T, *Step) error { return nil })
	}
	return b
}

// State is the implementation's state as the model's is compared with it
// after every step (StateDriver: the model's state decoded into project's
// result type, fields the type lacks not compared).
func (b *Binding[T]) State(project func(impl T) any) *Binding[T] {
	b.project = project
	return b
}

// Check adds a check after every step (StepChecker), with the
// implementation and the step: a comparison with the model's own def
// (Step.Eval) or one the state's projection cannot make.
func (b *Binding[T]) Check(check func(impl T, s *Step) error) *Binding[T] {
	b.checks = append(b.checks, check)
	return b
}

// Driver is the trace's driver, as connect.Run and connect.Test take it
// (b.Driver). It fails tb when an action's results are checked by
// nothing.
func (b *Binding[T]) Driver(tb testing.TB) Driver {
	tb.Helper()
	for _, n := range b.order {
		if a := b.actions[n]; a.handler == nil && len(a.results) > 0 && a.returns == "" {
			tb.Fatalf("connect.Bind: action %s: %s returns %s besides an error, and nothing checks it: compare it with a model def (Returns(%q, def, picks...)), or bind a func that drops it",
				n, a.fn.Type(), typesText(a.results), n)
		}
	}
	d := &boundDriver[T]{b: b, tb: tb}
	switch {
	case b.project != nil && len(b.checks) > 0:
		return boundStateChecker[T]{d}
	case b.project != nil:
		return boundState[T]{d}
	case len(b.checks) > 0:
		return boundChecker[T]{d}
	}
	return d
}

// boundDriver is one trace's driver: the implementation, made at init.
type boundDriver[T any] struct {
	b    *Binding[T]
	tb   testing.TB
	impl T
	made bool
}

func (d *boundDriver[T]) Step(s *Step) error {
	if s.Action == "init" {
		d.impl, d.made = d.b.init(d.tb), true
		return nil
	}
	a, ok := d.b.actions[s.Action]
	if !ok {
		return &UnimplementedError{Action: s.Action, Picks: s.pickNames()}
	}
	if !d.made {
		return fmt.Errorf("connect.Bind: step %d (%s) before init: the trace starts without the model's init", s.Index, s.Action)
	}
	if a.handler != nil {
		return a.handler(d.impl, s)
	}
	in := []reflect.Value{reflect.ValueOf(&d.impl).Elem()}
	if a.ctx {
		in = append(in, reflect.ValueOf(d.tb.Context()))
	}
	for i, p := range a.params {
		v, err := pickValue(s, p, a.types[i])
		if err != nil {
			return fmt.Errorf("connect.Bind: action %s, parameter %d (pick %s): %w", a.name, i+1, p, err)
		}
		in = append(in, v)
	}
	out := a.fn.Call(in)
	if a.errOut {
		if err, _ := out[len(out)-1].Interface().(error); err != nil {
			return err
		}
	}
	if a.returns != "" {
		return checkReturn(s, a, out[0])
	}
	return nil
}

// pickValue is the step's pick name decoded into t; a pointer type is an
// optional pick, nil when the model did not pick it.
func pickValue(s *Step, name string, t reflect.Type) (reflect.Value, error) {
	v, ok := s.Picks[name]
	if !ok {
		if t.Kind() == reflect.Pointer {
			return reflect.Zero(t), nil
		}
		return reflect.Value{}, fmt.Errorf("no such pick in %s (have %s): name the action's nondet picks, or make the parameter a pointer if the model may not pick it", s.Action, s.pickNames())
	}
	target := t
	if t.Kind() == reflect.Pointer {
		target = t.Elem()
	}
	p := reflect.New(target)
	if err := itf.Decode(v, p.Interface()); err != nil {
		return reflect.Value{}, fmt.Errorf("decoding %s into %s: %w", v, target, err)
	}
	if t.Kind() == reflect.Pointer {
		return p, nil
	}
	return p.Elem(), nil
}

// checkReturn compares an action's result with its model def.
func checkReturn(s *Step, a *boundAction, got reflect.Value) error {
	var args []any
	for _, n := range a.returnsArgs {
		v, ok := s.Picks[n]
		if !ok {
			return fmt.Errorf("connect.Bind: action %s: Returns' argument %s is no pick of the step (have %s)", a.name, n, s.pickNames())
		}
		args = append(args, v)
	}
	mv, err := s.Eval(a.returns, args...)
	if err != nil {
		return err
	}
	want := reflect.New(got.Type())
	if err := itf.Decode(mv, want.Interface()); err != nil {
		return fmt.Errorf("connect.Bind: action %s: the model's %s is %s, which does not decode into %s: %w", a.name, a.returns, mv, got.Type(), err)
	}
	if !reflect.DeepEqual(got.Interface(), want.Elem().Interface()) {
		return fmt.Errorf("connect.Bind: action %s returned %v, the model's %s = %v", a.name, got.Interface(), callText(a.returns, a.returnsArgs, s), want.Elem().Interface())
	}
	return nil
}

func callText(def string, args []string, s *Step) string {
	if len(args) == 0 {
		return def
	}
	var parts []string
	for _, n := range args {
		parts = append(parts, s.Picks[n].String())
	}
	return def + "(" + strings.Join(parts, ", ") + ")"
}

func typesText(ts []reflect.Type) string {
	var s []string
	for _, t := range ts {
		s = append(s, t.String())
	}
	return strings.Join(s, ", ")
}

func (d *boundDriver[T]) state() (any, error) {
	if !d.made {
		return nil, fmt.Errorf("connect.Bind: no implementation before init")
	}
	return d.b.project(d.impl), nil
}

func (d *boundDriver[T]) check(s *Step) error {
	if !d.made {
		return nil
	}
	for _, c := range d.b.checks {
		if err := c(d.impl, s); err != nil {
			return err
		}
	}
	return nil
}

// The driver's shapes: a StateDriver only when State is given and a
// StepChecker only when Check is, since Replay compares a state a driver
// has and checks what it checks.
type boundState[T any] struct{ *boundDriver[T] }

func (d boundState[T]) State() (any, error) { return d.state() }

type boundChecker[T any] struct{ *boundDriver[T] }

func (d boundChecker[T]) CheckStep(s *Step) error { return d.check(s) }

type boundStateChecker[T any] struct{ *boundDriver[T] }

func (d boundStateChecker[T]) State() (any, error)     { return d.state() }
func (d boundStateChecker[T]) CheckStep(s *Step) error { return d.check(s) }

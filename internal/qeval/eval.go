package qeval

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Unsupported is what the evaluator cannot evaluate: an operator outside
// its fragment, an action with more than one successor, an integer past
// 64 bits, a set too large to build. The caller asks quint instead.
type Unsupported struct {
	Op, Why string
	// Choice names the choice the refusal is about when quint's replay
	// samples it (a nondet, an any {} of enabled branches): its verdict at
	// that step is one draw of quint's, not the model's
	Choice string
}

func (u *Unsupported) Error() string {
	return fmt.Sprintf("qeval: %s is outside the Go evaluator's fragment: %s", u.Op, u.Why)
}

// An EvalError is the model's own runtime error (a map's missing key, a
// list's index out of range, a division by zero), as quint would report
// it.
type EvalError struct{ Msg string }

func (e *EvalError) Error() string { return "qeval: " + e.Msg }

func unsupported(op, why string, a ...any) error {
	return &Unsupported{Op: op, Why: fmt.Sprintf(why, a...)}
}

func evalErr(format string, a ...any) error { return &EvalError{fmt.Sprintf(format, a...)} }

// internal is the evaluator's own failure (a value of a kind a
// typechecked model cannot give, a name it did not bind): Unsupported, so
// that quint, not the evaluator, says what the model does.
func internal(format string, a ...any) error {
	return &Unsupported{Op: "evaluation", Why: fmt.Sprintf(format, a...)}
}

// maxSet bounds a set the evaluator builds (a range, a powerset, a
// product): past it, quint.
const maxSet = 1 << 16

// A State is the model's state variables' values, by name.
type State map[string]Value

// env binds parameters and let names to values, innermost first.
type env struct {
	name  string
	val   Value
	outer *env
	// err is a let's failure, thrown where its name is read (bindLet)
	err error
}

func (e *env) lookup(name string) (Value, bool) {
	for ; e != nil; e = e.outer {
		if e.name == name {
			if e.err != nil {
				throw(e.err)
			}
			return e.val, true
		}
	}
	return nil, false
}

func (e *env) bind(name string, v Value) *env { return &env{name: name, val: v, outer: e} }

// An evaluation: the model, its constants, the state it reads (nil for
// init), and the values of its vals, cached per state.
type evaluation struct {
	m      *Model
	consts map[string]Value
	state  State
	cache  map[int64]Value
	depth  int
	// labels: each successor carries what led to it (Transitions)
	labels bool
	// acts are the successors of each action call made so far (its
	// declaration and arguments), in this state: an action's successors
	// depend on nothing else (its body binds only its parameters), and a
	// step's choices call the same branch with the same arguments for
	// every value of the choices it does not take (kvstore's write(key,
	// value) once for each expected revision)
	acts map[string][]succ
	// fp, if set, records the state entries read and written (Tracking),
	// tainted the maps of the state's variables (and those made from them
	// by put, set, setBy) by variable, conj whether the expression about
	// to be evaluated is at a conjunctive position of a judged val
	// (Footprint.Judging), cur the conj of the node name, app or builtin
	// evaluates, and privileged the vals whose value took a forall over a
	// state map's keys as each entry's (cached: a use of one elsewhere is
	// whole)
	fp         *Footprint
	tainted    map[*Map]string
	conj, cur  bool
	privileged map[int64]bool
	perEntry   int // the foralls taken as each entry's so far
}

func (m *Model) evaluation(consts map[string]Value, s State) *evaluation {
	ev := &evaluation{m: m, consts: consts, state: s, cache: map[int64]Value{}, fp: m.fp}
	if ev.fp != nil && s != nil {
		ev.conj = ev.fp.Judging
	}
	return ev
}

// Eval is the value of the definition name (a val, or a def applied to
// args) in state s, with the instance's constants.
func (m *Model) Eval(consts map[string]Value, s State, name string, args ...Value) (v Value, err error) {
	defer recoverInto(&err)
	d, ok := m.Decl(name)
	if !ok {
		return nil, fmt.Errorf("qeval: no definition %s", name)
	}
	ev := m.evaluation(consts, s)
	return ev.apply(d, args, nil, name)
}

// Successors are every distinct state action name, applied to args, may
// step to from s (none: disabled), every variable assigned.
func (m *Model) Successors(consts map[string]Value, s State, name string, args ...Value) (out []State, err error) {
	defer recoverInto(&err)
	d, ok := m.Decl(name)
	if !ok {
		return nil, fmt.Errorf("qeval: no action %s", name)
	}
	ev := m.evaluation(consts, s)
	body, e, err := ev.bindArgs(d, args, nil, name)
	if err != nil {
		return nil, err
	}
	succs, err := ev.action(body, e)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, sc := range succs {
		a := sc.a
		st := State{}
		for _, v := range m.Vars {
			x, ok := a[v]
			if !ok {
				return nil, unsupported(name, "leaves %s unassigned", v)
			}
			st[v] = x
		}
		if len(succs) == 1 {
			// one successor, nothing to deduplicate: its key would write
			// the whole state, a cost linear in it at every step (I19)
			out = append(out, st)
			continue
		}
		if k := m.Key(st); !seen[k] {
			seen[k] = true
			out = append(out, st)
		}
	}
	return out, nil
}

// Key is a state as a string, equal exactly when the states are: its
// variables' values in the order of Vars.
func (m *Model) Key(s State) string {
	var b strings.Builder
	for _, v := range m.Vars {
		if x, ok := s[v]; ok {
			write(&b, x)
		}
		b.WriteByte(';')
	}
	return b.String()
}

// Step is what action name, applied to args, does in state s: whether it
// is enabled, and its successor, every variable assigned. More than one
// distinct successor (an action that chooses) is Unsupported: a replay
// takes one, and quint's choice is its own.
func (m *Model) Step(consts map[string]Value, s State, name string, args ...Value) (next State, enabled bool, err error) {
	out, err := m.Successors(consts, s, name, args...)
	if err != nil {
		return nil, false, err
	}
	switch len(out) {
	case 0:
		return nil, false, nil
	case 1:
		return out[0], true, nil
	}
	return nil, false, &Unsupported{Op: name, Why: fmt.Sprintf("has %d successors (it chooses): quint takes one", len(out)), Choice: m.choiceOf(consts, s, name, args)}
}

// choiceOf names what action name chooses in s: its nondets ("nondet v"),
// else its any {} ("any {} of 3 enabled branches").
func (m *Model) choiceOf(consts map[string]Value, s State, name string, args []Value) string {
	ts, err := m.Transitions(consts, s, name, args...)
	if err != nil {
		return "a choice"
	}
	var picks []string
	for _, t := range ts {
		for _, p := range t.Picks() {
			if !slices.Contains(picks, p.Pick) {
				picks = append(picks, p.Pick)
			}
		}
	}
	if len(picks) == 0 {
		return fmt.Sprintf("any {} of %d enabled branches", len(ts))
	}
	return "nondet " + strings.Join(picks, ", nondet ")
}

func sameState(a, b State) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok || Compare(v, w) != 0 {
			return false
		}
	}
	return true
}

// thrown carries an error out of the evaluation's recursion.
type thrown struct{ err error }

func throw(err error) { panic(thrown{err}) }

func recoverInto(err *error) {
	if r := recover(); r != nil {
		t, ok := r.(thrown)
		if !ok {
			// the evaluator's own failure (a value of a kind it did not
			// expect): never a verdict, quint's to give
			*err = &Unsupported{Op: "evaluation", Why: fmt.Sprint(r)}
			return
		}
		*err = t.err
	}
}

// bindArgs is a definition's body with its parameters bound to args.
func (ev *evaluation) bindArgs(d *Decl, args []Value, e *env, name string) (*Expr, *env, error) {
	body := d.Expr
	if body == nil {
		return nil, nil, fmt.Errorf("qeval: %s has no body", name)
	}
	if body.Kind == "lambda" {
		if len(body.Params) != len(args) {
			return nil, nil, fmt.Errorf("qeval: %s takes %d arguments, not %d", name, len(body.Params), len(args))
		}
		for i, p := range body.Params {
			e = e.bind(p.Name, args[i])
		}
		return body.Expr, e, nil
	}
	if len(args) > 0 {
		return nil, nil, fmt.Errorf("qeval: %s takes no arguments, not %d", name, len(args))
	}
	return body, e, nil
}

// apply is a definition's value applied to args.
func (ev *evaluation) apply(d *Decl, args []Value, e *env, name string) (Value, error) {
	body, e2, err := ev.bindArgs(d, args, nil, name)
	if err != nil {
		return nil, err
	}
	if d.Qualifier == "action" {
		return nil, unsupported(name, "an action evaluated as a value")
	}
	return ev.eval(body, e2), nil
}

// ---- actions ------------------------------------------------------------

// an assignment: the variables an action assigns, by name
type assignment map[string]Value

// a succ is one successor of an action: its assignment and, in a
// labelled evaluation (Transitions), what led to it: the actions called
// and the choices made, in order.
type succ struct {
	a   assignment
	lab *events
}

// events are a successor's labels as a list, first event first: a
// choice puts its event before each of its successors' in one small
// allocation, where a slice would copy them all at each choice around
// them.
type events struct {
	ev   Event
	next *events
}

// then is x's events followed by y's (y's shared).
func (x *events) then(y *events) *events {
	if x == nil {
		return y
	}
	return &events{x.ev, x.next.then(y)}
}

// slice is the events in order.
func (x *events) slice() []Event {
	var out []Event
	for ; x != nil; x = x.next {
		out = append(out, x.ev)
	}
	return out
}

// action is what e does as an action: its successors (none: disabled). A
// Boolean expression is an action that assigns nothing.
func (ev *evaluation) action(e *Expr, en *env) ([]succ, error) {
	ev.depth++
	defer func() { ev.depth-- }()
	if ev.depth > 10000 {
		return nil, unsupported("recursion", "deeper than 10000")
	}
	switch e.Kind {
	case "app":
		switch e.Opcode {
		case "assign":
			v := ev.varName(e.Args[0])
			if ev.fp != nil {
				return []succ{{a: assignment{v: ev.assigned(v, e.Args[1], en)}}}, nil
			}
			return []succ{{a: assignment{v: ev.eval(e.Args[1], en)}}}, nil
		case "actionAll":
			out := []succ{{a: assignment{}}}
			for _, a := range e.Args {
				next, err := ev.action(a, en)
				if err != nil {
					return nil, err
				}
				if len(next) == 0 {
					return nil, nil
				}
				var prod []succ
				for _, x := range out {
					for _, y := range next {
						z, err := merge(x.a, y.a)
						if err != nil {
							return nil, err
						}
						var lab *events
						if ev.labels {
							lab = x.lab.then(y.lab)
						}
						prod = append(prod, succ{z, lab})
					}
				}
				out = prod
				if len(out) > maxSet {
					return nil, unsupported("actionAll", "more than %d successors", maxSet)
				}
			}
			return out, nil
		case "actionAny":
			var out []succ
			for _, a := range e.Args {
				next, err := ev.action(a, en)
				if err != nil {
					return nil, err
				}
				out = append(out, next...)
			}
			return out, nil
		case "ite":
			if ev.boolOf(e.Args[0], en) {
				return ev.action(e.Args[1], en)
			}
			return ev.action(e.Args[2], en)
		}
		if d, args, ok := ev.calledAction(e, en); ok {
			return ev.callAction(d, args, true)
		}
	case "name":
		if d, _, ok := ev.calledAction(e, en); ok {
			return ev.callAction(d, nil, false)
		}
	case "let":
		d := e.Opdef
		if d.Qualifier == "nondet" {
			// a choice: each value of the set, its successors
			s, err := ev.choiceSet(d, en)
			if err != nil {
				return nil, err
			}
			var out []succ
			disabled := 0 // values with no successor
			for _, x := range s.Elems {
				next, err := ev.action(e.Expr, en.bind(d.Name, x))
				if err != nil {
					return nil, err
				}
				if len(next) == 0 {
					disabled++
				}
				out = append(out, ev.label(next, Event{Pick: ev.m.Display(d.Name), Value: x})...)
				if len(out) > maxSet {
					return nil, unsupported("nondet", "more than %d successors", maxSet)
				}
			}
			if ev.m.replaying && disabled > 0 && len(out) > 0 {
				return nil, &Unsupported{Op: "nondet", Why: fmt.Sprintf("%d of the %d values of %s disable the action: quint's replay picks one at random", disabled, len(s.Elems), d.Name),
					Choice: "nondet " + ev.m.Display(d.Name)}
			}
			return out, nil
		}
		return ev.action(e.Expr, ev.bindLet(d, en))
	}
	if ev.boolOf(e, en) {
		return []succ{{a: assignment{}}}, nil
	}
	return nil, nil
}

// choiceSet is the set a nondet chooses from.
func (ev *evaluation) choiceSet(d *Decl, en *env) (*Set, error) {
	if d.Expr.Kind != "app" || d.Expr.Opcode != "oneOf" {
		return nil, unsupported("nondet", "%s is no oneOf", d.Name)
	}
	return ev.setOf(ev.eval(d.Expr.Args[0], en), "oneOf"), nil
}

// calledAction is the action definition e applies (or names), with its
// arguments' values, if e is a call of one.
func (ev *evaluation) calledAction(e *Expr, en *env) (*Decl, []Value, bool) {
	r, ok := ev.m.refs[e.ID]
	if !ok || r.kind != "def" {
		return nil, nil, false
	}
	d := ev.m.byID[r.decl]
	if d == nil || d.Qualifier != "action" {
		return nil, nil, false
	}
	if e.Kind == "name" {
		return d, nil, true
	}
	args := make([]Value, len(e.Args))
	for i, a := range e.Args {
		args[i] = ev.eval(a, en)
	}
	return d, args, true
}

// callAction is the successors of action d applied to args (an
// application, with its parameters' names, or the action named),
// labelled with the call, each call's computed once per evaluation
// (acts). The result is the caller's to change: label rewrites its
// elements.
func (ev *evaluation) callAction(d *Decl, args []Value, applied bool) ([]succ, error) {
	var b strings.Builder
	b.WriteString(strconv.FormatInt(d.ID, 10))
	if applied {
		b.WriteByte('(')
	}
	memo := true
	for _, a := range args {
		if _, ok := a.(*Lambda); ok {
			// an operator argument (Choreo's step_with) is its closure, which
			// no key writes: such a call is not remembered
			memo = false
			break
		}
		b.WriteByte(',')
		write(&b, a)
	}
	k := b.String()
	if out, ok := ev.acts[k]; ok && memo {
		return slices.Clone(out), nil
	}
	body, e2, err := ev.bindArgs(d, args, nil, d.Name)
	if err != nil {
		return nil, err
	}
	out, err := ev.action(body, e2)
	if err != nil {
		return nil, err
	}
	var params []string
	if applied {
		params = ev.m.paramsOf(d)
	}
	out = ev.label(out, Event{Action: d.Name, Params: params, Args: args})
	if !memo {
		return out, nil
	}
	if ev.acts == nil {
		ev.acts = map[string][]succ{}
	}
	ev.acts[k] = slices.Clone(out)
	return out, nil
}

// label puts what led to out (a call, a pick) before each successor's
// own labels, in a labelled evaluation.
func (ev *evaluation) label(out []succ, l Event) []succ {
	if !ev.labels {
		return out
	}
	for i := range out {
		out[i].lab = &events{l, out[i].lab}
	}
	return out
}

func merge(a, b assignment) (assignment, error) {
	out := make(assignment, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if _, ok := out[k]; ok {
			return nil, unsupported("assign", "%s assigned twice", k)
		}
		out[k] = v
	}
	return out, nil
}

// varName is the state variable an assignment's target names.
func (ev *evaluation) varName(e *Expr) string {
	if e.Kind == "name" {
		if r, ok := ev.m.refs[e.ID]; ok && r.kind == "var" {
			if d := ev.m.byID[r.decl]; d != nil {
				return d.Name
			}
		}
		if d, ok := ev.m.Decl(e.Name); ok && d.Kind == "var" {
			return d.Name
		}
	}
	throw(unsupported("assign", "to %s, no state variable", e.Name))
	return ""
}

// bindLet binds a let's definition: a value now, or an operator. A
// value whose evaluation fails is the failure where the name is read,
// not here: quint evaluates a let only when its name is read (`val n =
// 1 / 0; 0` is 0 in both backends), and the evaluator, binding eagerly,
// threw a division by zero quint never makes (found by the differential
// test, differential_test.go).
func (ev *evaluation) bindLet(d *Decl, en *env) *env {
	if d.Expr.Kind == "lambda" {
		return en.bind(d.Name, &Lambda{params: paramNames(d.Expr), body: d.Expr.Expr, env: en}) // it closes over the lets and parameters around it (quint has no recursion)
	}
	v, err := ev.evalOrErr(d.Expr, en)
	if err != nil {
		return &env{name: d.Name, outer: en, err: err}
	}
	return en.bind(d.Name, v)
}

// callOrErr is f applied to args, or what the application threw.
func (ev *evaluation) callOrErr(f Value, args ...Value) (v Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			t, ok := r.(thrown)
			if !ok {
				panic(r)
			}
			v, err = nil, t.err
		}
	}()
	return ev.call(f, args...), nil
}

// evalOrErr is e's value, or what its evaluation threw (the model's
// runtime error, a refusal).
func (ev *evaluation) evalOrErr(e *Expr, en *env) (v Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			t, ok := r.(thrown)
			if !ok {
				panic(r)
			}
			v, err = nil, t.err
		}
	}()
	return ev.eval(e, en), nil
}

func paramNames(l *Expr) []string {
	out := make([]string, len(l.Params))
	for i, p := range l.Params {
		out[i] = p.Name
	}
	return out
}

// ---- expressions --------------------------------------------------------

func (ev *evaluation) boolOf(e *Expr, en *env) bool {
	v := ev.eval(e, en)
	b, ok := v.(Bool)
	if !ok {
		throw(internal("expected a Boolean, got %s", String(v)))
	}
	return bool(b)
}

func (ev *evaluation) intOf(v Value, op string) int64 {
	i, ok := v.(Int)
	if !ok {
		throw(internal("%s: expected an integer, got %s", op, String(v)))
	}
	return int64(i)
}

func (ev *evaluation) setOf(v Value, op string) *Set {
	switch s := v.(type) {
	case *Set:
		return s
	case infinite:
		throw(unsupported(op, "of an infinite set"))
	}
	throw(internal("%s: expected a set, got %s", op, String(v)))
	return nil
}

func (ev *evaluation) mapOf(v Value, op string) *Map {
	m, ok := v.(*Map)
	if !ok {
		throw(internal("%s: expected a map, got %s", op, String(v)))
	}
	return m
}

func (ev *evaluation) seqOf(v Value, op string) []Value {
	switch x := v.(type) {
	case List:
		return x
	case Tup:
		return x
	}
	throw(internal("%s: expected a list, got %s", op, String(v)))
	return nil
}

// call applies an operator value to args.
func (ev *evaluation) call(f Value, args ...Value) Value {
	l, ok := f.(*Lambda)
	if !ok {
		throw(internal("expected an operator, got %s", String(f)))
	}
	if len(l.params) != len(args) {
		throw(internal("an operator of %d parameters applied to %d", len(l.params), len(args)))
	}
	en := l.env
	for i, p := range l.params {
		en = en.bind(p, args[i])
	}
	return ev.eval(l.body, en)
}

func (ev *evaluation) eval(e *Expr, en *env) Value {
	ev.depth++
	defer func() { ev.depth-- }()
	if ev.depth > 10000 {
		throw(unsupported("recursion", "deeper than 10000"))
	}
	// a conjunctive position is the node's alone: its children are not,
	// but where it says so (and's, a let's body, a val's or def's)
	conj := ev.conj
	ev.conj = false
	switch e.Kind {
	case "int":
		n, err := strconv.ParseInt(e.Value.String(), 10, 64)
		if err != nil {
			throw(unsupported("int", "the literal %s past 64 bits", e.Value))
		}
		return Int(n)
	case "bool":
		return Bool(e.Bool)
	case "str":
		return Str(e.Value.String())
	case "name":
		ev.cur = conj
		return ev.name(e, en)
	case "lambda":
		return &Lambda{params: paramNames(e), body: e.Expr, env: en}
	case "let":
		if e.Opdef.Qualifier == "nondet" {
			throw(unsupported("nondet", "a choice outside an action"))
		}
		en = ev.bindLet(e.Opdef, en)
		ev.conj = conj
		return ev.eval(e.Expr, en)
	case "app":
		ev.cur = conj
		return ev.app(e, en)
	}
	throw(unsupported(e.Kind, "an expression of this kind"))
	return nil
}

func (ev *evaluation) name(e *Expr, en *env) Value {
	if v, ok := en.lookup(e.Name); ok {
		return v
	}
	r, ok := ev.m.refs[e.ID]
	if !ok {
		switch e.Name {
		case "Int":
			return infinite{}
		case "Nat":
			return infinite{nat: true}
		case "Bool":
			return &Set{[]Value{Bool(false), Bool(true)}}
		}
		throw(unsupported(e.Name, "a name the IR does not resolve"))
	}
	switch r.kind {
	case "param":
		if v, ok := en.lookup(r.param); ok {
			return v
		}
		throw(internal("parameter %s unbound", r.param))
	case "var":
		d := ev.m.byID[r.decl]
		if ev.state == nil {
			throw(internal("%s read before init", d.Name))
		}
		v, ok := ev.state[d.Name]
		if !ok {
			throw(internal("%s has no value", d.Name))
		}
		if ev.fp != nil {
			if m, ok := v.(*Map); ok {
				ev.taint(m, d.Name) // its entries recorded as they are read
			} else {
				ev.whole("reads %s", d.Name)
			}
		}
		return v
	case "const":
		d := ev.m.byID[r.decl]
		if v, ok := ev.consts[d.Name]; ok {
			if ev.fp != nil {
				if ev.fp.Consts == nil {
					ev.fp.Consts = map[string]bool{}
				}
				ev.fp.Consts[d.Name] = true
			}
			return v
		}
		throw(internal("constant %s has no value: give the instance's", d.Name))
	case "def":
		d := ev.m.byID[r.decl]
		if d == nil {
			throw(unsupported(e.Name, "a definition not in the IR"))
		}
		if d.Expr.Kind == "lambda" {
			return &Lambda{params: paramNames(d.Expr), body: d.Expr.Expr}
		}
		if d.Qualifier == "action" {
			throw(unsupported(e.Name, "an action as a value"))
		}
		conj := ev.cur
		if v, ok := ev.cache[d.ID]; ok {
			if ev.fp != nil && !conj && ev.privileged[d.ID] {
				ev.whole("%s, a forall over a state map's keys, used where it is not each entry's judgment", ev.m.Display(d.Name))
			}
			return v
		}
		before := ev.perEntry
		ev.conj = conj
		v := ev.eval(d.Expr, nil)
		ev.cache[d.ID] = v
		if ev.perEntry > before {
			if ev.privileged == nil {
				ev.privileged = map[int64]bool{}
			}
			ev.privileged[d.ID] = true
		}
		return v
	}
	throw(unsupported(e.Name, "a name of kind %s", r.kind))
	return nil
}

func add(a, b int64) int64 {
	c := a + b
	if (c > a) != (b > 0) {
		throw(unsupported("iadd", "%d + %d past 64 bits", a, b))
	}
	return c
}

func mul(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	c := a * b
	if c/b != a || (a == -1 && b == math.MinInt64) || (b == -1 && a == math.MinInt64) {
		throw(unsupported("imul", "%d * %d past 64 bits", a, b))
	}
	return c
}

// app is an operator applied: a definition of the model's, or a builtin.
func (ev *evaluation) app(e *Expr, en *env) Value {
	conj := ev.cur
	if f, ok := en.lookup(e.Opcode); ok { // a let-bound operator, or one passed as a parameter
		args := make([]Value, len(e.Args))
		for i, a := range e.Args {
			args[i] = ev.eval(a, en)
		}
		return ev.call(f, args...)
	}
	if r, ok := ev.m.refs[e.ID]; ok && (r.kind == "def" || r.kind == "param") {
		args := make([]Value, len(e.Args))
		for i, a := range e.Args {
			args[i] = ev.eval(a, en)
		}
		if r.kind == "param" {
			f, ok := en.lookup(r.param)
			if !ok {
				throw(internal("parameter %s unbound", r.param))
			}
			return ev.call(f, args...)
		}
		d := ev.m.byID[r.decl]
		if d == nil {
			throw(unsupported(e.Opcode, "a definition not in the IR"))
		}
		ev.conj = conj // a def's body is where its application is
		v, err := ev.apply(d, args, nil, d.Name)
		ev.conj = false
		if err != nil {
			throw(err)
		}
		return v
	}
	ev.cur = conj
	return ev.builtin(e, en)
}

func (ev *evaluation) builtin(e *Expr, en *env) Value {
	op := e.Opcode
	if ev.fp != nil {
		if v, ok := ev.tracked(e, en, ev.cur); ok {
			return v
		}
	}
	arg := func(i int) Value { return ev.eval(e.Args[i], en) }
	if ev.fp != nil && op != "ite" {
		// a state map passed whole to any other builtin is read whole
		arg = func(i int) Value {
			v := ev.eval(e.Args[i], en)
			if name := ev.taintOf(v); name != "" {
				ev.whole("%s read whole (%s)", name, op)
			}
			return v
		}
	}
	all := func() []Value {
		out := make([]Value, len(e.Args))
		for i := range e.Args {
			out[i] = arg(i)
		}
		return out
	}
	switch op {
	// Booleans, short-circuit
	case "and":
		for _, a := range e.Args {
			if !ev.boolOf(a, en) {
				return Bool(false)
			}
		}
		return Bool(true)
	case "or":
		for _, a := range e.Args {
			if ev.boolOf(a, en) {
				return Bool(true)
			}
		}
		return Bool(false)
	case "not":
		return Bool(!ev.boolOf(e.Args[0], en))
	case "implies":
		return Bool(!ev.boolOf(e.Args[0], en) || ev.boolOf(e.Args[1], en))
	case "iff":
		return Bool(ev.boolOf(e.Args[0], en) == ev.boolOf(e.Args[1], en))
	case "eq":
		return Bool(Compare(arg(0), arg(1)) == 0)
	case "neq":
		return Bool(Compare(arg(0), arg(1)) != 0)
	case "ite":
		if ev.boolOf(e.Args[0], en) {
			return arg(1)
		}
		return arg(2)
	case "assert":
		if !ev.boolOf(e.Args[0], en) {
			throw(evalErr("assertion failed"))
		}
		return Bool(true)

	// integers
	case "iadd":
		return Int(add(ev.intOf(arg(0), op), ev.intOf(arg(1), op)))
	case "isub":
		b := ev.intOf(arg(1), op)
		if b == math.MinInt64 {
			throw(unsupported(op, "past 64 bits"))
		}
		return Int(add(ev.intOf(arg(0), op), -b))
	case "imul":
		return Int(mul(ev.intOf(arg(0), op), ev.intOf(arg(1), op)))
	case "idiv", "imod":
		a, b := ev.intOf(arg(0), op), ev.intOf(arg(1), op)
		if b == 0 {
			throw(evalErr("division by zero"))
		}
		if a == math.MinInt64 && b == -1 {
			throw(unsupported(op, "past 64 bits"))
		}
		// as quint's backends do: truncated, the remainder with the
		// dividend's sign (-7 % 2 is -1), Go's / and %
		if op == "idiv" {
			return Int(a / b)
		}
		return Int(a % b)
	case "ipow":
		a, b := ev.intOf(arg(0), op), ev.intOf(arg(1), op)
		if b < 0 || a == 0 && b == 0 {
			throw(evalErr("%d^%d is undefined", a, b))
		}
		r, x := int64(1), a // by squaring, overflow checked
		for ; b > 0; b >>= 1 {
			if b&1 == 1 {
				r = mul(r, x)
			}
			if b > 1 {
				x = mul(x, x)
			}
		}
		return Int(r)
	case "iuminus":
		a := ev.intOf(arg(0), op)
		if a == math.MinInt64 {
			throw(unsupported(op, "past 64 bits"))
		}
		return Int(-a)
	case "igt":
		return Bool(ev.intOf(arg(0), op) > ev.intOf(arg(1), op))
	case "igte":
		return Bool(ev.intOf(arg(0), op) >= ev.intOf(arg(1), op))
	case "ilt":
		return Bool(ev.intOf(arg(0), op) < ev.intOf(arg(1), op))
	case "ilte":
		return Bool(ev.intOf(arg(0), op) <= ev.intOf(arg(1), op))

	// sets
	case "Set":
		return NewSet(all())
	case "to":
		lo, hi := ev.intOf(arg(0), op), ev.intOf(arg(1), op)
		if hi > lo && (hi-lo >= maxSet || hi-lo < 0) {
			throw(unsupported(op, "%d..%d, more than %d values", lo, hi, maxSet))
		}
		var out []Value
		for i := lo; i <= hi; i++ {
			out = append(out, Int(i))
		}
		return &Set{out}
	case "contains", "in":
		s, x := arg(0), arg(1)
		if op == "in" {
			s, x = x, s
		}
		if inf, ok := s.(infinite); ok {
			i, isInt := x.(Int)
			return Bool(isInt && (!inf.nat || i >= 0))
		}
		return Bool(ev.setOf(s, op).has(x))
	case "union":
		return NewSet(append(slices.Clone(ev.setOf(arg(0), op).Elems), ev.setOf(arg(1), op).Elems...))
	case "intersect":
		a, b := ev.setOf(arg(0), op), ev.setOf(arg(1), op)
		var out []Value
		for _, x := range a.Elems {
			if b.has(x) {
				out = append(out, x)
			}
		}
		return &Set{out}
	case "exclude":
		a, b := ev.setOf(arg(0), op), ev.setOf(arg(1), op)
		var out []Value
		for _, x := range a.Elems {
			if !b.has(x) {
				out = append(out, x)
			}
		}
		return &Set{out}
	case "subseteq":
		a, b := ev.setOf(arg(0), op), ev.setOf(arg(1), op)
		for _, x := range a.Elems {
			if !b.has(x) {
				return Bool(false)
			}
		}
		return Bool(true)
	case "size":
		return Int(len(ev.setOf(arg(0), op).Elems))
	case "isFinite":
		_, ok := arg(0).(*Set)
		return Bool(ok)
	case "filter", "select":
		if l, ok := arg(0).(List); ok {
			f := arg(1)
			var out List
			for _, x := range l {
				if ev.truth(ev.call(f, x)) {
					out = append(out, x)
				}
			}
			return out
		}
		s, f := ev.setOf(arg(0), op), arg(1)
		var out []Value
		for _, x := range s.Elems {
			if ev.truth(ev.call(f, x)) {
				out = append(out, x)
			}
		}
		return &Set{out}
	case "map":
		s, f := ev.setOf(arg(0), op), arg(1)
		out := make([]Value, len(s.Elems))
		for i, x := range s.Elems {
			out[i] = ev.call(f, x)
		}
		return NewSet(out)
	case "exists", "forall":
		// every element, not to the first witness: quint's TypeScript
		// backend evaluates each (Set(5, 33).forall(x => x == 33 implies
		// 1 / 0 > 0) is a division by zero), its Rust backend stops at a
		// witness in an order of its own (and gives false); the evaluator
		// gave false. An element's failure is the model's runtime error
		// only when no element decides (both backends fail); otherwise
		// the backends disagree, and the evaluator refuses.
		s, f := ev.setOf(arg(0), op), arg(1)
		witness := op == "exists" // the value that decides
		decided := false
		var failure *EvalError
		for _, x := range s.Elems {
			v, err := ev.callOrErr(f, x)
			var ee *EvalError
			switch {
			case errors.As(err, &ee):
				if failure == nil {
					failure = ee
				}
			case err != nil:
				throw(err)
			case ev.truth(v) == witness:
				decided = true
			}
		}
		if failure != nil {
			if decided {
				throw(unsupported(op, "an element fails (%s) where another decides it: quint's TypeScript backend evaluates every element, its Rust backend stops at a witness in an order of its own", failure.Msg))
			}
			throw(failure)
		}
		return Bool(decided == witness)
	case "fold":
		return ev.foldSet(ev.setOf(arg(0), op), arg(1), arg(2), op)
	case "flatten":
		var out []Value
		for _, x := range ev.setOf(arg(0), op).Elems {
			out = append(out, ev.setOf(x, op).Elems...)
		}
		return NewSet(out)
	case "powerset":
		s := ev.setOf(arg(0), op)
		if len(s.Elems) > 16 {
			throw(unsupported(op, "of %d elements", len(s.Elems)))
		}
		var out []Value
		for mask := 0; mask < 1<<len(s.Elems); mask++ {
			var sub []Value
			for i, x := range s.Elems {
				if mask&(1<<i) != 0 {
					sub = append(sub, x)
				}
			}
			out = append(out, &Set{sub})
		}
		return NewSet(out)
	case "tuples":
		sets := make([]*Set, len(e.Args))
		n := 1
		for i := range e.Args {
			sets[i] = ev.setOf(arg(i), op)
			n *= len(sets[i].Elems)
			if n > maxSet {
				throw(unsupported(op, "more than %d tuples", maxSet))
			}
		}
		out := []Value{Tup{}}
		for _, s := range sets {
			var next []Value
			for _, t := range out {
				for _, x := range s.Elems {
					next = append(next, append(slices.Clone(t.(Tup)), x))
				}
			}
			out = next
		}
		return NewSet(out)
	case "chooseSome", "oneOf":
		throw(unsupported(op, "a choice outside a nondet"))

	// maps
	case "Map":
		var ks, vs []Value
		for _, p := range all() {
			t, ok := p.(Tup)
			if !ok || len(t) != 2 {
				throw(internal("Map: expected pairs, got %s", String(p)))
			}
			ks, vs = append(ks, t[0]), append(vs, t[1])
		}
		return NewMap(ks, vs)
	case "mapBy":
		s, f := ev.setOf(arg(0), op), arg(1)
		vs := make([]Value, len(s.Elems))
		for i, x := range s.Elems {
			vs[i] = ev.call(f, x)
		}
		return &Map{Keys: slices.Clone(s.Elems), Vals: vs}
	case "setToMap":
		var ks, vs []Value
		for _, p := range ev.setOf(arg(0), op).Elems {
			t := p.(Tup)
			// the pairs are sorted: a key's pairs are neighbours
			if n := len(ks); n > 0 && Compare(ks[n-1], t[0]) == 0 {
				// which pair quint keeps is its set order's (Rust keeps
				// one, TypeScript another): the evaluator kept the
				// greatest (found by the differential test)
				throw(unsupported(op, "two pairs of the key %s: which value quint keeps depends on the order it iterates the set in", String(t[0])))
			}
			ks, vs = append(ks, t[0]), append(vs, t[1])
		}
		return NewMap(ks, vs)
	case "keys":
		return &Set{slices.Clone(ev.mapOf(arg(0), op).Keys)}
	case "get":
		if l, ok := arg(0).(List); ok { // a list's [i]
			i := ev.intOf(arg(1), op)
			if i < 0 || i >= int64(len(l)) {
				throw(evalErr("index %d out of a list of %d", i, len(l)))
			}
			return l[i]
		}
		m, k := ev.mapOf(arg(0), op), arg(1)
		i, ok := m.find(k)
		if !ok {
			throw(evalErr("key %s not in the map", String(k)))
		}
		return m.Vals[i]
	case "set":
		m, k := ev.mapOf(arg(0), op), arg(1)
		if _, ok := m.find(k); !ok {
			throw(evalErr("set: key %s not in the map", String(k)))
		}
		return m.put(k, arg(2))
	case "put":
		return ev.mapOf(arg(0), op).put(arg(1), arg(2))
	case "setBy":
		m, k, f := ev.mapOf(arg(0), op), arg(1), arg(2)
		i, ok := m.find(k)
		if !ok {
			throw(evalErr("setBy: key %s not in the map", String(k)))
		}
		return m.put(k, ev.call(f, m.Vals[i]))

	// records, tuples, variants
	case "Rec":
		var names []string
		var vals []Value
		for i := 0; i+1 < len(e.Args); i += 2 {
			n, ok := arg(i).(Str)
			if !ok {
				throw(internal("Rec: a field name"))
			}
			names, vals = append(names, string(n)), append(vals, arg(i+1))
		}
		return NewRec(names, vals)
	case "field":
		r, ok := arg(0).(*Rec)
		if !ok {
			throw(internal("field: expected a record"))
		}
		n := arg(1).(Str)
		v, ok := r.field(string(n))
		if !ok {
			throw(internal("no field %s", n))
		}
		return v
	case "with":
		r, ok := arg(0).(*Rec)
		if !ok {
			throw(internal("with: expected a record"))
		}
		n := string(arg(1).(Str))
		i, found := slices.BinarySearch(r.Names, n)
		if !found {
			throw(internal("with: no field %s", n))
		}
		out := &Rec{Names: r.Names, Vals: slices.Clone(r.Vals)}
		out.Vals[i] = arg(2)
		return out
	case "fieldNames":
		r := arg(0).(*Rec)
		out := make([]Value, len(r.Names))
		for i, n := range r.Names {
			out[i] = Str(n)
		}
		return &Set{out}
	case "Tup":
		return Tup(all())
	case "item":
		t, ok := arg(0).(Tup)
		if !ok {
			throw(internal("item: expected a tuple"))
		}
		i := ev.intOf(arg(1), op)
		if i < 1 || i > int64(len(t)) {
			throw(evalErr("item %d of a tuple of %d", i, len(t)))
		}
		return t[i-1]
	case "variant":
		return Variant{Label: string(arg(0).(Str)), Val: arg(1)}
	case "matchVariant":
		v, ok := arg(0).(Variant)
		if !ok {
			throw(internal("match: expected a variant"))
		}
		for i := 1; i+1 < len(e.Args); i += 2 {
			l := string(arg(i).(Str))
			if l == v.Label || l == "_" {
				return ev.call(arg(i+1), v.Val)
			}
		}
		throw(evalErr("match: no case for %s", v.Label))

	// lists
	case "List":
		return List(all())
	case "append":
		return List(append(slices.Clone(ev.seqOf(arg(0), op)), arg(1)))
	case "concat":
		return List(append(slices.Clone(ev.seqOf(arg(0), op)), ev.seqOf(arg(1), op)...))
	case "head":
		l := ev.seqOf(arg(0), op)
		if len(l) == 0 {
			throw(evalErr("head of an empty list"))
		}
		return l[0]
	case "tail":
		l := ev.seqOf(arg(0), op)
		if len(l) == 0 {
			throw(evalErr("tail of an empty list"))
		}
		return List(slices.Clone(l[1:]))
	case "length":
		return Int(len(ev.seqOf(arg(0), op)))
	case "nth":
		l, i := ev.seqOf(arg(0), op), ev.intOf(arg(1), op)
		if i < 0 || i >= int64(len(l)) {
			throw(evalErr("nth %d of a list of %d", i, len(l)))
		}
		return l[i]
	case "replaceAt":
		l, i := ev.seqOf(arg(0), op), ev.intOf(arg(1), op)
		if i < 0 || i >= int64(len(l)) {
			throw(evalErr("replaceAt %d of a list of %d", i, len(l)))
		}
		out := slices.Clone(l)
		out[i] = arg(2)
		return List(out)
	case "slice":
		l, s, t := ev.seqOf(arg(0), op), ev.intOf(arg(1), op), ev.intOf(arg(2), op)
		if s < 0 || t > int64(len(l)) || s > t {
			throw(evalErr("slice %d..%d of a list of %d", s, t, len(l)))
		}
		return List(slices.Clone(l[s:t]))
	case "range":
		lo, hi := ev.intOf(arg(0), op), ev.intOf(arg(1), op)
		if lo > hi {
			// quint's backends differ: Rust's is empty, TypeScript's counts
			// down ([5, 4, 3] for range(5, 2)); quint, whichever backend runs
			throw(unsupported(op, "range(%d, %d) counts down in quint's TypeScript backend and is empty in its Rust one", lo, hi))
		}
		if hi > lo && (hi-lo > maxSet || hi-lo < 0) {
			throw(unsupported(op, "more than %d values", maxSet))
		}
		var out List
		for i := lo; i < hi; i++ {
			out = append(out, Int(i))
		}
		return out
	case "indices":
		l := ev.seqOf(arg(0), op)
		out := make([]Value, len(l))
		for i := range l {
			out[i] = Int(i)
		}
		return &Set{out}
	case "foldl":
		l, acc, f := ev.seqOf(arg(0), op), arg(1), arg(2)
		for _, x := range l {
			acc = ev.call(f, acc, x)
		}
		return acc
	case "foldr":
		l, acc, f := ev.seqOf(arg(0), op), arg(1), arg(2)
		for i := len(l) - 1; i >= 0; i-- {
			acc = ev.call(f, l[i], acc)
		}
		return acc
	}
	throw(unsupported(op, "no builtin of this name"))
	return nil
}

func (ev *evaluation) truth(v Value) bool {
	b, ok := v.(Bool)
	if !ok {
		throw(internal("expected a Boolean, got %s", String(v)))
	}
	return bool(b)
}

// Init is the model's initial state: action name (init by default), with
// the instance's constants.
func (m *Model) Init(consts map[string]Value, name string) (State, error) {
	if name == "" {
		name = "init"
	}
	s, ok, err := m.Step(consts, nil, name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("qeval: %s is not enabled", name)
	}
	return s, nil
}

package qeval

import (
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
}

func (u *Unsupported) Error() string {
	return fmt.Sprintf("qeval: %s is outside the Go evaluator's fragment: %s", u.Op, u.Why)
}

// An EvalError is the model's own runtime error (a map's missing key, a
// list's index out of range), as quint would report it.
type EvalError struct{ Msg string }

func (e *EvalError) Error() string { return "qeval: " + e.Msg }

func unsupported(op, why string, a ...any) error {
	return &Unsupported{Op: op, Why: fmt.Sprintf(why, a...)}
}

func evalErr(format string, a ...any) error { return &EvalError{fmt.Sprintf(format, a...)} }

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
}

func (e *env) lookup(name string) (Value, bool) {
	for ; e != nil; e = e.outer {
		if e.name == name {
			return e.val, true
		}
	}
	return nil, false
}

func (e *env) bind(name string, v Value) *env { return &env{name, v, e} }

// An evaluation: the model, its constants, the state it reads (nil for
// init), and the values of its vals, cached per state.
type evaluation struct {
	m      *Model
	consts map[string]Value
	state  State
	cache  map[int64]Value
	depth  int
}

func (m *Model) evaluation(consts map[string]Value, s State) *evaluation {
	return &evaluation{m: m, consts: consts, state: s, cache: map[int64]Value{}}
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
	for _, a := range succs {
		st := State{}
		for _, v := range m.Vars {
			x, ok := a[v]
			if !ok {
				return nil, unsupported(name, "leaves %s unassigned", v)
			}
			st[v] = x
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
	return nil, false, unsupported(name, "has %d successors (it chooses): quint takes one", len(out))
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
			panic(r)
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

// action is what e does as an action: its successors' assignments (none:
// disabled). A Boolean expression is an action that assigns nothing.
func (ev *evaluation) action(e *Expr, en *env) ([]assignment, error) {
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
			return []assignment{{v: ev.eval(e.Args[1], en)}}, nil
		case "actionAll":
			out := []assignment{{}}
			for _, a := range e.Args {
				succ, err := ev.action(a, en)
				if err != nil {
					return nil, err
				}
				if len(succ) == 0 {
					return nil, nil
				}
				var next []assignment
				for _, x := range out {
					for _, y := range succ {
						z, err := merge(x, y)
						if err != nil {
							return nil, err
						}
						next = append(next, z)
					}
				}
				out = next
				if len(out) > maxSet {
					return nil, unsupported("actionAll", "more than %d successors", maxSet)
				}
			}
			return out, nil
		case "actionAny":
			var out []assignment
			for _, a := range e.Args {
				succ, err := ev.action(a, en)
				if err != nil {
					return nil, err
				}
				out = append(out, succ...)
			}
			return out, nil
		case "ite":
			if ev.boolOf(e.Args[0], en) {
				return ev.action(e.Args[1], en)
			}
			return ev.action(e.Args[2], en)
		}
		if r, ok := ev.m.refs[e.ID]; ok && r.kind == "def" {
			d := ev.m.byID[r.decl]
			if d != nil && d.Qualifier == "action" {
				args := make([]Value, len(e.Args))
				for i, a := range e.Args {
					args[i] = ev.eval(a, en)
				}
				body, e2, err := ev.bindArgs(d, args, nil, d.Name)
				if err != nil {
					return nil, err
				}
				return ev.action(body, e2)
			}
		}
	case "name":
		if r, ok := ev.m.refs[e.ID]; ok && r.kind == "def" {
			if d := ev.m.byID[r.decl]; d != nil && d.Qualifier == "action" {
				body, e2, err := ev.bindArgs(d, nil, nil, d.Name)
				if err != nil {
					return nil, err
				}
				return ev.action(body, e2)
			}
		}
	case "let":
		d := e.Opdef
		if d.Qualifier == "nondet" {
			// a choice: each value of the set, its successors
			if d.Expr.Kind != "app" || d.Expr.Opcode != "oneOf" {
				return nil, unsupported("nondet", "%s is no oneOf", d.Name)
			}
			s := ev.setOf(ev.eval(d.Expr.Args[0], en), "oneOf")
			var out []assignment
			for _, x := range s.Elems {
				succ, err := ev.action(e.Expr, en.bind(d.Name, x))
				if err != nil {
					return nil, err
				}
				out = append(out, succ...)
				if len(out) > maxSet {
					return nil, unsupported("nondet", "more than %d successors", maxSet)
				}
			}
			return out, nil
		}
		return ev.action(e.Expr, ev.bindLet(d, en))
	}
	if ev.boolOf(e, en) {
		return []assignment{{}}, nil
	}
	return nil, nil
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

// bindLet binds a let's definition: a value now, or an operator.
func (ev *evaluation) bindLet(d *Decl, en *env) *env {
	if d.Expr.Kind == "lambda" {
		return en.bind(d.Name, &Lambda{params: paramNames(d.Expr), body: d.Expr.Expr, env: nil})
	}
	return en.bind(d.Name, ev.eval(d.Expr, en))
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
		throw(evalErr("expected a Boolean, got %s", String(v)))
	}
	return bool(b)
}

func (ev *evaluation) intOf(v Value, op string) int64 {
	i, ok := v.(Int)
	if !ok {
		throw(evalErr("%s: expected an integer, got %s", op, String(v)))
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
	throw(evalErr("%s: expected a set, got %s", op, String(v)))
	return nil
}

func (ev *evaluation) mapOf(v Value, op string) *Map {
	m, ok := v.(*Map)
	if !ok {
		throw(evalErr("%s: expected a map, got %s", op, String(v)))
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
	throw(evalErr("%s: expected a list, got %s", op, String(v)))
	return nil
}

// call applies an operator value to args.
func (ev *evaluation) call(f Value, args ...Value) Value {
	l, ok := f.(*Lambda)
	if !ok {
		throw(evalErr("expected an operator, got %s", String(f)))
	}
	if len(l.params) != len(args) {
		throw(evalErr("an operator of %d parameters applied to %d", len(l.params), len(args)))
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
		return ev.name(e, en)
	case "lambda":
		return &Lambda{params: paramNames(e), body: e.Expr, env: en}
	case "let":
		if e.Opdef.Qualifier == "nondet" {
			throw(unsupported("nondet", "a choice outside an action"))
		}
		return ev.eval(e.Expr, ev.bindLet(e.Opdef, en))
	case "app":
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
		throw(evalErr("parameter %s unbound", r.param))
	case "var":
		d := ev.m.byID[r.decl]
		if ev.state == nil {
			throw(evalErr("%s read before init", d.Name))
		}
		v, ok := ev.state[d.Name]
		if !ok {
			throw(evalErr("%s has no value", d.Name))
		}
		return v
	case "const":
		d := ev.m.byID[r.decl]
		if v, ok := ev.consts[d.Name]; ok {
			return v
		}
		throw(evalErr("constant %s has no value: give the instance's", d.Name))
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
		if v, ok := ev.cache[d.ID]; ok {
			return v
		}
		v := ev.eval(d.Expr, nil)
		ev.cache[d.ID] = v
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
	if r, ok := ev.m.refs[e.ID]; ok && (r.kind == "def" || r.kind == "param") {
		args := make([]Value, len(e.Args))
		for i, a := range e.Args {
			args[i] = ev.eval(a, en)
		}
		if r.kind == "param" {
			f, ok := en.lookup(r.param)
			if !ok {
				throw(evalErr("parameter %s unbound", r.param))
			}
			return ev.call(f, args...)
		}
		d := ev.m.byID[r.decl]
		if d == nil {
			throw(unsupported(e.Opcode, "a definition not in the IR"))
		}
		v, err := ev.apply(d, args, nil, d.Name)
		if err != nil {
			throw(err)
		}
		return v
	}
	if f, ok := en.lookup(e.Opcode); ok { // a let-bound operator
		args := make([]Value, len(e.Args))
		for i, a := range e.Args {
			args[i] = ev.eval(a, en)
		}
		return ev.call(f, args...)
	}
	return ev.builtin(e, en)
}

func (ev *evaluation) builtin(e *Expr, en *env) Value {
	op := e.Opcode
	arg := func(i int) Value { return ev.eval(e.Args[i], en) }
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
		// quint divides as TLA+: floored for imod's sign, truncated for idiv
		if op == "idiv" {
			return Int(a / b)
		}
		m := a % b
		if m < 0 {
			if b > 0 {
				m += b
			} else {
				m -= b
			}
		}
		return Int(m)
	case "ipow":
		a, b := ev.intOf(arg(0), op), ev.intOf(arg(1), op)
		if b < 0 {
			throw(evalErr("a negative power"))
		}
		r := int64(1)
		for range b {
			r = mul(r, a)
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
		if hi-lo >= maxSet {
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
	case "exists":
		s, f := ev.setOf(arg(0), op), arg(1)
		for _, x := range s.Elems {
			if ev.truth(ev.call(f, x)) {
				return Bool(true)
			}
		}
		return Bool(false)
	case "forall":
		s, f := ev.setOf(arg(0), op), arg(1)
		for _, x := range s.Elems {
			if !ev.truth(ev.call(f, x)) {
				return Bool(false)
			}
		}
		return Bool(true)
	case "fold":
		// over the set in its order: quint's order is its own, so a fold
		// whose result depends on the order (none here) may differ
		s, acc, f := ev.setOf(arg(0), op), arg(1), arg(2)
		for _, x := range s.Elems {
			acc = ev.call(f, acc, x)
		}
		return acc
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
				throw(evalErr("Map: expected pairs, got %s", String(p)))
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
				throw(evalErr("Rec: a field name"))
			}
			names, vals = append(names, string(n)), append(vals, arg(i+1))
		}
		return NewRec(names, vals)
	case "field":
		r, ok := arg(0).(*Rec)
		if !ok {
			throw(evalErr("field: expected a record"))
		}
		n := arg(1).(Str)
		v, ok := r.field(string(n))
		if !ok {
			throw(evalErr("no field %s", n))
		}
		return v
	case "with":
		r, ok := arg(0).(*Rec)
		if !ok {
			throw(evalErr("with: expected a record"))
		}
		n := string(arg(1).(Str))
		i, found := slices.BinarySearch(r.Names, n)
		if !found {
			throw(evalErr("with: no field %s", n))
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
			throw(evalErr("item: expected a tuple"))
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
			throw(evalErr("match: expected a variant"))
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
		if hi-lo > maxSet {
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
		throw(evalErr("expected a Boolean, got %s", String(v)))
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

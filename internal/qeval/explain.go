package qeval

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unsafe"
)

// An Event is what led to a transition (Transitions): an action applied
// (Action, its parameters and arguments) or a choice made (Pick, the
// nondet's name, and the Value chosen).
type Event struct {
	Action string
	Params []string
	Args   []Value
	Pick   string
	Value  Value
}

// String is the event as Quint text: deposit(amount = 5), or x = 5.
func (e Event) String() string {
	if e.Action == "" {
		return e.Pick + " = " + String(e.Value)
	}
	return callText(e.Action, e.Params, e.Args)
}

func callText(name string, params []string, args []Value) string {
	if len(args) == 0 {
		return name
	}
	parts := make([]string, len(args))
	for i, a := range args {
		v := short(String(a))
		if i < len(params) && params[i] != "" {
			parts[i] = params[i] + " = " + v
		} else {
			parts[i] = v
		}
	}
	return name + "(" + strings.Join(parts, ", ") + ")"
}

// short is a value's text, cut past 200 characters.
func short(s string) string {
	if len(s) > 200 {
		return s[:200] + " ..."
	}
	return s
}

// A Transition is a successor with what led to it: the actions applied
// and the choices made on the way, outermost first, as the model's text
// names them (Display).
type Transition struct {
	Events []Event
	State  State
	// key is State's Key, where Transitions computed it
	key string
}

// appendEvents appends a list of events' encoding (appendEvent), ended
// by tagEnd: equal exactly when the events are.
func appendEvents(b []byte, evs []Event) []byte {
	for _, e := range evs {
		b = appendEvent(b, e)
	}
	return append(b, tagEnd)
}

// Actions are the transition's actions, outermost first.
func (t Transition) Actions() []Event {
	var out []Event
	for _, e := range t.Events {
		if e.Action != "" {
			out = append(out, e)
		}
	}
	return out
}

// Picks are the transition's choices, in the order they were made.
func (t Transition) Picks() []Event {
	var out []Event
	for _, e := range t.Events {
		if e.Action == "" {
			out = append(out, e)
		}
	}
	return out
}

// Transitions are the successors of action name, applied to args, in s,
// each labelled with the actions it applied and the choices it made: one
// per distinct label and state (two choices reaching one state are two
// transitions; Successors has one state for both).
func (m *Model) Transitions(consts map[string]Value, s State, name string, args ...Value) (out []Transition, err error) {
	defer recoverInto(&err)
	d, ok := m.Decl(name)
	if !ok {
		return nil, fmt.Errorf("qeval: no action %s", name)
	}
	ev := m.evaluation(consts, s)
	ev.labels = true
	body, e, err := ev.bindArgs(d, args, nil, name)
	if err != nil {
		return nil, err
	}
	succs, err := ev.action(body, e)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	// a call's successors are shared by every choice around it that does
	// not change it (callAction): each assignment's state and Key once
	type built struct {
		st  State
		key string
	}
	states := map[unsafe.Pointer]built{}
	display := map[string]string{}
	var key []byte // reused: a lookup of seen copies nothing
	for _, sc := range succs {
		p := reflect.ValueOf(sc.a).UnsafePointer()
		bs, ok := states[p]
		if !ok {
			bs.st = State{}
			for _, v := range m.Vars {
				x, ok := sc.a[v]
				if !ok {
					return nil, unsupported(name, "leaves %s unassigned", v)
				}
				bs.st[v] = x
			}
			bs.key = m.Key(bs.st)
			states[p] = bs
		}
		key = key[:0]
		for l := sc.lab; l != nil; l = l.next {
			key = appendEvent(key, l.ev)
		}
		key = append(append(key, tagEnd), bs.key...)
		if !seen[string(key)] {
			seen[string(key)] = true
			evs := sc.lab.slice()
			for i, l := range evs {
				if l.Action != "" {
					d, ok := display[l.Action]
					if !ok {
						d = m.Display(l.Action)
						display[l.Action] = d
					}
					l.Action = d
				}
				evs[i] = l
			}
			out = append(out, Transition{Events: evs, State: bs.st, key: bs.key})
		}
	}
	return out, nil
}

// BranchActions are the actions action name applies itself, outside any
// definition it calls: a step's branches (step = any { deposit(..),
// withdraw(..) } applies deposit and withdraw), in the order its text
// names them, as Display names them. An action whose whole body applies
// another (a wrapper) has that one's.
func (m *Model) BranchActions(name string) []string {
	d, ok := m.Decl(name)
	if !ok || d.Expr == nil {
		return nil
	}
	var out []string
	var walk func(e *Expr)
	walk = func(e *Expr) {
		if e == nil {
			return
		}
		if e.Kind == "app" || e.Kind == "name" {
			if r, ok := m.refs[e.ID]; ok && r.kind == "def" {
				if c := m.byID[r.decl]; c != nil && c.Qualifier == "action" {
					if n := m.Display(c.Name); !slices.Contains(out, n) {
						out = append(out, n)
					}
					return
				}
			}
		}
		for _, a := range e.Args {
			walk(a)
		}
		walk(e.Expr)
		if e.Opdef != nil {
			walk(e.Opdef.Expr)
		}
	}
	body := d.Expr
	if body.Kind == "lambda" {
		body = body.Expr
	}
	// a wrapper (the evaluation module's __gtg_act_step = step): the
	// action it is
	if body.Kind == "name" || body.Kind == "app" {
		if r, ok := m.refs[body.ID]; ok && r.kind == "def" {
			if c := m.byID[r.decl]; c != nil && c.Qualifier == "action" && c != d {
				return m.BranchActions(c.Name)
			}
		}
	}
	walk(body)
	return out
}

// A Why is why an action is disabled in a state: the chain of conditions
// that are false, from the action's own conjunct to the leaf, each where
// it is (an action or definition applied, a quantifier's witness, a
// choice's first value). An any {} none of whose branches is enabled
// ends the chain, with each branch's own Why.
type Why struct {
	// Action is the action explained, applied to its arguments, as
	// Quint text: admit(client = "c")
	Action   string
	Chain    []Cond
	Branches []*Why
	// Branch names the branch of an any {} this Why is (its first
	// action's call, or its text)
	Branch string
	// passed counts the conjuncts that held before the false ones on the
	// way: how close to enabled (a choice's values are compared by it)
	passed int
}

// A Cond is one false condition of a Why: In where it is (the action or
// definition it is in, by name, with the branches and witnesses on the
// way: its arguments are among Values), Expr the
// condition as Quint text (printed from quint's IR, print.go), and the
// values of its free names and of its operands that are no names.
type Cond struct {
	In     string
	Expr   string
	Values []Named
	// Frame is the action or definition applied that In names, if it
	// names one: an explanation's vocabulary (validate's for grist) reads
	// its arguments
	Frame *Frame
}

// A Named is a value with the name, or the expression, it is the value of.
type Named struct {
	Name  string
	Value Value
}

// A Frame is an action or definition applied: its name (as Display
// gives it), its parameters and its arguments' values.
type Frame struct {
	Name   string
	Params []string
	Args   []Value
}

// Arg is the frame's argument of parameter p.
func (f *Frame) Arg(p string) (Value, bool) {
	for i, q := range f.Params {
		if q == p && i < len(f.Args) {
			return f.Args[i], true
		}
	}
	return nil, false
}

// Leaf is the last condition of the chain: the most specific false one.
func (w *Why) Leaf() *Cond {
	if len(w.Chain) == 0 {
		return nil
	}
	return &w.Chain[len(w.Chain)-1]
}

// Explain is why action name, applied to args, is disabled in s: nil if
// it is enabled. Only, if set, keeps the branches of an any {} (at any
// depth) that apply the action of that name themselves (BranchActions):
// why a step cannot take deposit. A Why from an evaluation the
// evaluator cannot make is its error (Unsupported, EvalError).
func (m *Model) Explain(consts map[string]Value, s State, only, name string, args ...Value) (w *Why, err error) {
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
	succ, err := ev.action(body, e)
	if err != nil {
		return nil, err
	}
	if len(succ) > 0 && only == "" {
		return nil, nil
	}
	x := &explainer{ev: ev, only: only}
	f := &Frame{Name: m.Display(name), Params: m.paramsOf(d), Args: args}
	w = &Why{}
	w.Action = callText(f.Name, f.Params, args)
	if !x.act(w, body, e, ctxt{in: f.Name, frame: f}, true) {
		return nil, nil // only's branches are enabled, or there are none
	}
	return w, nil
}

type explainer struct {
	ev   *evaluation
	only string
}

// a ctxt is where a condition is
type ctxt struct {
	in    string
	frame *Frame
	// picks are the choices on the way to it that every value of
	// disables, carried into the actions called after them
	picks string
}

func (c ctxt) with(more string) ctxt {
	return ctxt{in: c.in + ", " + more, frame: c.frame, picks: c.picks}
}

// into is the context of a call from c: the frame's, with c's picks.
func (c ctxt) into(in string, f *Frame) ctxt {
	if c.picks != "" {
		in += " (" + c.picks + ")"
	}
	return ctxt{in: in, frame: f, picks: c.picks}
}

func (x *explainer) cond(w *Why, c ctxt, e *Expr, en *env) {
	w.Chain = append(w.Chain, Cond{In: c.in, Expr: x.ev.m.Text(e), Values: x.values(e, en), Frame: c.frame})
}

func (x *explainer) disabled(e *Expr, en *env) bool {
	succ, err := x.ev.action(e, en)
	if err != nil {
		throw(err)
	}
	return len(succ) == 0
}

// act explains e, an action with no successors, into w; whole says e is
// the whole of where it is (no condition of its own to name). false if,
// under only, nothing of e is to explain.
func (x *explainer) act(w *Why, e *Expr, en *env, c ctxt, whole bool) bool {
	m := x.ev.m
	switch e.Kind {
	case "app":
		switch e.Opcode {
		case "actionAll":
			for i, a := range e.Args {
				if x.disabled(a, en) {
					w.passed += i
					return x.act(w, a, en, c, whole && len(e.Args) == 1)
				}
			}
			return false // (a merge conflict is the evaluator's error)
		case "actionAny":
			var bs []*Expr
			for _, a := range e.Args {
				if x.only == "" || slices.Contains(x.branchActions(a), x.only) {
					bs = append(bs, a)
				}
			}
			if x.only != "" && len(bs) == 1 {
				if !x.disabled(bs[0], en) {
					return false
				}
				return x.act(w, bs[0], en, c, whole)
			}
			if len(bs) == 0 {
				return false
			}
			for _, b := range bs {
				if !x.disabled(b, en) {
					return false
				}
			}
			w.Chain = append(w.Chain, Cond{In: c.in, Expr: fmt.Sprintf("any { ... } (%d branches, none enabled)", len(bs)), Frame: c.frame})
			for _, b := range bs {
				sub := &Why{Branch: x.branchName(b)}
				x.act(sub, b, en, c, true)
				w.Branches = append(w.Branches, sub)
			}
			return true
		case "ite":
			if x.ev.boolOf(e.Args[0], en) {
				return x.act(w, e.Args[1], en, c.with("the then branch of if ("+m.Text(e.Args[0])+")"), whole)
			}
			return x.act(w, e.Args[2], en, c.with("the else branch of if ("+m.Text(e.Args[0])+")"), whole)
		}
		if d, args, ok := x.ev.calledAction(e, en); ok {
			return x.call(w, e, en, c, whole, d, args)
		}
	case "name":
		if d, _, ok := x.ev.calledAction(e, en); ok {
			return x.call(w, e, en, c, whole, d, nil)
		}
	case "let":
		d := e.Opdef
		if d.Qualifier == "nondet" {
			s, err := x.ev.choiceSet(d, en)
			if err != nil {
				throw(err)
			}
			if len(s.Elems) == 0 {
				x.cond(w, c, d.Expr, en)
				w.Chain[len(w.Chain)-1].Expr += " (the set is empty: no value to choose)"
				return true
			}
			// the value whose action gets furthest (the most conjuncts
			// holding before the false one: admit's 2 < 2 for a client not
			// yet admitted, not the first client's already admitted), the
			// first of those
			v, best, which := s.Elems[0], -1, "the first"
			// same: every value explains it alike, the pick no part of why
			// (step's nondet for another action, which this one never reads)
			same, first := true, ""
			if len(s.Elems) <= 64 {
				for i, x2v := range s.Elems {
					sub, x2 := &Why{}, *x
					x2.act(sub, e.Expr, en.bind(d.Name, x2v), c, whole)
					if k := whyKey(sub); i == 0 {
						first = k
					} else if k != first {
						same = false
					}
					if sub.passed > best {
						v, best = x2v, sub.passed
					}
				}
				which = "the one closest to enabled"
			}
			if same && len(s.Elems) > 1 && len(s.Elems) <= 64 {
				// done takes no id, yet was explained "(id = 1, the one
				// closest to enabled of the 3 values of REQUESTS)", step's
				// nondet for apply
				return x.act(w, e.Expr, en.bind(d.Name, v), c, whole)
			}
			pick := fmt.Sprintf("%s = %s, %s of the %d values of %s, each disabling it", m.Display(d.Name), short(String(v)), which, len(s.Elems), m.Text(d.Expr.Args[0]))
			c = c.with("for " + pick)
			if c.picks != "" {
				pick = c.picks + "; " + pick
			}
			c.picks = pick
			return x.act(w, e.Expr, en.bind(d.Name, v), c, whole)
		}
		return x.act(w, e.Expr, x.ev.bindLet(d, en), c, whole)
	}
	if x.only != "" {
		return false // only names an action no branch here applies
	}
	x.bool(w, e, en, c, whole)
	return true
}

// whyKey is w's conditions as text, values included: two values of a
// choice whose whys have one key are explained alike.
func whyKey(w *Why) string {
	var b strings.Builder
	var walk func(w *Why)
	walk = func(w *Why) {
		fmt.Fprintf(&b, "%d|%s|", w.passed, w.Branch)
		for _, c := range w.Chain {
			fmt.Fprintf(&b, "%s|%s|", c.In, c.Expr)
			for _, v := range c.Values {
				fmt.Fprintf(&b, "%s=%s|", v.Name, String(v.Value))
			}
		}
		for _, br := range w.Branches {
			walk(br)
		}
	}
	walk(w)
	return b.String()
}

// call explains the application e of action d to args, disabled.
func (x *explainer) call(w *Why, e *Expr, en *env, c ctxt, whole bool, d *Decl, args []Value) bool {
	m := x.ev.m
	body, e2, err := x.ev.bindArgs(d, args, nil, d.Name)
	if err != nil {
		throw(err)
	}
	f := &Frame{Name: m.Display(d.Name), Params: m.paramsOf(d), Args: args}
	if x.only != "" && f.Name == x.only {
		// the branch's own action: why it is disabled, every condition
		x.only = ""
	}
	if !whole {
		x.cond(w, c, e, en)
	}
	return x.act(w, body, e2, c.into(f.Name, f), true)
}

// bool explains e, a Boolean that is false.
func (x *explainer) bool(w *Why, e *Expr, en *env, c ctxt, whole bool) {
	m, ev := x.ev.m, x.ev
	switch e.Kind {
	case "let":
		x.bool(w, e.Expr, ev.bindLet(e.Opdef, en), c, whole)
		return
	case "app":
		switch e.Opcode {
		case "and":
			for i, a := range e.Args {
				if !ev.boolOf(a, en) {
					w.passed += i
					x.bool(w, a, en, c, whole && len(e.Args) == 1)
					return
				}
			}
		case "ite":
			if ev.boolOf(e.Args[0], en) {
				x.bool(w, e.Args[1], en, c.with("the then branch of if ("+m.Text(e.Args[0])+")"), whole)
			} else {
				x.bool(w, e.Args[2], en, c.with("the else branch of if ("+m.Text(e.Args[0])+")"), whole)
			}
			return
		case "implies":
			if !whole {
				x.cond(w, c, e, en)
			}
			x.bool(w, e.Args[1], en, c.with(m.Text(e.Args[0])+" holds"), true)
			return
		case "forall":
			if l := e.Args[1]; l.Kind == "lambda" && len(l.Params) == 1 {
				set := ev.setOf(ev.eval(e.Args[0], en), "forall")
				for _, v := range set.Elems {
					inner := en.bind(l.Params[0].Name, v)
					if !ev.boolOf(l.Expr, inner) {
						if !whole {
							x.cond(w, c, e, en)
						}
						x.bool(w, l.Expr, inner, c.with(fmt.Sprintf("for %s = %s of %s", m.Display(l.Params[0].Name), short(String(v)), m.Text(e.Args[0]))), false)
						return
					}
				}
			}
		}
		if f, ok := en.lookup(e.Opcode); ok { // a let-bound operator
			if l, ok := f.(*Lambda); ok && len(l.params) == len(e.Args) {
				if !whole {
					x.cond(w, c, e, en)
				}
				inner := l.env
				for i, p := range l.params {
					inner = inner.bind(p, ev.eval(e.Args[i], en))
				}
				x.bool(w, l.body, inner, ctxt{in: m.Display(e.Opcode), frame: c.frame, picks: c.picks}, true)
				return
			}
		}
		if r, ok := m.refs[e.ID]; ok && r.kind == "def" {
			if d := m.byID[r.decl]; d != nil && d.Qualifier != "action" && d.Expr != nil {
				args := make([]Value, len(e.Args))
				for i, a := range e.Args {
					args[i] = ev.eval(a, en)
				}
				body, e2, err := ev.bindArgs(d, args, nil, d.Name)
				if err != nil {
					throw(err)
				}
				if !whole {
					x.cond(w, c, e, en)
				}
				f := &Frame{Name: m.Display(d.Name), Params: m.paramsOf(d), Args: args}
				x.bool(w, body, e2, c.into(f.Name, f), true)
				return
			}
		}
	}
	x.cond(w, c, e, en) // the leaf
}

// values are the values of e's free names (parameters, lets, state
// variables, constants, vals) and of its operands that are applications,
// as an explanation shows them.
func (x *explainer) values(e *Expr, en *env) []Named {
	m := x.ev.m
	var out []Named
	seen := map[string]bool{}
	add := func(name string, f func() Value) {
		if seen[name] {
			return
		}
		seen[name] = true
		if v, ok := x.try(f); ok {
			if _, op := v.(*Lambda); !op {
				out = append(out, Named{name, v})
			}
		}
	}
	// operands first: a comparison's sides are what it compares
	ops := e
	if ops.Kind == "app" && ops.Opcode == "not" && len(ops.Args) == 1 {
		ops = ops.Args[0]
	}
	if ops.Kind == "app" && builtin(ops.Opcode) {
		for _, a := range ops.Args {
			if a.Kind == "app" && !(a.Opcode == "Tup" || a.Opcode == "Rec" || a.Opcode == "Set" || a.Opcode == "List" || a.Opcode == "Map") {
				add(m.Text(a), func() Value { return x.ev.eval(a, en) })
			}
		}
	}
	var walk func(e *Expr, bound map[string]bool)
	walk = func(e *Expr, bound map[string]bool) {
		if e == nil {
			return
		}
		switch e.Kind {
		case "name":
			if !bound[e.Name] {
				add(m.Display(e.Name), func() Value { return x.ev.name(e, en) })
			}
			return
		case "lambda":
			inner := map[string]bool{}
			for k := range bound {
				inner[k] = true
			}
			for _, p := range e.Params {
				inner[p.Name] = true
			}
			walk(e.Expr, inner)
			return
		case "let":
			walk(e.Opdef.Expr, bound)
			inner := map[string]bool{e.Opdef.Name: true}
			for k := range bound {
				inner[k] = true
			}
			walk(e.Expr, inner)
			return
		}
		for _, a := range e.Args {
			walk(a, bound)
		}
	}
	walk(e, map[string]bool{})
	return out
}

// try is f's value, or false if the evaluation fails (a value an
// explanation shows is no verdict: one it cannot evaluate is left out).
func (x *explainer) try(f func() Value) (v Value, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			v, ok = nil, false
		}
	}()
	return f(), true
}

// branchActions are the actions e applies itself (BranchActions of an
// expression).
func (x *explainer) branchActions(e *Expr) []string {
	m := x.ev.m
	var out []string
	var walk func(e *Expr)
	walk = func(e *Expr) {
		if e == nil {
			return
		}
		if e.Kind == "app" || e.Kind == "name" {
			if r, ok := m.refs[e.ID]; ok && r.kind == "def" {
				if c := m.byID[r.decl]; c != nil && c.Qualifier == "action" {
					out = append(out, m.Display(c.Name))
					return
				}
			}
		}
		for _, a := range e.Args {
			walk(a)
		}
		walk(e.Expr)
		if e.Opdef != nil {
			walk(e.Opdef.Expr)
		}
	}
	walk(e)
	return out
}

// branchName names a branch of an any {}: the first action it applies,
// else its text.
func (x *explainer) branchName(e *Expr) string {
	if as := x.branchActions(e); len(as) > 0 {
		return as[0]
	}
	return short(x.ev.m.Text(e))
}

// String is w as an explanation's lines, each prefixed with pfx: the
// chain, a condition a line, its values below it, and an any's branches
// indented.
func (w *Why) String(pfx string) string {
	var b strings.Builder
	w.write(&b, pfx)
	return b.String()
}

func (w *Why) write(b *strings.Builder, pfx string) {
	if w.Action != "" {
		fmt.Fprintf(b, "%s%s is disabled:\n", pfx, w.Action)
	}
	for _, c := range w.Chain {
		fmt.Fprintf(b, "%sin %s: %s is false\n", pfx, c.In, c.Expr)
		if len(c.Values) > 0 {
			parts := make([]string, len(c.Values))
			for i, v := range c.Values {
				parts[i] = v.Name + " = " + short(String(v.Value))
			}
			fmt.Fprintf(b, "%s  where %s\n", pfx, strings.Join(parts, ", "))
		}
	}
	for _, br := range w.Branches {
		fmt.Fprintf(b, "%s  branch %s:\n", pfx, br.Branch)
		br.write(b, pfx+"    ")
	}
}

// A DefInfo is a definition of the model's: its name in the IR (Eval
// takes it), as Display names it, and its parameters.
type DefInfo struct {
	Name, Display string
	Params        []string
}

// DefsEndingIn are the model's non-action definitions whose displayed
// name ends in suffix (and is longer), sorted by name.
func (m *Model) DefsEndingIn(suffix string) []DefInfo {
	var out []DefInfo
	for _, d := range m.byName {
		if d.Kind != "def" || d.Qualifier == "action" {
			continue
		}
		if n := m.Display(d.Name); strings.HasSuffix(n, suffix) && len(n) > len(suffix) {
			out = append(out, DefInfo{Name: d.Name, Display: n, Params: m.paramsOf(d)})
		}
	}
	slices.SortFunc(out, func(a, b DefInfo) int { return strings.Compare(a.Name, b.Name) })
	return out
}

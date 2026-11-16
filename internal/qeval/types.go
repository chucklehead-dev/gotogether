package qeval

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

// A Type is a Quint type as quint's type checker inferred it: the IR's
// type table (`quint compile --target json`'s "types", by node id). Kind
// is quint's: int, bool, str, set, list (Elem), fun (a map: Arg to Res),
// oper (an operator: Args to Res), tup, rec and sum (Fields, in the
// order quint lists them), var (a type variable, Name) and const (an
// uninterpreted type, Name). A kind not listed is kept by its name alone.
type Type struct {
	Kind   string
	Name   string // var, const
	Elem   *Type  // set, list
	Arg    *Type  // fun
	Res    *Type  // fun, oper
	Args   []*Type
	Fields []Field // tup (named "0", "1", ...), rec, sum (a variant's payload)
	// Open: a row variable stands for fields not listed (a record type
	// inferred from the fields used)
	Open bool
	// ID is the IR's id of the type's node, 0 when it has none: where
	// quint writes an alias out (a polymorphic alias's application
	// substitutes its arguments), the alias's body keeps its node's id
	ID int64
}

// A Field is a record's field, a tuple's element or a sum's variant.
type Field struct {
	Name string
	Type *Type
}

// Nullary reports whether t is a variant's empty payload: the unit, an
// empty tuple (or record) with no fields to come.
func (t *Type) Nullary() bool {
	return t != nil && (t.Kind == "tup" || t.Kind == "rec") && len(t.Fields) == 0 && !t.Open
}

// Option reports whether t is a sum of the variants Some and None alone
// (Quint's Option), and Some's payload.
func (t *Type) Option() (*Type, bool) {
	if t == nil || t.Kind != "sum" || t.Open || len(t.Fields) == 0 {
		return nil, false
	}
	var some *Type
	for _, f := range t.Fields {
		switch f.Name {
		case "Some":
			some = f.Type
		case "None":
		default:
			return nil, false
		}
	}
	return some, true
}

// String is t in Quint's syntax.
func (t *Type) String() string {
	if t == nil {
		return "?"
	}
	var b strings.Builder
	t.write(&b)
	return b.String()
}

func (t *Type) write(b *strings.Builder) {
	switch t.Kind {
	case "set", "list":
		b.WriteString(map[string]string{"set": "Set[", "list": "List["}[t.Kind])
		t.Elem.write(b)
		b.WriteString("]")
	case "fun":
		t.Arg.write(b)
		b.WriteString(" -> ")
		t.Res.write(b)
	case "oper":
		b.WriteString("(")
		for i, a := range t.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			a.write(b)
		}
		b.WriteString(") => ")
		t.Res.write(b)
	case "tup":
		b.WriteString("(")
		for i, f := range t.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			f.Type.write(b)
		}
		if t.Open {
			b.WriteString(", ...")
		}
		b.WriteString(")")
	case "rec":
		b.WriteString("{ ")
		for i, f := range t.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(f.Name + ": ")
			f.Type.write(b)
		}
		if t.Open {
			b.WriteString(", ...")
		}
		b.WriteString(" }")
	case "sum":
		for i, f := range t.Fields {
			if i > 0 {
				b.WriteString(" | ")
			}
			b.WriteString(f.Name)
			if !f.Type.Nullary() {
				b.WriteString("(")
				f.Type.write(b)
				b.WriteString(")")
			}
		}
	case "var", "const":
		b.WriteString(t.Name)
	default:
		b.WriteString(t.Kind)
	}
}

// irType is a type as the IR writes it; a row is {kind: row, fields,
// other} (other the rest: another row, empty, or a row variable).
type irType struct {
	ID     int64     `json:"id"`
	Kind   string    `json:"kind"`
	Name   string    `json:"name"`
	Elem   *irType   `json:"elem"`
	Arg    *irType   `json:"arg"`
	Res    *irType   `json:"res"`
	Args   []*irType `json:"args"`
	Fields *irRow    `json:"fields"`
}

type irRow struct {
	Kind   string `json:"kind"`
	Fields []struct {
		FieldName string  `json:"fieldName"`
		FieldType *irType `json:"fieldType"`
	} `json:"fields"`
	Other *irRow `json:"other"`
}

func (t *irType) typ() *Type {
	if t == nil {
		return nil
	}
	out := &Type{Kind: t.Kind, Name: t.Name, Elem: t.Elem.typ(), Arg: t.Arg.typ(), Res: t.Res.typ(), ID: t.ID}
	for _, a := range t.Args {
		out.Args = append(out.Args, a.typ())
	}
	for r := t.Fields; r != nil; r = r.Other {
		switch r.Kind {
		case "row":
			for _, f := range r.Fields {
				out.Fields = append(out.Fields, Field{Name: f.FieldName, Type: f.FieldType.typ()})
			}
		case "empty":
		default: // a row variable: more fields than listed
			out.Open = true
		}
	}
	return out
}

// ParseType reads one type as quint's IR writes it: a typedef's type or
// a var's typeAnnotation in `quint typecheck --out`'s modules, where an
// alias is kept by its name (kind const, Name the alias as the module
// refers to it: Item, lib::Item) and a polymorphic alias's application is
// written out. Nil for JSON null.
func ParseType(raw json.RawMessage) (*Type, error) {
	var t *irType
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("qeval: a type: %w", err)
	}
	return t.typ(), nil
}

// typeTable is the IR's types, read the first time one is asked for:
// most models are only evaluated.
type typeTable struct {
	raw  json.RawMessage
	once sync.Once
	byID map[int64]*Type
	err  error
}

func (tt *typeTable) read() (map[int64]*Type, error) {
	tt.once.Do(func() {
		tt.byID = map[int64]*Type{}
		if len(tt.raw) == 0 {
			return
		}
		var raw map[string]struct {
			Type *irType `json:"type"`
		}
		if err := json.Unmarshal(tt.raw, &raw); err != nil {
			tt.err = fmt.Errorf("qeval: the IR's types: %w", err)
			return
		}
		for id, s := range raw {
			var n int64
			if _, err := fmt.Sscan(id, &n); err != nil {
				tt.err = fmt.Errorf("qeval: the IR's types: id %q", id)
				return
			}
			tt.byID[n] = s.Type.typ()
		}
	})
	return tt.byID, tt.err
}

// TypeOf is the type quint inferred for d: a var's, a val's, an
// operator's (kind oper: its parameters' types and its result's). False
// when the IR has no types (an IR written without them).
func (m *Model) TypeOf(d *Decl) (*Type, bool) {
	if m.types == nil {
		return nil, false
	}
	byID, err := m.types.read()
	if err != nil {
		return nil, false
	}
	t, ok := byID[d.ID]
	return t, ok && t != nil
}

// Params are a definition's parameters as the source names them, each
// with its type (nil when the IR has none).
func (m *Model) Params(d *Decl) []Field {
	names := m.paramsOf(d)
	t, _ := m.TypeOf(d)
	out := make([]Field, len(names))
	for i, n := range names {
		out[i].Name = n
		if t != nil && t.Kind == "oper" && i < len(t.Args) {
			out[i].Type = t.Args[i]
		}
	}
	return out
}

// Nondets are the choices action name makes (its nondets, and those of
// every action and definition it applies), by the name a trace's
// mbt::nondetPicks gives them, each with the types it is chosen at (one
// name may be chosen in two actions, at two types). Nil when name is no
// definition of m.
func (m *Model) Nondets(name string) map[string][]*Type {
	d, ok := m.Decl(name)
	if !ok {
		return nil
	}
	out := map[string][]*Type{}
	seen := map[int64]bool{d.ID: true}
	var walk func(e *Expr)
	walk = func(e *Expr) {
		if e == nil {
			return
		}
		if e.Kind == "app" || e.Kind == "name" {
			if r, ok := m.refs[e.ID]; ok && r.kind == "def" && !seen[r.decl] {
				seen[r.decl] = true
				if c := m.byID[r.decl]; c != nil {
					walk(c.Expr)
				}
			}
		}
		if e.Kind == "let" && e.Opdef != nil && e.Opdef.Qualifier == "nondet" {
			n := m.Display(e.Opdef.Name)
			t, _ := m.TypeOf(e.Opdef)
			if !slices.ContainsFunc(out[n], func(u *Type) bool { return u.String() == t.String() }) {
				out[n] = append(out[n], t)
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
	walk(d.Expr)
	return out
}

// Actions are the model's actions as Display names them, sorted, each
// once (a flattened import's m::deposit and q::init are deposit and
// init).
func (m *Model) Actions() []string {
	var out []string
	for _, d := range m.byID {
		if d.Kind == "def" && d.Qualifier == "action" {
			if n := m.Display(d.Name); !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	slices.Sort(out)
	return out
}

// ActionPicks are each action's own choices, as a step of it records
// them: quint run --mbt records every nondet step evaluates, at every
// step, so Nondets(step) holds every action's picks, and a pick of
// another action's decodes that action's choice. An action's own are
// the nondets whose values flow into its call in step (nondet d = ...;
// attempt(d), through lets and the expressions they bind), those of a
// guard or condition the call is under (all { d > 0, attempt(d) }, if
// (d > 0) attempt(d) else ...), and those bound in its body and in what
// it applies. A nondet several calls use is each one's.
//
// unsure names the actions whose picks the step's shape does not let
// the attribution follow: an action applied under an operator other
// than any, all, and, or, if and an action's application (a match, a
// then), one a let binds, a recursive one, and one step does not reach.
// Their picks are Nondets(step), the whole model's: a refusal of a pick
// the action does make is worse than a borrowed pick let through. Every
// action of the model is in own. Nil when step is no definition of m.
func (m *Model) ActionPicks(step string) (own map[string]map[string][]*Type, unsure map[string]bool) {
	d, ok := m.Decl(step)
	if !ok || d.Expr == nil {
		return nil, nil
	}
	a := &attribution{m: m, env: map[int64]nondetSet{}, nondet: map[int64]*Decl{}, own: map[string]nondetSet{}, unsure: map[string]bool{}, stack: map[int64]bool{d.ID: true}}
	body := d.Expr
	if body.Kind == "lambda" {
		body = body.Expr
	}
	a.attribute(body, nondetSet{})
	whole := m.Nondets(step)
	own, unsure = map[string]map[string][]*Type{}, map[string]bool{}
	for _, n := range m.Actions() {
		set, ok := a.own[n]
		if !ok || a.unsure[n] {
			own[n], unsure[n] = whole, true
			continue
		}
		picks := map[string][]*Type{}
		for id := range set {
			nd := a.nondet[id]
			name := m.Display(nd.Name)
			t, _ := m.TypeOf(nd)
			if !slices.ContainsFunc(picks[name], func(u *Type) bool { return u.String() == t.String() }) {
				picks[name] = append(picks[name], t)
			}
		}
		own[n] = picks
	}
	return own, unsure
}

// nondetSet is a set of nondets, by their let's id.
type nondetSet map[int64]bool

func (s nondetSet) union(o nondetSet) nondetSet {
	out := make(nondetSet, len(s)+len(o))
	maps.Copy(out, s)
	maps.Copy(out, o)
	return out
}

// attribution is ActionPicks' walk of step: env says which nondets the
// value of a let or a parameter comes from (a nondet itself, and those
// its set is drawn from), own the nondets each action is found to take.
type attribution struct {
	m      *Model
	env    map[int64]nondetSet
	nondet map[int64]*Decl // the nondets bound, by id
	own    map[string]nondetSet
	unsure map[string]bool
	stack  map[int64]bool // the actions being followed, against recursion
}

// action is the model's action e names or applies, if it does.
func (a *attribution) action(e *Expr) (*Decl, bool) {
	if e == nil || e.Kind != "app" && e.Kind != "name" {
		return nil, false
	}
	r, ok := a.m.refs[e.ID]
	if !ok || r.kind != "def" {
		return nil, false
	}
	d := a.m.byID[r.decl]
	return d, d != nil && d.Kind == "def" && d.Qualifier == "action"
}

// bind records a let's definition in env.
func (a *attribution) bind(d *Decl) {
	from := a.refs(d.Expr)
	if d.Qualifier == "nondet" {
		a.nondet[d.ID] = d
		from = from.union(nondetSet{d.ID: true})
	}
	a.env[d.ID] = from
}

// refs are the nondets e's value comes from: those it names, directly
// or through a let or a parameter env knows.
func (a *attribution) refs(e *Expr) nondetSet {
	out := nondetSet{}
	var walk func(e *Expr)
	walk = func(e *Expr) {
		if e == nil {
			return
		}
		if e.Kind == "let" && e.Opdef != nil {
			a.bind(e.Opdef)
			walk(e.Expr)
			return
		}
		if e.Kind == "app" || e.Kind == "name" {
			if r, ok := a.m.refs[e.ID]; ok && (r.kind == "def" || r.kind == "param") {
				maps.Copy(out, a.env[r.decl])
			}
		}
		for _, x := range e.Args {
			walk(x)
		}
		walk(e.Expr)
	}
	walk(e)
	return out
}

// callsAction reports whether e applies an action (the model's, or one
// a let binds).
func (a *attribution) callsAction(e *Expr) bool {
	found := false
	var walk func(e *Expr)
	walk = func(e *Expr) {
		if e == nil || found {
			return
		}
		if _, ok := a.action(e); ok || e.Kind == "let" && e.Opdef != nil && e.Opdef.Qualifier == "action" {
			found = true
			return
		}
		for _, x := range e.Args {
			walk(x)
		}
		walk(e.Expr)
		if e.Opdef != nil {
			walk(e.Opdef.Expr)
		}
	}
	walk(e)
	return found
}

// unsureIn marks every action e applies, and those they apply, unsure.
func (a *attribution) unsureIn(e *Expr) {
	seen := map[int64]bool{}
	var walk func(e *Expr)
	walk = func(e *Expr) {
		if e == nil {
			return
		}
		if d, ok := a.action(e); ok && !seen[d.ID] {
			seen[d.ID] = true
			a.unsure[a.m.Display(d.Name)] = true
			walk(d.Expr)
		}
		for _, x := range e.Args {
			walk(x)
		}
		walk(e.Expr)
		if e.Opdef != nil {
			walk(e.Opdef.Expr)
		}
	}
	walk(e)
}

// attribute walks an action-valued expression of step, s the nondets
// of the guards and conditions it is under.
func (a *attribution) attribute(e *Expr, s nondetSet) {
	switch {
	case e == nil:
		return
	case e.Kind == "let" && e.Opdef != nil:
		a.bind(e.Opdef)
		if e.Opdef.Qualifier == "action" || a.callsAction(e.Opdef.Expr) {
			a.unsureIn(e.Opdef.Expr) // an action a let binds: where it is applied is not followed
		}
		a.attribute(e.Expr, s)
		return
	}
	if d, ok := a.action(e); ok {
		a.call(d, e.Args, s)
		return
	}
	if !a.callsAction(e) {
		return
	}
	if e.Kind != "app" {
		a.unsureIn(e) // a lambda's body, or a let-bound action applied
		return
	}
	switch e.Opcode {
	case "actionAny", "or":
		for _, x := range e.Args {
			a.attribute(x, s)
		}
	case "actionAll", "and", "ite":
		// the conjuncts (an if's condition) that apply no action guard
		// every call among the others
		inner := s
		for _, x := range e.Args {
			if !a.callsAction(x) {
				inner = inner.union(a.refs(x))
			}
		}
		for _, x := range e.Args {
			if a.callsAction(x) {
				a.attribute(x, inner)
			}
		}
	default:
		a.unsureIn(e)
	}
}

// call attributes an application of action d to args, under s: what
// flows into its arguments, and the nondets bound in its body and in
// what it applies, actions included (a step of d may record any of
// them). The actions d applies take what flows into d, with the
// nondets of d's own guards and lets.
func (a *attribution) call(d *Decl, args []*Expr, s nondetSet) {
	name := a.m.Display(d.Name)
	if a.stack[d.ID] {
		a.unsure[name] = true // recursion: not followed
		return
	}
	a.stack[d.ID] = true
	defer delete(a.stack, d.ID)
	all := s
	argRefs := make([]nondetSet, len(args))
	for i, x := range args {
		argRefs[i] = a.refs(x)
		all = all.union(argRefs[i])
	}
	body := d.Expr
	if body != nil && body.Kind == "lambda" {
		for i, p := range body.Params {
			if i < len(argRefs) {
				a.env[p.ID] = argRefs[i]
			}
		}
		body = body.Expr
	}
	var bound func(e *Expr)
	seen := map[int64]bool{d.ID: true}
	bound = func(e *Expr) {
		if e == nil {
			return
		}
		if e.Kind == "let" && e.Opdef != nil {
			if e.Opdef.Qualifier == "nondet" {
				all = all.union(nondetSet{e.Opdef.ID: true})
				a.nondet[e.Opdef.ID] = e.Opdef
			}
			bound(e.Opdef.Expr)
		}
		if e.Kind == "app" || e.Kind == "name" {
			if r, ok := a.m.refs[e.ID]; ok && r.kind == "def" && !seen[r.decl] {
				seen[r.decl] = true
				if c := a.m.byID[r.decl]; c != nil {
					bound(c.Expr)
				}
			}
		}
		for _, x := range e.Args {
			bound(x)
		}
		bound(e.Expr)
	}
	bound(body)
	a.own[name] = a.own[name].union(all)
	if a.callsAction(body) {
		a.attribute(body, a.own[name])
	}
}

package qeval

import (
	"encoding/json"
	"fmt"
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
	out := &Type{Kind: t.Kind, Name: t.Name, Elem: t.Elem.typ(), Arg: t.Arg.typ(), Res: t.Res.typ()}
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

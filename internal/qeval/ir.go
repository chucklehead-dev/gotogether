package qeval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// An Expr is a node of quint's IR: a name, a literal, an application of
// an operator (a builtin's opcode, or a definition's name, resolved by
// the IR's table), a lambda or a let.
type Expr struct {
	ID        int64       `json:"id"`
	Kind      string      `json:"kind"`
	Name      string      `json:"name"`
	Opcode    string      `json:"opcode"`
	Args      []*Expr     `json:"args"`
	Value     json.Number `json:"value"`
	Bool      bool        `json:"-"`
	Params    []Param     `json:"params"`
	Qualifier string      `json:"qualifier"`
	Expr      *Expr       `json:"expr"`
	Opdef     *Decl       `json:"opdef"`
}

// A Param is a lambda's parameter.
type Param struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// A Decl is a declaration: a var, a const, or a def (its qualifier says
// which: val, def, pureval, puredef, action, nondet).
type Decl struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Qualifier string `json:"qualifier"`
	Expr      *Expr  `json:"expr"`
}

// UnmarshalJSON reads a bool literal's value, which shares "value" with
// numbers and strings.
func (e *Expr) UnmarshalJSON(b []byte) error {
	type plain Expr
	var aux struct {
		plain
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*e = Expr(aux.plain)
	v := aux.Value
	switch {
	case len(v) == 0:
	case bytes.Equal(v, []byte("true")), bytes.Equal(v, []byte("false")):
		e.Bool = bytes.Equal(v, []byte("true"))
	case v[0] == '"':
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return err
		}
		e.Value = json.Number(s) // a string literal's text
	default:
		e.Value = json.Number(v)
	}
	return nil
}

// A Model is a compiled Quint module: its declarations by name and id, its
// state variables, and the table that resolves each name and operator
// application to its declaration.
type Model struct {
	byName map[string]*Decl
	byID   map[int64]*Decl
	// refs resolves a name's or an application's node id to what it names:
	// a declaration's id, or a parameter's name
	refs map[int64]ref
	// Vars are the state variables' names, sorted
	Vars []string
	// Consts are the constants' names (an instance's are pass-through
	// constants of the module that imports the model)
	Consts []string
	// replaying: a choice some of whose values disable the action is
	// Unsupported (AsReplayed)
	replaying bool
	// ren holds the source names of lambda parameters (print.go)
	ren *renames
	// confirm memoizes Unconfirmed (confirm.go)
	confirm *confirmMemo
}

// AsReplayed is m judging steps as a quint replay of them takes each:
// quint's replay picks one value of a choice (nondet x = oneOf(S)) at
// random, not one that enables the action, so a step that some of the
// choice's values disable is a sample of quint's, not a verdict
// (`nondet x = oneOf(1.to(1000)); all { x == 7, v' = x }` passes the
// evaluator and fails quint test); AsReplayed refuses it, as Unsupported,
// where m takes it. A shallow copy: m's declarations are shared, read-only.
func (m *Model) AsReplayed() *Model {
	c := *m
	c.replaying = true
	return &c
}

type ref struct {
	kind  string // "var", "const", "def", "param"
	decl  int64
	param string
}

// Load reads quint's JSON IR (`quint compile --target json --out f`): the
// main module, flattened.
func Load(data []byte) (*Model, error) {
	var ir struct {
		Modules []struct {
			Name         string            `json:"name"`
			Declarations []json.RawMessage `json:"declarations"`
		} `json:"modules"`
		Table  map[string]json.RawMessage `json:"table"`
		Main   string                     `json:"main"`
		Errors []json.RawMessage          `json:"errors"`
	}
	if err := json.Unmarshal(data, &ir); err != nil {
		return nil, fmt.Errorf("qeval: reading quint's IR: %w", err)
	}
	if len(ir.Errors) > 0 {
		return nil, fmt.Errorf("qeval: quint's IR has %d errors: %s", len(ir.Errors), ir.Errors[0])
	}
	if len(ir.Modules) == 0 {
		return nil, fmt.Errorf("qeval: quint's IR has no module")
	}
	mod := ir.Modules[0]
	for _, m := range ir.Modules {
		if m.Name == ir.Main {
			mod = m
		}
	}
	m := &Model{byName: map[string]*Decl{}, byID: map[int64]*Decl{}, refs: map[int64]ref{}, ren: &renames{}, confirm: &confirmMemo{}}
	for _, raw := range mod.Declarations {
		var d Decl
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("qeval: a declaration: %w", err)
		}
		switch d.Kind {
		case "var", "const", "def":
		default:
			continue // types, imports, assumptions
		}
		dd := d
		m.byName[d.Name] = &dd
		m.byID[d.ID] = &dd
		switch d.Kind {
		case "var":
			m.Vars = append(m.Vars, d.Name)
		case "const":
			m.Consts = append(m.Consts, d.Name)
		}
	}
	slices.Sort(m.Vars)
	for id, raw := range ir.Table {
		var t struct {
			ID   int64  `json:"id"`
			Kind string `json:"kind"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("qeval: the IR's table: %w", err)
		}
		var n int64
		if _, err := fmt.Sscan(id, &n); err != nil {
			return nil, fmt.Errorf("qeval: the IR's table: id %q", id)
		}
		r := ref{kind: t.Kind, decl: t.ID}
		if t.Kind == "param" {
			r.param = t.Name
		}
		m.refs[n] = r
	}
	return m, nil
}

// Constructor reports whether tag is a variant constructor of the model's
// (a definition that makes the variant tag: a val for one without a
// payload), not another definition that shares its name (a val BIG = 5).
func (m *Model) Constructor(tag string) bool {
	d, ok := m.Decl(tag)
	if !ok || d.Kind != "def" || d.Expr == nil {
		return false
	}
	e := d.Expr
	if e.Kind == "lambda" {
		e = e.Expr
	}
	return e != nil && e.Kind == "app" && e.Opcode == "variant" && len(e.Args) > 0 && e.Args[0].Kind == "str" && e.Args[0].Value.String() == tag
}

// Decl is the declaration of name: its full name (a::b::c) or, when it is
// none, the shortest declaration's whose name ends in ::name (quint's
// q::init aliases a module's init; a flattened import is m::model::name).
func (m *Model) Decl(name string) (*Decl, bool) {
	if d, ok := m.byName[name]; ok {
		return d, true
	}
	var found *Decl
	for n, d := range m.byName {
		if strings.HasSuffix(n, "::"+name) && (found == nil || len(n) < len(found.Name) || len(n) == len(found.Name) && n < found.Name) {
			found = d
		}
	}
	return found, found != nil
}

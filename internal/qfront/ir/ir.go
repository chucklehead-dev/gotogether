// Package ir is quint's intermediate representation as the Go front end
// builds it (Q2, docs/q2-front-end.md): the modules, declarations,
// expressions and types of quint 0.33.1's quintIr.ts and quintTypes.ts,
// written as quint writes them (`quint parse --out`'s modules). A field
// quint leaves undefined is left out of the JSON, as JSON.stringify
// leaves it out; a field it sets to an empty list is written empty.
package ir

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strconv"
)

// ID is a node's id: quint's bigint ids, numbered from 1 by a generator
// every phase shares.
type ID uint64

// IDGen numbers nodes as quint's newIdGenerator does: the first id is 1.
type IDGen struct{ last ID }

// Next is the next id.
func (g *IDGen) Next() ID {
	g.last++
	return g.last
}

// Last is the last id given (0 before any).
func (g *IDGen) Last() ID { return g.last }

// Reset makes the next id last+1: a parser that speculates takes back
// the ids of a branch it abandons.
func (g *IDGen) Reset(last ID) { g.last = last }

// A Pos is a position in a source: 0-based line and column, and the
// 0-based index of its character (code points, as quint's lexer counts).
type Pos struct {
	Line  int `json:"line"`
	Col   int `json:"col"`
	Index int `json:"index"`
}

// A Loc is where a node came from: its file, its start and, unless the
// node ends nowhere (NoEnd), its end, both inclusive.
type Loc struct {
	Source     string
	Start, End Pos
	NoEnd      bool
}

// A Module is `module name { declarations }`.
type Module struct {
	ID    ID
	Name  string
	Doc   *string
	Decls []*Decl
}

// MarshalJSON writes the module as quint does.
func (m *Module) MarshalJSON() ([]byte, error) {
	o := newObj()
	o.put("id", m.ID)
	o.put("name", m.Name)
	if m.Doc != nil {
		o.put("doc", *m.Doc)
	}
	decls := m.Decls
	if decls == nil {
		decls = []*Decl{}
	}
	o.put("declarations", decls)
	return o.bytes()
}

// Declaration kinds.
const (
	KindVar      = "var"
	KindConst    = "const"
	KindDef      = "def"
	KindAssume   = "assume"
	KindTypeDef  = "typedef"
	KindImport   = "import"
	KindExport   = "export"
	KindInstance = "instance"
)

// A Decl is a module's declaration, or a let's definition (kind def).
// Which fields are written depends on its kind.
type Decl struct {
	ID   ID
	Kind string
	// Name is a var's, const's, def's, assume's or typedef's.
	Name string
	// Qualifier is a def's: val, def, pureval, puredef, action, run,
	// temporal or nondet.
	Qualifier string
	// Expr is a def's body; Assumption an assume's.
	Expr, Assumption *Expr
	// TypeAnnotation is a var's, const's, or an annotated def's.
	TypeAnnotation *Type
	// Type and Params are a typedef's (Type nil for an abstract one;
	// Params nil when it has none).
	Type   *Type
	Params []string
	// Doc is the declaration's `///` comment, Depth quint's name
	// collector's (it sets 0 on a top-level var, const, typedef and
	// assume it collects).
	Doc   *string
	Depth *int
	// DefName, ProtoName, QualifiedName and FromSource are an import's,
	// export's or instance's, nil where quint leaves them undefined.
	DefName, ProtoName, QualifiedName, FromSource *string
	// Overrides and IdentityOverride are an instance's.
	Overrides        []Override
	IdentityOverride bool
}

// An Override is an instance's `name = expr`.
type Override struct {
	Param *Param
	Expr  *Expr
}

// MarshalJSON writes the declaration as quint does.
func (d *Decl) MarshalJSON() ([]byte, error) {
	o := newObj()
	if d.Doc != nil {
		o.put("doc", *d.Doc)
	}
	o.put("id", d.ID)
	o.put("kind", d.Kind)
	switch d.Kind {
	case KindVar, KindConst:
		o.put("name", d.Name)
		o.put("typeAnnotation", d.TypeAnnotation)
	case KindDef:
		o.put("name", d.Name)
		o.put("qualifier", d.Qualifier)
		o.put("expr", d.Expr)
		if d.TypeAnnotation != nil {
			o.put("typeAnnotation", d.TypeAnnotation)
		}
	case KindAssume:
		o.put("name", d.Name)
		o.put("assumption", d.Assumption)
	case KindTypeDef:
		o.put("name", d.Name)
		if d.Type != nil {
			o.put("type", d.Type)
		}
		if d.Params != nil {
			o.put("params", d.Params)
		}
	case KindImport, KindExport:
		o.putStr("defName", d.DefName)
		o.putStr("protoName", d.ProtoName)
		o.putStr("qualifiedName", d.QualifiedName)
		if d.Kind == KindImport {
			o.putStr("fromSource", d.FromSource)
		}
	case KindInstance:
		o.putStr("qualifiedName", d.QualifiedName)
		o.putStr("protoName", d.ProtoName)
		ov := make([][2]any, len(d.Overrides))
		for i, x := range d.Overrides {
			ov[i] = [2]any{x.Param, x.Expr}
		}
		o.put("overrides", ov)
		o.put("identityOverride", d.IdentityOverride)
		o.putStr("fromSource", d.FromSource)
	}
	if d.Depth != nil {
		o.put("depth", *d.Depth)
	}
	return o.bytes()
}

// Expression kinds.
const (
	ExName   = "name"
	ExBool   = "bool"
	ExInt    = "int"
	ExStr    = "str"
	ExApp    = "app"
	ExLambda = "lambda"
	ExLet    = "let"
)

// An Expr is an expression. Which fields are written depends on its kind.
type Expr struct {
	ID   ID
	Kind string
	// Name is a name's.
	Name string
	// Bool, Int and Str are the literals' values.
	Bool bool
	Int  *big.Int
	Str  string
	// Opcode and Args are an application's.
	Opcode string
	Args   []*Expr
	// Params and Qualifier are a lambda's; Body a lambda's or a let's
	// expression.
	Params    []*Param
	Qualifier string
	Body      *Expr
	// Opdef is a let's definition.
	Opdef *Decl
}

// MarshalJSON writes the expression as quint does.
func (e *Expr) MarshalJSON() ([]byte, error) {
	o := newObj()
	o.put("id", e.ID)
	o.put("kind", e.Kind)
	switch e.Kind {
	case ExName:
		o.put("name", e.Name)
	case ExBool:
		o.put("value", e.Bool)
	case ExInt:
		o.raw("value", []byte(e.Int.String()))
	case ExStr:
		o.put("value", e.Str)
	case ExApp:
		o.put("opcode", e.Opcode)
		args := e.Args
		if args == nil {
			args = []*Expr{}
		}
		o.put("args", args)
	case ExLambda:
		o.put("params", e.Params)
		o.put("qualifier", e.Qualifier)
		o.put("expr", e.Body)
	case ExLet:
		o.put("opdef", e.Opdef)
		o.put("expr", e.Body)
	}
	return o.bytes()
}

// A Param is a lambda's or an operator's parameter.
type Param struct {
	ID             ID
	Name           string
	TypeAnnotation *Type
}

// MarshalJSON writes the parameter as quint does.
func (p *Param) MarshalJSON() ([]byte, error) {
	o := newObj()
	o.put("id", p.ID)
	o.put("name", p.Name)
	if p.TypeAnnotation != nil {
		o.put("typeAnnotation", p.TypeAnnotation)
	}
	return o.bytes()
}

// Type kinds.
const (
	TyBool  = "bool"
	TyInt   = "int"
	TyStr   = "str"
	TyConst = "const"
	TyVar   = "var"
	TySet   = "set"
	TyList  = "list"
	TyFun   = "fun"
	TyOper  = "oper"
	TyTup   = "tup"
	TyRec   = "rec"
	TySum   = "sum"
	TyApp   = "app"
)

// A Type is a type. An operator's annotation that quint builds itself
// has no id (NoID).
type Type struct {
	ID   ID
	NoID bool
	Kind string
	// Name is a const's or var's.
	Name string
	// Elem is a set's or list's; Arg and Res a function's; Args and Res
	// an operator's; Ctor and Args an application's.
	Elem, Arg, Res, Ctor *Type
	Args                 []*Type
	// Fields is a tuple's, record's or sum's row.
	Fields *Row
}

// MarshalJSON writes the type as quint does.
func (t *Type) MarshalJSON() ([]byte, error) {
	o := newObj()
	if !t.NoID {
		o.put("id", t.ID)
	}
	o.put("kind", t.Kind)
	switch t.Kind {
	case TyConst, TyVar:
		o.put("name", t.Name)
	case TySet, TyList:
		o.put("elem", t.Elem)
	case TyFun:
		o.put("arg", t.Arg)
		o.put("res", t.Res)
	case TyOper:
		o.put("args", orEmptyTypes(t.Args))
		o.put("res", t.Res)
	case TyTup, TyRec, TySum:
		o.put("fields", t.Fields)
	case TyApp:
		o.put("ctor", t.Ctor)
		o.put("args", orEmptyTypes(t.Args))
	}
	return o.bytes()
}

func orEmptyTypes(ts []*Type) []*Type {
	if ts == nil {
		return []*Type{}
	}
	return ts
}

// Row kinds.
const (
	RowRow   = "row"
	RowEmpty = "empty"
	RowVar   = "var"
)

// A Row is a record's, tuple's or sum's fields: a row of fields and the
// rest (Other), the empty row, or a row variable.
type Row struct {
	Kind   string
	Fields []RowField
	Other  *Row
	Name   string
}

// A RowField is a row's field.
type RowField struct {
	FieldName string `json:"fieldName"`
	FieldType *Type  `json:"fieldType"`
}

// MarshalJSON writes the row as quint does.
func (r *Row) MarshalJSON() ([]byte, error) {
	o := newObj()
	o.put("kind", r.Kind)
	switch r.Kind {
	case RowRow:
		fs := r.Fields
		if fs == nil {
			fs = []RowField{}
		}
		o.put("fields", fs)
		o.put("other", r.Other)
	case RowVar:
		o.put("name", r.Name)
	}
	return o.bytes()
}

// obj writes a JSON object with its keys in the order put, as quint's
// objects are written.
type obj struct {
	b   bytes.Buffer
	err error
}

func newObj() *obj {
	o := &obj{}
	o.b.WriteByte('{')
	return o
}

func (o *obj) key(k string) {
	if o.b.Len() > 1 {
		o.b.WriteByte(',')
	}
	o.b.WriteString(strconv.Quote(k))
	o.b.WriteByte(':')
}

func (o *obj) put(k string, v any) {
	b, err := json.Marshal(v)
	if err != nil && o.err == nil {
		o.err = err
	}
	o.raw(k, b)
}

func (o *obj) putStr(k string, v *string) {
	if v != nil {
		o.put(k, *v)
	}
}

func (o *obj) raw(k string, b []byte) {
	o.key(k)
	o.b.Write(b)
}

func (o *obj) bytes() ([]byte, error) {
	o.b.WriteByte('}')
	return o.b.Bytes(), o.err
}

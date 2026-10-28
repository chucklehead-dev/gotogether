package binding

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/chucklehead-dev/gotogether/itf"
)

// Schema is the types of the model's variables and nondet picks, for
// reading its traces (itf.ParseWith): quint 0.33's Rust backend writes an
// integer of 16 characters or more as a bignumber.js object, which only
// its type tells from a record of fields s, e and c. A variable or pick
// whose type the typecheck did not give (or two picks of one name and
// other types) is absent, so that such an object in it fails the read.
// A Model made by hand has none.
func (m *Model) Schema() *itf.Schema {
	if m == nil || m.schema == nil {
		return &itf.Schema{}
	}
	return m.schema
}

// schemaOf is module's Schema from its typecheck: every module's
// variables and picks, module's own last so that its names win.
func schemaOf(tc *tcOut, module string) *itf.Schema {
	scopes := typeScopes(tc)
	s := &itf.Schema{Vars: map[string]*itf.Type{}, Picks: map[string]*itf.Type{}}
	picks := map[string]string{} // a pick's type, as written: two of one name and other types are no type
	var order []int              // module's own last
	for i, mod := range tc.Modules {
		if mod.Name != module {
			order = append(order, i)
		}
	}
	for i, mod := range tc.Modules {
		if mod.Name == module {
			order = append(order, i)
		}
	}
	for _, i := range order {
		mod := tc.Modules[i]
		for _, d := range mod.Declarations {
			switch {
			case d.Kind == "var":
				if t, err := irType(d.Type, scopes[mod.Name], nil, 0); err == nil {
					s.Vars[d.Name] = t.itfType()
				} else {
					delete(s.Vars, d.Name)
				}
			case d.Kind == "def" && (d.Qualifier == "action" || d.Qualifier == "run"):
				for _, n := range nondetsIn(d.RawExpr) {
					var t *qtype
					if tt, ok := tc.Types[n.id]; ok {
						t, _ = irType(tt.Type, scopes[mod.Name], nil, 0)
					}
					text := "?"
					if t != nil {
						text = t.String()
					}
					if prev, ok := picks[n.name]; ok && prev != text {
						text = "?"
					}
					picks[n.name] = text
					if text == "?" {
						delete(s.Picks, n.name)
						continue
					}
					s.Picks[n.name] = t.itfType()
				}
			}
		}
	}
	return s
}

type nondet struct{ name, id string }

// nondetsIn are the nondet bindings of an action's IR, with their ids.
func nondetsIn(raw json.RawMessage) []nondet {
	var out []nondet
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["kind"] == "let" {
				if d, ok := x["opdef"].(map[string]any); ok && d["qualifier"] == "nondet" {
					n, _ := d["name"].(string)
					id, _ := d["id"].(json.Number)
					out = append(out, nondet{n, id.String()})
				}
			}
			for _, k := range slices.Sorted(maps.Keys(x)) {
				walk(x[k])
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) == nil {
		walk(v)
	}
	return out
}

// itfType is t as itf reads by it: aliases resolved, a type variable not
// known.
func (t *qtype) itfType() *itf.Type {
	r := t.resolved()
	if r == nil {
		return nil
	}
	switch r.kind {
	case "int":
		return &itf.Type{Kind: itf.Int}
	case "str":
		return &itf.Type{Kind: itf.Str}
	case "bool":
		return &itf.Type{Kind: itf.Bool}
	case "set":
		return &itf.Type{Kind: itf.Set, Elem: r.elem.itfType()}
	case "list":
		return &itf.Type{Kind: itf.List, Elem: r.elem.itfType()}
	case "map":
		return &itf.Type{Kind: itf.Map, Elem: r.elem.itfType(), Value: r.val.itfType()}
	case "tup":
		out := &itf.Type{Kind: itf.Tuple}
		for _, f := range r.fields {
			out.Elems = append(out.Elems, f.typ.itfType())
		}
		return out
	case "rec", "sum":
		out := &itf.Type{Kind: itf.Record, Fields: map[string]*itf.Type{}}
		if r.kind == "sum" {
			out.Kind = itf.Variant
		}
		for _, f := range r.fields {
			out.Fields[f.name] = f.typ.itfType()
		}
		return out
	}
	return &itf.Type{} // a type variable: not known
}

// TraceReader reads the ITF traces quint wrote of Spec's Module (quint
// the binary, "" for "quint"): at the first that holds an object only
// the model's types tell from a record (itf.AmbiguousError: quint's Rust
// backend's integer of 16 characters or more), it typechecks the model,
// once, and reads that trace and every later one by its Schema.
type TraceReader struct {
	Quint, Spec, Module string
	schema              *itf.Schema
}

// Read reads the trace at path.
func (r *TraceReader) Read(path string) (*itf.Trace, error) {
	tr, err := itf.ReadFileWith(path, r.schema)
	if _, ok := errors.AsType[*itf.AmbiguousError](err); !ok || r.schema != nil {
		return tr, err
	}
	m, merr := LoadModel(r.Quint, r.Spec, r.Module)
	if merr != nil {
		return nil, fmt.Errorf("%w (typechecking the model for its types: %v)", err, merr)
	}
	r.schema = m.Schema()
	return itf.ReadFileWith(path, r.schema)
}

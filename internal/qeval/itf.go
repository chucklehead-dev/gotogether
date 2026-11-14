package qeval

import (
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/itf"
)

// FromITF is an ITF value (a trace's, or a Quint literal parsed by
// itf.ParseQuint) as a Value.
func FromITF(v itf.Value) (Value, error) {
	elems := func() ([]Value, error) {
		out := make([]Value, len(v.Elems))
		for i, e := range v.Elems {
			x, err := FromITF(e)
			if err != nil {
				return nil, err
			}
			out[i] = x
		}
		return out, nil
	}
	switch v.Kind {
	case itf.Int:
		if !v.I.IsInt64() {
			return nil, &Unsupported{Op: "int", Why: fmt.Sprintf("%s past 64 bits", v.I)}
		}
		return Int(v.I.Int64()), nil
	case itf.Bool:
		return Bool(v.B), nil
	case itf.Str:
		return Str(v.S), nil
	case itf.List:
		es, err := elems()
		return List(es), err
	case itf.Tuple:
		es, err := elems()
		return Tup(es), err
	case itf.Set:
		es, err := elems()
		if err != nil {
			return nil, err
		}
		return NewSet(es), nil
	case itf.Map:
		ks, vs := make([]Value, len(v.Pairs)), make([]Value, len(v.Pairs))
		for i, p := range v.Pairs {
			k, err := FromITF(p[0])
			if err != nil {
				return nil, err
			}
			x, err := FromITF(p[1])
			if err != nil {
				return nil, err
			}
			ks[i], vs[i] = k, x
		}
		return NewMap(ks, vs), nil
	case itf.Record:
		names := slices.Sorted(maps.Keys(v.Fields))
		vals := make([]Value, len(names))
		for i, n := range names {
			x, err := FromITF(v.Fields[n])
			if err != nil {
				return nil, err
			}
			vals[i] = x
		}
		return &Rec{Names: names, Vals: vals}, nil
	case itf.Variant:
		inner := Value(Tup{})
		if v.Inner != nil {
			x, err := FromITF(*v.Inner)
			if err != nil {
				return nil, err
			}
			inner = x
		}
		return Variant{Label: v.Tag, Val: inner}, nil
	}
	return nil, fmt.Errorf("qeval: an ITF value of kind %d", v.Kind)
}

// StateOf is an ITF state (quint's) as the model's State: each of its
// variables by its qualified name, else its name's last segment, as ITF
// names them.
func StateOf(m *Model, st itf.State) (State, error) {
	out := State{}
	for _, v := range m.Vars {
		short := v[strings.LastIndex(v, ":")+1:]
		x, ok := st.Vars[v] // qualified, where two modules' variables share a name
		if !ok {
			x, ok = st.Vars[short]
		}
		if !ok {
			return nil, fmt.Errorf("qeval: the ITF state has no %s", short)
		}
		val, err := FromITF(x)
		if err != nil {
			return nil, err
		}
		out[v] = val
	}
	return out, nil
}

// ToITF is a Value as an ITF value, FromITF's inverse: what a trace the
// evaluator found (a graph's path) carries, as quint would write it. An
// operator or an infinite set has no ITF form.
func ToITF(v Value) (itf.Value, error) {
	elems := func(vs []Value) ([]itf.Value, error) {
		out := make([]itf.Value, len(vs))
		for i, e := range vs {
			x, err := ToITF(e)
			if err != nil {
				return nil, err
			}
			out[i] = x
		}
		return out, nil
	}
	switch x := v.(type) {
	case Int:
		return itf.Value{Kind: itf.Int, I: big.NewInt(int64(x))}, nil
	case Bool:
		return itf.Value{Kind: itf.Bool, B: bool(x)}, nil
	case Str:
		return itf.Value{Kind: itf.Str, S: string(x)}, nil
	case List:
		es, err := elems(x)
		return itf.Value{Kind: itf.List, Elems: es}, err
	case Tup:
		es, err := elems(x)
		return itf.Value{Kind: itf.Tuple, Elems: es}, err
	case *Set:
		es, err := elems(x.Elems)
		return itf.Value{Kind: itf.Set, Elems: es}, err
	case *Map:
		pairs := make([][2]itf.Value, len(x.Keys))
		for i := range x.Keys {
			k, err := ToITF(x.Keys[i])
			if err != nil {
				return itf.Value{}, err
			}
			val, err := ToITF(x.Vals[i])
			if err != nil {
				return itf.Value{}, err
			}
			pairs[i] = [2]itf.Value{k, val}
		}
		return itf.Value{Kind: itf.Map, Pairs: pairs}, nil
	case *Rec:
		fields := make(map[string]itf.Value, len(x.Names))
		for i, n := range x.Names {
			f, err := ToITF(x.Vals[i])
			if err != nil {
				return itf.Value{}, err
			}
			fields[n] = f
		}
		return itf.Value{Kind: itf.Record, Fields: fields}, nil
	case Variant:
		inner, err := ToITF(x.Val)
		if err != nil {
			return itf.Value{}, err
		}
		return itf.Value{Kind: itf.Variant, Tag: x.Label, Inner: &inner}, nil
	}
	return itf.Value{}, fmt.Errorf("qeval: %s has no ITF form", short(String(v)))
}

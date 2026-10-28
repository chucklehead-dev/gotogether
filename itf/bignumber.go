package itf

import (
	"encoding/json"
	"fmt"
	"math/big"
)

// Type is a Quint type, as far as reading a trace needs it: what an
// object of bignumber.js's form is (an integer, where Kind is Int, or a
// record). binding.Model.Schema gives the model's.
type Type struct {
	// Kind is the type's: Int, Bool, Str, List, Set, Map, Tuple, Record or
	// Variant (a sum type); Invalid where it is not known (a type variable).
	Kind Kind
	// Elem is a List's or a Set's element type, and a Map's key type.
	Elem *Type
	// Value is a Map's value type.
	Value *Type
	// Elems are a Tuple's element types, in order.
	Elems []*Type
	// Fields are a Record's field types, and a Variant's payload types by
	// tag (an empty Tuple for a tag with none).
	Fields map[string]*Type
}

// Schema is the types of a trace's values: its variables', by their
// names without module qualifiers, and its nondet picks', by the names
// of their nondet bindings. A name it lacks has no known type.
type Schema struct {
	Vars  map[string]*Type
	Picks map[string]*Type
}

// picks is the type of mbt::nondetPicks: a record of each pick's Option.
func (s *Schema) picks() *Type {
	if s == nil || len(s.Picks) == 0 {
		return nil
	}
	t := &Type{Kind: Record, Fields: map[string]*Type{}}
	for name, p := range s.Picks {
		t.Fields[name] = &Type{Kind: Variant, Fields: map[string]*Type{"Some": p, "None": {Kind: Tuple}}}
	}
	return t
}

// the types within t, nil where t (nil: not known) says nothing of them

func (t *Type) elem(k Kind) *Type {
	if t == nil || t.Kind != k {
		return nil
	}
	return t.Elem
}

func (t *Type) tupleElem(i int) *Type {
	if t == nil || t.Kind != Tuple || i >= len(t.Elems) {
		return nil
	}
	return t.Elems[i]
}

func (t *Type) mapTypes() (*Type, *Type) {
	if t == nil || t.Kind != Map {
		return nil, nil
	}
	return t.Elem, t.Value
}

func (t *Type) payload(tag string) *Type {
	if t == nil || t.Kind != Variant {
		return nil
	}
	return t.Fields[tag]
}

func (t *Type) field(name string) *Type {
	if t == nil || t.Kind != Record {
		return nil
	}
	return t.Fields[name]
}

// AmbiguousError is an object of bignumber.js's form, which quint 0.33's
// Rust backend writes for an integer of 16 characters or more, where the
// reader knows no type to tell it from a record of fields s, e and c.
type AmbiguousError struct {
	State int    // the state's index
	Var   string // the variable's name, or mbt::nondetPicks
	Path  string // where within the variable's value (".balance", "[2]", ".#map[0][1]"), "" for the whole value
	JSON  string // the object, as JSON
	Int   *big.Int
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("state %d: %s%s is %s: the integer %s in bignumber.js's form, which quint 0.33's Rust backend writes "+
		"for an integer of 16 characters or more (not ITF's {\"#bigint\": ...}), or a record of fields c, e and s, and nothing gives its type to tell. "+
		"Read the trace with the model's types (itf.ParseWith or ReadFileWith, with binding.Model.Schema), "+
		"or have quint write it with --backend typescript",
		e.State, e.Var, e.Path, e.JSON, e.Int)
}

// limb is bignumber.js's base, 1e14: a coefficient's digits in chunks of
// 14 (its LOG_BASE). Read-only.
var limb = big.NewInt(100_000_000_000_000)

// bigNumber reads an object of bignumber.js's form ({s: sign, e:
// exponent, c: coefficient}) as the integer it is, false for any object
// that is not one. bignumber.js's value is s × d.ddd… × 10^e, where the
// digits d are c's limbs in base 1e14, the first holding e%14+1 of them
// (a whole limb for every 14 places past it) and trailing zero limbs
// dropped; it is an integer when every limb is left of the point. A
// non-integer (e < 0, or limbs past the point), a limb out of range, a
// leading limb with other than e%14+1 digits or a trailing zero limb is
// not a value bignumber.js writes, and so a record.
func bigNumber(v map[string]any) (*big.Int, bool) {
	if len(v) != 3 {
		return nil, false
	}
	cs, ok := v["c"].([]any)
	if !ok || len(cs) == 0 {
		return nil, false
	}
	small := func(x any) (*big.Int, bool) { // a number, as JSON or #bigint
		switch y := x.(type) {
		case json.Number, float64:
		case map[string]any:
			if _, ok := y["#bigint"].(string); !ok || len(y) != 1 {
				return nil, false
			}
		default:
			return nil, false
		}
		val, err := fromAny(x)
		if err != nil || val.Kind != Int {
			return nil, false
		}
		return val.I, true
	}
	s, ok1 := small(v["s"])
	e, ok2 := small(v["e"])
	if !ok1 || !ok2 || (s.Cmp(big.NewInt(1)) != 0 && s.Cmp(big.NewInt(-1)) != 0) || e.Sign() < 0 || !e.IsInt64() {
		return nil, false
	}
	exp := e.Int64()
	whole := exp/14 + 1 // limbs left of the point
	if int64(len(cs)) > whole {
		return nil, false
	}
	n := new(big.Int)
	for i, x := range cs {
		d, ok := small(x)
		if !ok || d.Sign() < 0 || d.Cmp(limb) >= 0 {
			return nil, false
		}
		switch {
		case i == 0 && d.Sign() == 0:
			if len(cs) != 1 || exp != 0 { // zero is c [0], e 0
				return nil, false
			}
		case i == 0 && int64(len(d.String())) != exp%14+1:
			return nil, false
		case i > 0 && i == len(cs)-1 && d.Sign() == 0:
			return nil, false
		}
		n.Mul(n, limb).Add(n, d)
	}
	n.Mul(n, new(big.Int).Exp(limb, big.NewInt(whole-int64(len(cs))), nil))
	return n.Mul(n, s), true
}

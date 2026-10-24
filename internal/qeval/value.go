// Package qeval evaluates Quint models in Go, over the IR quint compiles
// them to (`quint compile --target json`): a model's actions stepped from
// a state held in memory, its vals and defs evaluated in a state, as
// quint's own evaluators do, for the fragment of Quint the toolkit's
// models use. What it cannot evaluate (an operator outside the fragment,
// an action with several successors, an integer past 64 bits) it refuses
// with an *Unsupported, and the caller asks quint instead: it is a
// faster oracle for searches, never the only one.
package qeval

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// A Value is a Quint value: Int, Bool, Str, *Set, *Map, *Rec, Tup, List,
// Variant, or a *Lambda (an operator passed as an argument).
type Value interface {
	rank() int // the kind's place in the order of values
}

// Int is a Quint integer, in 64 bits: an overflow is Unsupported.
type Int int64

// Bool is a Quint Boolean.
type Bool bool

// Str is a Quint string.
type Str string

// A Set is a finite set, its elements sorted by Compare and distinct.
type Set struct{ Elems []Value }

// A Map is a finite map, its keys sorted by Compare.
type Map struct {
	Keys, Vals []Value
}

// A Rec is a record, its fields sorted by name.
type Rec struct {
	Names []string
	Vals  []Value
}

// A Tup is a tuple.
type Tup []Value

// A List is a list.
type List []Value

// A Variant is a sum type's value: its label and payload (an empty Tup for
// none).
type Variant struct {
	Label string
	Val   Value
}

// A Lambda is an operator value: its parameters and body, closed over the
// environment it was made in.
type Lambda struct {
	params []string
	body   *Expr
	env    *env
}

// an infinite set (Int, Nat): only membership is defined
type infinite struct{ nat bool }

func (Int) rank() int      { return 0 }
func (Bool) rank() int     { return 1 }
func (Str) rank() int      { return 2 }
func (*Set) rank() int     { return 3 }
func (*Map) rank() int     { return 4 }
func (*Rec) rank() int     { return 5 }
func (Tup) rank() int      { return 6 }
func (List) rank() int     { return 7 }
func (Variant) rank() int  { return 8 }
func (*Lambda) rank() int  { return 9 }
func (infinite) rank() int { return 10 }

// Compare orders values totally: by kind, then by content. Equal values
// compare 0, so a set of values is a sorted slice and equality is
// Compare(a, b) == 0.
func Compare(a, b Value) int {
	if ra, rb := a.rank(), b.rank(); ra != rb {
		return ra - rb
	}
	switch x := a.(type) {
	case Int:
		y := b.(Int)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	case Bool:
		y := b.(Bool)
		switch {
		case x == y:
			return 0
		case !bool(x):
			return -1
		}
		return 1
	case Str:
		return strings.Compare(string(x), string(b.(Str)))
	case *Set:
		return compareSeq(x.Elems, b.(*Set).Elems)
	case *Map:
		y := b.(*Map)
		if c := compareSeq(x.Keys, y.Keys); c != 0 {
			return c
		}
		return compareSeq(x.Vals, y.Vals)
	case *Rec:
		y := b.(*Rec)
		if c := slices.Compare(x.Names, y.Names); c != 0 {
			return c
		}
		return compareSeq(x.Vals, y.Vals)
	case Tup:
		return compareSeq(x, b.(Tup))
	case List:
		return compareSeq(x, b.(List))
	case Variant:
		y := b.(Variant)
		if c := strings.Compare(x.Label, y.Label); c != 0 {
			return c
		}
		return Compare(x.Val, y.Val)
	case *Lambda:
		y := b.(*Lambda)
		if x == y {
			return 0
		}
		return strings.Compare(fmt.Sprintf("%p", x), fmt.Sprintf("%p", y)) // operators compare by identity
	case infinite:
		y := b.(infinite)
		switch {
		case x.nat == y.nat:
			return 0
		case x.nat:
			return -1
		}
		return 1
	}
	panic(fmt.Sprintf("qeval: no order of %T", a))
}

func compareSeq(a, b []Value) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	return len(a) - len(b)
}

// NewSet is the set of vs, sorted and deduplicated.
func NewSet(vs []Value) *Set {
	out := slices.Clone(vs)
	slices.SortFunc(out, Compare)
	out = slices.CompactFunc(out, func(a, b Value) bool { return Compare(a, b) == 0 })
	return &Set{out}
}

func (s *Set) has(v Value) bool {
	_, ok := slices.BinarySearchFunc(s.Elems, v, Compare)
	return ok
}

// NewMap is the map of keys to vals (a later key wins).
func NewMap(keys, vals []Value) *Map {
	idx := make([]int, len(keys))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(i, j int) int { return Compare(keys[i], keys[j]) })
	m := &Map{}
	for _, i := range idx {
		if n := len(m.Keys); n > 0 && Compare(m.Keys[n-1], keys[i]) == 0 {
			m.Vals[n-1] = vals[i]
			continue
		}
		m.Keys, m.Vals = append(m.Keys, keys[i]), append(m.Vals, vals[i])
	}
	return m
}

func (m *Map) find(k Value) (int, bool) { return slices.BinarySearchFunc(m.Keys, k, Compare) }

// put is m with k set to v.
func (m *Map) put(k, v Value) *Map {
	i, ok := m.find(k)
	out := &Map{Keys: slices.Clone(m.Keys), Vals: slices.Clone(m.Vals)}
	if ok {
		out.Vals[i] = v
		return out
	}
	out.Keys, out.Vals = slices.Insert(out.Keys, i, k), slices.Insert(out.Vals, i, v)
	return out
}

// NewRec is the record of names to vals.
func NewRec(names []string, vals []Value) *Rec {
	idx := make([]int, len(names))
	for i := range idx {
		idx[i] = i
	}
	slices.SortFunc(idx, func(i, j int) int { return strings.Compare(names[i], names[j]) })
	r := &Rec{}
	for _, i := range idx {
		r.Names, r.Vals = append(r.Names, names[i]), append(r.Vals, vals[i])
	}
	return r
}

func (r *Rec) field(name string) (Value, bool) {
	i, ok := slices.BinarySearch(r.Names, name)
	if !ok {
		return nil, false
	}
	return r.Vals[i], true
}

// String is v as a Quint literal (sets and maps in order).
func String(v Value) string {
	var b strings.Builder
	write(&b, v)
	return b.String()
}

func write(b *strings.Builder, v Value) {
	seq := func(open string, vs []Value, close string) {
		b.WriteString(open)
		for i, e := range vs {
			if i > 0 {
				b.WriteString(", ")
			}
			write(b, e)
		}
		b.WriteString(close)
	}
	switch x := v.(type) {
	case Int:
		b.WriteString(strconv.FormatInt(int64(x), 10))
	case Bool:
		b.WriteString(strconv.FormatBool(bool(x)))
	case Str:
		b.WriteString(strconv.Quote(string(x)))
	case *Set:
		seq("Set(", x.Elems, ")")
	case *Map:
		b.WriteString("Map(")
		for i := range x.Keys {
			if i > 0 {
				b.WriteString(", ")
			}
			write(b, x.Keys[i])
			b.WriteString(" -> ")
			write(b, x.Vals[i])
		}
		b.WriteString(")")
	case *Rec:
		b.WriteString("{ ")
		for i, n := range x.Names {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(n + ": ")
			write(b, x.Vals[i])
		}
		b.WriteString(" }")
	case Tup:
		seq("(", x, ")")
	case List:
		seq("[", x, "]")
	case Variant:
		b.WriteString(x.Label)
		if t, ok := x.Val.(Tup); !ok || len(t) > 0 {
			b.WriteString("(")
			write(b, x.Val)
			b.WriteString(")")
		}
	case *Lambda:
		b.WriteString("<operator>")
	case infinite:
		if x.nat {
			b.WriteString("Nat")
		} else {
			b.WriteString("Int")
		}
	}
}

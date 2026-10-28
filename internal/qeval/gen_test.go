package qeval

// The generator of the differential test (differential_test.go):
// well-typed random Quint expressions and actions over the builtins the
// evaluator supports, as trees it renders and shrinks. Hand-rolled on
// math/rand/v2, so the core takes no dependency on Hegel; a seed makes
// one batch, the same on every machine.

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
)

type gkind int

const (
	kInt gkind = iota
	kBool
	kStr
	kVariant // the batch's sum type V = VA(int) | VB(bool) | VC
	kSet
	kList
	kMap
	kTup
	kRec
)

// A gtyp is a Quint type of the generator's: a scalar, V, or a
// collection of them.
type gtyp struct {
	k     gkind
	elem  *gtyp   // a set's or a list's; a map's key
	val   *gtyp   // a map's value
	items []*gtyp // a tuple's, a record's fields'
	names []string
}

var (
	tInt  = &gtyp{k: kInt}
	tBool = &gtyp{k: kBool}
	tStr  = &gtyp{k: kStr}
	tV    = &gtyp{k: kVariant}
)

func setOfT(t *gtyp) *gtyp  { return &gtyp{k: kSet, elem: t} }
func listOfT(t *gtyp) *gtyp { return &gtyp{k: kList, elem: t} }

func (t *gtyp) String() string {
	switch t.k {
	case kInt:
		return "int"
	case kBool:
		return "bool"
	case kStr:
		return "str"
	case kVariant:
		return "V"
	case kSet:
		return "Set[" + t.elem.String() + "]"
	case kList:
		return "List[" + t.elem.String() + "]"
	case kMap:
		return "(" + t.elem.String() + " -> " + t.val.String() + ")"
	case kTup:
		var s []string
		for _, x := range t.items {
			s = append(s, x.String())
		}
		return "(" + strings.Join(s, ", ") + ")"
	case kRec:
		var s []string
		for i, x := range t.items {
			s = append(s, t.names[i]+": "+x.String())
		}
		return "{ " + strings.Join(s, ", ") + " }"
	}
	panic("no type")
}

func sameType(a, b *gtyp) bool { return a.String() == b.String() }

// A gnode is an expression of type t, or an action (t nil). kids[i] is
// evaluated with binds[i] bound (a lambda's parameters, a let's name, a
// match case's payload, a nondet's choice).
type gnode struct {
	t     *gtyp
	op    string
	lit   string   // a literal's text, a name, a field name, a tuple index, an assigned variable
	names []string // a record's field names (op rec, spread: the field set)
	kids  []*gnode
	binds [][]string
}

func (n *gnode) bindsOf(i int) []string {
	if i < len(n.binds) {
		return n.binds[i]
	}
	return nil
}

func (n *gnode) size() int {
	s := 1
	for _, k := range n.kids {
		s += k.size()
	}
	return s
}

func (n *gnode) clone() *gnode {
	c := *n
	c.kids = make([]*gnode, len(n.kids))
	for i, k := range n.kids {
		c.kids[i] = k.clone()
	}
	return &c
}

// free adds the names n reads that it does not bind.
func (n *gnode) free(out map[string]bool) {
	n.freeIn(nil, out)
}

func (n *gnode) freeIn(bound []string, out map[string]bool) {
	if n.op == "name" && !slices.Contains(bound, n.lit) {
		out[n.lit] = true
	}
	for i, k := range n.kids {
		k.freeIn(append(slices.Clone(bound), n.bindsOf(i)...), out)
	}
}

// ---- rendering ----------------------------------------------------------

func (n *gnode) String() string {
	var b strings.Builder
	n.render(&b)
	return b.String()
}

func (n *gnode) render(b *strings.Builder) {
	k := func(i int) string { return n.kids[i].String() }
	recv := func() string { return "(" + k(0) + ")" } // a method's receiver: -1.to(3) is -(1.to(3))
	list := func(from int, sep string) string {
		var s []string
		for _, x := range n.kids[from:] {
			s = append(s, x.String())
		}
		return strings.Join(s, sep)
	}
	lam := func(i int) string {
		ps := n.bindsOf(i)
		if len(ps) == 1 {
			return ps[0] + " => " + k(i)
		}
		return "(" + strings.Join(ps, ", ") + ") => " + k(i)
	}
	switch n.op {
	case "lit", "name":
		b.WriteString(n.lit)
	case "Set":
		b.WriteString("Set(" + list(0, ", ") + ")")
	case "List":
		b.WriteString("List(" + list(0, ", ") + ")")
	case "Map":
		var s []string
		for i := 0; i+1 < len(n.kids); i += 2 {
			s = append(s, k(i)+" -> "+k(i+1))
		}
		b.WriteString("Map(" + strings.Join(s, ", ") + ")")
	case "Tup":
		b.WriteString("(" + list(0, ", ") + ")")
	case "rec":
		var s []string
		for i, f := range n.names {
			s = append(s, f+": "+k(i))
		}
		b.WriteString("{ " + strings.Join(s, ", ") + " }")
	case "spread":
		b.WriteString("{ ..." + k(0) + ", " + n.lit + ": " + k(1) + " }")
	case "VA", "VB":
		b.WriteString(n.op + "(" + k(0) + ")")
	case "VC":
		b.WriteString("VC")
	case "+", "-", "*", "/", "%", "^", "==", "!=", "<", "<=", ">", ">=", "and", "or", "implies", "iff":
		b.WriteString("(" + k(0) + " " + n.op + " " + k(1) + ")")
	case "andAll", "orAll":
		b.WriteString(strings.TrimSuffix(n.op, "All") + " { " + list(0, ", ") + " }")
	case "neg":
		b.WriteString("(-(" + k(0) + "))")
	case "not":
		b.WriteString("not(" + k(0) + ")")
	case "ite":
		b.WriteString("(if (" + k(0) + ") " + k(1) + " else " + k(2) + ")")
	case "let":
		// the semicolon: a body that starts with ( would apply the value
		fmt.Fprintf(b, "{\n    val %s = %s;\n    %s\n  }", n.binds[1][0], k(0), k(1))
	case "match":
		var cs []string
		for i := 1; i < len(n.kids); i++ {
			switch {
			case n.names[i-1] == "_":
				cs = append(cs, "_ => "+k(i))
			case len(n.bindsOf(i)) == 1:
				cs = append(cs, n.names[i-1]+"("+n.bindsOf(i)[0]+") => "+k(i))
			default:
				cs = append(cs, n.names[i-1]+" => "+k(i))
			}
		}
		b.WriteString("(match " + k(0) + " { " + strings.Join(cs, " | ") + " })")
	case "item":
		b.WriteString(recv() + "._" + n.lit)
	case "field":
		b.WriteString(recv() + "." + n.lit)
	case "nth":
		b.WriteString(recv() + "[" + k(1) + "]")
	case "tuples":
		b.WriteString("tuples(" + list(0, ", ") + ")")
	case "with":
		b.WriteString(recv() + ".with(\"" + n.lit + "\", " + k(1) + ")")
	case "Nat", "Int":
		b.WriteString(n.op + ".contains(" + k(0) + ")")
	// methods whose last argument is an operator
	case "fold", "foldl":
		b.WriteString(recv() + "." + n.op + "(" + k(1) + ", " + lam(2) + ")")
	case "foldr":
		b.WriteString(recv() + ".foldr(" + k(1) + ", " + lam(2) + ")")
	case "filter", "map", "exists", "forall", "mapBy", "select":
		b.WriteString(recv() + "." + n.op + "(" + lam(1) + ")")
	case "setBy":
		b.WriteString(recv() + ".setBy(" + k(1) + ", " + lam(2) + ")")
	// actions
	case "assign":
		b.WriteString(n.lit + "' = " + k(0))
	case "all", "any":
		b.WriteString(n.op + " { " + list(0, ", ") + " }")
	case "actIte":
		b.WriteString("if (" + k(0) + ") " + k(1) + " else " + k(2))
	case "nondet":
		fmt.Fprintf(b, "{\n    nondet %s = oneOf(%s);\n    %s\n  }", n.binds[1][0], k(0), k(1))
	default:
		// a method: e.op(args...)
		b.WriteString(recv() + "." + n.op + "(" + list(1, ", ") + ")")
	}
}

// ---- generation ---------------------------------------------------------

type gbind struct {
	name string
	t    *gtyp
}

type gen struct {
	r     *rand.Rand
	fresh int
}

func newGen(seed uint64) *gen { return &gen{r: rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))} }

func (g *gen) name(prefix string) string {
	g.fresh++
	return fmt.Sprintf("%s%d", prefix, g.fresh)
}

func (g *gen) chance(p float64) bool { return g.r.Float64() < p }

// genInts are the integer literals: small ones, and the corners of 64 bits
// (quint's integers are unbounded, the evaluator's int64, refusing past).
var genInts = []string{
	"0", "1", "2", "3", "(-1)", "(-2)", "7", "(-7)", "10", "100",
	"2147483647", "4294967296", "3037000499", "3037000500", "4611686018427387904",
	"9223372036854775807", "9223372036854775806", "(-9223372036854775807)",
	"(-9223372036854775807 - 1)", // no literal past 64 bits: the Rust backend refuses a module with one, every run of it
}

var genStrs = []string{`""`, `"a"`, `"b"`, `"ab"`}

var genFields = []string{"a", "b", "c"}

func (g *gen) randType(d int) *gtyp {
	n := 4
	if d > 0 {
		n = 9
	}
	switch g.r.IntN(n + 3) {
	case 0, 1, 2, 3:
		return tInt
	case 4:
		return tBool
	case 5:
		return tStr
	case 6:
		return tV
	case 7:
		return setOfT(g.randType(d - 1))
	case 8:
		return listOfT(g.randType(d - 1))
	case 9:
		return &gtyp{k: kMap, elem: g.randType(d - 1), val: g.randType(d - 1)}
	case 10:
		t := &gtyp{k: kTup}
		for range 2 + g.r.IntN(2) {
			t.items = append(t.items, g.randType(d-1))
		}
		return t
	default:
		t := &gtyp{k: kRec}
		for _, f := range genFields[:1+g.r.IntN(3)] {
			t.names = append(t.names, f)
			t.items = append(t.items, g.randType(d-1))
		}
		return t
	}
}

func lit(t *gtyp, s string) *gnode { return &gnode{t: t, op: "lit", lit: s} }

func app(t *gtyp, op string, kids ...*gnode) *gnode { return &gnode{t: t, op: op, kids: kids} }

// literal is a value of t built from literals, small.
func (g *gen) literal(t *gtyp, d int) *gnode {
	n := func() int { return g.r.IntN(4) }
	switch t.k {
	case kInt:
		if g.chance(0.6) {
			return lit(t, genInts[g.r.IntN(10)])
		}
		return lit(t, genInts[g.r.IntN(len(genInts))])
	case kBool:
		return lit(t, []string{"false", "true"}[g.r.IntN(2)])
	case kStr:
		return lit(t, genStrs[g.r.IntN(len(genStrs))])
	case kVariant:
		switch g.r.IntN(3) {
		case 0:
			return app(t, "VA", g.literal(tInt, d))
		case 1:
			return app(t, "VB", g.literal(tBool, d))
		}
		return &gnode{t: t, op: "VC"}
	case kSet, kList:
		x := &gnode{t: t, op: map[gkind]string{kSet: "Set", kList: "List"}[t.k]}
		for range n() {
			x.kids = append(x.kids, g.literal(t.elem, d-1))
		}
		return x
	case kMap:
		x := &gnode{t: t, op: "Map"}
		for range n() {
			x.kids = append(x.kids, g.literal(t.elem, d-1), g.literal(t.val, d-1))
		}
		return x
	case kTup:
		x := &gnode{t: t, op: "Tup"}
		for _, it := range t.items {
			x.kids = append(x.kids, g.literal(it, d-1))
		}
		return x
	case kRec:
		x := &gnode{t: t, op: "rec", names: t.names}
		for _, it := range t.items {
			x.kids = append(x.kids, g.literal(it, d-1))
		}
		return x
	}
	panic("no literal")
}

func (g *gen) leaf(t *gtyp, env []gbind) *gnode {
	var names []string
	for _, b := range env {
		if sameType(b.t, t) {
			names = append(names, b.name)
		}
	}
	if len(names) > 0 && g.chance(0.6) {
		return &gnode{t: t, op: "name", lit: names[g.r.IntN(len(names))]}
	}
	return g.literal(t, 1)
}

// small is an integer expression small enough to bound a range or index
// a list: quint would build 0.to(2^62) element by element.
func (g *gen) small(env []gbind) *gnode {
	if g.chance(0.7) {
		// 11 and 14: a range past the sets whose folds the evaluator
		// checks in every order (maxFoldCheck)
		return lit(tInt, []string{"0", "1", "2", "3", "4", "(-1)", "5", "11", "14"}[g.r.IntN(9)])
	}
	return app(tInt, "+", lit(tInt, []string{"0", "1", "2"}[g.r.IntN(3)]), lit(tInt, []string{"0", "1", "(-1)"}[g.r.IntN(3)]))
}

// expr is an expression of type t, of depth at most d, over env's names.
func (g *gen) expr(t *gtyp, d int, env []gbind) *gnode {
	if d <= 0 || g.chance(0.15) {
		return g.leaf(t, env)
	}
	var opts []func() *gnode
	add := func(w int, f func() *gnode) {
		for range w {
			opts = append(opts, f)
		}
	}
	e := func(t *gtyp) *gnode { return g.expr(t, d-1, env) }
	with := func(bs ...gbind) []gbind { return append(slices.Clone(env), bs...) }

	// any type
	add(2, func() *gnode { return app(t, "ite", e(tBool), e(t), e(t)) })
	add(1, func() *gnode {
		u := g.randType(1)
		b := gbind{g.name("n"), u}
		return &gnode{t: t, op: "let", kids: []*gnode{e(u), g.expr(t, d-1, with(b))}, binds: [][]string{nil, {b.name}}}
	})
	add(1, func() *gnode {
		a, bb := gbind{g.name("p"), tInt}, gbind{g.name("q"), tBool}
		x := &gnode{t: t, op: "match", kids: []*gnode{e(tV)}, binds: [][]string{nil}}
		x.names = []string{"VA", "VB", "VC"}
		x.kids = append(x.kids, g.expr(t, d-1, with(a)), g.expr(t, d-1, with(bb)), e(t))
		x.binds = append(x.binds, []string{a.name}, []string{bb.name}, nil)
		if g.chance(0.3) { // a wildcard for the last cases
			x.names = []string{"VA", "_"}
			x.kids, x.binds = x.kids[:3], x.binds[:3]
			x.kids[2], x.binds[2] = e(t), nil
		}
		return x
	})
	add(1, func() *gnode {
		tt := &gtyp{k: kTup}
		at := g.r.IntN(2 + g.r.IntN(2))
		for i := range max(2, at+1) {
			if i == at {
				tt.items = append(tt.items, t)
			} else {
				tt.items = append(tt.items, g.randType(1))
			}
		}
		return &gnode{t: t, op: "item", lit: fmt.Sprint(at + 1), kids: []*gnode{e(tt)}}
	})
	add(1, func() *gnode {
		rt := &gtyp{k: kRec}
		at := g.r.IntN(3)
		for i, f := range genFields[:max(at+1, 1+g.r.IntN(3))] {
			rt.names = append(rt.names, f)
			if i == at {
				rt.items = append(rt.items, t)
			} else {
				rt.items = append(rt.items, g.randType(1))
			}
		}
		return &gnode{t: t, op: "field", lit: rt.names[at], kids: []*gnode{e(rt)}}
	})
	add(1, func() *gnode {
		mt := &gtyp{k: kMap, elem: g.randType(1), val: t}
		return app(t, "get", e(mt), e(mt.elem))
	})
	add(1, func() *gnode { return app(t, "nth", e(listOfT(t)), g.small(env)) })
	add(1, func() *gnode { return app(t, "head", e(listOfT(t))) })
	fold := func(body func(acc, x gbind) *gnode, u *gtyp) *gnode {
		acc, x := gbind{g.name("acc"), t}, gbind{g.name("x"), u}
		return &gnode{t: t, op: "fold", kids: []*gnode{e(setOfT(u)), e(t), body(acc, x)}, binds: [][]string{nil, nil, {acc.name, x.name}}}
	}
	add(2, func() *gnode {
		u := g.randType(1)
		return fold(func(acc, x gbind) *gnode { return g.expr(t, d-1, with(acc, x)) }, u)
	})
	add(1, func() *gnode {
		u := g.randType(1)
		acc, x := gbind{g.name("acc"), t}, gbind{g.name("x"), u}
		return &gnode{t: t, op: "foldl", kids: []*gnode{e(listOfT(u)), e(t), g.expr(t, d-1, with(acc, x))}, binds: [][]string{nil, nil, {acc.name, x.name}}}
	})
	add(1, func() *gnode {
		u := g.randType(1)
		acc, x := gbind{g.name("acc"), t}, gbind{g.name("x"), u}
		return &gnode{t: t, op: "foldr", kids: []*gnode{e(listOfT(u)), e(t), g.expr(t, d-1, with(x, acc))}, binds: [][]string{nil, nil, {x.name, acc.name}}}
	})

	switch t.k {
	case kInt:
		for _, op := range []string{"+", "-", "*", "/", "%"} {
			add(2, func() *gnode { return app(t, op, e(tInt), e(tInt)) })
		}
		add(1, func() *gnode {
			return app(t, "^", e(tInt), lit(tInt, []string{"0", "1", "2", "3", "(-1)", "31", "62", "63", "64"}[g.r.IntN(9)]))
		})
		add(1, func() *gnode { return app(t, "neg", e(tInt)) })
		add(2, func() *gnode { return app(t, "size", e(setOfT(g.randType(1)))) })
		add(1, func() *gnode { return app(t, "length", e(listOfT(g.randType(1)))) })
		// folds of the shapes the evaluator takes as commuting (fold.go):
		// it folds those in one order, unchecked
		add(3, func() *gnode {
			u := g.randType(1)
			return fold(func(acc, x gbind) *gnode {
				a := &gnode{t: t, op: "name", lit: acc.name}
				term := func() *gnode { return g.expr(tInt, d-1, with(x)) }
				switch g.r.IntN(5) {
				case 0:
					return app(t, "+", a, term())
				case 1:
					return app(t, "+", term(), a)
				case 2:
					return app(t, "-", a, term())
				case 3:
					return app(t, "ite", g.expr(tBool, d-1, with(x)), app(t, "+", a, term()), app(t, "-", a, term()))
				}
				tm := term()
				cmp := []string{"<", "<=", ">", ">="}[g.r.IntN(4)]
				if g.chance(0.5) {
					return app(t, "ite", app(tBool, cmp, a, tm), a.clone(), tm.clone())
				}
				return app(t, "ite", app(tBool, cmp, tm, a), a.clone(), tm.clone())
			}, u)
		})
	case kBool:
		for _, op := range []string{"and", "or", "implies", "iff"} {
			add(1, func() *gnode { return app(t, op, e(tBool), e(tBool)) })
		}
		add(1, func() *gnode { return app(t, "andAll", e(tBool), e(tBool), e(tBool)) })
		add(1, func() *gnode { return app(t, "orAll", e(tBool), e(tBool), e(tBool)) })
		add(1, func() *gnode { return app(t, "not", e(tBool)) })
		add(3, func() *gnode {
			u := g.randType(2)
			return app(t, []string{"==", "!="}[g.r.IntN(2)], e(u), e(u))
		})
		for _, op := range []string{"<", "<=", ">", ">="} {
			add(1, func() *gnode { return app(t, op, e(tInt), e(tInt)) })
		}
		add(2, func() *gnode {
			u := g.randType(1)
			return app(t, "contains", e(setOfT(u)), e(u))
		})
		add(1, func() *gnode {
			u := g.randType(1)
			return app(t, "in", e(u), e(setOfT(u)))
		})
		add(1, func() *gnode {
			u := setOfT(g.randType(1))
			return app(t, "subseteq", e(u), e(u))
		})
		for _, op := range []string{"exists", "forall"} {
			add(1, func() *gnode {
				u := g.randType(1)
				x := gbind{g.name("x"), u}
				return &gnode{t: t, op: op, kids: []*gnode{e(setOfT(u)), g.expr(tBool, d-1, with(x))}, binds: [][]string{nil, {x.name}}}
			})
		}
		add(1, func() *gnode { return app(t, []string{"Nat", "Int"}[g.r.IntN(2)], e(tInt)) })
		add(1, func() *gnode { return app(t, "isFinite", e(setOfT(g.randType(1)))) })
		add(1, func() *gnode { return app(t, "assert", e(tBool)) })
	case kVariant:
		add(2, func() *gnode { return app(t, "VA", e(tInt)) })
		add(1, func() *gnode { return app(t, "VB", e(tBool)) })
	case kSet:
		add(3, func() *gnode {
			x := app(t, "Set")
			for range g.r.IntN(4) {
				x.kids = append(x.kids, e(t.elem))
			}
			return x
		})
		for _, op := range []string{"union", "intersect", "exclude"} {
			add(1, func() *gnode { return app(t, op, e(t), e(t)) })
		}
		add(2, func() *gnode {
			x := gbind{g.name("x"), t.elem}
			return &gnode{t: t, op: "filter", kids: []*gnode{e(t), g.expr(tBool, d-1, with(x))}, binds: [][]string{nil, {x.name}}}
		})
		add(2, func() *gnode {
			u := g.randType(1)
			x := gbind{g.name("x"), u}
			return &gnode{t: t, op: "map", kids: []*gnode{e(setOfT(u)), g.expr(t.elem, d-1, with(x))}, binds: [][]string{nil, {x.name}}}
		})
		add(1, func() *gnode { return app(t, "keys", e(&gtyp{k: kMap, elem: t.elem, val: g.randType(1)})) })
		add(1, func() *gnode { return app(t, "flatten", e(setOfT(t))) })
		if t.elem.k == kBool {
			add(1, func() *gnode { return lit(t, "Bool") })
		}
		if t.elem.k == kInt {
			add(2, func() *gnode { return app(t, "to", g.small(env), g.small(env)) })
			add(1, func() *gnode { return app(t, "indices", e(listOfT(g.randType(1)))) })
		}
		if t.elem.k == kSet && t.elem.elem.k != kSet {
			// a powerset of a set of no sets: quint builds it whole
			add(1, func() *gnode { return app(t, "powerset", g.expr(t.elem, min(d-1, 2), env)) })
		}
		if t.elem.k == kTup {
			add(1, func() *gnode {
				x := app(t, "tuples")
				for _, it := range t.elem.items {
					x.kids = append(x.kids, e(setOfT(it)))
				}
				return x
			})
		}
		if t.elem.k == kStr {
			add(1, func() *gnode {
				rt := g.randType(1)
				for rt.k != kRec {
					rt = g.randType(1)
				}
				return app(t, "fieldNames", e(rt))
			})
		}
		// a set built by a fold: union commutes, put does not
		add(1, func() *gnode {
			u := g.randType(1)
			acc, x := gbind{g.name("acc"), t}, gbind{g.name("x"), u}
			body := app(t, "union", &gnode{t: t, op: "name", lit: acc.name}, g.expr(t, d-1, with(x)))
			return &gnode{t: t, op: "fold", kids: []*gnode{e(setOfT(u)), e(t), body}, binds: [][]string{nil, nil, {acc.name, x.name}}}
		})
	case kList:
		add(3, func() *gnode {
			x := app(t, "List")
			for range g.r.IntN(4) {
				x.kids = append(x.kids, e(t.elem))
			}
			return x
		})
		add(1, func() *gnode { return app(t, "append", e(t), e(t.elem)) })
		add(1, func() *gnode { return app(t, "concat", e(t), e(t)) })
		add(1, func() *gnode { return app(t, "tail", e(t)) })
		add(1, func() *gnode { return app(t, "slice", e(t), g.small(env), g.small(env)) })
		add(1, func() *gnode { return app(t, "replaceAt", e(t), g.small(env), e(t.elem)) })
		add(1, func() *gnode {
			x := gbind{g.name("x"), t.elem}
			return &gnode{t: t, op: "select", kids: []*gnode{e(t), g.expr(tBool, d-1, with(x))}, binds: [][]string{nil, {x.name}}}
		})
		if t.elem.k == kInt {
			add(1, func() *gnode { return &gnode{t: t, op: "range", kids: []*gnode{g.small(env), g.small(env)}} })
		}
	case kMap:
		add(3, func() *gnode {
			x := app(t, "Map")
			for range g.r.IntN(4) {
				x.kids = append(x.kids, e(t.elem), e(t.val))
			}
			return x
		})
		add(2, func() *gnode {
			x := gbind{g.name("x"), t.elem}
			return &gnode{t: t, op: "mapBy", kids: []*gnode{e(setOfT(t.elem)), g.expr(t.val, d-1, with(x))}, binds: [][]string{nil, {x.name}}}
		})
		add(1, func() *gnode {
			return app(t, "setToMap", e(setOfT(&gtyp{k: kTup, items: []*gtyp{t.elem, t.val}})))
		})
		add(1, func() *gnode { return app(t, "put", e(t), e(t.elem), e(t.val)) })
		add(1, func() *gnode { return app(t, "set", e(t), e(t.elem), e(t.val)) })
		add(1, func() *gnode {
			old := gbind{g.name("old"), t.val}
			return &gnode{t: t, op: "setBy", kids: []*gnode{e(t), e(t.elem), g.expr(t.val, d-1, with(old))}, binds: [][]string{nil, nil, {old.name}}}
		})
		// a map built by a fold of puts: which key wins depends on the order
		add(1, func() *gnode {
			u := g.randType(1)
			acc, x := gbind{g.name("acc"), t}, gbind{g.name("x"), u}
			body := app(t, "put", &gnode{t: t, op: "name", lit: acc.name}, g.expr(t.elem, d-1, with(acc, x)), g.expr(t.val, d-1, with(acc, x)))
			return &gnode{t: t, op: "fold", kids: []*gnode{e(setOfT(u)), e(t), body}, binds: [][]string{nil, nil, {acc.name, x.name}}}
		})
	case kTup:
		add(3, func() *gnode {
			x := app(t, "Tup")
			for _, it := range t.items {
				x.kids = append(x.kids, e(it))
			}
			return x
		})
	case kRec:
		add(3, func() *gnode {
			x := &gnode{t: t, op: "rec", names: t.names}
			for _, it := range t.items {
				x.kids = append(x.kids, e(it))
			}
			return x
		})
		add(1, func() *gnode {
			i := g.r.IntN(len(t.names))
			return &gnode{t: t, op: "with", lit: t.names[i], kids: []*gnode{e(t), e(t.items[i])}}
		})
		add(1, func() *gnode {
			i := g.r.IntN(len(t.names))
			return &gnode{t: t, op: "spread", lit: t.names[i], kids: []*gnode{e(t), e(t.items[i])}}
		})
	}
	return opts[g.r.IntN(len(opts))]()
}

// action is an action assigning variable v, of type t, from no state:
// its choices, guards and branches over expressions of depth d.
func (g *gen) action(v string, t *gtyp, d int, env []gbind) *gnode {
	with := func(bs ...gbind) []gbind { return append(slices.Clone(env), bs...) }
	if d <= 0 || g.chance(0.2) {
		return &gnode{op: "assign", lit: v, kids: []*gnode{g.expr(t, 2, env)}}
	}
	switch g.r.IntN(5) {
	case 0: // a guard before or after
		a := g.action(v, t, d-1, env)
		p := g.expr(tBool, 2, env)
		if g.chance(0.5) {
			return &gnode{op: "all", kids: []*gnode{p, a}}
		}
		return &gnode{op: "all", kids: []*gnode{a, p}}
	case 1:
		x := &gnode{op: "any"}
		for range 2 + g.r.IntN(2) {
			x.kids = append(x.kids, g.action(v, t, d-1, env))
		}
		return x
	case 2:
		return &gnode{op: "actIte", kids: []*gnode{g.expr(tBool, 2, env), g.action(v, t, d-1, env), g.action(v, t, d-1, env)}}
	}
	u := g.randType(1)
	x := gbind{g.name("c"), u}
	return &gnode{op: "nondet", kids: []*gnode{g.expr(setOfT(u), 2, env), g.action(v, t, d-1, with(x))}, binds: [][]string{nil, {x.name}}}
}

// successors is the set of values action a assigns, as an expression:
// what the differential test checks the evaluator's successors against
// (Set(E) for v' = E, a guard's if, a union for any, a choice's
// flattened map).
func successors(a *gnode, t *gtyp) *gnode {
	st := setOfT(t)
	switch a.op {
	case "assign":
		return app(st, "Set", a.kids[0].clone())
	case "all":
		var guards []*gnode
		var act *gnode
		for _, k := range a.kids {
			if k.t == nil {
				act = k
			} else {
				guards = append(guards, k.clone())
			}
		}
		c := guards[0]
		for _, gd := range guards[1:] {
			c = app(tBool, "and", c, gd)
		}
		return app(st, "ite", c, successors(act, t), app(st, "Set"))
	case "any":
		out := successors(a.kids[0], t)
		for _, k := range a.kids[1:] {
			out = app(st, "union", out, successors(k, t))
		}
		return out
	case "actIte":
		return app(st, "ite", a.kids[0].clone(), successors(a.kids[1], t), successors(a.kids[2], t))
	case "nondet":
		m := &gnode{t: setOfT(st), op: "map", kids: []*gnode{a.kids[0].clone(), successors(a.kids[1], t)}, binds: [][]string{nil, slices.Clone(a.binds[1])}}
		return app(st, "flatten", m)
	}
	panic("no action " + a.op)
}

// ---- shrinking ----------------------------------------------------------

// smallest is the least literal of t, which shrinking tries first.
func smallest(t *gtyp) *gnode {
	switch t.k {
	case kInt:
		return lit(t, "0")
	case kBool:
		return lit(t, "false")
	case kStr:
		return lit(t, `""`)
	case kVariant:
		return &gnode{t: t, op: "VC"}
	case kSet:
		return app(t, "Set")
	case kList:
		return app(t, "List")
	case kMap:
		return app(t, "Map")
	case kTup:
		x := app(t, "Tup")
		for _, it := range t.items {
			x.kids = append(x.kids, smallest(it))
		}
		return x
	case kRec:
		x := &gnode{t: t, op: "rec", names: t.names}
		for _, it := range t.items {
			x.kids = append(x.kids, smallest(it))
		}
		return x
	}
	panic("no type")
}

// reductions are the trees one step smaller than root: a node replaced by
// one of its descendants of its type (whose names stay bound), by the
// least literal of its type, an element of a literal dropped, an
// integer literal made smaller, an action replaced by one of its
// branches.
func reductions(root *gnode) []*gnode {
	var out []*gnode
	var walk func(n *gnode, replace func(*gnode) *gnode)
	walk = func(n *gnode, replace func(*gnode) *gnode) {
		try := func(r *gnode) { out = append(out, replace(r)) }
		if n.t != nil {
			if s := smallest(n.t); s.String() != n.String() {
				try(s)
			}
			descend(n, nil, func(d *gnode, bound []string) {
				if d != n && d.t != nil && sameType(d.t, n.t) && !reads(d, bound) {
					try(d.clone())
				}
			})
			if n.op == "lit" && n.t.k == kInt && n.lit != "0" && n.lit != "1" {
				try(lit(n.t, "1"))
			}
		} else {
			switch n.op {
			case "all":
				for i, k := range n.kids {
					c := n.clone()
					c.kids = slices.Delete(c.kids, i, i+1)
					if k.t != nil && len(c.kids) > 1 {
						try(c)
					}
					if k.t == nil && len(n.kids) == 2 {
						try(k.clone())
					}
				}
			case "any", "actIte":
				for i, k := range n.kids {
					if k.t == nil {
						try(k.clone())
					}
					if n.op == "any" && len(n.kids) > 2 {
						c := n.clone()
						c.kids = slices.Delete(c.kids, i, i+1)
						try(c)
					}
				}
			case "nondet":
				if !reads(n.kids[1], n.binds[1]) {
					try(n.kids[1].clone())
				}
			}
		}
		switch n.op {
		case "Set", "List", "Tup", "Map":
			step := 1
			if n.op == "Map" {
				step = 2
			}
			if n.op != "Tup" {
				for i := 0; i+step <= len(n.kids); i += step {
					c := n.clone()
					c.kids = slices.Delete(c.kids, i, i+step)
					try(c)
				}
			}
		}
		for i := range n.kids {
			walk(n.kids[i], func(r *gnode) *gnode {
				c := *n
				c.kids = slices.Clone(n.kids)
				c.kids[i] = r
				return replace(&c)
			})
		}
	}
	walk(root, func(r *gnode) *gnode { return r })
	return out
}

// descend visits n's descendants with the names bound between n and each.
func descend(n *gnode, bound []string, f func(*gnode, []string)) {
	f(n, bound)
	for i, k := range n.kids {
		descend(k, append(slices.Clone(bound), n.bindsOf(i)...), f)
	}
}

// reads reports whether n reads one of names freely.
func reads(n *gnode, names []string) bool {
	fr := map[string]bool{}
	n.free(fr)
	for _, x := range names {
		if fr[x] {
			return true
		}
	}
	return false
}

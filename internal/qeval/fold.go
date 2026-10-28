package qeval

import (
	"errors"
	"math/bits"
)

// maxFoldCheck is the largest set whose fold the evaluator checks over
// every order: n·2^(n-1) applications of the operator (5120 at 10).
const maxFoldCheck = 10

// foldSet folds s from init with f as quint would, or refuses. Quint
// folds a set in an order of its own: the TypeScript backend's hash
// order, which also depends on the order elements were added
// (Set("b", "a", ...) and Set("a", "b", ...) fold differently), and the
// Rust backend's, which is another (Set(1, 33, 2) folds as [1, 33, 2]
// in TypeScript and otherwise in Rust). Neither is documented, so the
// evaluator gives a fold's result only when every order gives it: when
// the operator is one that commutes whatever the elements (commutes),
// or, for a set of at most maxFoldCheck elements, when every order was
// tried. Folded sorted and reversed, a round-robin assignment agreed
// both ways and not in quint's order, and a complete graph "proved" an
// invariant quint violates (review 11, F1).
func (ev *evaluation) foldSet(s *Set, init, f Value, op string) Value {
	n := len(s.Elems)
	if n <= 1 || ev.commutes(f) {
		acc := init
		for _, x := range s.Elems {
			acc = ev.call(f, acc, x)
		}
		return acc
	}
	if n > maxFoldCheck {
		throw(unsupported(op, "a fold over %d elements whose operator is not one known to commute (a sum, a min or max, a union): quint folds a set in an order of its own, and the evaluator checks every order only up to %d elements", n, maxFoldCheck))
	}
	// by subsets: every order of a subset T gives one result exactly when
	// f(acc(T \ {x}), x) is the same for every x in T, acc(T \ {x}) being
	// that subset's one result; each step of every order is tried once
	accs := make([]Value, 1<<n)
	accs[0] = init
	for mask := 1; mask < 1<<n; mask++ {
		for m := mask; m != 0; m &= m - 1 {
			i := bits.TrailingZeros(uint(m))
			v := ev.tryCall(f, accs[mask&^(1<<i)], s.Elems[i], op)
			if accs[mask] == nil {
				accs[mask] = v
			} else if Compare(accs[mask], v) != 0 {
				throw(unsupported(op, "a fold whose result depends on the set's order: quint folds a set in an order of its own"))
			}
		}
	}
	return accs[1<<n-1]
}

// tryCall is f(acc, x) in one of a fold's orders: the model's runtime
// error there may be in an order quint does not take, so it is quint's
// to report (Unsupported), not a verdict.
func (ev *evaluation) tryCall(f, acc, x Value, op string) (v Value) {
	defer func() {
		if r := recover(); r != nil {
			t, ok := r.(thrown)
			var ee *EvalError
			if ok && errors.As(t.err, &ee) {
				throw(unsupported(op, "a fold that fails (%s) in some of the set's orders: quint folds a set in an order of its own", ee.Msg))
			}
			panic(r)
		}
	}()
	return ev.call(f, acc, x)
}

// commutes reports whether a fold with f gives one result in every order
// of any set, by the shape of its body (acc its accumulator, E any
// expression that does not read acc):
//   - a sum: acc, acc + E, E + acc, acc - E, and if C then B1 else B2 of
//     sums with C not reading acc: acc plus a sum of terms, one per element;
//   - a union: acc, acc.union(E), E.union(acc), and conditionals of them;
//   - a min or max: if (acc < E) acc else E, with any comparison and either
//     side, E the same expression in the comparison and the branch.
//
// Each order evaluates the same terms, so a term's runtime error is the
// same in every order. Anything else is checked by foldSet over every
// order, or refused.
func (ev *evaluation) commutes(f Value) bool {
	l, ok := f.(*Lambda)
	if !ok || len(l.params) != 2 || l.body == nil {
		return false
	}
	c := commuter{ev: ev, l: l, acc: l.params[0]}
	return c.joins(l.body, "iadd") || c.joins(l.body, "union") || c.selects(l.body)
}

type commuter struct {
	ev  *evaluation
	l   *Lambda
	acc string
}

// builtin reports whether e applies the builtin op, not a definition or
// an operator bound around the fold of that name.
func (c commuter) builtin(e *Expr, op string) bool {
	if e.Kind != "app" || e.Opcode != op {
		return false
	}
	if _, ok := c.l.env.lookup(op); ok {
		return false
	}
	if r, ok := c.ev.m.refs[e.ID]; ok && (r.kind == "def" || r.kind == "param") {
		return false
	}
	return true
}

func (c commuter) isAcc(e *Expr) bool { return e.Kind == "name" && e.Name == c.acc }

// joins: acc combined with terms by op (iadd, with isub for a sum; union).
func (c commuter) joins(e *Expr, op string) bool {
	switch {
	case c.isAcc(e):
		return true
	case c.builtin(e, op) && len(e.Args) == 2:
		return c.joins(e.Args[0], op) && c.free(e.Args[1]) || c.free(e.Args[0]) && c.joins(e.Args[1], op)
	case op == "iadd" && c.builtin(e, "isub") && len(e.Args) == 2:
		return c.joins(e.Args[0], op) && c.free(e.Args[1])
	case c.builtin(e, "ite") && len(e.Args) == 3:
		return c.free(e.Args[0]) && c.joins(e.Args[1], op) && c.joins(e.Args[2], op)
	}
	return false
}

// selects: if (P cmp Q) R else S with {P, Q} = {R, S} = {acc, E}.
func (c commuter) selects(e *Expr) bool {
	if !c.builtin(e, "ite") || len(e.Args) != 3 {
		return false
	}
	cond, r, s := e.Args[0], e.Args[1], e.Args[2]
	cmp := false
	for _, op := range []string{"ilt", "ilte", "igt", "igte"} {
		cmp = cmp || c.builtin(cond, op) && len(cond.Args) == 2
	}
	if !cmp {
		return false
	}
	var term *Expr
	switch {
	case c.isAcc(cond.Args[0]) && c.free(cond.Args[1]):
		term = cond.Args[1]
	case c.isAcc(cond.Args[1]) && c.free(cond.Args[0]):
		term = cond.Args[0]
	default:
		return false
	}
	return c.isAcc(r) && c.same(s, term) || c.isAcc(s) && c.same(r, term)
}

// free reports whether e never reads acc (and binds no name acc, which
// would make the scan's answer depend on scoping).
func (c commuter) free(e *Expr) bool {
	if e == nil {
		return true
	}
	if e.Kind == "name" && e.Name == c.acc {
		return false
	}
	for _, p := range e.Params {
		if p.Name == c.acc {
			return false
		}
	}
	if e.Opdef != nil && (e.Opdef.Name == c.acc || !c.free(e.Opdef.Expr)) {
		return false
	}
	for _, a := range e.Args {
		if !c.free(a) {
			return false
		}
	}
	return c.free(e.Expr)
}

// same reports whether a and b are one expression of names, literals and
// applications (ids aside): the min's term in its comparison and its
// branch.
func (c commuter) same(a, b *Expr) bool {
	if a.Kind != b.Kind || a.Name != b.Name || a.Opcode != b.Opcode || a.Value != b.Value || a.Bool != b.Bool || len(a.Args) != len(b.Args) {
		return false
	}
	switch a.Kind {
	case "name", "int", "str", "bool":
	case "app":
		ra, oka := c.ev.m.refs[a.ID]
		rb, okb := c.ev.m.refs[b.ID]
		if oka != okb || ra != rb {
			return false
		}
		for i := range a.Args {
			if !c.same(a.Args[i], b.Args[i]) {
				return false
			}
		}
	default:
		return false
	}
	if a.Kind == "name" {
		ra, oka := c.ev.m.refs[a.ID]
		rb, okb := c.ev.m.refs[b.ID]
		return oka == okb && ra == rb
	}
	return true
}

package qeval

import (
	"fmt"
	"slices"
)

// A Footprint records what a tracking model's evaluations (Tracking) read
// and wrote of the state, entry by entry of its map variables, for a
// check that a model factors by an entity: that a step of one entity's
// reads and writes only entries no other entity's step writes
// (validate's partition), so each entity's history can be judged alone.
// An access that is not to one entry (a map's keys, its size, an
// iteration over it, a comparison of it whole, a scalar variable read, a
// map assigned anew) makes the evaluation depend on every entity's: the
// first is Whole, and the evaluation goes on.
//
// The caller reads Reads and Writes after each evaluation and empties
// them (Reset). Judging says the next evaluations are of vals over a
// state (invariants, eventual vals), where a forall over a state map's
// keys at a conjunctive position (the val itself, an and of such, a val
// or def it names) is each entry's judgment: the val holds of a state
// whose entries are the union of the entities' exactly when it holds of
// each entity's, provided the forall's body reads entries alone. Such a
// forall elsewhere (under a not, an or, inside another forall's body) is
// Whole.
type Footprint struct {
	// Judging: the next evaluations are vals over a state, not a step's.
	Judging bool
	// Reads and Writes are the entries read and written since the last
	// Reset, in order (repeated as they recur).
	Reads, Writes []Entry
	// Consts are the constants read, by name: the others' values did not
	// matter to any evaluation recorded.
	Consts map[string]bool
	// Whole is the first access that was not to one entry ("" none),
	// described: "keys(charges)", "reads total", "charges compared whole
	// (eq)".
	Whole string
}

// An Entry is one entry of a state map: the variable and the key, as a
// Quint literal (String).
type Entry struct{ Var, Key string }

// Reset empties Reads and Writes (Whole stays: it is the evaluation's
// verdict, not one step's).
func (f *Footprint) Reset() { f.Reads, f.Writes = f.Reads[:0], f.Writes[:0] }

// Tracking is m recording into fp what its evaluations read and write of
// the state (Footprint): a shallow copy, as AsReplayed's. Its
// evaluations are the same, and so are their values; one Footprint is
// one goroutine's.
func (m *Model) Tracking(fp *Footprint) *Model {
	c := *m
	c.fp = fp
	return &c
}

// whole records an access that is not to one entry, the first one's
// description kept.
func (ev *evaluation) whole(format string, a ...any) {
	if ev.fp.Whole == "" {
		for i, x := range a { // a variable's name as the model says it
			if s, ok := x.(string); ok {
				a[i] = ev.m.Display(s)
			}
		}
		ev.fp.Whole = fmt.Sprintf(format, a...)
	}
}

// taintOf is the state variable a map is (or was made from by put, set or
// setBy in this evaluation), "" for a map of no variable's.
func (ev *evaluation) taintOf(v Value) string {
	m, ok := v.(*Map)
	if !ok || ev.tainted == nil {
		return ""
	}
	return ev.tainted[m]
}

func (ev *evaluation) taint(m *Map, name string) {
	if ev.tainted == nil {
		ev.tainted = map[*Map]string{}
	}
	ev.tainted[m] = name
}

func (ev *evaluation) read(name string, k Value) {
	ev.fp.Reads = append(ev.fp.Reads, Entry{ev.m.Display(name), String(k)})
}

func (ev *evaluation) wrote(name string, k Value) {
	ev.fp.Writes = append(ev.fp.Writes, Entry{ev.m.Display(name), String(k)})
}

// setArg evaluates e, a set argument of a builtin: for keys(M) with M a
// state variable's map, or one made from it, M and its variable instead
// of the set, which is not built (contains(keys(M), k) reads one entry,
// a forall over it may be each entry's); else e's value.
func (ev *evaluation) setArg(e *Expr, en *env) (Value, *Map, string) {
	if e.Kind != "app" || e.Opcode != "keys" || len(e.Args) != 1 {
		return ev.eval(e, en), nil, ""
	}
	if _, ok := en.lookup(e.Opcode); ok {
		return ev.eval(e, en), nil, "" // a let-bound operator named keys
	}
	if r, ok := ev.m.refs[e.ID]; ok && (r.kind == "def" || r.kind == "param") {
		return ev.eval(e, en), nil, ""
	}
	v := ev.eval(e.Args[0], en)
	if name := ev.taintOf(v); name != "" {
		return nil, v.(*Map), name
	}
	return &Set{slices.Clone(ev.mapOf(v, "keys").Keys)}, nil, ""
}

// tracked evaluates the builtins a footprint reads entries through: get,
// contains (in) of a state map's keys, put, set and setBy of a state map,
// and a forall over a state map's keys at a conjunctive position conj of
// a judged val (each entry's judgment). ok false: e is another builtin,
// evaluated as any (a state map among its arguments read whole).
func (ev *evaluation) tracked(e *Expr, en *env, conj bool) (Value, bool) {
	op := e.Opcode
	switch op {
	case "get":
		c := ev.eval(e.Args[0], en)
		k := ev.eval(e.Args[1], en)
		if l, ok := c.(List); ok {
			i := ev.intOf(k, op)
			if i < 0 || i >= int64(len(l)) {
				throw(evalErr("index %d out of a list of %d", i, len(l)))
			}
			return l[i], true
		}
		m := ev.mapOf(c, op)
		if name := ev.taintOf(m); name != "" {
			ev.read(name, k)
		}
		i, ok := m.find(k)
		if !ok {
			throw(evalErr("key %s not in the map", String(k)))
		}
		return m.Vals[i], true
	case "contains", "in":
		se, xe := e.Args[0], e.Args[1]
		if op == "in" {
			se, xe = xe, se
		}
		s, m, name := ev.setArg(se, en)
		x := ev.eval(xe, en)
		if m != nil {
			ev.read(name, x)
			_, ok := m.find(x)
			return Bool(ok), true
		}
		if name := ev.taintOf(s); name != "" { // (a set is never a map: for the type's sake)
			ev.whole("%s read whole (%s)", name, op)
		}
		if inf, ok := s.(infinite); ok {
			i, isInt := x.(Int)
			return Bool(isInt && (!inf.nat || i >= 0)), true
		}
		return Bool(ev.setOf(s, op).has(x)), true
	case "put", "set", "setBy":
		m := ev.mapOf(ev.eval(e.Args[0], en), op)
		k := ev.eval(e.Args[1], en)
		name := ev.taintOf(m)
		var out *Map
		switch op {
		case "put":
			out = m.put(k, ev.eval(e.Args[2], en))
		case "set":
			if name != "" {
				ev.read(name, k) // whether it is there
			}
			if _, ok := m.find(k); !ok {
				throw(evalErr("set: key %s not in the map", String(k)))
			}
			out = m.put(k, ev.eval(e.Args[2], en))
		default:
			if name != "" {
				ev.read(name, k)
			}
			i, ok := m.find(k)
			if !ok {
				throw(evalErr("setBy: key %s not in the map", String(k)))
			}
			out = m.put(k, ev.call(ev.eval(e.Args[2], en), m.Vals[i]))
		}
		if name != "" {
			ev.wrote(name, k)
			ev.taint(out, name)
		}
		return out, true
	case "forall":
		s, m, name := ev.setArg(e.Args[0], en)
		f := ev.eval(e.Args[1], en)
		if m != nil {
			if !conj || !ev.fp.Judging || ev.state[name] != Value(m) {
				ev.whole("forall over keys(%s) where it is not each entry's judgment (a judged val's conjunct over the state's %s)", name, name)
			} else {
				ev.perEntry++
			}
			for _, k := range m.Keys {
				if !ev.truth(ev.call(f, k)) {
					return Bool(false), true
				}
			}
			return Bool(true), true
		}
		for _, x := range ev.setOf(s, op).Elems {
			if !ev.truth(ev.call(f, x)) {
				return Bool(false), true
			}
		}
		return Bool(true), true
	case "and":
		for _, a := range e.Args {
			ev.conj = conj
			if !ev.boolOf(a, en) {
				return Bool(false), true
			}
		}
		return Bool(true), true
	}
	return nil, false
}

// assigned is the value an assignment of variable v to e gives, in a
// tracking evaluation: v unchanged (v' = v) reads nothing; a map
// variable assigned anything but its map, changed entry by entry (put,
// set, setBy), is written whole; a scalar's value is written, never read.
func (ev *evaluation) assigned(v string, e *Expr, en *env) Value {
	if ev.state == nil { // init: every entity's, before any step
		return ev.eval(e, en)
	}
	if e.Kind == "name" {
		if r, ok := ev.m.refs[e.ID]; ok && r.kind == "var" {
			if d := ev.m.byID[r.decl]; d != nil && d.Name == v {
				if _, bound := en.lookup(e.Name); !bound {
					return ev.state[v]
				}
			}
		}
	}
	x := ev.eval(e, en)
	if _, isMap := ev.state[v].(*Map); isMap && ev.taintOf(x) != v {
		ev.whole("%s assigned anew, not changed entry by entry", v)
	}
	return x
}

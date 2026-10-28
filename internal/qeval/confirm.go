package qeval

import "sync"

// Unshadowed are the builtin operators the evaluator took since the last
// nightly whose shadow run (GOTOGETHER_EVAL=shadow, every pass judged by
// quint too) came back clean: a pass that reaches one is sampled for
// quint more often (validate's raised sample), until a clean night
// confirms the operator and it leaves this list. Read-only: an operator
// added to the evaluator goes here with it, by its opcode.
var Unshadowed = map[string]bool{}

// confirmMemo is a model's Unconfirmed answers by definition name, shared
// by the copies AsReplayed makes.
type confirmMemo struct{ m sync.Map }

// Unconfirmed says why a pass of the definition name (an action) is less
// sure than the evaluator's others, "" if it is not: it reaches, through
// the definitions it calls, a set fold whose operator is not of a shape
// known to commute (the evaluator tries every order of such a fold, up
// to maxFoldCheck elements: sound, but the reason review 11's F1 was a
// silent wrong answer), or an operator new since the last shadow-clean
// nightly (Unshadowed). The answer is the model's, not a run's: what the
// definition may evaluate, not what one call did.
func (m *Model) Unconfirmed(name string) string {
	if m.confirm != nil {
		if v, ok := m.confirm.m.Load(name); ok {
			return v.(string)
		}
	}
	why := ""
	if d, ok := m.Decl(name); ok {
		why = m.unconfirmed(d.Expr, map[int64]bool{d.ID: true})
	}
	if m.confirm != nil {
		m.confirm.m.Store(name, why)
	}
	return why
}

// unconfirmed walks e, and the definitions it names or applies (each
// once: seen), for an ordered set fold or an unshadowed operator.
func (m *Model) unconfirmed(e *Expr, seen map[int64]bool) string {
	if e == nil {
		return ""
	}
	follow := func() string {
		r, ok := m.refs[e.ID]
		if !ok || r.kind != "def" || seen[r.decl] {
			return ""
		}
		seen[r.decl] = true
		if d := m.byID[r.decl]; d != nil {
			return m.unconfirmed(d.Expr, seen)
		}
		return ""
	}
	switch e.Kind {
	case "name":
		return follow()
	case "app":
		if r, ok := m.refs[e.ID]; ok && (r.kind == "def" || r.kind == "param") {
			if why := follow(); why != "" {
				return why
			}
		} else {
			if Unshadowed[e.Opcode] {
				return "operator " + e.Opcode + ", new to the Go evaluator since its last shadow-clean nightly"
			}
			if e.Opcode == "fold" && len(e.Args) == 3 && !m.commutesStatic(e.Args[2]) {
				return "a set fold whose operator is not one known to commute (the evaluator tries its every order)"
			}
		}
	case "let":
		if e.Opdef != nil {
			if why := m.unconfirmed(e.Opdef.Expr, seen); why != "" {
				return why
			}
		}
	}
	for _, a := range e.Args {
		if why := m.unconfirmed(a, seen); why != "" {
			return why
		}
	}
	return m.unconfirmed(e.Expr, seen)
}

// commutesStatic is commutes for a fold's operator as written: a lambda
// literal of a commuting shape, or a definition of one passed by name.
// Anything else is not known to commute.
func (m *Model) commutesStatic(f *Expr) bool {
	if r, ok := m.refs[f.ID]; f.Kind == "name" && ok && r.kind == "def" {
		if d := m.byID[r.decl]; d != nil && d.Expr != nil {
			f = d.Expr // a definition's operator, passed by name
		}
	}
	if f.Kind != "lambda" || len(f.Params) != 2 || f.Expr == nil {
		return false
	}
	ev := m.evaluation(nil, nil)
	return ev.commutes(&Lambda{params: paramNames(f), body: f.Expr})
}

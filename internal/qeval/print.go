package qeval

import (
	"strconv"
	"strings"
	"sync"
)

// Quint text from quint's IR. An explanation (Explain) shows the
// condition that is false as source text; quint's JSON IR has node ids
// but no positions, and the parser's source map (`quint parse
// --source-map`) numbers the nodes of the files as parsed, not of the
// flattened module `quint compile` writes (an instance's import copies
// the model's definitions under new ids). So the text is printed from
// the IR: the same expression, with quint's renamings undone (a
// parameter p is p_<id> in the IR, unsuffixed) and the
// evaluation module's names (binding.EvalModule's __gtg_ prefixes)
// shown as the model's. Its layout is the printer's, not the file's:
// method calls for the builtins quint's docs write so (s.contains(x)),
// infix for the operators, parentheses around any infix operand.

// evalPrefix is binding.EvalModule's prefix for the names it adds.
const evalPrefix = "__gtg_"

// unsuffixed is a lambda parameter's name without the suffix quint's
// compiler gives it (p_<id>, the id of its lambda in the module it was
// declared in, which an instance's copy renumbers): a suffix of two
// digits or more, after an underscore.
func unsuffixed(name string) (string, bool) {
	i := strings.LastIndexByte(name, '_')
	if i <= 0 || len(name)-i-1 < 2 {
		return "", false
	}
	if _, err := strconv.ParseUint(name[i+1:], 10, 64); err != nil {
		return "", false
	}
	return name[:i], true
}

// renames are the IR's suffixed names of lambda parameters, each to its
// name in the source: built once per model, read-only after.
type renames struct {
	once sync.Once
	m    map[string]string
}

func (m *Model) renamed() map[string]string {
	m.ren.once.Do(func() {
		m.ren.m = map[string]string{}
		var walk func(e *Expr)
		walk = func(e *Expr) {
			if e == nil {
				return
			}
			if e.Kind == "lambda" {
				for _, p := range e.Params {
					if base, ok := unsuffixed(p.Name); ok {
						m.ren.m[p.Name] = base
					}
				}
			}
			for _, a := range e.Args {
				walk(a)
			}
			walk(e.Expr)
			if e.Opdef != nil {
				walk(e.Opdef.Expr)
			}
		}
		for _, d := range m.byID {
			walk(d.Expr)
		}
	})
	return m.ren.m
}

// Display is how an explanation names a name of the IR: its last
// segment (a flattened import's m::model::x is x), a lambda parameter as
// the source names it, and the evaluation module's own names as the
// model's (__gtg_act_deposit is deposit, __gtg_p_amount amount).
func (m *Model) Display(name string) string {
	if r, ok := m.renamed()[name]; ok {
		name = r
	}
	if i := strings.LastIndex(name, "::"); i >= 0 {
		name = name[i+2:]
	}
	for _, p := range []string{"act_", "p_", "c_", "expect_"} {
		if rest, ok := strings.CutPrefix(name, evalPrefix+p); ok {
			return rest
		}
	}
	return name
}

// paramsOf are a definition's parameters, as the source names them.
func (m *Model) paramsOf(d *Decl) []string {
	if d.Expr == nil || d.Expr.Kind != "lambda" {
		return nil
	}
	out := make([]string, len(d.Expr.Params))
	for i, p := range d.Expr.Params {
		out[i] = m.Display(p.Name)
	}
	return out
}

// Text is e as Quint text.
func (m *Model) Text(e *Expr) string {
	var b strings.Builder
	m.print(&b, e)
	return b.String()
}

// infix are the builtins printed between their arguments. Read-only.
var infix = map[string]string{
	"iadd": "+", "isub": "-", "imul": "*", "idiv": "/", "imod": "%", "ipow": "^",
	"igt": ">", "ilt": "<", "igte": ">=", "ilte": "<=", "eq": "==", "neq": "!=",
	"implies": "implies", "iff": "iff", "and": "and", "or": "or", "to": ".to",
}

// methods are the builtins printed as a method of their first argument.
// Read-only.
var methods = map[string]bool{
	"contains": true, "in": true, "union": true, "intersect": true, "exclude": true, "subseteq": true,
	"size": true, "isFinite": true, "filter": true, "select": true, "map": true, "exists": true, "forall": true,
	"fold": true, "flatten": true, "powerset": true, "mapBy": true, "setToMap": true, "keys": true,
	"set": true, "put": true, "setBy": true, "with": true, "fieldNames": true, "append": true, "concat": true,
	"head": true, "tail": true, "length": true, "nth": true, "replaceAt": true, "slice": true, "indices": true,
	"foldl": true, "foldr": true, "oneOf": true, "chooseSome": true, "get": true,
}

func (m *Model) print(b *strings.Builder, e *Expr) {
	if e == nil {
		b.WriteString("?")
		return
	}
	switch e.Kind {
	case "pinnedName":
		// PinAction has established this identifier's source binding. Unlike
		// explanation names it must not be shortened or have suffixes removed.
		b.WriteString(e.Name)
	case "int":
		b.WriteString(e.Value.String())
	case "bool":
		b.WriteString(strconv.FormatBool(e.Bool))
	case "str":
		b.WriteString(strconv.Quote(e.Value.String()))
	case "name":
		b.WriteString(m.Display(e.Name))
	case "lambda":
		if len(e.Params) == 1 {
			b.WriteString(m.Display(e.Params[0].Name))
		} else {
			b.WriteString("(")
			for i, p := range e.Params {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(m.Display(p.Name))
			}
			b.WriteString(")")
		}
		b.WriteString(" => ")
		m.print(b, e.Expr)
	case "let":
		d := e.Opdef
		b.WriteString(d.Qualifier + " " + m.Display(d.Name) + " = ")
		m.print(b, d.Expr)
		b.WriteString("; ")
		m.print(b, e.Expr)
	case "app":
		m.app(b, e)
	default:
		b.WriteString("<" + e.Kind + ">")
	}
}

// operand prints e, parenthesised if it is an infix application.
func (m *Model) operand(b *strings.Builder, e *Expr) {
	if e != nil && e.Kind == "app" && infix[e.Opcode] != "" && e.Opcode != "to" || e != nil && e.Kind == "lambda" || e != nil && e.Kind == "let" {
		b.WriteString("(")
		m.print(b, e)
		b.WriteString(")")
		return
	}
	m.print(b, e)
}

func (m *Model) list(b *strings.Builder, es []*Expr) {
	for i, a := range es {
		if i > 0 {
			b.WriteString(", ")
		}
		m.print(b, a)
	}
}

func (m *Model) app(b *strings.Builder, e *Expr) {
	op, args := e.Opcode, e.Args
	if r, ok := m.refs[e.ID]; ok && (r.kind == "def" || r.kind == "param") || !builtin(op) {
		// a definition of the model's, or an operator bound by a let or a
		// parameter
		b.WriteString(m.Display(op) + "(")
		m.list(b, args)
		b.WriteString(")")
		return
	}
	switch {
	case op == "not":
		b.WriteString("not(")
		m.print(b, args[0])
		b.WriteString(")")
	case op == "iuminus":
		b.WriteString("-")
		m.operand(b, args[0])
	case op == "to" && len(args) == 2:
		m.operand(b, args[0])
		b.WriteString(".to(")
		m.print(b, args[1])
		b.WriteString(")")
	case infix[op] != "":
		for i, a := range args {
			if i > 0 {
				b.WriteString(" " + infix[op] + " ")
			}
			m.operand(b, a)
		}
	case op == "ite":
		b.WriteString("if (")
		m.print(b, args[0])
		b.WriteString(") ")
		m.operand(b, args[1])
		b.WriteString(" else ")
		m.operand(b, args[2])
	case op == "assign":
		m.print(b, args[0])
		b.WriteString("' = ")
		m.print(b, args[1])
	case op == "actionAll" || op == "actionAny":
		b.WriteString(map[string]string{"actionAll": "all", "actionAny": "any"}[op] + " { ")
		m.list(b, args)
		b.WriteString(" }")
	case op == "field" && len(args) == 2 && args[1].Kind == "str":
		m.operand(b, args[0])
		b.WriteString("." + args[1].Value.String())
	case op == "item" && len(args) == 2:
		m.operand(b, args[0])
		b.WriteString("._" + args[1].Value.String())
	case op == "nth" && len(args) == 2:
		m.operand(b, args[0])
		b.WriteString("[")
		m.print(b, args[1])
		b.WriteString("]")
	case op == "Rec":
		b.WriteString("{ ")
		for i := 0; i+1 < len(args); i += 2 {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(args[i].Value.String() + ": ")
			m.print(b, args[i+1])
		}
		b.WriteString(" }")
	case op == "Tup":
		b.WriteString("(")
		m.list(b, args)
		b.WriteString(")")
	case op == "List":
		b.WriteString("[")
		m.list(b, args)
		b.WriteString("]")
	case op == "Set" || op == "Map":
		b.WriteString(op + "(")
		if op == "Map" {
			for i, a := range args {
				if i > 0 {
					b.WriteString(", ")
				}
				if a.Kind == "app" && a.Opcode == "Tup" && len(a.Args) == 2 {
					m.print(b, a.Args[0])
					b.WriteString(" -> ")
					m.print(b, a.Args[1])
				} else {
					m.print(b, a)
				}
			}
		} else {
			m.list(b, args)
		}
		b.WriteString(")")
	case op == "variant" && len(args) == 2:
		b.WriteString(args[0].Value.String())
		if !(args[1].Kind == "app" && args[1].Opcode == "Tup" && len(args[1].Args) == 0) {
			b.WriteString("(")
			m.print(b, args[1])
			b.WriteString(")")
		}
	case op == "matchVariant" && len(args) >= 1:
		b.WriteString("match ")
		m.operand(b, args[0])
		b.WriteString(" {")
		for i := 1; i+1 < len(args); i += 2 {
			b.WriteString(" | " + args[i].Value.String() + "(")
			if l := args[i+1]; l.Kind == "lambda" && len(l.Params) == 1 {
				b.WriteString(m.Display(l.Params[0].Name) + ") => ")
				m.print(b, l.Expr)
			} else {
				b.WriteString("_) => ")
				m.print(b, l)
			}
		}
		b.WriteString(" }")
	case methods[op] && len(args) > 0:
		m.operand(b, args[0])
		b.WriteString("." + op + "(")
		m.list(b, args[1:])
		b.WriteString(")")
	default:
		b.WriteString(op + "(")
		m.list(b, args)
		b.WriteString(")")
	}
}

// builtin reports whether op is one of quint's builtins (the evaluator's,
// or one it knows to print).
func builtin(op string) bool {
	if infix[op] != "" || methods[op] {
		return true
	}
	switch op {
	case "not", "iuminus", "ite", "assign", "actionAll", "actionAny", "field", "item", "Rec", "Tup", "List", "Set", "Map",
		"variant", "matchVariant", "assert", "range", "tuples", "next", "always", "eventually", "enabled", "orKeep", "mustChange",
		"weakFair", "strongFair", "then", "reps", "fail", "q::debug":
		return true
	}
	return false
}

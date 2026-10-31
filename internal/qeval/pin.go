package qeval

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// pinIdentifier is the spelling of an identifier supplied by the caller's
// visible-variable map. Read-only; expressions never come from this map.
var pinIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(::[A-Za-z_][A-Za-z0-9_]*)*$`)

// PinAction specializes an actual finite transition of action to deterministic
// Quint text. It retains the original symbolic guards, domains and assignments;
// it does not synthesize assignments from to. vars maps IR variable identities
// to their names visible in the replay's imported module. Unknown identities,
// unsupported expressions and excessive expansion refuse the pin.
//
// Equivalent choices reaching to may produce a different legal specialization
// than a Transitions label; callers must not describe it using those Picks.
func (m *Model) PinAction(consts map[string]Value, from State, action string, args []Value, to State, vars map[string]string) (text string, err error) {
	defer recoverInto(&err)
	ordinary := *m
	ordinary.replaying = false
	ordinary.fp = nil // pin validation must not alter another evaluation's footprint
	m = &ordinary
	d, ok := m.Decl(action)
	if !ok || d.Qualifier != "action" {
		return "", unsupported("pin", "%s is not an action", action)
	}
	if len(to) != len(m.Vars) {
		return "", unsupported("pin", "target does not name every state variable")
	}
	used := map[string]bool{}
	prefix := "__gtg_pin_"
	for _, v := range m.Vars {
		n := vars[v]
		if !pinIdentifier.MatchString(n) || used[n] {
			return "", unsupported("pin", "variable %s has no unique visible replay name", v)
		}
		used[n] = true
	}
	for {
		collision := false
		for n := range used {
			collision = collision || strings.HasPrefix(n, prefix)
		}
		if !collision {
			break
		}
		prefix += "p_"
	}
	ev := m.evaluation(consts, from)
	body, en, err := ev.bindArgs(d, args, nil, action)
	if err != nil {
		return "", err
	}
	bound := map[int64]*Expr{}
	if d.Expr.Kind == "lambda" {
		for i, p := range d.Expr.Params {
			bound[p.ID] = pinLiteral(args[i])
		}
	}
	p := &pinner{m: m, ev: ev, target: to, vars: vars, prefix: prefix}
	if !p.admits(body, en, true) {
		return "", unsupported("pin", "target is not a successor of %s", action)
	}
	x, err := p.action(body, en, bound)
	if err != nil {
		return "", err
	}
	// The expanded tree has source-bound identifiers, not original IR references.
	// Original resolution/display tables could capture a generated node id/name.
	printer := &Model{ren: &renames{}}
	return printer.Text(x), nil
}

type pinner struct {
	m      *Model
	ev     *evaluation
	target State
	vars   map[string]string
	prefix string
	nodes  int
}

func (p *pinner) node() error {
	p.nodes++
	if p.nodes > 10000 {
		return unsupported("pin", "expression expansion exceeds 10000 nodes")
	}
	return nil
}

// admits checks a sub-action's partial assignment against the complete target.
func (p *pinner) admits(e *Expr, en *env, complete bool) bool {
	xs, err := p.ev.action(e, en)
	if err != nil {
		throw(err)
	}
	for _, x := range xs {
		if complete && len(x.a) != len(p.m.Vars) {
			continue
		}
		ok := true
		for v, value := range x.a {
			want, exists := p.target[v]
			if !exists || Compare(value, want) != 0 {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func pinApp(op string, args ...*Expr) *Expr { return &Expr{Kind: "app", Opcode: op, Args: args} }
func pinName(name string) *Expr             { return &Expr{Kind: "pinnedName", Name: name} }
func pinBindings(in map[int64]*Expr) map[int64]*Expr {
	out := make(map[int64]*Expr, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (p *pinner) action(e *Expr, en *env, bs map[int64]*Expr) (*Expr, error) {
	if err := p.node(); err != nil {
		return nil, err
	}
	if e.Kind == "let" {
		d := e.Opdef
		if d.Qualifier == "nondet" {
			values, err := p.ev.choiceSet(d, en)
			if err != nil {
				return nil, err
			}
			domain, err := p.expr(d.Expr.Args[0], bs)
			if err != nil {
				return nil, err
			}
			for _, v := range values.Elems {
				next := en.bind(d.Name, v)
				if !p.admits(e.Expr, next, false) {
					continue
				}
				bound := pinBindings(bs)
				bound[d.ID] = pinLiteral(v)
				body, err := p.action(e.Expr, next, bound)
				if err != nil {
					return nil, err
				}
				return pinApp("actionAll", pinApp("contains", domain, pinLiteral(v)), body), nil
			}
			return nil, unsupported("pin", "no finite choice reaches target")
		}
		value, err := p.expr(d.Expr, bs)
		if err != nil {
			return nil, err
		}
		bound := pinBindings(bs)
		bound[d.ID] = value
		return p.action(e.Expr, p.ev.bindLet(d, en), bound)
	}
	if e.Kind == "app" {
		switch e.Opcode {
		case "actionAny":
			for _, a := range e.Args {
				if p.admits(a, en, false) {
					return p.action(a, en, bs)
				}
			}
			return nil, unsupported("pin", "no any branch reaches target")
		case "actionAll":
			out := pinApp("actionAll")
			for _, a := range e.Args {
				x, err := p.action(a, en, bs)
				if err != nil {
					return nil, err
				}
				out.Args = append(out.Args, x)
			}
			return out, nil
		case "ite":
			guard, err := p.expr(e.Args[0], bs)
			if err != nil {
				return nil, err
			}
			branch := 1
			if !p.ev.boolOf(e.Args[0], en) {
				branch = 2
				guard = pinApp("not", guard)
			}
			body, err := p.action(e.Args[branch], en, bs)
			if err != nil {
				return nil, err
			}
			return pinApp("actionAll", guard, body), nil
		}
	}
	if d, args, ok := p.ev.calledAction(e, en); ok {
		body, next, err := p.ev.bindArgs(d, args, nil, d.Name)
		if err != nil {
			return nil, err
		}
		bound := map[int64]*Expr{}
		if d.Expr.Kind == "lambda" {
			for i, param := range d.Expr.Params {
				x, err := p.expr(e.Args[i], bs)
				if err != nil {
					return nil, err
				}
				bound[param.ID] = x
			}
		}
		return p.action(body, next, bound)
	}
	return p.expr(e, bs)
}

func (p *pinner) expr(e *Expr, bs map[int64]*Expr) (*Expr, error) {
	if err := p.node(); err != nil {
		return nil, err
	}
	if e == nil {
		return nil, unsupported("pin", "missing expression")
	}
	if e.Kind == "name" || e.Kind == "app" {
		if r, ok := p.m.refs[e.ID]; ok {
			if value, ok := bs[r.decl]; ok {
				if e.Kind == "name" {
					return value, nil
				}
				return p.apply(value, e.Args, bs)
			}
			switch r.kind {
			case "var":
				d := p.m.byID[r.decl]
				if d == nil || p.vars[d.Name] == "" {
					return nil, unsupported("pin", "unmapped state variable")
				}
				return pinName(p.vars[d.Name]), nil
			case "const":
				d := p.m.byID[r.decl]
				if d == nil {
					return nil, unsupported("pin", "unknown constant")
				}
				v, ok := p.ev.consts[d.Name]
				if !ok {
					return nil, unsupported("pin", "constant %s is not bound", d.Name)
				}
				return pinLiteral(v), nil
			case "def":
				d := p.m.byID[r.decl]
				if d == nil || d.Qualifier == "action" || d.Qualifier == "nondet" {
					return nil, unsupported("pin", "unresolved or action-valued definition")
				}
				if e.Kind == "name" {
					return p.expr(d.Expr, map[int64]*Expr{})
				}
				return p.inline(d.Expr, e.Args, bs)
			case "param":
				return nil, unsupported("pin", "unbound operator parameter %s", r.param)
			}
		}
	}
	switch e.Kind {
	case "int", "str", "bool":
		copy := *e
		copy.ID = 0
		return &copy, nil
	case "name":
		if e.Name == "Int" || e.Name == "Nat" || e.Name == "Bool" {
			return pinName(e.Name), nil
		}
		return nil, unsupported("pin", "unresolved name %s", e.Name)
	case "lambda":
		bound := pinBindings(bs)
		copy := *e
		copy.ID = 0
		copy.Params = append([]Param(nil), e.Params...)
		for i, param := range e.Params {
			name := p.prefix + strconv.FormatInt(param.ID, 10)
			bound[param.ID] = pinName(name)
			copy.Params[i].Name = name
		}
		body, err := p.expr(e.Expr, bound)
		copy.Expr = body
		return &copy, err
	case "let":
		if e.Opdef.Qualifier == "action" || e.Opdef.Qualifier == "nondet" {
			return nil, unsupported("pin", "choice or action in value expression")
		}
		value, err := p.expr(e.Opdef.Expr, bs)
		if err != nil {
			return nil, err
		}
		bound := pinBindings(bs)
		bound[e.Opdef.ID] = value
		return p.expr(e.Expr, bound)
	case "app":
		if !builtin(e.Opcode) || e.Opcode == "oneOf" || e.Opcode == "variant" || e.Opcode == "matchVariant" {
			return nil, unsupported("pin", "operator %s has no supported source representation", e.Opcode)
		}
		out := pinApp(e.Opcode)
		for _, a := range e.Args {
			x, err := p.expr(a, bs)
			if err != nil {
				return nil, err
			}
			out.Args = append(out.Args, x)
		}
		return out, nil
	}
	return nil, unsupported("pin", "expression kind %s", e.Kind)
}

func (p *pinner) inline(lambda *Expr, args []*Expr, bs map[int64]*Expr) (*Expr, error) {
	if lambda.Kind != "lambda" || len(lambda.Params) != len(args) {
		return nil, unsupported("pin", "definition application does not match parameters")
	}
	bound := map[int64]*Expr{}
	for i, param := range lambda.Params {
		value, err := p.expr(args[i], bs)
		if err != nil {
			return nil, err
		}
		bound[param.ID] = value
	}
	return p.expr(lambda.Expr, bound)
}

func (p *pinner) apply(lambda *Expr, args []*Expr, bs map[int64]*Expr) (*Expr, error) {
	// Bound operator values have already been expanded with hygienic parameters.
	// Applying them would require a second reference table; leave this explicitly
	// outside the first pinning fragment rather than guess at name substitution.
	return nil, unsupported("pin", "application of a local operator value")
}

func pinLiteral(v Value) *Expr {
	switch x := v.(type) {
	case Int:
		return &Expr{Kind: "int", Value: json.Number(strconv.FormatInt(int64(x), 10))}
	case Bool:
		return &Expr{Kind: "bool", Bool: bool(x)}
	case Str:
		return &Expr{Kind: "str", Value: json.Number(x)}
	case Tup:
		out := pinApp("Tup")
		for _, v := range x {
			out.Args = append(out.Args, pinLiteral(v))
		}
		return out
	case List:
		out := pinApp("List")
		for _, v := range x {
			out.Args = append(out.Args, pinLiteral(v))
		}
		return out
	case *Set:
		out := pinApp("Set")
		for _, v := range x.Elems {
			out.Args = append(out.Args, pinLiteral(v))
		}
		return out
	case *Map:
		out := pinApp("Map")
		for i, k := range x.Keys {
			out.Args = append(out.Args, pinApp("Tup", pinLiteral(k), pinLiteral(x.Vals[i])))
		}
		return out
	case *Rec:
		out := pinApp("Rec")
		for i, n := range x.Names {
			out.Args = append(out.Args, pinLiteral(Str(n)), pinLiteral(x.Vals[i]))
		}
		return out
	default:
		throw(unsupported("pin", "value %s is not a supported literal", fmt.Sprint(v)))
	}
	return nil
}

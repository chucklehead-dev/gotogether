package binding

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Model is what gotogether needs to know about a Quint module: its actions with
// their parameter names and types, its vals (candidate invariants), its vars
// and consts. It is read from `quint typecheck --out`, so it is always the
// model's own view of itself.
type Model struct {
	Module  string
	Actions map[string][]Param
	Vals    map[string]bool
	// Antecedents are the boolean vals of the form `A implies B`, with A
	// as Quint text: where A never holds, the val holds vacuously.
	Antecedents map[string]string
	Vars        []string
	Consts      map[string]string // name -> type
}

// Param is an action parameter. Shape is the kind of its resolved type
// (type aliases expanded): "set", "list", "map", or "" for anything else.
type Param struct{ Name, Type, Shape string }

// DefaultTemplate is the template of a parameter the binding does not
// give: the step field of the same name, converted from its recorded JSON
// form when the parameter is a set, list or map. A parameter named like
// one of the step's own fields (process, outcome, obs, step, args) is the
// argument, through .args: {{.process}} is the actor's incarnation.
func (p Param) DefaultTemplate() string {
	f := "." + p.Name
	if reserved[p.Name] {
		f = ".args." + p.Name
	}
	switch p.Shape {
	case "set":
		return "{{ toSet " + f + " }}"
	case "list":
		return "{{ toList " + f + " }}"
	case "map":
		return "{{ toMap " + f + " }}"
	}
	return "{{" + f + "}}"
}

// reserved are the template names that are the step's, whatever its
// arguments (StepData); seq and action are not: an argument wins there.
// Read-only.
var reserved = map[string]bool{"process": true, "outcome": true, "obs": true, "step": true, "args": true}

// Signature renders an action as `name(p: T, ...)`.
func (m *Model) Signature(action string) string {
	ps := m.Actions[action]
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.Name + ": " + p.Type
	}
	return action + "(" + strings.Join(parts, ", ") + ")"
}

// ActionNames in sorted order.
func (m *Model) ActionNames() []string {
	var out []string
	for a := range m.Actions {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// LoadModel typechecks spec with the quint CLI and extracts module.
func LoadModel(quint, spec, module string) (*Model, error) {
	if quint == "" {
		quint = "quint"
	}
	tmp, err := os.CreateTemp("", "gotogether-typecheck-*.json")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	cmd := exec.Command(quint, "typecheck", filepath.Base(spec), "--out", tmp.Name())
	cmd.Dir = filepath.Dir(spec)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("quint typecheck %s: %v\n%s", spec, err, out)
	}
	b, err := os.ReadFile(tmp.Name())
	if err != nil {
		return nil, err
	}
	return parseTypecheck(b, module)
}

type tcExpr struct {
	ID     json.Number `json:"id"`
	Kind   string      `json:"kind"`
	Params []struct {
		Name string          `json:"name"`
		Type json.RawMessage `json:"typeAnnotation"`
		ID   json.Number     `json:"id"`
	} `json:"params"`
}

type tcDecl struct {
	ID        json.Number     `json:"id"`
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	Qualifier string          `json:"qualifier"`
	Expr      *tcExpr         `json:"expr"`
	RawExpr   json.RawMessage `json:"-"`
	Type      json.RawMessage `json:"typeAnnotation"`
	Def       json.RawMessage `json:"type"` // of a typedef
}

type tcOut struct {
	Modules []struct {
		Name         string   `json:"name"`
		Declarations []tcDecl `json:"declarations"`
	} `json:"modules"`
	Types map[string]struct {
		Type json.RawMessage `json:"type"`
	} `json:"types"`
}

func parseTypecheck(b []byte, module string) (*Model, error) {
	var tc tcOut
	if err := json.Unmarshal(b, &tc); err != nil {
		return nil, err
	}
	// each declaration's expression as JSON too, for the antecedents
	var raw struct {
		Modules []struct {
			Declarations []struct {
				Expr json.RawMessage `json:"expr"`
			} `json:"declarations"`
		} `json:"modules"`
	}
	if json.Unmarshal(b, &raw) == nil && len(raw.Modules) == len(tc.Modules) {
		for i := range tc.Modules {
			for j := range tc.Modules[i].Declarations {
				if j < len(raw.Modules[i].Declarations) {
					tc.Modules[i].Declarations[j].RawExpr = raw.Modules[i].Declarations[j].Expr
				}
			}
		}
	}
	// type aliases, from every module: a parameter's Shape looks through them
	aliases := map[string]json.RawMessage{}
	for _, mod := range tc.Modules {
		for _, d := range mod.Declarations {
			if d.Kind == "typedef" && len(d.Def) > 0 {
				aliases[d.Name] = d.Def
			}
		}
	}
	for _, mod := range tc.Modules {
		if mod.Name != module {
			continue
		}
		m := &Model{Module: module, Actions: map[string][]Param{}, Vals: map[string]bool{}, Consts: map[string]string{}, Antecedents: map[string]string{}}
		for _, d := range mod.Declarations {
			switch {
			case d.Kind == "var":
				m.Vars = append(m.Vars, d.Name)
			case d.Kind == "const":
				m.Consts[d.Name] = renderType(d.Type)
			case d.Kind == "def" && d.Qualifier == "action":
				var ps []Param
				if d.Expr != nil && d.Expr.Kind == "lambda" {
					// parameter types: from the annotation, else the inferred operator type
					var inferred []json.RawMessage
					if t, ok := tc.Types[string(d.ID)]; ok {
						var op struct {
							Kind string            `json:"kind"`
							Args []json.RawMessage `json:"args"`
						}
						if json.Unmarshal(t.Type, &op) == nil && op.Kind == "oper" {
							inferred = op.Args
						}
					}
					for i, p := range d.Expr.Params {
						typ, shape := renderType(p.Type), typeShape(p.Type, aliases)
						if i < len(inferred) {
							if typ == "?" {
								typ = renderType(inferred[i])
							}
							shape = typeShape(inferred[i], aliases)
						}
						ps = append(ps, Param{Name: p.Name, Type: typ, Shape: shape})
					}
				}
				m.Actions[d.Name] = ps
			case d.Kind == "def" && (d.Qualifier == "val" || d.Qualifier == "def"):
				// candidate invariants: boolean vals (not operators, not sum-type constructors)
				if d.Expr != nil && d.Expr.Kind != "lambda" {
					if t, ok := tc.Types[string(d.ID)]; ok && renderType(t.Type) == "bool" {
						m.Vals[d.Name] = true
						if a, ok := antecedent(d.RawExpr); ok {
							m.Antecedents[d.Name] = a
						}
					}
				}
			}
		}
		return m, nil
	}
	return nil, fmt.Errorf("module %q not found in typecheck output", module)
}

// renderType prints a Quint type from its JSON IR (enough for messages).
func renderType(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "?"
	}
	var t struct {
		Kind   string            `json:"kind"`
		Name   string            `json:"name"`
		Elem   json.RawMessage   `json:"elem"`
		Args   []json.RawMessage `json:"args"`
		Res    json.RawMessage   `json:"res"`
		Fields json.RawMessage   `json:"fields"`
	}
	if json.Unmarshal(raw, &t) != nil {
		return "?"
	}
	switch t.Kind {
	case "const", "var":
		return t.Name
	case "int", "bool", "str":
		return t.Kind
	case "set":
		return "Set[" + renderType(t.Elem) + "]"
	case "list":
		return "List[" + renderType(t.Elem) + "]"
	case "rec":
		return "{...}"
	case "sum":
		return "(sum type)"
	case "fun":
		return "(" + renderType(firstArg(t.Args)) + " -> ...)"
	}
	return t.Kind
}

// typeShape is the Param.Shape of a Quint type's JSON IR.
func typeShape(raw json.RawMessage, aliases map[string]json.RawMessage) string {
	var t struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	for range 10 { // alias chains, bounded
		if len(raw) == 0 || json.Unmarshal(raw, &t) != nil {
			return ""
		}
		a, ok := aliases[t.Name]
		if t.Kind != "const" || !ok {
			break
		}
		raw = a
	}
	switch t.Kind {
	case "set", "list":
		return t.Kind
	case "fun":
		return "map"
	}
	return ""
}

func firstArg(a []json.RawMessage) json.RawMessage {
	if len(a) == 0 {
		return nil
	}
	return a[0]
}

// antecedent is A of a val `A implies B`, rendered as Quint, if the
// val's expression is one and A is made of what renderIR prints.
func antecedent(raw json.RawMessage) (string, bool) {
	var e irExpr
	if json.Unmarshal(raw, &e) != nil || e.Kind != "app" || e.Opcode != "implies" || len(e.Args) != 2 {
		return "", false
	}
	return renderIR(e.Args[0])
}

type irExpr struct {
	Kind   string            `json:"kind"`
	Name   string            `json:"name"`
	Opcode string            `json:"opcode"`
	Args   []json.RawMessage `json:"args"`
	Value  json.RawMessage   `json:"value"`
	Params []struct {
		Name string `json:"name"`
	} `json:"params"`
	Expr json.RawMessage `json:"expr"`
}

// infix are the built-in operators written between their arguments.
// Read-only.
var infix = map[string]string{"eq": "==", "neq": "!=", "ilt": "<", "ilte": "<=", "igt": ">", "igte": ">=",
	"iadd": "+", "isub": "-", "imul": "*", "idiv": "/", "imod": "%", "and": "and", "or": "or", "implies": "implies", "iff": "iff"}

// renderIR prints a Quint expression from its JSON IR: names, literals,
// operator applications (infix where Quint writes them so, else
// op(args)) and lambdas. Anything else (let-in, nondet) is not printed.
func renderIR(raw json.RawMessage) (string, bool) {
	var e irExpr
	if json.Unmarshal(raw, &e) != nil {
		return "", false
	}
	switch e.Kind {
	case "name":
		return e.Name, e.Name != ""
	case "bool", "int":
		return string(e.Value), len(e.Value) > 0
	case "str":
		var s string
		if json.Unmarshal(e.Value, &s) != nil {
			return "", false
		}
		return fmt.Sprintf("%q", s), true
	case "lambda":
		body, ok := renderIR(e.Expr)
		if !ok || len(e.Params) == 0 {
			return "", false
		}
		names := make([]string, len(e.Params))
		for i, p := range e.Params {
			names[i] = p.Name
		}
		ps := names[0]
		if len(names) > 1 {
			ps = "(" + strings.Join(names, ", ") + ")"
		}
		return "(" + ps + " => " + body + ")", true
	case "app":
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			s, ok := renderIR(a)
			if !ok {
				return "", false
			}
			args[i] = s
		}
		if op, ok := infix[e.Opcode]; ok && len(args) >= 2 {
			return "(" + strings.Join(args, " "+op+" ") + ")", true
		}
		if e.Opcode == "not" && len(args) == 1 {
			return "not(" + args[0] + ")", true
		}
		return e.Opcode + "(" + strings.Join(args, ", ") + ")", e.Opcode != ""
	}
	return "", false
}

package binding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/memo"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/quintnames"
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
	// Helpers are the frame conditions (`sameNet`: every assignment
	// leaves a variable as it was) other actions call and step never
	// names: parts of steps, not steps. Scaffold writes none, and a lint's
	// list of actions leaves them out. A nested model's real steps, called
	// through another action, assign something: they are no helpers.
	Helpers map[string]bool
	// assigns are the variables each action of the file's modules assigns
	// itself, and calls are the names it applies: an action's
	// assignments are its own and its calls' (Assigns)
	assigns, calls map[string]map[string]bool
	// reads are the names each action reads outside its assignments'
	// left sides: of the variables among them, the state it reads
	reads map[string]map[string]bool
	// typedefs are the type aliases of the file's modules, for toQuint;
	// types caches the types its templates name (src -> *qtype or error)
	typedefs map[string]typedef
	types    *memo.Map[string, typeResult]
}

// Reads reports which of the model's variables action reads, itself or
// through the actions it calls (a snapshot or a resume, which start a
// trace from no state, must read none), and whether the model says.
func (m *Model) Reads(action string) (map[string]bool, bool) {
	if m.reads == nil {
		return nil, false
	}
	vars := map[string]bool{}
	for _, v := range m.Vars {
		vars[v] = true
	}
	out, seen := map[string]bool{}, map[string]bool{}
	var walk func(a string)
	walk = func(a string) {
		if seen[a] {
			return
		}
		seen[a] = true
		for n := range m.reads[a] {
			if vars[n] {
				out[n] = true
			}
		}
		for n := range m.calls[a] {
			if _, ok := m.assigns[n]; ok {
				walk(n)
			}
		}
	}
	walk(action)
	return out, true
}

// Assigns reports which of the model's variables action assigns, itself
// or through the actions it calls (on any path: an action that assigns a
// variable on one branch only is quint's effect checker's to refuse), and
// whether the model says (a Model made by hand does not).
func (m *Model) Assigns(action string) (map[string]bool, bool) {
	if m.assigns == nil {
		return nil, false
	}
	out, seen := map[string]bool{}, map[string]bool{}
	var walk func(a string)
	walk = func(a string) {
		if seen[a] {
			return
		}
		seen[a] = true
		for v := range m.assigns[a] {
			out[v] = true
		}
		for n := range m.calls[a] {
			if _, ok := m.assigns[n]; ok {
				walk(n)
			}
		}
	}
	walk(action)
	return out, true
}

// Param is an action parameter. Type is its type as Quint writes it
// (aliases by name). Shape is the kind of its resolved type (type aliases
// expanded): "set", "list", "map", or "" for anything else.
type Param struct {
	Name, Type, Shape string

	typ *qtype // the resolved type, when the model gave it
}

// DefaultTemplate is the template of a parameter the binding does not
// give: the step field of the same name, converted from its recorded JSON
// form when the parameter is a set, list or map. A parameter whose type
// holds a record or a sum, or a collection toSet, toList and toMap do not
// render (of collections, of records), is converted by the model's type:
// {{ toQuint "List[Event]" .events }}. A parameter named like one of the
// step's own fields (process, outcome, obs, step, args) is the argument,
// through .args: {{.process}} is the actor's incarnation.
func (p Param) DefaultTemplate() string {
	f := "." + p.Name
	if reserved[p.Name] {
		f = ".args." + p.Name
	}
	if p.typ != nil && p.typ.structured() {
		return "{{ toQuint " + strconv.Quote(p.Type) + " " + f + " }}"
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
	return slices.Sorted(maps.Keys(m.Actions))
}

// LoadModel typechecks spec with the quint CLI and extracts module.
func LoadModel(quint, spec, module string) (*Model, error) {
	if quint == "" {
		quint = "quint"
	}
	if _, err := os.Stat(spec); errors.Is(err, fs.ErrNotExist) {
		// quint's error would be the chdir into its missing directory
		return nil, fmt.Errorf("quint typecheck: the spec %s does not exist (a binding's spec is relative to the binding file's directory; gotogether lint -binding names the path to write)", spec)
	}
	tmp, err := os.CreateTemp("", "gotogether-typecheck-*.json")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	cmd := quintcmd.Command(context.Background(), quint, filepath.Dir(spec), "typecheck", filepath.Base(spec), "--out", tmp.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		// with --out, quint writes its errors there, not to its output: a
		// refused model once said only "exit status 1"
		out = append(out, typecheckErrors(tmp.Name())...)
		return nil, fmt.Errorf("quint typecheck %s: %w\n%s%s", spec, err, out, reservedHint(string(out)))
	}
	b, err := os.ReadFile(tmp.Name())
	if err != nil {
		return nil, err
	}
	return parseTypecheck(b, module)
}

// typecheckErrors are the errors quint typecheck --out wrote to path,
// one per line with where each is (file:line:col, 1-based), or nothing
// when it wrote none.
func typecheckErrors(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out struct {
		Errors []struct {
			Explanation string `json:"explanation"`
			Locs        []struct {
				Source string `json:"source"`
				Start  struct{ Line, Col int }
			} `json:"locs"`
		} `json:"errors"`
	}
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	var b []byte
	for _, e := range out.Errors {
		if len(e.Locs) > 0 {
			l := e.Locs[0]
			b = fmt.Appendf(b, "%s:%d:%d: ", l.Source, l.Start.Line+1, l.Start.Col+1)
		}
		b = fmt.Appendf(b, "%s\n", e.Explanation)
	}
	return b
}

// builtinRedefined is quint's refusal of a built-in's name as an
// identifier (QNT101). Read-only.
var builtinRedefined = regexp.MustCompile(`Built-in name '([A-Za-z_][A-Za-z0-9_]*)' is redefined`)

// reservedHint is the fix for quint's refusals of reserved names in its
// output: a model's parameter named after a step's field (item, to) that
// quint keeps for a built-in, renamed, and the binding mapping the field
// to it. "" when quint refused none.
func reservedHint(out string) string {
	var names []string
	for _, m := range builtinRedefined.FindAllStringSubmatch(out, -1) {
		if !slices.Contains(names, m[1]) && quintnames.Reserved(m[1]) {
			names = append(names, m[1])
		}
	}
	if len(names) == 0 {
		return ""
	}
	n, are := names[0], "is a Quint built-in's name"
	if len(names) > 1 {
		are = "are Quint built-ins' names"
	}
	return fmt.Sprintf("%s %s, which no parameter may take: rename it (%s, say) and map the step's field to it in the binding (args: %s: '{{.%s}}'); gotogether scaffold writes that mapping for a parameter so renamed\n",
		strings.Join(names, ", "), are, quintnames.Rename(n), quintnames.Rename(n), n)
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
	Def       json.RawMessage `json:"type"`   // of a typedef
	TypeVars  []string        `json:"params"` // of a typedef
	// an import's: the module, the one name it imports ("*": every
	// one; "": none, the module's names qualified), and the qualifier
	ProtoName     string `json:"protoName"`
	DefName       string `json:"defName"`
	QualifiedName string `json:"qualifiedName"`
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

// ParseTypecheck is module's model from the JSON `quint typecheck --out`
// wrote, as LoadModel reads it: for a caller that keeps quint's output
// (validate's cache of typechecks across processes).
func ParseTypecheck(data []byte, module string) (*Model, error) { return parseTypecheck(data, module) }

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
	// type aliases, from every module: a parameter's Shape looks through
	// them. toQuint's are scoped as Quint scopes them: a module's own, and
	// those it imports (lib.T, lib.* or lib::T), each resolved in its own
	// module's scope. Two modules of one file may each have their Event,
	// and keyed by name alone the last one read was every module's.
	aliases := map[string]json.RawMessage{}
	for _, mod := range tc.Modules {
		for _, d := range mod.Declarations {
			if d.Kind == "typedef" && len(d.Def) > 0 {
				aliases[d.Name] = d.Def
			}
		}
	}
	for _, mod := range tc.Modules { // the model's own win the Shape's lookup
		if mod.Name == module {
			for _, d := range mod.Declarations {
				if d.Kind == "typedef" && len(d.Def) > 0 {
					aliases[d.Name] = d.Def
				}
			}
		}
	}
	typedefs := typeScopes(&tc)[module]
	if typedefs == nil {
		typedefs = map[string]typedef{}
	}
	// every module's actions' assignments and calls, by name: an action
	// may assign through one it imports
	assigns, calls, reads := map[string]map[string]bool{}, map[string]map[string]bool{}, map[string]map[string]bool{}
	for _, mod := range tc.Modules {
		for _, d := range mod.Declarations {
			if d.Kind == "def" && d.Qualifier == "action" {
				assigns[d.Name], calls[d.Name], reads[d.Name] = assigned(d.RawExpr), namesIn(d.RawExpr), readNames(d.RawExpr)
			}
		}
	}
	// a module's names are its own and those it re-exports whole (`export
	// lib.*`, transitively: a generated skeleton's module the model's
	// exports), its own last so that they win
	decls := map[string][]tcDecl{}
	for _, mod := range tc.Modules {
		decls[mod.Name] = mod.Declarations
	}
	var visible []tcDecl
	seenMod := map[string]bool{}
	var gather func(name string)
	gather = func(name string) {
		if seenMod[name] {
			return
		}
		seenMod[name] = true
		for _, d := range decls[name] {
			if d.Kind == "export" && d.DefName == "*" && d.QualifiedName == "" {
				gather(d.ProtoName)
			}
		}
		visible = append(visible, decls[name]...)
	}
	for _, mod := range tc.Modules {
		if mod.Name != module {
			continue
		}
		gather(module)
		m := &Model{Module: module, Actions: map[string][]Param{}, Vals: map[string]bool{}, Consts: map[string]string{}, Antecedents: map[string]string{}, Helpers: map[string]bool{},
			assigns: assigns, calls: calls, reads: reads, typedefs: typedefs, types: &memo.Map[string, typeResult]{}}
		calledBy := map[string]map[string]bool{} // action -> the names its body refers to
		frames := map[string]bool{}              // actions that leave every variable they assign as it was
		for _, d := range visible {
			switch {
			case d.Kind == "var":
				if !slices.Contains(m.Vars, d.Name) {
					m.Vars = append(m.Vars, d.Name)
				}
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
						raw := p.Type
						typ, shape := renderType(raw), typeShape(raw, aliases)
						if i < len(inferred) {
							if typ == "?" {
								raw, typ = inferred[i], renderType(inferred[i])
							}
							shape = typeShape(inferred[i], aliases)
						}
						param := Param{Name: p.Name, Type: typ, Shape: shape}
						if t, err := irType(raw, typedefs, nil, 0); err == nil {
							param.typ = t
						}
						ps = append(ps, param)
					}
				}
				m.Actions[d.Name] = ps
				calledBy[d.Name] = namesIn(d.RawExpr)
				if unchanging(d.RawExpr) {
					frames[d.Name] = true
				}
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
		// a helper: called by an action, and not by step (whose calls are
		// the steps); a model with no step has none
		if step, ok := calledBy["step"]; ok {
			for a, names := range calledBy {
				if a == "step" {
					continue
				}
				for n := range names {
					if frames[n] && n != a && !step[n] && n != "init" && n != "step" {
						m.Helpers[n] = true
					}
				}
			}
		}
		return m, nil
	}
	return nil, fmt.Errorf("module %q not found in typecheck output", module)
}

// unchanging reports whether an expression's IR assigns some variable
// and every assignment leaves its variable as it was (`y' = y`).
func unchanging(raw json.RawMessage) bool {
	assigns, same := 0, true
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["kind"] == "app" && x["opcode"] == "assign" {
				assigns++
				args, _ := x["args"].([]any)
				name := func(i int) string {
					if i < len(args) {
						if a, ok := args[i].(map[string]any); ok && a["kind"] == "name" {
							n, _ := a["name"].(string)
							return n
						}
					}
					return ""
				}
				same = same && name(0) != "" && name(0) == name(1)
			}
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		walk(v)
	}
	return assigns > 0 && same
}

// assigned are the variables an expression's IR assigns (x' = e).
func assigned(raw json.RawMessage) map[string]bool {
	out := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["kind"] == "app" && x["opcode"] == "assign" {
				if args, _ := x["args"].([]any); len(args) > 0 {
					if a, ok := args[0].(map[string]any); ok && a["kind"] == "name" {
						n, _ := a["name"].(string)
						out[n] = true
					}
				}
			}
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		walk(v)
	}
	return out
}

// readNames are the names an expression's IR reads: every name but an
// assignment's left side (x in x' = e).
func readNames(raw json.RawMessage) map[string]bool {
	out := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["kind"] == "name" {
				if n, ok := x["name"].(string); ok {
					out[n] = true
				}
			}
			if x["kind"] == "app" && x["opcode"] == "assign" {
				if args, _ := x["args"].([]any); len(args) > 1 {
					walk(args[1])
				}
				return
			}
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		walk(v)
	}
	return out
}

// namesIn are the names an expression's IR refers to: operators applied
// and names read.
func namesIn(raw json.RawMessage) map[string]bool {
	out := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			switch x["kind"] {
			case "app":
				if op, ok := x["opcode"].(string); ok {
					out[op] = true
				}
			case "name":
				if n, ok := x["name"].(string); ok {
					out[n] = true
				}
			}
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		walk(v)
	}
	return out
}

// typeScopes is each module's type aliases by the names it can use them
// by: its own, then what it imports. Each typedef carries its module's
// scope, which its definition's names are resolved in.
func typeScopes(tc *tcOut) map[string]map[string]typedef {
	scopes := map[string]map[string]typedef{}
	for _, mod := range tc.Modules {
		scopes[mod.Name] = map[string]typedef{}
	}
	for _, mod := range tc.Modules {
		for _, d := range mod.Declarations {
			if d.Kind == "typedef" && len(d.Def) > 0 {
				scopes[mod.Name][d.Name] = typedef{params: d.TypeVars, def: d.Def, scope: scopes[mod.Name]}
			}
		}
	}
	// imports, in module order (a module's imports precede it in the IR);
	// a module's own names are never shadowed by an import's
	for _, mod := range tc.Modules {
		own := map[string]bool{}
		for n := range scopes[mod.Name] {
			own[n] = true
		}
		for _, d := range mod.Declarations {
			if d.Kind != "import" {
				continue
			}
			from, ok := scopes[d.ProtoName]
			if !ok {
				continue
			}
			add := func(name, as string) {
				if td, ok := from[name]; ok && !own[as] {
					scopes[mod.Name][as] = td
				}
			}
			for name := range from {
				switch {
				case d.DefName == "*":
					add(name, name)
				case d.DefName == name:
					add(name, name)
				case d.DefName == "" && d.QualifiedName != "":
					add(name, d.QualifiedName+"::"+name)
				case d.DefName == "":
					add(name, d.ProtoName+"::"+name)
				}
			}
		}
	}
	return scopes
}

// renderType prints a Quint type from its JSON IR, aliases by name: as
// toQuint's type argument writes it ("?" for what it cannot print).
func renderType(raw json.RawMessage) string {
	t, err := irType(raw, nil, nil, 0)
	if err != nil {
		return "?"
	}
	return t.String()
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
		if json.Unmarshal(e.Value, &s) != nil || strings.Contains(s, `"`) {
			return "", false
		}
		return quintStr(s), true
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

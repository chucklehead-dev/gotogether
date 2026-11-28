// Package veilfrag reads a Quint model's IR (quint parse) for what quint's
// Veil backend (`quint verify --backend veil`) cannot prove, and for what
// makes a proof of it vacuous: the model lint's notes (`gotogether lint
// -veil`) and simulate -inductive's vacuity warnings share it (DESIGN
// §116). Every rule reads the parsed IR, never the text: a rule that the
// IR cannot decide is not here.
package veilfrag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
)

// A Finding is one Veil-blocking idiom or vacuous property: its rule
// (Code), where it is (Path, 1-based Line and Column), what it is and
// the fix.
type Finding struct {
	Code         string
	Path         string
	Line, Column int
	Symbol       string
	Message, Fix string
}

// Codes of the rules, as lint reports them.
const (
	CodeSize       = "model.veil.size"
	CodeKeysRemove = "model.veil.keysremove"
	CodeMatchMapBy = "model.veil.matchmapby"
	CodeMapLiteral = "model.veil.maplit"
	CodeFold       = "model.veil.fold"
	CodeAssume     = "model.veil.assume"
	CodeVacuous    = "model.veil.vacuous"
)

// Expr is a node of quint's parsed IR.
type Expr struct {
	ID     int64           `json:"id"`
	Kind   string          `json:"kind"`
	Name   string          `json:"name"`
	Opcode string          `json:"opcode"`
	Args   []*Expr         `json:"args"`
	Value  json.RawMessage `json:"value"`
	Expr   *Expr           `json:"expr"`
	Opdef  *Decl           `json:"opdef"`
}

// Decl is a declaration of a module.
type Decl struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Qualifier  string `json:"qualifier"`
	Expr       *Expr  `json:"expr"`
	Assumption *Expr  `json:"assumption"`
	ProtoName  string `json:"protoName"`
}

// Module is a parsed module.
type Module struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Declarations []Decl `json:"declarations"`
}

type entry struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	ID   int64  `json:"id"`
}

// Parsed is a model file as quint parse reads it: its modules (those of
// the files it imports too), the table resolving each name, and where
// each node is.
type Parsed struct {
	Modules []Module
	table   map[int64]entry
	where   map[int64]place
	files   map[int]string
	sources map[string][]byte
	// path is the file as the caller named it, abs its absolute path
	path, abs string
}

type place struct {
	file         int
	line, column int // 0-based, quint's
	start, end   int // byte offsets, end inclusive
}

// Parse reads the model file path with quint parse (quint: the binary,
// "" is quint): an error is quint's (not installed, or the model does
// not parse).
func Parse(ctx context.Context, quint, path string) (*Parsed, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp("", "gotogether-veilfrag-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	out, sm := filepath.Join(work, "ir.json"), filepath.Join(work, "sourcemap.json")
	combined, err := quintcmd.Command(ctx, quint, filepath.Dir(abs), "parse", "--out", out, "--source-map", sm, abs).CombinedOutput()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		if _, exited := errors.AsType[*exec.ExitError](err); exited {
			return nil, fmt.Errorf("quint parse %s: %w\n%s", path, err, combined)
		}
		return nil, fmt.Errorf("quint parse %s: %w", path, err)
	}
	return read(path, abs, out, sm)
}

func read(path, abs, out, sm string) (*Parsed, error) {
	data, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("quint parse %s: %w", path, err)
	}
	var ir struct {
		Modules []Module         `json:"modules"`
		Table   map[string]entry `json:"table"`
	}
	if err := json.Unmarshal(data, &ir); err != nil {
		return nil, fmt.Errorf("quint parse %s: its IR: %w", path, err)
	}
	p := &Parsed{Modules: ir.Modules, table: map[int64]entry{}, where: map[int64]place{}, files: map[int]string{}, sources: map[string][]byte{}, path: path, abs: abs}
	for k, e := range ir.Table {
		if id, err := strconv.ParseInt(k, 10, 64); err == nil {
			p.table[id] = e
		}
	}
	if data, err := os.ReadFile(sm); err == nil {
		var m struct {
			SourceIndex map[string]string            `json:"sourceIndex"`
			Map         map[string][]json.RawMessage `json:"map"`
		}
		if json.Unmarshal(data, &m) == nil {
			for k, f := range m.SourceIndex {
				if i, err := strconv.Atoi(k); err == nil {
					p.files[i] = f
				}
			}
			for k, v := range m.Map {
				id, err := strconv.ParseInt(k, 10, 64)
				if err != nil || len(v) != 3 {
					continue
				}
				var file int
				var start, end struct{ Line, Col, Index int }
				if json.Unmarshal(v[0], &file) != nil || json.Unmarshal(v[1], &start) != nil || json.Unmarshal(v[2], &end) != nil {
					continue
				}
				p.where[id] = place{file, start.Line, start.Col, start.Index, end.Index}
			}
		}
	}
	return p, nil
}

// Module is the module named name, nil if none.
func (p *Parsed) Module(name string) *Module {
	for i := range p.Modules {
		if p.Modules[i].Name == name {
			return &p.Modules[i]
		}
	}
	return nil
}

// Main is the module quint takes as main without --main: the one named
// as the file, else the file's last.
func (p *Parsed) Main() string {
	base := strings.TrimSuffix(filepath.Base(p.abs), filepath.Ext(p.abs))
	var last string
	for _, m := range p.Modules {
		if m.Name == base {
			return base
		}
		if p.inFile(m.ID) {
			last = m.Name
		}
	}
	return last
}

// inFile reports whether node id is in the parsed file itself (not a
// file it imports); a node with no position counts as in it.
func (p *Parsed) inFile(id int64) bool {
	w, ok := p.where[id]
	if !ok {
		return true
	}
	return filepath.Clean(p.files[w.file]) == filepath.Clean(p.abs)
}

// Text is node id's source text, "" if unknown.
func (p *Parsed) Text(id int64) string {
	w, ok := p.where[id]
	if !ok {
		return ""
	}
	f := p.files[w.file]
	src, ok := p.sources[f]
	if !ok {
		src, _ = os.ReadFile(f)
		p.sources[f] = src
	}
	if w.start < 0 || w.end >= len(src) || w.start > w.end {
		return ""
	}
	return string(src[w.start : w.end+1])
}

func (p *Parsed) finding(code string, id int64, symbol, msg, fix string) Finding {
	f := Finding{Code: code, Path: p.path, Symbol: symbol, Message: msg, Fix: fix}
	if w, ok := p.where[id]; ok {
		f.Line, f.Column = w.line+1, w.column+1
		if file := p.files[w.file]; filepath.Clean(file) != filepath.Clean(p.abs) {
			f.Path = file
		}
	}
	return f
}

// Def is the def named name of module m (looking through what it imports
// by import M.*), nil if none.
func (p *Parsed) Def(m, name string) *Decl {
	seen := map[string]bool{}
	var find func(string) *Decl
	find = func(mod string) *Decl {
		if seen[mod] {
			return nil
		}
		seen[mod] = true
		mm := p.Module(mod)
		if mm == nil {
			return nil
		}
		for i := range mm.Declarations {
			if d := &mm.Declarations[i]; d.Kind == "def" && d.Name == name {
				return d
			}
		}
		for _, d := range mm.Declarations {
			if (d.Kind == "import" || d.Kind == "instance") && d.ProtoName != "" {
				if x := find(d.ProtoName); x != nil {
					return x
				}
			}
		}
		return nil
	}
	return find(m)
}

// walk calls f on e and every node below it, with the stack of nodes
// above it.
func walk(e *Expr, f func(e *Expr, above []*Expr)) {
	var rec func(*Expr, []*Expr)
	rec = func(e *Expr, above []*Expr) {
		if e == nil {
			return
		}
		f(e, above)
		above = append(above, e)
		for _, a := range e.Args {
			rec(a, above)
		}
		rec(e.Expr, above)
		if e.Opdef != nil {
			rec(e.Opdef.Expr, above)
		}
	}
	rec(e, nil)
}

func literal(e *Expr) bool {
	return e != nil && (e.Kind == "int" || e.Kind == "str" || e.Kind == "bool")
}

func smallInt(e *Expr) (int, bool) {
	if e == nil || e.Kind != "int" {
		return 0, false
	}
	n, err := strconv.Atoi(string(e.Value))
	return n, err == nil && n >= 0 && n <= 3
}

// comparisons are the comparison opcodes as written. Read-only.
var comparisons = map[string]string{"eq": "==", "neq": "!=", "ilt": "<", "ilte": "<=", "igt": ">", "igte": ">="}

// flipped is op with its sides exchanged (k < S.size() is S.size() > k).
// Read-only.
var flipped = map[string]string{"==": "==", "!=": "!=", "<": ">", "<=": ">=", ">": "<", ">=": "<="}

// sizeFix is how to state X.size() op k without the size.
func sizeFix(x, op string, k int) string {
	switch {
	case op == "==" && k == 0, op == "<=" && k == 0, op == "<" && k == 1:
		return fmt.Sprintf("%s == Set()", x)
	case op == "!=" && k == 0, op == ">" && k == 0, op == ">=" && k == 1:
		return fmt.Sprintf("%s != Set()", x)
	case op == "<=" && k == 1, op == "<" && k == 2:
		return fmt.Sprintf("%s.forall(a => %s.forall(b => a == b))", x, x)
	case op == "==" && k == 1:
		return fmt.Sprintf("%s != Set() and %s.forall(a => %s.forall(b => a == b))", x, x, x)
	case op == ">" && k == 1, op == ">=" && k == 2:
		return fmt.Sprintf("%s.exists(a => %s.exists(b => a != b))", x, x)
	}
	return fmt.Sprintf("quantifiers over %s's elements (%d distinct elements exist, or do not), or a ghost count kept by each action", x, k)
}

// Findings are the Veil-blocking idioms of the modules defined in the
// parsed file, and the assumes Veil ignores: those of the modules main
// imports ("": Main), which quint's Veil backend does not carry into
// main's proof.
func (p *Parsed) Findings(main string) []Finding {
	if main == "" {
		main = p.Main()
	}
	var out []Finding
	for _, m := range p.Modules {
		if !p.inFile(m.ID) {
			continue
		}
		for _, d := range m.Declarations {
			out = append(out, p.declFindings(d)...)
		}
	}
	out = append(out, p.assumeFindings(main)...)
	slices.SortStableFunc(out, func(a, b Finding) int {
		if a.Path != b.Path {
			return strings.Compare(a.Path, b.Path)
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return a.Column - b.Column
	})
	return out
}

func (p *Parsed) declFindings(d Decl) []Finding {
	var out []Finding
	walk(d.Expr, func(e *Expr, above []*Expr) {
		if e.Kind != "app" {
			return
		}
		switch e.Opcode {
		case "eq", "neq", "ilt", "ilte", "igt", "igte":
			if len(e.Args) != 2 {
				return
			}
			op := comparisons[e.Opcode]
			s, k := e.Args[0], e.Args[1]
			if !(s.Kind == "app" && s.Opcode == "size") {
				s, k, op = e.Args[1], e.Args[0], flipped[op]
			}
			n, small := smallInt(k)
			if s.Kind != "app" || s.Opcode != "size" || len(s.Args) != 1 || !small {
				return
			}
			x := p.Text(s.Args[0].ID)
			if x == "" {
				x = "the set"
			}
			out = append(out, p.finding(CodeSize, e.ID, d.Name,
				fmt.Sprintf("%s.size() %s %d in %s: Veil refuses the size of a set whose elements it does not know (\"the size of a set whose elements are not known is not supported\")", x, op, n, d.Name),
				"state it without the size: "+sizeFix(x, op, n)))
		case "mapBy":
			if len(e.Args) != 2 {
				return
			}
			if ex := e.Args[0]; ex.Kind == "app" && ex.Opcode == "exclude" && len(ex.Args) == 2 && ex.Args[0].Kind == "app" && ex.Args[0].Opcode == "keys" {
				m := p.Text(ex.Args[0].Args[0].ID)
				out = append(out, p.finding(CodeKeysRemove, e.ID, d.Name,
					fmt.Sprintf("%s.keys().exclude(...).mapBy(...) in %s removes a key from a map: Veil's maps are total functions, and it cannot prove a property of a map whose keys shrink (a limitation of Veil, not of the model)", orThe(m, "the map"), d.Name),
					"keep a ghost key set beside the map (a var the actions add to and exclude from, the map read only at its keys), or make the map total over a constant domain with a value meaning absent"))
			}
			walk(e.Args[1], func(x *Expr, _ []*Expr) {
				if x.Kind == "app" && x.Opcode == "matchVariant" {
					out = append(out, p.finding(CodeMatchMapBy, x.ID, d.Name,
						fmt.Sprintf("a match inside mapBy's function in %s: Veil cannot translate a match there", d.Name),
						"compare with the variant's constructors instead (if (v == Done) ... else ...), or match outside the mapBy"))
				}
			})
		case "Map":
			if len(e.Args) < 2 {
				return
			}
			for _, t := range e.Args {
				if t.Kind == "app" && t.Opcode == "Tup" && len(t.Args) == 2 && !literal(t.Args[0]) {
					out = append(out, p.finding(CodeMapLiteral, e.ID, d.Name,
						fmt.Sprintf("a map literal of %d entries whose keys are not literals (%s) in %s: Veil cannot tell its keys apart, which may be equal", len(e.Args), orThe(p.Text(t.Args[0].ID), "a key"), d.Name),
						"build it entry by entry: Map(k1 -> v1).put(k2, v2), or mapBy over a constant domain"))
					break
				}
			}
		case "fold":
			if len(e.Args) == 0 || e.Args[0].Kind != "name" {
				return
			}
			en, ok := p.table[e.Args[0].ID]
			if !ok || en.Kind != "const" && en.Kind != "param" {
				return
			}
			what := "the constant"
			if en.Kind == "param" {
				what = "the parameter"
			}
			out = append(out, p.finding(CodeFold, e.ID, d.Name,
				fmt.Sprintf("a fold over %s %s in %s: Veil cannot prove a property of it for every value of %s, so no proof for all constants covers it (the instance's graph can: simulate -graph)", what, e.Args[0].Name, d.Name, e.Args[0].Name),
				"for a proof for every constant, keep the fold's value as a ghost variable each action updates; else prove it on the instance alone (qtest.Proves)"))
		}
	})
	return out
}

func orThe(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// assumeFindings are the assumes of the modules main imports, which Veil
// leaves out of main's proof.
func (p *Parsed) assumeFindings(main string) []Finding {
	mm := p.Module(main)
	if mm == nil {
		return nil
	}
	var out []Finding
	var own []*Expr // main's assumes: one restated there is in the proof
	for _, d := range mm.Declarations {
		if d.Kind == "assume" {
			own = append(own, d.Assumption)
		}
	}
	restated := func(a *Expr) bool { return slices.ContainsFunc(own, func(o *Expr) bool { return same(o, a) }) }
	seen := map[string]bool{main: true}
	var visit func(m *Module)
	visit = func(m *Module) {
		for _, d := range m.Declarations {
			if d.Kind != "import" && d.Kind != "instance" || d.ProtoName == "" || seen[d.ProtoName] {
				continue
			}
			seen[d.ProtoName] = true
			im := p.Module(d.ProtoName)
			if im == nil {
				continue
			}
			for _, a := range im.Declarations {
				if a.Kind != "assume" || restated(a.Assumption) {
					continue
				}
				text := p.Text(a.Assumption.ID)
				out = append(out, p.finding(CodeAssume, a.ID, a.Name,
					fmt.Sprintf("assume %s of %s, which %s imports: quint's Veil backend ignores an assume outside the main module, so a proof of %s cannot rely on it", a.Name, im.Name, main, main),
					fmt.Sprintf("restate it in %s under a name of its own (assume %s_ = %s), or prove %s itself", main, a.Name, orThe(text, "..."), im.Name)))
			}
			visit(im)
		}
	}
	visit(mm)
	return out
}

// Vacuous are the holds invariants of module main conditioned on a
// boolean state variable that neither init nor any action step reaches
// ever sets true: each holds vacuously (`settled implies ...` where step
// never settles), and a proof of it, Veil's or the graph's, says nothing.
func (p *Parsed) Vacuous(main string, holds []string) []Finding {
	if p.Module(main) == nil {
		return nil
	}
	var out []Finding
	for _, inv := range holds {
		d := p.Def(main, inv)
		if d == nil || d.Expr == nil {
			continue
		}
		for _, flag := range p.flags(d.Expr) {
			if p.everTrue(main, flag) {
				continue
			}
			out = append(out, p.finding(CodeVacuous, d.ID, inv,
				fmt.Sprintf("%s is conditioned on %s, which init and step never set true: as a holds invariant it holds vacuously, and proving it (Veil's induction, the graph) says nothing", inv, flag),
				fmt.Sprintf("list %s in eventually, judged after a recorded settle (gotogether-patterns: eventual properties on a settle), or let step set %s", inv, flag)))
		}
	}
	return out
}

// flags are the state variables that an implication's antecedent is or
// conjoins: settled in `settled implies P` or `settled and done implies P`.
func (p *Parsed) flags(e *Expr) []string {
	if e.Kind != "app" || e.Opcode != "implies" || len(e.Args) != 2 {
		return nil
	}
	var out []string
	var conj func(*Expr)
	conj = func(a *Expr) {
		switch {
		case a.Kind == "name":
			if en, ok := p.table[a.ID]; ok && en.Kind == "var" {
				out = append(out, a.Name)
			}
		case a.Kind == "app" && a.Opcode == "and":
			for _, x := range a.Args {
				conj(x)
			}
		}
	}
	conj(e.Args[0])
	return out
}

// everTrue reports whether init or an action step reaches assigns v
// anything but false or v itself.
func (p *Parsed) everTrue(main, v string) bool {
	seen := map[string]bool{}
	var visit func(name string) bool
	visit = func(name string) bool {
		if seen[name] {
			return false
		}
		seen[name] = true
		d := p.Def(main, name)
		if d == nil {
			return false
		}
		found := false
		walk(d.Expr, func(e *Expr, _ []*Expr) {
			if found || e.Kind != "app" {
				return
			}
			if e.Opcode == "assign" && len(e.Args) == 2 && e.Args[0].Name == v {
				rhs := e.Args[1]
				if !(rhs.Kind == "bool" && string(rhs.Value) == "false" || rhs.Kind == "name" && rhs.Name == v) {
					found = true
				}
				return
			}
			if _, builtin := builtins[e.Opcode]; !builtin && p.Def(main, e.Opcode) != nil && visit(e.Opcode) {
				found = true
			}
		})
		walk(d.Expr, func(e *Expr, _ []*Expr) {
			// an action named without arguments (any { bump, finish }) is a name
			if en, ok := p.table[e.ID]; !found && e.Kind == "name" && ok && en.Kind == "def" && visit(e.Name) {
				found = true
			}
		})
		return found
	}
	return visit("init") || visit("step")
}

// builtins are opcodes that are never a definition's name (assign and the
// action combinators the walk passes through). Read-only.
var builtins = map[string]struct{}{"assign": {}, "actionAll": {}, "actionAny": {}, "and": {}, "or": {}, "ite": {}}

// Antecedent is the source text of the antecedent of the invariant inv of
// module main when inv is an implication (A implies B), "" otherwise, and
// the lemmas among lemmas that negate it (not(A), or a conjunction with
// not(A) among its parts): a proof that needs one rests on A never
// holding.
func (p *Parsed) Antecedent(main, inv string, lemmas []string) (string, []string) {
	d := p.Def(main, inv)
	if d == nil || d.Expr == nil || d.Expr.Kind != "app" || d.Expr.Opcode != "implies" || len(d.Expr.Args) != 2 {
		return "", nil
	}
	a := d.Expr.Args[0]
	var negating []string
	for _, l := range lemmas {
		ld := p.Def(main, l)
		if ld == nil || ld.Expr == nil {
			continue
		}
		if p.negates(ld.Expr, a) {
			negating = append(negating, l)
		}
	}
	return p.Text(a.ID), negating
}

func (p *Parsed) negates(l, a *Expr) bool {
	if l.Kind != "app" {
		return false
	}
	switch l.Opcode {
	case "not":
		return len(l.Args) == 1 && same(l.Args[0], a)
	case "and":
		return slices.ContainsFunc(l.Args, func(x *Expr) bool { return p.negates(x, a) })
	case "eq":
		return len(l.Args) == 2 && (same(l.Args[0], a) && isFalse(l.Args[1]) || same(l.Args[1], a) && isFalse(l.Args[0]))
	}
	return false
}

func isFalse(e *Expr) bool { return e.Kind == "bool" && string(e.Value) == "false" }

// same compares two expressions as written, ignoring their ids.
func same(a, b *Expr) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind || a.Name != b.Name || a.Opcode != b.Opcode || string(a.Value) != string(b.Value) || len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if !same(a.Args[i], b.Args[i]) {
			return false
		}
	}
	return same(a.Expr, b.Expr)
}

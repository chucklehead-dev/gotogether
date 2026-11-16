// Package invtable generates a design document's table of a model's
// invariants: for each property, where the Go code enforces it and which
// tests check it, from annotations in the code (ROADMAP CW5's static
// half), so that nobody re-traces file:line references by hand.
//
//	//quint:enforces limitsHonoured
//	func (l *Limiter) Admit(r Request) error { /* ... */ }
//
//	func TestAdmit(t *testing.T) {
//		//quint:checks limitsHonoured, noLostIncrements
//		/* ... */
//	}
//
// A directive names one or more properties, separated by spaces or
// commas: a property's name as the model's main module sees it, or
// qualified by a module of the spec that sees it (`exposure.limitsHonoured`,
// or `exposure::limitsHonoured`). On a declaration's doc comment it
// annotates the declaration; inside a function it annotates the statement
// on the next line (or the one it trails). A checks directive is in a
// test: on a Test or Fuzz function of a _test.go file, or inside one, and
// the test it names is that function.
//
// Go source is read with go/parser alone: the table needs positions and
// comments, not types, so it stays in the core with no x/tools (as
// `gotogether types` does).
package invtable

import (
	"cmp"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/nearest"
)

// Begin and End bound the place in a document where `gotogether
// invariants -write` writes the table.
const (
	Begin = "<!-- gotogether:invariants -->"
	End   = "<!-- /gotogether:invariants -->"
)

// A Property is one of the model's properties: a boolean val (or a
// temporal formula) its main module sees unqualified, its own or one it
// imports or instantiates flattened (`import base(N = 3).*`).
type Property struct {
	// Name is the property's name as the main module sees it
	Name string
	// Module is the module that declares it
	Module string
	// Doc is the first line of its doc comment (quint's `///`, else the
	// `//` comment above it in the spec), "" when it has none
	Doc string
	id  int64
}

func (p *Property) key() string { return p.Module + "::" + p.Name }

// A Model is the properties of a model's main module, and what each of
// the spec's modules sees, to resolve a qualified name.
type Model struct {
	// Main is the main module
	Main string
	// Properties are in the model's order (quint numbers declarations as
	// it parses them)
	Properties []*Property
	// visible is, per module, each name it sees unqualified and the
	// declaration's key (module::name)
	visible map[string]map[string]string
	byKey   map[string]*Property
	modules []string
	// left are the properties Restrict left out, by key
	left map[string]bool
}

type tcDecl struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	ID            int64  `json:"id"`
	Qualifier     string `json:"qualifier"`
	Doc           string `json:"doc"`
	ProtoName     string `json:"protoName"`
	DefName       string `json:"defName"`
	QualifiedName string `json:"qualifiedName"`
}

// Read is the properties of module main in quint typecheck's output tc.
// comments are `//` comments above definitions (Comments), the doc of a
// property quint's IR has none for.
func Read(tc []byte, main string, comments map[string]string) (*Model, error) {
	var out struct {
		Modules []struct {
			Name         string   `json:"name"`
			Declarations []tcDecl `json:"declarations"`
		} `json:"modules"`
		Types map[string]struct {
			Type struct {
				Kind string `json:"kind"`
			} `json:"type"`
		} `json:"types"`
	}
	if err := json.Unmarshal(tc, &out); err != nil {
		return nil, fmt.Errorf("reading quint typecheck's output: %w", err)
	}
	decls := map[string][]tcDecl{}
	m := &Model{Main: main, visible: map[string]map[string]string{}, byKey: map[string]*Property{}}
	for _, mod := range out.Modules {
		decls[mod.Name] = mod.Declarations
		m.modules = append(m.modules, mod.Name)
	}
	if _, ok := decls[main]; !ok {
		return nil, fmt.Errorf("the spec has no module %s: name its main module with -main (its modules: %s)", main, strings.Join(m.modules, ", "))
	}
	// what a module gives a module that imports it flattened: its own
	// definitions and what it exports
	exports := map[string]map[string]string{}
	var exportsOf func(mod string, seen map[string]bool) map[string]string
	exportsOf = func(mod string, seen map[string]bool) map[string]string {
		if e, ok := exports[mod]; ok {
			return e
		}
		e := map[string]string{}
		if seen[mod] {
			return e
		}
		seen[mod] = true
		for _, d := range decls[mod] {
			switch {
			case d.Kind == "def":
				e[d.Name] = mod + "::" + d.Name
			case d.Kind == "export":
				addFrom(e, d, exportsOf(d.ProtoName, seen))
			}
		}
		exports[mod] = e
		return e
	}
	for _, mod := range m.modules {
		v := map[string]string{}
		for _, d := range decls[mod] {
			switch d.Kind {
			case "def":
				v[d.Name] = mod + "::" + d.Name
			case "import", "instance", "export":
				if _, ok := decls[d.ProtoName]; ok {
					addFrom(v, d, exportsOf(d.ProtoName, map[string]bool{}))
				}
			}
		}
		m.visible[mod] = v
	}
	// the properties: what main sees that is a boolean val or temporal
	byKey := map[string]tcDecl{}
	for mod, ds := range decls {
		for _, d := range ds {
			if d.Kind == "def" {
				d.ProtoName = mod // the declaring module, for below
				byKey[mod+"::"+d.Name] = d
			}
		}
	}
	for name, key := range m.visible[main] {
		d, ok := byKey[key]
		if !ok || (d.Qualifier != "val" && d.Qualifier != "temporal") {
			continue
		}
		if t, ok := out.Types[fmt.Sprint(d.ID)]; !ok || t.Type.Kind != "bool" {
			continue
		}
		doc, _, _ := strings.Cut(strings.TrimSpace(d.Doc), "\n")
		if doc == "" {
			doc = comments[key]
		}
		p := &Property{Name: name, Module: d.ProtoName, Doc: strings.TrimSpace(doc), id: d.ID}
		m.Properties = append(m.Properties, p)
		m.byKey[key] = p
	}
	slices.SortFunc(m.Properties, func(a, b *Property) int { return cmp.Or(cmp.Compare(a.id, b.id), cmp.Compare(a.Name, b.Name)) })
	return m, nil
}

// addFrom adds to v what an import, instance or export declaration d
// brings unqualified from the module whose exports are e: all of them
// when it is flattened (`.*`, or an instance with no `as`), the one it
// names (`import M.x`), none when it is qualified (`import M as Q`).
func addFrom(v map[string]string, d tcDecl, e map[string]string) {
	switch {
	case d.DefName == "*", d.Kind == "instance" && d.QualifiedName == "":
		for n, k := range e {
			if _, own := v[n]; !own {
				v[n] = k
			}
		}
	case d.DefName != "" && d.QualifiedName == "":
		if k, ok := e[d.DefName]; ok {
			v[d.DefName] = k
		}
	}
}

// Restrict keeps the properties named (a binding's invariants and
// eventual vals), in the model's order. A name that is no property is an
// error naming the properties.
func (m *Model) Restrict(names []string) error {
	var bad []string
	keep := map[*Property]bool{}
	for _, n := range names {
		p := m.byKey[m.visible[m.Main][n]]
		if p == nil {
			bad = append(bad, n)
			continue
		}
		keep[p] = true
	}
	if len(bad) > 0 {
		return fmt.Errorf("the binding checks %s, which %s no boolean val of module %s: fix the binding (its properties: %s)", strings.Join(bad, ", "), map[bool]string{true: "is", false: "are"}[len(bad) == 1], m.Main, strings.Join(m.names(), ", "))
	}
	m.left = map[string]bool{}
	var kept []*Property
	for _, p := range m.Properties {
		if keep[p] {
			kept = append(kept, p)
		} else {
			m.left[p.key()] = true
			delete(m.byKey, p.key())
		}
	}
	m.Properties = kept
	return nil
}

func (m *Model) names() []string {
	var out []string
	for _, p := range m.Properties {
		out = append(out, p.Name)
	}
	return out
}

// resolve is the property name means, or why it is none and what to do.
func (m *Model) resolve(name string) (*Property, string) {
	mod, short, qualified := strings.Cut(strings.Replace(name, "::", ".", 1), ".")
	if !qualified {
		mod, short = m.Main, name
	}
	if !ident.MatchString(mod) || !ident.MatchString(short) {
		return nil, "a property is a val's name, or module.name"
	}
	vis, ok := m.visible[mod]
	if !ok {
		return nil, fmt.Sprintf("%s is no module of the spec (its modules: %s)", mod, strings.Join(m.modules, ", "))
	}
	key, ok := vis[short]
	if !ok {
		if qualified {
			return nil, fmt.Sprintf("module %s has no definition %s", mod, short)
		}
		return nil, fmt.Sprintf("model %s has no property %s", m.Main, short)
	}
	if p := m.byKey[key]; p != nil {
		return p, ""
	}
	switch {
	case m.left[key]:
		return nil, fmt.Sprintf("%s is a val of model %s that the binding does not check: add it to the binding's invariants (or eventually), or name one it checks", name, m.Main)
	case qualified && mod != m.Main:
		if _, seen := m.visible[m.Main][short]; !seen {
			return nil, fmt.Sprintf("model %s does not see %s's %s (it imports it qualified, or not at all)", m.Main, mod, short)
		}
	}
	return nil, fmt.Sprintf("%s is no boolean val of model %s", name, m.Main)
}

var ident = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// definitionLine matches a top-level val's or temporal's line in a .qnt.
var definitionLine = regexp.MustCompile(`^\s*(?:val|temporal)\s+([A-Za-z_][A-Za-z0-9_]*)`)
var moduleLine = regexp.MustCompile(`^\s*module\s+([A-Za-z_][A-Za-z0-9_]*)`)

// Comments are the first lines of the `//` comments directly above each
// val and temporal of a .qnt's source, by module::name: the meaning of a
// property whose doc comment is not quint's `///`.
func Comments(src []byte) map[string]string {
	out := map[string]string{}
	mod := ""
	var block []string
	for line := range strings.SplitSeq(string(src), "\n") {
		t := strings.TrimSpace(line)
		if mm := moduleLine.FindStringSubmatch(line); mm != nil {
			mod = mm[1]
		}
		if c, ok := strings.CutPrefix(t, "//"); ok {
			block = append(block, strings.TrimSpace(strings.TrimPrefix(c, "/")))
			continue
		}
		if dm := definitionLine.FindStringSubmatch(line); dm != nil && len(block) > 0 && block[0] != "" {
			if _, ok := out[mod+"::"+dm[1]]; !ok {
				out[mod+"::"+dm[1]] = block[0]
			}
		}
		block = nil
	}
	return out
}

// Kind is a directive's: enforces or checks.
type Kind string

// The two directives.
const (
	Enforces Kind = "enforces"
	Checks   Kind = "checks"
)

// A Site is a place a directive annotates.
type Site struct {
	// Path is the file, as scanned
	Path string
	// Line is the declaration's or the statement's
	Line int
	// Func is the declaration (Type.Method for a method), or for a
	// statement the function it is in; for a checks directive, the test
	Func string
}

// An Annotation is one //quint:enforces or //quint:checks directive.
type Annotation struct {
	Kind Kind
	// Line is the directive's own line (Site.Line is what it annotates)
	Line  int
	Site  Site
	Names []string
}

// Scan reads the directives of the Go files that patterns name: a
// directory, or dir/... for it and the directories below it (vendor,
// testdata, hidden and _ directories left out, and those of another
// module, as the go command's ./... does). problems are directives that
// annotate nothing a table can name, each with its fix; err is a file
// that does not parse, or a pattern that names no directory.
func Scan(patterns []string) (anns []Annotation, problems []string, err error) {
	fset := token.NewFileSet()
	var files []string
	for _, pat := range patterns {
		root, recursive := pat, false
		if r, ok := strings.CutSuffix(filepath.ToSlash(pat), "/..."); ok || pat == "..." {
			root, recursive = filepath.FromSlash(r), true
			if !ok || root == "" {
				root = "."
			}
		}
		st, err := os.Stat(root)
		if err != nil || !st.IsDir() {
			return nil, nil, fmt.Errorf("%s names no directory: give directories, or dir/... for a directory and those below it (import paths are not resolved)", pat)
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path == root {
					return nil
				}
				n := d.Name()
				if !recursive || n == "vendor" || n == "testdata" || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".go") && !slices.Contains(files, path) {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	slices.Sort(files)
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, nil, fmt.Errorf("%w (fix the file, or leave its directory out of the patterns)", err)
		}
		a, p := annotationsOf(fset, f, strings.HasSuffix(path, "_test.go"))
		anns, problems = append(anns, a...), append(problems, p...)
	}
	return anns, problems, nil
}

// directive is c's kind and argument text when c is one of the two
// directives.
func directive(c *ast.Comment) (Kind, string, bool) {
	for _, k := range []Kind{Enforces, Checks} {
		if rest, ok := strings.CutPrefix(c.Text, "//quint:"+string(k)); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			return k, rest, true
		}
	}
	return "", "", false
}

// annotationsOf are one parsed file's directives, and those misplaced.
func annotationsOf(fset *token.FileSet, f *ast.File, test bool) (out []Annotation, problems []string) {
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			kind, rest, ok := directive(c)
			if !ok {
				continue
			}
			pos := fset.Position(c.Pos())
			at := fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
			names := strings.FieldsFunc(rest, func(r rune) bool { return r == ' ' || r == '\t' || r == ',' })
			if len(names) == 0 {
				problems = append(problems, fmt.Sprintf("%s: //quint:%s names no property: name the properties after it (//quint:%s limitsHonoured)", at, kind, kind))
				continue
			}
			site, fn, why := siteOf(fset, f, cg)
			if why != "" {
				problems = append(problems, fmt.Sprintf("%s: //quint:%s %s", at, kind, why))
				continue
			}
			if kind == Checks && (!test || fn == nil || !isTest(fn)) {
				where := "outside any function"
				if fn != nil {
					where = "in " + funcName(fn) + ", which is no test"
				}
				problems = append(problems, fmt.Sprintf("%s: //quint:checks is %s: put it on the Test or Fuzz function of a _test.go file that checks the property, or on a statement inside one", at, where))
				continue
			}
			site.Path = pos.Filename
			out = append(out, Annotation{Kind: kind, Line: pos.Line, Site: site, Names: names})
		}
	}
	return out, problems
}

// siteOf is what comment group cg annotates: the declaration it is the
// doc comment of, or the statement on the line after it (or the one it
// trails) in a function; fn is the function, nil outside one; why, when
// it annotates nothing, says where to put it.
func siteOf(fset *token.FileSet, f *ast.File, cg *ast.CommentGroup) (site Site, fn *ast.FuncDecl, why string) {
	line := func(p token.Pos) int { return fset.Position(p).Line }
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Doc == cg {
				return Site{Line: line(d.Pos()), Func: funcName(d)}, d, ""
			}
			if d.Body == nil || cg.Pos() < d.Body.Lbrace || cg.End() > d.Body.Rbrace {
				continue
			}
			first, next := line(cg.Pos()), line(cg.End())+1
			var target ast.Stmt
			ast.Inspect(d.Body, func(n ast.Node) bool {
				s, ok := n.(ast.Stmt)
				if !ok || n == d.Body || target != nil {
					return target == nil
				}
				// a statement the comment trails, else the one on the
				// next line: the outermost that starts there
				if l := line(s.Pos()); (l == first && s.Pos() < cg.Pos()) || (l == next && s.Pos() > cg.End()) {
					target = s
				}
				return target == nil
			})
			if target == nil {
				return Site{}, d, fmt.Sprintf("in %s annotates no statement: put it on the line above the statement that does it, or on %s's doc comment", funcName(d), funcName(d))
			}
			return Site{Line: line(target.Pos()), Func: funcName(d)}, d, ""
		case *ast.GenDecl:
			if d.Doc == cg || (len(d.Specs) == 1 && specDoc(d.Specs[0]) == cg) {
				return Site{Line: line(d.Pos()), Func: specName(d.Specs)}, nil, ""
			}
			for _, s := range d.Specs {
				if specDoc(s) == cg {
					return Site{Line: line(s.Pos()), Func: specName([]ast.Spec{s})}, nil, ""
				}
			}
		}
	}
	return Site{}, nil, "annotates nothing: put it on a declaration's doc comment, or on the line above a statement in a function"
}

func specDoc(s ast.Spec) *ast.CommentGroup {
	switch s := s.(type) {
	case *ast.TypeSpec:
		return s.Doc
	case *ast.ValueSpec:
		return s.Doc
	}
	return nil
}

func specName(specs []ast.Spec) string {
	for _, s := range specs {
		switch s := s.(type) {
		case *ast.TypeSpec:
			return s.Name.Name
		case *ast.ValueSpec:
			if len(s.Names) > 0 {
				return s.Names[0].Name
			}
		}
	}
	return ""
}

// funcName is a function's name, Type.Method for a method.
func funcName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return d.Name.Name
	}
	t := d.Recv.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	switch g := t.(type) {
	case *ast.IndexExpr:
		t = g.X
	case *ast.IndexListExpr:
		t = g.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + d.Name.Name
	}
	return d.Name.Name
}

// isTest reports whether d is a test the go command runs: TestXxx or
// FuzzXxx, no receiver, the letter after the prefix no lower case.
func isTest(d *ast.FuncDecl) bool {
	if d.Recv != nil {
		return false
	}
	for _, p := range []string{"Test", "Fuzz"} {
		if rest, ok := strings.CutPrefix(d.Name.Name, p); ok && (rest == "" || !('a' <= rest[0] && rest[0] <= 'z')) {
			return true
		}
	}
	return false
}

// A Row is a property's: where it is enforced and which tests check it.
type Row struct {
	Property *Property
	Enforced []Site
	Checked  []Site
}

// A Table is a row per property, in the model's order.
type Table struct {
	Model *Model
	Rows  []Row
}

// Table puts each annotation in its property's row. errs are the
// annotations naming no property, each with its file:line, the model's
// properties and the fix.
func (m *Model) Table(anns []Annotation) (*Table, []string) {
	rows := map[*Property]*Row{}
	t := &Table{Model: m}
	for _, p := range m.Properties {
		t.Rows = append(t.Rows, Row{Property: p})
	}
	for i := range t.Rows {
		rows[t.Rows[i].Property] = &t.Rows[i]
	}
	var errs []string
	for _, a := range anns {
		for _, n := range a.Names {
			p, why := m.resolve(n)
			if p == nil {
				msg := fmt.Sprintf("%s:%d: //quint:%s names %s: %s: ", a.Site.Path, a.Line, a.Kind, n, why)
				if s := nearest.Name(n, m.names()); s != "" {
					msg += fmt.Sprintf("did you mean %s? ", s)
				}
				if len(m.Properties) == 0 {
					msg += fmt.Sprintf("model %s has no properties (boolean vals): add one to the model, or name another module with -main", m.Main)
				} else {
					msg += fmt.Sprintf("name one of model %s's properties: %s", m.Main, strings.Join(m.names(), ", "))
				}
				errs = append(errs, msg)
				continue
			}
			r := rows[p]
			if a.Kind == Enforces {
				r.Enforced = appendSite(r.Enforced, a.Site)
			} else {
				r.Checked = appendSite(r.Checked, a.Site)
			}
		}
	}
	for i := range t.Rows {
		for _, s := range []*[]Site{&t.Rows[i].Enforced, &t.Rows[i].Checked} {
			slices.SortFunc(*s, func(a, b Site) int {
				return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Func, b.Func))
			})
		}
	}
	return t, errs
}

func appendSite(s []Site, x Site) []Site {
	if slices.Contains(s, x) {
		return s
	}
	return append(s, x)
}

// Warnings are the properties nothing enforces and those no test
// checks, each with the directive to add: of kind enforced, checked or
// both (nil: both).
func (t *Table) Warnings(kinds ...string) []string {
	var out []string
	want := func(k string) bool { return len(kinds) == 0 || slices.Contains(kinds, k) }
	for _, r := range t.Rows {
		if len(r.Enforced) == 0 && want("enforced") {
			out = append(out, fmt.Sprintf("property %s: nothing enforces it: put //quint:enforces %s on the code that does", r.Property.Name, r.Property.Name))
		}
		if len(r.Checked) == 0 && want("checked") {
			out = append(out, fmt.Sprintf("property %s: no test checks it: put //quint:checks %s on the test that does", r.Property.Name, r.Property.Name))
		}
	}
	return out
}

// Markdown is the table, its paths relative to dir (the document's
// directory) with forward slashes: the same text whichever directory it
// is generated from. With dir "" (no document: the table printed) they
// are as scanned. The meaning column is there when a property has a doc
// comment.
func (t *Table) Markdown(dir string) string {
	absDir, _ := filepath.Abs(dir)
	rel := func(p string) string {
		if dir == "" {
			return filepath.ToSlash(filepath.Clean(p))
		}
		if abs, err := filepath.Abs(p); err == nil {
			if r, err := filepath.Rel(absDir, abs); err == nil {
				p = r
			}
		}
		return filepath.ToSlash(p)
	}
	meaning := slices.ContainsFunc(t.Rows, func(r Row) bool { return r.Property.Doc != "" })
	var b strings.Builder
	if meaning {
		b.WriteString("| property | meaning | enforced by | checked by |\n|---|---|---|---|\n")
	} else {
		b.WriteString("| property | enforced by | checked by |\n|---|---|---|\n")
	}
	cell := func(sites []Site) string {
		if len(sites) == 0 {
			return "—"
		}
		var parts []string
		for _, s := range sites {
			p := rel(s.Path)
			parts = append(parts, fmt.Sprintf("`%s` [%s:%d](%s#L%d)", s.Func, p, s.Line, p, s.Line))
		}
		return strings.Join(parts, "<br>")
	}
	for _, r := range t.Rows {
		fmt.Fprintf(&b, "| `%s` |", r.Property.Name)
		if meaning && r.Property.Doc != "" {
			fmt.Fprintf(&b, " %s |", strings.ReplaceAll(r.Property.Doc, "|", `\|`))
		} else if meaning {
			b.WriteString(" |")
		}
		fmt.Fprintf(&b, " %s | %s |\n", cell(r.Enforced), cell(r.Checked))
	}
	return b.String()
}

// Changes are what differs between a table as a document has it (have)
// and as generated now (want): each property row added, removed or
// changed, by its property, and the header. Empty when they are equal,
// or differ only outside rows (the text was edited by hand).
func Changes(have, want string) []string {
	h, hh := rowsOf(have)
	w, wh := rowsOf(want)
	var out []string
	if hh != wh {
		out = append(out, "the header")
	}
	for _, k := range w.order {
		switch old, ok := h.rows[k]; {
		case !ok:
			out = append(out, "row "+k+" added")
		case old != w.rows[k]:
			out = append(out, "row "+k+" changed")
		}
	}
	for _, k := range h.order {
		if _, ok := w.rows[k]; !ok {
			out = append(out, "row "+k+" removed")
		}
	}
	return out
}

type rowSet struct {
	order []string
	rows  map[string]string
}

// rowsOf is a table's rows by their property (the first cell, without
// its backquotes), and its header (every line that is no property row).
func rowsOf(text string) (rowSet, string) {
	s := rowSet{rows: map[string]string{}}
	var header []string
	for line := range strings.SplitSeq(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "| `"); ok {
			if k, _, ok := strings.Cut(rest, "`"); ok {
				if _, dup := s.rows[k]; !dup {
					s.order = append(s.order, k)
				}
				s.rows[k] = line
				continue
			}
		}
		if strings.TrimSpace(line) != "" {
			header = append(header, line)
		}
	}
	return s, strings.Join(header, "\n")
}

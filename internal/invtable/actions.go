package invtable

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/nearest"
)

// ActionsBegin and ActionsEnd bound the place in a document where
// `gotogether invariants -write` writes the table of the model's actions.
const (
	ActionsBegin = "<!-- gotogether:actions -->"
	ActionsEnd   = "<!-- /gotogether:actions -->"
)

// connectPath is the import path of the model-based testing package.
const connectPath = "github.com/chucklehead-dev/gotogether/connect"

// An ActionRow is an action's: the code that records its steps and the
// drivers that run it.
type ActionRow struct {
	Action *Property
	// Recorded are //quint:action directives (the function's line) and
	// literal qobs.Record and Invoke calls (the literal's)
	Recorded []Site
	// Driven are connect.Bind entries (Action, Handle, Skip) and
	// connect.Switch keys, each in the function it is written in
	Driven []Site
}

// actionsOf are the places one parsed file implements actions, as
// annotations of kind Records or Drives naming them, and the directives
// that name none:
//   - a //quint:action directive's action:NAME (binding's AnnotationsOf,
//     which splits its arguments by the rule qweave shares);
//   - a qobs.Record or Recorder.Record (or Invoke) call whose action is
//     a string literal, in a file that imports qobs (binding's RecordsOf);
//   - in a file that imports connect, under any name: a literal action
//     given to Action, Handle or Skip on a connect.Bind chain (or a
//     variable, parameter or function result of type *connect.Binding,
//     or assigned from such a chain), and a literal key of a
//     connect.Switch composite literal.
//
// Only string literals are seen: an action named through a constant or
// a variable is not. Matching is by name, without types, as the
// binding's lint does.
func actionsOf(fset *token.FileSet, f *ast.File) (out []Annotation, problems []string) {
	line := func(p token.Pos) int { return fset.Position(p).Line }
	enclosing := func(p token.Pos) string {
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Pos() <= p && p < fn.End() {
				return funcName(fn)
			}
		}
		return "package level"
	}
	for _, a := range binding.AnnotationsOf(fset, f) {
		if a.Action == "" {
			problems = append(problems, fmt.Sprintf("%s:%d: //quint:action on %s names no action: give it action:NAME, the model's action it records (//quint:action action:deposit ...)", a.Path, a.Line, funcName(a.Decl)))
			continue
		}
		out = append(out, Annotation{Kind: Records, Line: a.Line, Names: []string{a.Action},
			Site:   Site{Path: a.Path, Line: line(a.Decl.Pos()), Func: funcName(a.Decl), Pkg: f.Name.Name},
			Source: "//quint:action action:" + a.Action})
	}
	for _, r := range binding.RecordsOf(fset, f) {
		call := "Record"
		if sel, ok := r.Call.Fun.(*ast.SelectorExpr); ok {
			call = sel.Sel.Name
			if id, ok := sel.X.(*ast.Ident); ok && !r.Method {
				call = id.Name + "." + call
			}
		}
		out = append(out, Annotation{Kind: Records, Line: r.Line, Names: []string{r.Action},
			Site:   Site{Path: r.Path, Line: r.Line, Func: enclosing(r.At), Pkg: f.Name.Name},
			Source: fmt.Sprintf("%s(ctx, %q, ...)", call, r.Action)})
	}
	pkg := importedAs(f, connectPath)
	if pkg == "" {
		return out, problems
	}
	isB := bindings(f, pkg)
	var drives []Annotation
	var at []token.Pos // each drive's literal, to sort them by
	var specs []string // the specs the file's connect.Configs name
	add := func(lit *ast.BasicLit, source string, skip bool) {
		name, err := strconv.Unquote(lit.Value)
		if err != nil {
			return
		}
		pos := fset.Position(lit.Pos())
		at = append(at, lit.Pos())
		drives = append(drives, Annotation{Kind: Drives, Line: pos.Line, Names: []string{name},
			Site:   Site{Path: pos.Filename, Line: pos.Line, Func: enclosing(lit.Pos()), Pkg: f.Name.Name, Skipped: skip},
			Source: fmt.Sprintf(source, name)})
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CompositeLit:
			if !isSelector(n.Type, pkg, "Config") {
				break
			}
			for _, e := range n.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Spec" {
						if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							if v, err := strconv.Unquote(lit.Value); err == nil && !slices.Contains(specs, v) {
								specs = append(specs, v)
							}
						}
					}
				}
			}
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || !slices.Contains([]string{"Action", "Handle", "Skip"}, sel.Sel.Name) || !isB(sel.X) {
				return true
			}
			for i, arg := range n.Args {
				if i > 0 && sel.Sel.Name != "Skip" {
					break
				}
				if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					add(lit, "."+sel.Sel.Name+"(%q, ...)", sel.Sel.Name == "Skip")
				}
			}
		case *ast.CompositeLit:
			if !isSelector(n.Type, pkg, "Switch") {
				return true
			}
			for _, e := range n.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					if lit, ok := kv.Key.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value != `"_"` {
						add(lit, pkg+".Switch{%q: ...}", false)
					}
				}
			}
		}
		return true
	})
	// in the source's order: a chain's outer call is visited first
	order := make([]int, len(drives))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(i, j int) int { return int(at[i]) - int(at[j]) })
	for _, i := range order {
		drives[i].Specs = specs
		out = append(out, drives[i])
	}
	return out, problems
}

// OtherSpecs leaves out of anns the connect drivers of files whose
// connect.Config names a spec, none of them spec (another model's
// driver: actions cannot be qualified, but a driver says its model),
// and counts them by the specs they name. A Config's spec is relative to
// its package's directory, where go test runs; one matches spec when it
// resolves to it, or has its base name (a .md literate model and its
// .qnt alike).
func OtherSpecs(anns []Annotation, spec string) (kept []Annotation, skipped map[string]int) {
	abs, _ := filepath.Abs(spec)
	base := strings.TrimSuffix(filepath.Base(spec), filepath.Ext(spec))
	ours := func(a Annotation, s string) bool {
		if !filepath.IsAbs(s) {
			s = filepath.Join(filepath.Dir(a.Site.Path), s)
		}
		if r, err := filepath.Abs(s); err == nil && r == abs {
			return true
		}
		return strings.TrimSuffix(filepath.Base(s), filepath.Ext(s)) == base
	}
	skipped = map[string]int{}
	for _, a := range anns {
		if a.Kind == Drives && len(a.Specs) > 0 && !slices.ContainsFunc(a.Specs, func(s string) bool { return ours(a, s) }) {
			skipped[strings.Join(a.Specs, ", ")]++
			continue
		}
		kept = append(kept, a)
	}
	return kept, skipped
}

// importedAs is the name file f imports path under, "" when it does not
// (or imports it blank or dotted, where no selector names it).
func importedAs(f *ast.File, path string) string {
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p != path {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				return ""
			}
			return imp.Name.Name
		}
		return path[strings.LastIndex(path, "/")+1:]
	}
	return ""
}

// isSelector reports whether e is pkg.name, or an instantiation of it.
func isSelector(e ast.Expr, pkg, name string) bool {
	switch g := e.(type) {
	case *ast.IndexExpr:
		e = g.X
	case *ast.IndexListExpr:
		e = g.X
	}
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && sel.Sel.Name == name
}

// bindings is a test of whether an expression is a connect.Binding, by
// its syntax in file f (connect imported as pkg): a pkg.Bind(...) call,
// a method call on one (the chain), a call of a function of f that
// returns *pkg.Binding, or a name declared with that type or assigned
// from such an expression anywhere in the file (by name: no scopes).
func bindings(f *ast.File, pkg string) func(ast.Expr) bool {
	typed := func(t ast.Expr) bool {
		if s, ok := t.(*ast.StarExpr); ok {
			t = s.X
		}
		return isSelector(t, pkg, "Binding")
	}
	funcs, vars := map[string]bool{}, map[string]bool{}
	var is func(e ast.Expr) bool
	is = func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.ParenExpr:
			return is(e.X)
		case *ast.Ident:
			return vars[e.Name]
		case *ast.SelectorExpr: // a field: d.b
			return vars[e.Sel.Name]
		case *ast.CallExpr:
			fun := e.Fun
			switch g := fun.(type) {
			case *ast.IndexExpr:
				fun = g.X
			case *ast.IndexListExpr:
				fun = g.X
			}
			switch fn := fun.(type) {
			case *ast.Ident:
				return funcs[fn.Name]
			case *ast.SelectorExpr:
				if isSelector(fn, pkg, "Bind") {
					return true
				}
				return is(fn.X)
			}
		}
		return false
	}
	fields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, fd := range fl.List {
			if typed(fd.Type) {
				for _, n := range fd.Names {
					vars[n.Name] = true
				}
			}
		}
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Type.Results != nil {
			for _, r := range fn.Type.Results.List {
				if typed(r.Type) {
					funcs[fn.Name.Name] = true
				}
			}
		}
	}
	// declarations and assignments, twice: one assigned from another
	// declared later in the file
	for range 2 {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncType:
				fields(n.Params)
				fields(n.Results)
			case *ast.ValueSpec:
				for i, id := range n.Names {
					if (n.Type != nil && typed(n.Type)) || (i < len(n.Values) && is(n.Values[i])) {
						vars[id.Name] = true
					}
				}
			case *ast.AssignStmt:
				for i, l := range n.Lhs {
					if id, ok := l.(*ast.Ident); ok && i < len(n.Rhs) && len(n.Lhs) == len(n.Rhs) && is(n.Rhs[i]) {
						vars[id.Name] = true
					}
				}
			case *ast.StructType:
				fields(n.Fields)
			}
			return true
		})
	}
	return is
}

func (m *Model) actionNames() []string {
	var out []string
	for _, a := range m.Actions {
		out = append(out, a.Name)
	}
	return out
}

// placeAction puts a Records or Drives annotation in its action's row;
// errs name each name that is no action of the model. An action with no
// row (init and step, a frame action, one -exclude left out) is neither.
// In a file whose property annotations all name another model's module,
// otherMod, an unknown action is that model's: counted in others.
func (m *Model) placeAction(a Annotation, rows map[string]*ActionRow, otherMod string, others map[string]int) (errs []string) {
	for _, n := range a.Names {
		r, ok := rows[n]
		if !ok && m.actionsAll[n] {
			continue // an action, but no row: init, step, a frame action, one -exclude left out
		}
		if !ok && otherMod != "" {
			others[otherMod]++
			continue
		}
		if !ok {
			msg := fmt.Sprintf("%s:%d: %s names %s, which is no action of model %s: ", a.Site.Path, a.Line, a.Source, n, m.Main)
			if s := nearest.Name(n, m.actionNames()); s != "" {
				msg += fmt.Sprintf("did you mean %s? ", s)
			}
			if len(m.Actions) == 0 {
				msg += fmt.Sprintf("model %s has no actions but init and step: name another module with -main, or leave this code out of the patterns", m.Main)
			} else {
				msg += fmt.Sprintf("name one of its actions (%s), or leave code of another model out of the patterns", strings.Join(m.actionNames(), ", "))
			}
			errs = append(errs, msg)
			continue
		}
		if a.Kind == Records {
			r.Recorded = appendSite(r.Recorded, a.Site)
		} else {
			r.Driven = appendSite(r.Driven, a.Site)
		}
	}
	return errs
}

// actionWarnings are the actions nothing records and those nothing
// drives, of the kinds want admits (recorded, driven).
func (t *Table) actionWarnings(want func(string) bool) []string {
	var out []string
	for _, r := range t.Actions {
		n := r.Action.Name
		if len(r.Recorded) == 0 && want("recorded") {
			out = append(out, fmt.Sprintf("action %s: nothing records it: put //quint:action action:%s on the function that does it, or record it with qobs.Record(ctx, %q, ...)", n, n, n))
		}
		if len(r.Driven) == 0 && want("driven") {
			out = append(out, fmt.Sprintf("action %s: nothing drives it: bind it in a connect driver (connect.Bind(...).Action(%q, ...), Handle or Skip, or a connect.Switch key)", n, n))
		}
	}
	return out
}

// MarkdownActions is the table of actions, paths relative to dir as in
// Markdown.
func (t *Table) MarkdownActions(dir string) string {
	var b strings.Builder
	b.WriteString("| action | recorded by | driven by |\n|---|---|---|\n")
	for _, r := range t.Actions {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", r.Action.Name, t.siteCell(r.Recorded, dir), t.siteCell(r.Driven, dir))
	}
	return b.String()
}

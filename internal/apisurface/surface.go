// Package apisurface keeps the exported API of the published modules in
// api/<module>.txt: every exported function, type, field, method,
// interface method, constant and variable of every package, as written
// in its declaration, so a change to what users can call shows in review
// and, until tags give apidiff a baseline, is never made by accident.
//
// It reads the source with go/ast rather than `go doc -short` (which
// lists no method or field) or go/types: type-checking a sub-module from
// the core's test would need that module's dependencies (Hegel, x/tools),
// which are not in the core's module graph, and a declaration as written
// is what a reviewer reads. What go/types would add, promoted members,
// shows under the embedded type's own record.
package apisurface

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Module is a published module: its directory under the repository root
// ("." for the core), its import path, and the file its API is recorded in.
type Module struct {
	Dir, Path, File string
	// Tags are build tags the module publishes API behind (qgosim's
	// "sim"): each package whose API differs under them is recorded again,
	// as "== path [tags]".
	Tags []string
}

// Modules are the published modules (AGENTS.md: the core, qhegel, qweave,
// qanalysis, qgosim, grist, qnav, and the doubles' modules under qenv/).
// qgosim stays last (its test reads the sim side from it).
// Read-only.
var Modules = []Module{
	{Dir: ".", Path: "github.com/chucklehead-dev/gotogether", File: "api/core.txt"},
	{Dir: "qhegel", Path: "github.com/chucklehead-dev/gotogether/qhegel", File: "api/qhegel.txt"},
	{Dir: "qweave", Path: "github.com/chucklehead-dev/gotogether/qweave", File: "api/qweave.txt"},
	{Dir: "qanalysis", Path: "github.com/chucklehead-dev/gotogether/qanalysis", File: "api/qanalysis.txt"},
	{Dir: "grist", Path: "github.com/chucklehead-dev/gotogether/grist", File: "api/grist.txt"},
	{Dir: "qnav", Path: "github.com/chucklehead-dev/gotogether/qnav", File: "api/qnav.txt"},
	{Dir: "qenv/simgrpc", Path: "github.com/chucklehead-dev/gotogether/qenv/simgrpc", File: "api/simgrpc.txt"},
	{Dir: "qenv/kubesim", Path: "github.com/chucklehead-dev/gotogether/qenv/kubesim", File: "api/kubesim.txt"},
	{Dir: "qgosim", Path: "github.com/chucklehead-dev/gotogether/qgosim", File: "api/qgosim.txt", Tags: []string{"sim"}},
}

// Surface is module m's API, read from the repository at root: its
// packages in import-path order, each with its exported declarations.
// Internal packages, commands, testdata and nested modules are not API.
func Surface(root string, m Module) ([]byte, error) {
	base := filepath.Join(root, m.Dir)
	var dirs []string
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		name := d.Name()
		rel, _ := filepath.Rel(base, p)
		if p != base && (name == "testdata" || name == "internal" || name == "cmd" || name == "examples" ||
			strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
			return filepath.SkipDir
		}
		if p != base {
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir // another module
			}
		}
		dirs = append(dirs, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	r := &reader{module: m.Path, base: base, ctx: build.Default}
	var tagged *reader
	if len(m.Tags) > 0 {
		ctx := build.Default
		ctx.BuildTags = append(append([]string(nil), ctx.BuildTags...), m.Tags...)
		tagged = &reader{module: m.Path, base: base, ctx: ctx}
	}
	var b bytes.Buffer
	for _, rel := range dirs {
		path := m.Path
		if rel != "." {
			path += "/" + filepath.ToSlash(rel)
		}
		decls, name, err := r.pkgDecls(filepath.Join(base, rel))
		if err != nil {
			return nil, err
		}
		if name != "" && name != "main" && len(decls) > 0 {
			section(&b, path, decls)
		}
		if tagged == nil {
			continue
		}
		td, tname, err := tagged.pkgDecls(filepath.Join(base, rel))
		if err != nil {
			return nil, err
		}
		if tname != "" && tname != "main" && len(td) > 0 && strings.Join(td, "\n") != strings.Join(decls, "\n") {
			section(&b, path+" ["+strings.Join(m.Tags, ",")+"]", td)
		}
	}
	return b.Bytes(), nil
}

func section(b *bytes.Buffer, head string, decls []string) {
	fmt.Fprintf(b, "== %s\n", head)
	for _, d := range decls {
		b.WriteString(d + "\n")
	}
	b.WriteString("\n")
}

// pkgDecls are the exported declarations of the package in dir, sorted
// (a type's fields and methods after it), and its name ("" when dir holds
// no Go package for the default build). An alias of a type of one of the
// module's internal packages (qenv.Rates of choice.Rates) is that type's
// API too: its fields and methods follow the alias.
func (r *reader) pkgDecls(dir string) ([]string, string, error) {
	p, err := r.parse(dir)
	if err != nil || p == nil {
		return nil, "", err
	}
	var heads []string
	blocks := map[string][]string{}
	for _, b := range p.blocks {
		heads = append(heads, b[0])
		blocks[b[0]] = b
	}
	sort.Strings(heads)
	var res []string
	for _, h := range heads {
		b := blocks[h]
		res = append(res, b[0])
		if name, ok := p.typeOf[h]; ok {
			res = append(res, b[1:]...)
			res = append(res, p.methods[name]...)
			if target, ok := p.aliases[name]; ok {
				res = append(res, r.aliased(dir, p, target)...)
			}
			continue
		}
		res = append(res, b[1:]...)
	}
	return res, p.name, nil
}

// aliased is the API of an alias's target in an internal package of the
// module: the target's fields and methods, or nothing for any other.
func (r *reader) aliased(dir string, p *parsed, target *ast.SelectorExpr) []string {
	x, ok := target.X.(*ast.Ident)
	if !ok {
		return nil
	}
	path, ok := p.imports[x.Name]
	if !ok || !strings.HasPrefix(path, r.module+"/") || !strings.Contains(path, "/internal/") {
		return nil
	}
	tp, err := r.parse(filepath.Join(r.base, filepath.FromSlash(strings.TrimPrefix(path, r.module+"/"))))
	if err != nil || tp == nil {
		return nil
	}
	for _, b := range tp.blocks {
		if name, ok := tp.typeOf[b[0]]; ok && name == target.Sel.Name {
			return append(append([]string(nil), b[1:]...), tp.methods[name]...)
		}
	}
	return nil
}

// A reader reads one module's packages.
type reader struct {
	module, base string        // the module's path and directory
	ctx          build.Context // which files are built (Module.Tags)
	cache        map[string]*parsed
}

// parsed is a package's exported declarations: each a block, a head line
// and (a type's) its fields or interface methods; a type's head's name;
// methods by receiver type; aliases by name; the files' imports by name.
type parsed struct {
	name    string
	blocks  [][]string
	typeOf  map[string]string
	methods map[string][]string
	aliases map[string]*ast.SelectorExpr
	imports map[string]string
}

func (r *reader) parse(dir string) (*parsed, error) {
	if p, ok := r.cache[dir]; ok {
		return p, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	p := &parsed{typeOf: map[string]string{}, methods: map[string][]string{}, aliases: map[string]*ast.SelectorExpr{}, imports: map[string]string{}}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		if ok, err := r.ctx.MatchFile(dir, n); err != nil || !ok {
			continue // another platform's, or behind a build tag (gosim's "sim")
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		p.name = f.Name.Name
		for _, im := range f.Imports {
			path := strings.Trim(im.Path.Value, `"`)
			name := strings.SplitN(filepath.Base(path), ".", 2)[0] // yaml of gopkg.in/yaml.v3
			if im.Name != nil {
				name = im.Name.Name
			}
			p.imports[name] = path
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() {
					continue
				}
				if d.Recv == nil {
					p.blocks = append(p.blocks, []string{"func " + d.Name.Name + sig(fset, d.Type)})
					continue
				}
				recv := recvName(d.Recv.List[0].Type)
				if !ast.IsExported(recv) {
					continue
				}
				p.methods[recv] = append(p.methods[recv], fmt.Sprintf("    method (%s) %s%s", expr(fset, d.Recv.List[0].Type), d.Name.Name, sig(fset, d.Type)))
			case *ast.GenDecl:
				var implicit, implicitVal ast.Expr // a const group's type and value, which specs without one repeat
				for k, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							b := typeDecl(fset, s)
							p.blocks = append(p.blocks, b)
							p.typeOf[b[0]] = s.Name.Name
							if sel, ok := s.Type.(*ast.SelectorExpr); ok && s.Assign.IsValid() {
								p.aliases[s.Name.Name] = sel
							}
						}
					case *ast.ValueSpec:
						if d.Tok == token.CONST && (s.Type != nil || len(s.Values) > 0) {
							implicit, implicitVal = s.Type, nil
							if len(s.Values) > 0 {
								implicitVal = s.Values[0]
							}
						}
						for i, n := range s.Names {
							if !n.IsExported() {
								continue
							}
							line := d.Tok.String() + " " + n.Name
							switch {
							case s.Type != nil:
								line += " " + expr(fset, s.Type)
							case d.Tok == token.CONST && len(s.Values) == 0 && implicit != nil:
								line += " " + expr(fset, implicit)
							}
							switch {
							case i < len(s.Values):
								line += " = " + value(fset, s.Values[i]) // a value's change changes the API
							case d.Tok == token.CONST && implicitVal != nil:
								line += fmt.Sprintf(" = %s (spec %d of its group)", expr(fset, implicitVal), k) // iota's value is its place
							}
							p.blocks = append(p.blocks, []string{line})
						}
					}
				}
			}
		}
	}
	for _, ms := range p.methods {
		sort.Strings(ms)
	}
	if p.name == "" {
		p = nil
	}
	if r.cache == nil {
		r.cache = map[string]*parsed{}
	}
	r.cache[dir] = p
	return p, nil
}

// typeDecl is a type's line, then a struct's exported fields or an
// interface's methods, indented.
func typeDecl(fset *token.FileSet, s *ast.TypeSpec) []string {
	head := "type " + s.Name.Name
	if s.TypeParams != nil {
		head += "[" + fields(fset, s.TypeParams, ", ") + "]"
	}
	if s.Assign.IsValid() {
		head += " ="
	}
	switch t := s.Type.(type) {
	case *ast.StructType:
		out := []string{head + " struct"}
		for _, f := range t.Fields.List {
			ty := expr(fset, f.Type)
			tag := ""
			if f.Tag != nil {
				tag = " " + f.Tag.Value // a YAML or JSON name is what a config file spells
			}
			if len(f.Names) == 0 { // embedded, exported or not: it promotes its exported members
				out = append(out, "    embed "+ty+tag)
				continue
			}
			for _, n := range f.Names {
				if n.IsExported() {
					out = append(out, "    field "+n.Name+" "+ty+tag)
				}
			}
		}
		return out
	case *ast.InterfaceType:
		out := []string{head + " interface"}
		for _, m := range t.Methods.List {
			if len(m.Names) == 0 {
				out = append(out, "    embed "+expr(fset, m.Type))
				continue
			}
			for _, n := range m.Names {
				if fn, ok := m.Type.(*ast.FuncType); ok { // an unexported one too: adding it breaks every outside implementation
					out = append(out, "    "+n.Name+sig(fset, fn))
				}
			}
		}
		return out
	}
	return []string{head + " " + expr(fset, s.Type)}
}

func recvName(e ast.Expr) string {
	for {
		switch t := e.(type) {
		case *ast.StarExpr:
			e = t.X
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.SelectorExpr:
			return t.Sel.Name
		case *ast.Ident:
			return t.Name
		default:
			return ""
		}
	}
}

// sig is a function type without its "func" keyword.
func sig(fset *token.FileSet, t *ast.FuncType) string {
	return strings.TrimPrefix(expr(fset, t), "func")
}

func fields(fset *token.FileSet, fl *ast.FieldList, sep string) string {
	var ps []string
	for _, f := range fl.List {
		var ns []string
		for _, n := range f.Names {
			ns = append(ns, n.Name)
		}
		p := expr(fset, f.Type)
		if len(ns) > 0 {
			p = strings.Join(ns, ", ") + " " + p
		}
		ps = append(ps, p)
	}
	return strings.Join(ps, sep)
}

// expr prints an expression on one line.
func expr(fset *token.FileSet, e ast.Node) string {
	var b bytes.Buffer
	_ = printer.Fprint(&b, fset, e)
	return strings.Join(strings.Fields(b.String()), " ")
}

// value prints a constant's or variable's value. A function literal's
// body and a composite literal's computed parts are implementation (an
// analyzer's Run, a table built from others): elided. A composite's
// literal parts are kept, since they are what a user names (an analyzer's
// Name, which flags and nolint comments spell).
func value(fset *token.FileSet, e ast.Expr) string {
	switch v := e.(type) {
	case *ast.FuncLit:
		return expr(fset, v.Type) + " {…}"
	case *ast.CompositeLit:
		if v.Type != nil {
			return expr(fset, v.Type) + composite(fset, v)
		}
	case *ast.UnaryExpr:
		if c, ok := v.X.(*ast.CompositeLit); ok && v.Op == token.AND && c.Type != nil {
			return "&" + expr(fset, c.Type) + composite(fset, c)
		}
	}
	return expr(fset, e)
}

// composite is a composite literal's braces: its elements whose value is
// a basic literal (`Name: "quintaction"`, `"a"`; a Doc aside), then …
// for the rest.
func composite(fset *token.FileSet, c *ast.CompositeLit) string {
	var kept []string
	elided := false
	for _, el := range c.Elts {
		val := el
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Doc" {
				continue // documentation, not what a user names
			}
			val = kv.Value
		}
		if _, ok := val.(*ast.BasicLit); ok {
			kept = append(kept, expr(fset, el))
		} else {
			elided = true
		}
	}
	if elided {
		kept = append(kept, "…")
	}
	return "{" + strings.Join(kept, ", ") + "}"
}

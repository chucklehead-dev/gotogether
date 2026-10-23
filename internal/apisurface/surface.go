// Package apisurface keeps the exported API of the published modules in
// api/<module>.txt: every exported function, type, field, method,
// interface method, constant and variable of every package, as written
// in its declaration, so a change to what users can call shows in review
// and, until tags give apidiff a baseline, is never made by accident.
//
// It reads the source with go/ast rather than `go doc -short` (which
// lists no method or field) or go/types (whose loader, x/tools, the core
// does not depend on): a declaration is recorded as written, which is
// what a reviewer reads.
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
}

// Modules are the published modules (AGENTS.md: the core, qhegel, qweave,
// qanalysis, qgosim).
var Modules = []Module{
	{".", "github.com/chucklehead-dev/gotogether", "api/core.txt"},
	{"qhegel", "github.com/chucklehead-dev/gotogether/qhegel", "api/qhegel.txt"},
	{"qweave", "github.com/chucklehead-dev/gotogether/qweave", "api/qweave.txt"},
	{"qanalysis", "github.com/chucklehead-dev/gotogether/qanalysis", "api/qanalysis.txt"},
	{"qgosim", "github.com/chucklehead-dev/gotogether/qgosim", "api/qgosim.txt"},
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
	r := &reader{module: m.Path, base: base}
	var b bytes.Buffer
	for _, rel := range dirs {
		decls, name, err := r.pkgDecls(filepath.Join(base, rel))
		if err != nil {
			return nil, err
		}
		if name == "" || name == "main" || len(decls) == 0 {
			continue
		}
		path := m.Path
		if rel != "." {
			path += "/" + filepath.ToSlash(rel)
		}
		fmt.Fprintf(&b, "== %s\n", path)
		for _, d := range decls {
			b.WriteString(d + "\n")
		}
		b.WriteString("\n")
	}
	return b.Bytes(), nil
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
	module, base string // the module's path and directory
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
		if ok, err := build.Default.MatchFile(dir, n); err != nil || !ok {
			continue // another platform's, or behind a build tag (gosim's "sim")
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		p.name = f.Name.Name
		for _, im := range f.Imports {
			path := strings.Trim(im.Path.Value, `"`)
			name := filepath.Base(path)
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
				var implicit ast.Expr // a const group's type, which specs without one repeat
				for _, spec := range d.Specs {
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
							implicit = s.Type
						}
						for i, n := range s.Names {
							if !n.IsExported() {
								continue
							}
							line := d.Tok.String() + " " + n.Name
							switch {
							case s.Type != nil:
								line += " " + expr(fset, s.Type)
							case d.Tok == token.CONST && i < len(s.Values):
								line += " = " + expr(fset, s.Values[i])
							case d.Tok == token.CONST && implicit != nil:
								line += " " + expr(fset, implicit)
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
			if len(f.Names) == 0 { // embedded: exported when its type's name is
				if ast.IsExported(recvName(f.Type)) {
					out = append(out, "    embed "+ty)
				}
				continue
			}
			for _, n := range f.Names {
				if n.IsExported() {
					out = append(out, "    field "+n.Name+" "+ty)
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
				if fn, ok := m.Type.(*ast.FuncType); ok && n.IsExported() {
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

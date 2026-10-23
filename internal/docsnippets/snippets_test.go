// Package docsnippets checks the Go in the docs: every fenced go block of
// the README, the skills, the examples' READMEs and docs/ parses, and
// every name it takes from one of the toolkit's packages (qtest.System,
// qenv.Rates) is one that package exports. A renamed or removed API in a
// snippet once lived on until a reader tried it.
package docsnippets

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// root is the repository's root, from this package's directory.
const root = "../.."

// packages are the toolkit's packages a snippet may name, by name: their
// directories under root.
var packages = map[string]string{
	"binding": "binding", "connect": "connect", "convert": "convert", "input": "input", "itf": "itf",
	"otelio": "otelio", "qcover": "qcover", "qenv": "qenv", "qhegel": "qhegel", "qload": "qload",
	"qmeta": "qmeta", "qmut": "qmut", "qobs": "qobs", "qrole": "qrole", "qrun": "qrun", "qsweep": "qsweep",
	"qtest": "qtest", "qtrace": "qtrace", "sktrace": "sktrace", "validate": "validate",
}

// docs are the files whose snippets are checked: HISTORY's and ROADMAP's
// record what was, and may name what is gone.
func docs(t *testing.T) []string {
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (strings.HasPrefix(d.Name(), ".") && p != root || d.Name() == "node_modules" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if strings.HasSuffix(p, ".md") && d.Name() != "HISTORY.md" && d.Name() != "ROADMAP.md" {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var fenced = regexp.MustCompile("(?s)```go\n(.*?)```")

// parse parses a snippet as a file, as declarations, or as statements;
// its error is that of the form its first word says it is.
func parse(src string) (*ast.File, error) {
	forms := []string{src, "package p\n" + src, "package p\nfunc _() {\n" + src + "\n}"}
	var errs []error
	for _, f := range forms {
		file, err := parser.ParseFile(token.NewFileSet(), "snippet.go", f, 0)
		if err == nil {
			return file, nil
		}
		errs = append(errs, err)
	}
	word := strings.Fields(regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(src, "") + " _")[0]
	switch word {
	case "package":
		return nil, errs[0]
	case "func", "var", "type", "const", "import":
		return nil, errs[1]
	}
	return nil, errs[2]
}

// exported are the package-level exported names of the package in dir.
func exported(t *testing.T, dir string) map[string]bool {
	fs := token.NewFileSet()
	names := map[string]bool{}
	paths, _ := filepath.Glob(filepath.Join(root, dir, "*.go"))
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fs, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					names[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						names[s.Name.Name] = true
					case *ast.ValueSpec:
						for _, n := range s.Names {
							names[n.Name] = true
						}
					}
				}
			}
		}
	}
	return names
}

func TestSnippets(t *testing.T) {
	apis := map[string]map[string]bool{}
	n := 0
	for _, doc := range docs(t) {
		b, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, doc)
		for _, m := range fenced.FindAllStringSubmatchIndex(string(b), -1) {
			n++
			src := string(b[m[2]:m[3]])
			line := 1 + strings.Count(string(b[:m[2]]), "\n")
			f, err := parse(src)
			if err != nil {
				t.Errorf("%s:%d: a go block that does not parse (write elided code as /* ... */): %v", rel, line, err)
				continue
			}
			var missing []string
			ast.Inspect(f, func(x ast.Node) bool {
				sel, ok := x.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok || packages[id.Name] == "" {
					return true
				}
				if apis[id.Name] == nil {
					apis[id.Name] = exported(t, packages[id.Name])
				}
				if !apis[id.Name][sel.Sel.Name] {
					missing = append(missing, id.Name+"."+sel.Sel.Name)
				}
				return true
			})
			if len(missing) > 0 {
				sort.Strings(missing)
				t.Errorf("%s:%d: a go block names what its package does not export (renamed or removed?): %s", rel, line, strings.Join(missing, ", "))
			}
		}
	}
	if n < 20 {
		t.Fatalf("only %d go blocks found under %s: is the walk wrong?", n, root)
	}
}

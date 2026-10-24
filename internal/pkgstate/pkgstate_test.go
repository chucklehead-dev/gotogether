// Package pkgstate checks that the core's package-level variables state
// their scope. A package-level default that parallel runs shared made
// their results depend on each other (AGENTS: per-run state is a
// qrun.Key); a variable is either read after its initialization only,
// or deliberately shared by the whole process, and says which.
package pkgstate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// immutable initializers: values no code writes after initialization
var immutable = []string{"errors.New(", "fmt.Errorf(", "regexp.MustCompile(", "reflect.TypeOf(", "reflect.TypeFor[",
	"qrun.NewKey[", "DeclareFaults("} // a run key and a fault declaration are identities

// Every package-level var of the core's non-test files, but those
// initialized by one of immutable, says "read-only" or "process-wide" in
// its comment (its own, its line's, or its group's).
func TestPackageStateStatesItsScope(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "examples" || name == "cmd") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil && path != root {
				return filepath.SkipDir // another module
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			g, ok := decl.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			for _, spec := range g.Specs {
				vs := spec.(*ast.ValueSpec)
				if len(vs.Values) == 1 {
					init := string(src[fset.Position(vs.Values[0].Pos()).Offset:fset.Position(vs.Values[0].End()).Offset])
					if hasPrefix(init, immutable) {
						continue
					}
				}
				text := strings.ToLower(vs.Doc.Text() + vs.Comment.Text() + g.Doc.Text())
				if strings.Contains(text, "read-only") || strings.Contains(text, "process-wide") {
					continue
				}
				for _, id := range vs.Names {
					if id.Name != "_" {
						rel, _ := filepath.Rel(root, path)
						t.Errorf("%s:%d: %s: say whether it is read-only or process-wide in its comment (per-run state is a qrun.Key)", rel, fset.Position(id.Pos()).Line, id.Name)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func hasPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

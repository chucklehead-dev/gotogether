package docsnippets

import (
	"fmt"
	"go/ast"
	"go/doc/comment"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// goFiles are the Go files of the published modules whose doc comments
// are checked: tests, testdata and the gosim translation (.gosim) aside.
func goFiles(t *testing.T) []string {
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (strings.HasPrefix(d.Name(), ".") && p != root || d.Name() == "testdata" || d.Name() == "examples" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The code in doc comments (an indented block, as go doc prints it) is
// held to the docs' rule: it parses, and names only what the toolkit's
// packages export. An example in a package's doc is the first code a
// reader of pkg.go.dev copies.
func TestDocComments(t *testing.T) {
	apis := map[string]map[string]bool{}
	var p comment.Parser
	n := 0
	for _, path := range goFiles(t) {
		fs := token.NewFileSet()
		f, err := parser.ParseFile(fs, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, path)
		var groups []*ast.CommentGroup
		if f.Doc != nil {
			groups = append(groups, f.Doc)
		}
		ast.Inspect(f, func(x ast.Node) bool {
			switch x := x.(type) {
			case *ast.FuncDecl:
				groups = append(groups, x.Doc)
			case *ast.GenDecl:
				groups = append(groups, x.Doc)
			case *ast.TypeSpec:
				groups = append(groups, x.Doc)
			case *ast.ValueSpec:
				groups = append(groups, x.Doc)
			case *ast.Field:
				groups = append(groups, x.Doc)
			}
			return true
		})
		for _, g := range groups {
			if g == nil {
				continue
			}
			for _, b := range p.Parse(g.Text()).Content {
				code, ok := b.(*comment.Code)
				if !ok || !looksLikeGo(code.Text) {
					continue
				}
				n++
				check(t, apis, fmt.Sprintf("%s:%d", rel, fs.Position(g.Pos()).Line), code.Text)
			}
		}
	}
	if n < 20 {
		t.Fatalf("only %d code blocks in doc comments under %s: is the walk wrong?", n, root)
	}
}

// looksLikeGo tells Go from the other code doc comments show (tables,
// YAML, command lines, formulas, connect's table of quint-connect's
// Rust): it does not start with #, and it starts with a Go keyword or
// directive, declares with :=, or calls or composes a toolkit name
// (qenv.Mono(, qtest.System{).
func looksLikeGo(src string) bool {
	if strings.HasPrefix(strings.TrimSpace(src), "#") { // YAML, a shell comment, a Rust attribute
		return false
	}
	if goStart.MatchString(strings.TrimSpace(src)) || strings.Contains(src, ":=") {
		return true
	}
	for _, m := range toolkitUse.FindAllStringSubmatch(src, -1) {
		if packages[m[1]] != "" {
			return true
		}
	}
	return false
}

var (
	goStart    = regexp.MustCompile(`^((package|import|func|type|var|const|for|if|defer|return)\b|go (func|\w+\.\w+)\(|//[a-z]+:)`)
	toolkitUse = regexp.MustCompile(`\b([a-z][a-z0-9]*)\.[A-Z]\w*[({]`)
)

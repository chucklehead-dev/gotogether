package quintcmd_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// literalQuintLaunches catches direct os/exec launches with a literal Quint
// executable. It does not prove where dynamic executable names, shell strings
// or assigned command function values go. Every file is inspected independently
// of build tags, including tests and the repository's nested modules.
func literalQuintLaunches(fset *token.FileSet, file *ast.File) []token.Position {
	aliases := map[string]bool{}
	dot := false
	for _, imp := range file.Imports {
		name, err := strconv.Unquote(imp.Path.Value)
		if err != nil || name != "os/exec" {
			continue
		}
		alias := "exec"
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		if alias == "." {
			dot = true
		} else {
			aliases[alias] = true
		}
	}
	var out []token.Position
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := ""
		switch fun := unparen(call.Fun).(type) {
		case *ast.SelectorExpr:
			if recv, ok := unparen(fun.X).(*ast.Ident); ok && recv.Obj == nil && aliases[recv.Name] {
				name = fun.Sel.Name
			}
		case *ast.Ident:
			if dot && fun.Obj == nil {
				name = fun.Name
			}
		}
		arg := 0
		switch name {
		case "Command":
		case "CommandContext":
			arg = 1
		default:
			return true
		}
		if len(call.Args) <= arg {
			return true
		}
		lit, ok := unparen(call.Args[arg]).(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		binary, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		// Normalize both separators even when inspecting another OS's source.
		switch path.Base(strings.ReplaceAll(binary, `\`, "/")) {
		case "quint", "quint.exe", "quint.cmd":
			out = append(out, fset.Position(call.Pos()))
		}
		return true
	})
	return out
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// Fixtures are excluded because their source intentionally demonstrates bad
// programs. Real examples and published modules participate in this guard too.
func TestRepositoryQuintLaunchesUseAdmission(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	files := 0
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "testdata" || entry.Name() == "vendor" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			return err
		}
		files++
		for _, pos := range literalQuintLaunches(fset, file) {
			t.Errorf("%s: literal Quint launch bypasses cancellation and admission: use internal/quintcmd.Command with the caller's context", pos)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("no repository Go files inspected")
	}
}

func TestLiteralQuintLaunchControls(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         int
	}{
		{"command", `package p; import "os/exec"; func f() { exec.Command("quint", "run") }`, 1},
		{"alias context", `package p; import proc "os/exec"; func f(ctx any) { proc.CommandContext(ctx, "/usr/bin/quint", "test") }`, 1},
		{"dot raw", "package p; import . \"os/exec\"; func f() { Command(`quint`) }", 1},
		{"parentheses", `package p; import "os/exec"; func f() { (exec.Command)(("quint")) }`, 1},
		{"windows", `package p; import "os/exec"; func f() { exec.Command("C:\\tools\\quint.cmd") }`, 1},
		{"other binary", `package p; import "os/exec"; func f() { exec.Command("go", "quint") }`, 0},
		{"lookup", `package p; import "os/exec"; func f() { exec.LookPath("quint") }`, 0},
		{"other package", `package p; import "example/exec"; func f() { exec.Command("quint") }`, 0},
		{"shadowed alias", `package p; import "os/exec"; func f() { exec := struct{ Command func(string) }{}; exec.Command("quint") }`, 0},
		{"shadowed dot", `package p; import . "os/exec"; func Command(string) {}; func f() { Command("quint") }`, 0},
		{"dynamic outside scope", `package p; import "os/exec"; func f(binary string) { exec.Command(binary) }`, 0},
		{"shell outside scope", `package p; import "os/exec"; func f() { exec.Command("sh", "-c", "quint run model.qnt") }`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, tc.name+".go", tc.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := literalQuintLaunches(fset, file); len(got) != tc.want {
				t.Fatalf("literal launches: %v, want %d", got, tc.want)
			}
		})
	}
}

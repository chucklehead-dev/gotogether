package qanalysis

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"strconv"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
)

const qenvPath = "github.com/chucklehead-dev/gotogether/qenv"

// simFixes are the fixes for quintsim's finding at at, where a context
// is in scope (a context.Context parameter of an enclosing function):
//   - `go f()` (a call without arguments, whose arguments would otherwise
//     be evaluated later) becomes qenv.Go(ctx, "f", func(context.Context) { f() });
//   - time.Now() becomes qenv.Now(ctx), time.Sleep(d) qenv.Sleep(ctx, d).
//
// None for the rest: a wall-clock value, a random draw, a select, or
// no context to give.
func simFixes(pass *analysis.Pass, f *ast.File, at token.Pos) []analysis.SuggestedFix {
	path, _ := astutil.PathEnclosingInterval(f, at, at)
	ctx := ""
	for _, n := range path {
		var ft *ast.FuncType
		switch x := n.(type) {
		case *ast.FuncLit:
			ft = x.Type
		case *ast.FuncDecl:
			ft = x.Type
		}
		if ft != nil {
			if ctx = contextParam(pass, ft); ctx != "" {
				break
			}
		}
	}
	qenv, ok := importedAs(f, qenvPath)
	if ctx == "" || !ok {
		return nil
	}
	for _, n := range path {
		switch x := n.(type) {
		case *ast.GoStmt:
			if x.Pos() != at || len(x.Call.Args) > 0 {
				return nil
			}
			cx, ok := importedAs(f, "context")
			if !ok {
				return nil
			}
			name := "goroutine"
			switch fn := x.Call.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			}
			call := source(pass.Fset, x.Call)
			text := qenv + "Go(" + ctx + ", " + strconv.Quote(name) + ", func(" + cx + "Context) { " + call + " })"
			return []analysis.SuggestedFix{{
				Message:   "start it with qenv.Go",
				TextEdits: []analysis.TextEdit{{Pos: x.Pos(), End: x.End(), NewText: []byte(text)}},
			}}
		case *ast.CallExpr:
			if x.Pos() != at {
				continue
			}
			fn := calleeFunc(pass, x)
			if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != "time" {
				return nil
			}
			var text string
			switch {
			case fn.Name() == "Now" && len(x.Args) == 0:
				text = qenv + "Now(" + ctx + ")"
			case fn.Name() == "Sleep" && len(x.Args) == 1:
				text = qenv + "Sleep(" + ctx + ", " + source(pass.Fset, x.Args[0]) + ")"
			default:
				return nil
			}
			return []analysis.SuggestedFix{{
				Message:   "use qenv." + fn.Name() + " (virtual time)",
				TextEdits: []analysis.TextEdit{{Pos: x.Pos(), End: x.End(), NewText: []byte(text)}},
			}}
		}
	}
	return nil
}

// contextParam is a function's first named context.Context parameter.
func contextParam(pass *analysis.Pass, ft *ast.FuncType) string {
	if ft.Params == nil {
		return ""
	}
	for _, field := range ft.Params.List {
		if !isContext(pass.TypesInfo.TypeOf(field.Type)) {
			continue
		}
		for _, name := range field.Names {
			if name.Name != "_" {
				return name.Name
			}
		}
	}
	return ""
}

// importedAs is the qualifier f uses for the package path ("qenv.", ""
// through a dot import), and whether f imports it.
func importedAs(f *ast.File, path string) (string, bool) {
	for _, im := range f.Imports {
		if p, _ := strconv.Unquote(im.Path.Value); p == path {
			switch {
			case im.Name == nil:
				return importName(path) + ".", true
			case im.Name.Name == ".":
				return "", true
			case im.Name.Name == "_":
				return "", false
			default:
				return im.Name.Name + ".", true
			}
		}
	}
	return "", false
}

func importName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

func calleeFunc(pass *analysis.Pass, call *ast.CallExpr) *types.Func {
	var id *ast.Ident
	switch f := call.Fun.(type) {
	case *ast.SelectorExpr:
		id = f.Sel
	case *ast.Ident:
		id = f
	default:
		return nil
	}
	fn, _ := pass.TypesInfo.Uses[id].(*types.Func)
	return fn
}

func source(fset *token.FileSet, n ast.Node) string {
	var b bytes.Buffer
	_ = format.Node(&b, fset, n)
	return b.String()
}

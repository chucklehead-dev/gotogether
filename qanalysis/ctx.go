package qanalysis

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Ctx reports context.Background() and context.TODO() called in a
// function that has a context of its own: a context.Context parameter,
// or a test's t (testing.TB, *testing.T, *testing.B, *testing.F), whose
// Context() ends with the test. A fresh context drops the caller's
// cancellation (a check that should end with its test ran quint on, the
// eleventh review's H1) and its run (qobs.Record then finds the run
// through the goroutine, the slow and strict path). Only the function's
// own parameters count, not an enclosing function's: a closure handed
// to a goroutine or a t.Cleanup may outlive them by design. The fix
// passes the parameter (or t.Context()); context.WithoutCancel(ctx)
// keeps the run's values without its cancellation. //quintctx:ok on
// the line exempts it (give the reason after it). The default of a nil
// parameter (ctx = context.Background()) is not reported.
var Ctx = &analysis.Analyzer{
	Name: "quintctx",
	Doc:  "report context.Background() and context.TODO() in a function that has a ctx parameter or a test's t",
	URL:  "https://github.com/chucklehead-dev/gotogether/tree/main/qanalysis",
	Run:  runCtx,
}

func runCtx(pass *analysis.Pass) (any, error) {
	for _, f := range pass.Files {
		exempt := map[int]bool{}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if strings.HasPrefix(c.Text, "//quintctx:ok") {
					exempt[pass.Fset.Position(c.Pos()).Line] = true
				}
			}
		}
		defaulted := map[*ast.CallExpr]bool{}  // ctx = context.Background(): a nil parameter's default
		var visit func(n ast.Node, own string) // own: the innermost function's context, as an expression ("" none)
		visit = func(n ast.Node, own string) {
			ast.Inspect(n, func(m ast.Node) bool {
				switch x := m.(type) {
				case *ast.AssignStmt:
					for i, l := range x.Lhs {
						if id, ok := l.(*ast.Ident); ok && id.Name == own && i < len(x.Rhs) {
							if c, ok := x.Rhs[i].(*ast.CallExpr); ok {
								defaulted[c] = true
							}
						}
					}
				case *ast.FuncLit:
					if x != n {
						visit(x.Body, contextOf(pass, x.Type))
						return false
					}
				case *ast.CallExpr:
					name, ok := freshContext(pass, x)
					if !ok || own == "" || defaulted[x] || exempt[pass.Fset.Position(x.Pos()).Line] {
						break
					}
					pass.Report(analysis.Diagnostic{
						Pos: x.Pos(), End: x.End(),
						Message: "context." + name + "() in a function that has " + own + ": pass " + own +
							" (context.WithoutCancel(" + own + ") to outlive it), so cancelling the caller reaches this call",
						SuggestedFixes: []analysis.SuggestedFix{{
							Message:   "pass " + own,
							TextEdits: []analysis.TextEdit{{Pos: x.Pos(), End: x.End(), NewText: []byte(own)}},
						}},
					})
				}
				return true
			})
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
				visit(fd.Body, contextOf(pass, fd.Type))
			}
		}
	}
	return nil, nil
}

// freshContext reports whether call is context.Background() or
// context.TODO(), and which.
func freshContext(pass *analysis.Pass, call *ast.CallExpr) (string, bool) {
	var id *ast.Ident
	switch f := call.Fun.(type) {
	case *ast.SelectorExpr:
		id = f.Sel
	case *ast.Ident:
		id = f
	default:
		return "", false
	}
	fn, ok := pass.TypesInfo.Uses[id].(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "context" || (fn.Name() != "Background" && fn.Name() != "TODO") {
		return "", false
	}
	return fn.Name(), true
}

// contextOf is the context a function's parameters give it, as an
// expression: its first named context.Context parameter, else t.Context()
// of its first named test parameter, else "".
func contextOf(pass *analysis.Pass, ft *ast.FuncType) string {
	if ft.Params == nil {
		return ""
	}
	test := ""
	for _, field := range ft.Params.List {
		t := pass.TypesInfo.TypeOf(field.Type)
		for _, name := range field.Names {
			if name.Name == "_" {
				continue
			}
			switch {
			case isContext(t):
				return name.Name
			case test == "" && isTest(t):
				test = name.Name + ".Context()"
			}
		}
	}
	return test
}

func isContext(t types.Type) bool { return isNamed(t, "context", "Context") }

func isTest(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
		return isNamed(t, "testing", "T") || isNamed(t, "testing", "B") || isNamed(t, "testing", "F")
	}
	return isNamed(t, "testing", "TB")
}

func isNamed(t types.Type, pkg, name string) bool {
	n, ok := t.(*types.Named)
	if !ok {
		if a, ok := t.(*types.Alias); ok {
			return isNamed(types.Unalias(a), pkg, name)
		}
		return false
	}
	o := n.Obj()
	return o.Pkg() != nil && o.Pkg().Path() == pkg && o.Name() == name
}

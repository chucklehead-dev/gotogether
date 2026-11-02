package qlint

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

// runtimeResults checks only statically resolved Sim methods. The syntax-only
// CLI cannot distinguish them from unrelated Run/Settle methods and must not
// guess from receiver names. Method-value aliases require dataflow and are not
// resolved here.
func runtimeResults(fset *token.FileSet, f *ast.File, info *types.Info) []Finding {
	if info == nil {
		return nil
	}
	exempt := map[int]bool{}
	for _, group := range f.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//qenv:ok") {
				exempt[fset.Position(comment.Pos()).Line] = true
			}
		}
	}
	var findings []Finding
	check := func(expr ast.Expr, discarded func(int) bool) {
		call, ok := ast.Unparen(expr).(*ast.CallExpr)
		if !ok {
			return
		}
		name, errorIndex, ok := runtimeResultMethod(call, info)
		if !ok || !discarded(errorIndex) {
			return
		}
		pos := fset.Position(call.Pos())
		if exempt[pos.Line] {
			return
		}
		findings = append(findings, Finding{
			Code: "sim-runtime-result", At: call.Pos(),
			Pos:  fmt.Sprintf("%s:%d:%d", pos.Filename, pos.Line, pos.Column),
			Path: pos.Filename, Line: pos.Line, Column: pos.Column,
			What: "discarded qenv.Sim." + name + " runtime error",
			Fix:  "handle or propagate the error (return it from qtest.System.Run); otherwise an unsettled or bounded run can lose its runtime outcome",
		})
	}
	all := func(int) bool { return true }
	blank := func(expr ast.Expr) bool {
		id, ok := expr.(*ast.Ident)
		return ok && id.Name == "_"
	}
	assignment := func(lhs, rhs []ast.Expr) {
		for i, expr := range rhs {
			check(expr, func(result int) bool {
				at := i
				if len(rhs) == 1 {
					at = result
				} else if result != 0 {
					return false // multi-result calls must stand alone in valid Go
				}
				return at < len(lhs) && blank(lhs[at])
			})
		}
	}
	ast.Inspect(f, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.ExprStmt:
			check(n.X, all)
		case *ast.GoStmt:
			check(n.Call, all)
		case *ast.DeferStmt:
			check(n.Call, all)
		case *ast.AssignStmt:
			assignment(n.Lhs, n.Rhs)
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, len(n.Names))
			for i, name := range n.Names {
				lhs[i] = name
			}
			assignment(lhs, n.Values)
		}
		return true
	})
	return findings
}

func runtimeResultMethod(call *ast.CallExpr, info *types.Info) (string, int, bool) {
	var id *ast.Ident
	switch fn := ast.Unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		id = fn.Sel
	case *ast.Ident:
		id = fn
	default:
		return "", 0, false
	}
	fn, ok := info.Uses[id].(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != qenvPath {
		return "", 0, false
	}
	switch fn.Name() {
	case "Run", "RunUntil", "Settle", "SettleCore":
	default:
		return "", 0, false
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil || sig.Results().Len() == 0 {
		return "", 0, false
	}
	receiver := types.Unalias(sig.Recv().Type())
	if pointer, ok := receiver.(*types.Pointer); ok {
		receiver = types.Unalias(pointer.Elem())
	}
	named, ok := receiver.(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != qenvPath || named.Obj().Name() != "Sim" {
		return "", 0, false
	}
	errorIndex := sig.Results().Len() - 1
	if !types.Identical(sig.Results().At(errorIndex).Type(), types.Universe.Lookup("error").Type()) {
		return "", 0, false
	}
	return fn.Name(), errorIndex, true
}

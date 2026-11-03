package qanalysis

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
)

// runtimeResultFixes propagates a discarded simulation error only where the
// containing function can return it directly. It does not invent other results
// or move an asynchronous/deferred call into the current control flow.
func runtimeResultFixes(pass *analysis.Pass, f *ast.File, at token.Pos) []analysis.SuggestedFix {
	path, _ := astutil.PathEnclosingInterval(f, at, at)
	var call *ast.CallExpr
	var statement ast.Stmt
	statementIndex := -1
	var function *ast.FuncType
	for i, node := range path {
		if c, ok := node.(*ast.CallExpr); ok && c.Pos() == at && call == nil {
			call = c
		}
		if s, ok := node.(ast.Stmt); ok && statement == nil {
			statement, statementIndex = s, i
		}
		switch fn := node.(type) {
		case *ast.FuncLit:
			function = fn.Type
		case *ast.FuncDecl:
			function = fn.Type
		}
		if function != nil {
			break // An outer function's error return does not belong to a closure.
		}
	}
	if call == nil || statement == nil || function == nil || !runtimeErrorReturn(pass, function) {
		return nil
	}
	// An if statement cannot replace a SimpleStmt in an initializer/post slot.
	if statementIndex+1 >= len(path) {
		return nil
	}
	switch path[statementIndex+1].(type) {
	case *ast.BlockStmt, *ast.CaseClause, *ast.CommClause:
	default:
		return nil
	}
	switch s := statement.(type) {
	case *ast.ExprStmt:
		if ast.Unparen(s.X) != call {
			return nil
		}
	case *ast.AssignStmt:
		if len(s.Rhs) != 1 || ast.Unparen(s.Rhs[0]) != call {
			return nil
		}
		for _, lhs := range s.Lhs {
			if id, ok := lhs.(*ast.Ident); !ok || id.Name != "_" {
				return nil // A retained RunUntil boolean must remain available.
			}
		}
	default:
		return nil
	}
	fn, ok := runtimeResultMethod(pass, call)
	if !ok {
		return nil
	}
	if scope := pass.TypesInfo.Scopes[function]; scope != nil {
		if inner := scope.Innermost(at); inner != nil {
			scope = inner
		}
		if _, nilObject := scope.LookupParent("nil", at); nilObject != types.Universe.Lookup("nil") {
			return nil
		}
	} else {
		return nil // Do not guess the lexical meaning of the generated nil.
	}
	bind := "err"
	if fn.Name() == "RunUntil" {
		bind = "_, err"
	}
	file := pass.Fset.File(call.Pos())
	if file == nil || pass.ReadFile == nil {
		return nil
	}
	data, err := pass.ReadFile(file.Name())
	if err != nil {
		return nil
	}
	start, end := file.Offset(call.Pos()), file.Offset(call.End())
	if start < 0 || end < start || end > len(data) {
		return nil
	}
	// Keep comments inside predicates/arguments as well as outside the edit.
	text := "if " + bind + " := " + string(data[start:end]) + "; err != nil { return err }"
	return []analysis.SuggestedFix{{
		Message:   "propagate the simulation error",
		TextEdits: []analysis.TextEdit{{Pos: statement.Pos(), End: statement.End(), NewText: []byte(text)}},
	}}
}

func runtimeErrorReturn(pass *analysis.Pass, fn *ast.FuncType) bool {
	if fn.Results == nil || len(fn.Results.List) != 1 {
		return false
	}
	result := fn.Results.List[0]
	return len(result.Names) <= 1 && types.Identical(pass.TypesInfo.TypeOf(result.Type), types.Universe.Lookup("error").Type())
}

func runtimeResultMethod(pass *analysis.Pass, call *ast.CallExpr) (*types.Func, bool) {
	selector, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	fn, ok := pass.TypesInfo.Uses[selector.Sel].(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != qenvPath {
		return nil, false
	}
	switch fn.Name() {
	case "Run", "RunUntil", "Settle", "SettleCore":
	default:
		return nil, false
	}
	signature, ok := fn.Type().(*types.Signature)
	if !ok || signature.Recv() == nil {
		return nil, false
	}
	results := signature.Results()
	want := 1
	if fn.Name() == "RunUntil" {
		want = 2
	}
	if results.Len() != want || !types.Identical(results.At(want-1).Type(), types.Universe.Lookup("error").Type()) {
		return nil, false
	}
	receiver := types.Unalias(signature.Recv().Type())
	if ptr, ok := receiver.(*types.Pointer); ok {
		receiver = types.Unalias(ptr.Elem())
	}
	named, ok := receiver.(*types.Named)
	if !ok || named.Obj().Name() != "Sim" || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != qenvPath {
		return nil, false
	}
	return fn, true
}

package qanalysis

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// gristPkg is grist's package: Effect is its effect's constructor.
const gristPkg = "github.com/chucklehead-dev/gotogether/grist"

// Raw reports a grist effect that calls a dependency directly, past the
// seam grist records: a net/http client (http.Get, Post, Head, PostForm,
// an *http.Client's Do, Get, Post, Head or PostForm), a gRPC connection
// (a *grpc.ClientConn's Invoke or NewStream, grpc.NewClient or Dial) or a
// database (an *sql.DB's, *sql.Conn's or *sql.Tx's Exec, Query, Prepare
// and Begin methods, sql.Open), in the function literal (or the package's
// function) passed to grist.Effect, closures within it included. grist
// records a declared call at both ends (the caller's step and the
// dependency's site), simulates its faults and models its outcome; a raw
// call is none of these: no step says it happened, no simulation loses
// its answer, and the model cannot judge it. The fix: declare it
// (grist.NewDependency, NewCall, the processor's Uses) and call Call.Do,
// served by grist.OverHTTP or Local on the real tier. //grist:raw
// <reason> on the line (or the line above) exempts a call grist cannot
// declare yet; one without a reason is reported for it.
var Raw = &analysis.Analyzer{
	Name: "quintraw",
	Doc:  "report a grist effect calling net/http, gRPC or database/sql directly, past the declared calls grist records (grist.NewCall, Call.Do)",
	URL:  "https://github.com/chucklehead-dev/gotogether/tree/main/qanalysis",
	Run:  runRaw,
}

// rawCalls are the calls past grist's seam, by package path and the
// receiver's named type ("" for a package function), each with what it
// reaches.
var rawCalls = map[string]map[string][]string{ // read-only
	"net/http": {
		"":       {"Get", "Post", "Head", "PostForm"},
		"Client": {"Do", "Get", "Post", "Head", "PostForm"},
	},
	"google.golang.org/grpc": {
		"":           {"NewClient", "Dial", "DialContext"},
		"ClientConn": {"Invoke", "NewStream"},
	},
	"database/sql": {
		"":     {"Open", "OpenDB"},
		"DB":   {"Exec", "ExecContext", "Query", "QueryContext", "QueryRow", "QueryRowContext", "Prepare", "PrepareContext", "Begin", "BeginTx", "Conn"},
		"Conn": {"ExecContext", "QueryContext", "QueryRowContext", "PrepareContext", "BeginTx"},
		"Tx":   {"Exec", "ExecContext", "Query", "QueryContext", "QueryRow", "QueryRowContext", "Prepare", "PrepareContext", "Commit"},
	},
}

// rawWhat names what a dependency's package reaches, for the message.
var rawWhat = map[string]string{"net/http": "an HTTP service", "google.golang.org/grpc": "a gRPC service", "database/sql": "a database"} // read-only

func runRaw(pass *analysis.Pass) (any, error) {
	// the package's functions by object, for an effect given by name
	decls := map[types.Object]*ast.FuncDecl{}
	for _, f := range pass.Files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Body != nil {
				decls[pass.TypesInfo.Defs[fd.Name]] = fd
			}
		}
	}
	for _, f := range pass.Files {
		raw := map[int]string{} // line -> //grist:raw's reason
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if r, ok := strings.CutPrefix(c.Text, "//grist:raw"); ok && (r == "" || r[0] == ' ') {
					raw[pass.Fset.Position(c.Pos()).Line] = strings.TrimSpace(r)
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isGristEffect(pass, call) || len(call.Args) == 0 {
				return true
			}
			var body ast.Node
			switch a := ast.Unparen(call.Args[0]).(type) {
			case *ast.FuncLit:
				body = a.Body
			case *ast.Ident:
				if fd := decls[pass.TypesInfo.Uses[a]]; fd != nil {
					body = fd.Body
				}
			}
			if body == nil {
				return true
			}
			ast.Inspect(body, func(m ast.Node) bool {
				c, ok := m.(*ast.CallExpr)
				if !ok {
					return true
				}
				pkg, recv, name, ok := rawCall(pass, c)
				if !ok {
					return true
				}
				line := pass.Fset.Position(c.Pos()).Line
				reason, exempt := raw[line]
				if !exempt {
					reason, exempt = raw[line-1]
				}
				callee := pkg[strings.LastIndex(pkg, "/")+1:] + "." + name
				if recv != "" {
					callee = "(*" + pkg[strings.LastIndex(pkg, "/")+1:] + "." + recv + ")." + name
				}
				switch {
				case exempt && reason != "":
				case exempt:
					pass.Reportf(c.Pos(), "//grist:raw without a reason on a call of %s: say why grist cannot declare it (//grist:raw <reason>)", callee)
				default:
					pass.Reportf(c.Pos(), "a grist effect calls %s directly, reaching %s past the seam grist records: no step says it happened, no simulation loses its answer, the model cannot judge it; "+
						"declare it (grist.NewDependency, grist.NewCall, the processor's grist.Uses) and call it with Call.Do, or mark the line //grist:raw <reason>", callee, rawWhat[pkg])
				}
				return true
			})
			return true
		})
	}
	return nil, nil
}

// isGristEffect reports whether call is grist.Effect(...).
func isGristEffect(pass *analysis.Pass, call *ast.CallExpr) bool {
	var id *ast.Ident
	switch f := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return false
	}
	fn, ok := pass.TypesInfo.Uses[id].(*types.Func)
	return ok && fn.Pkg() != nil && fn.Pkg().Path() == gristPkg && fn.Name() == "Effect"
}

// rawCall is c's callee when it is one of rawCalls: its package, its
// receiver's type name ("" for a function) and its name.
func rawCall(pass *analysis.Pass, c *ast.CallExpr) (pkg, recv, name string, ok bool) {
	var id *ast.Ident
	switch f := ast.Unparen(c.Fun).(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return "", "", "", false
	}
	fn, ok := pass.TypesInfo.Uses[id].(*types.Func)
	if !ok || fn.Pkg() == nil {
		return "", "", "", false
	}
	byRecv, ok := rawCalls[fn.Pkg().Path()]
	if !ok {
		return "", "", "", false
	}
	if r := fn.Type().(*types.Signature).Recv(); r != nil {
		t := r.Type()
		if p, isPtr := t.(*types.Pointer); isPtr {
			t = p.Elem()
		}
		named, isNamed := t.(*types.Named)
		if !isNamed {
			return "", "", "", false
		}
		recv = named.Obj().Name()
	}
	for _, n := range byRecv[recv] {
		if n == fn.Name() {
			return fn.Pkg().Path(), recv, n, true
		}
	}
	return "", "", "", false
}

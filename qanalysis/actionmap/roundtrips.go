package actionmap

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"maps"
	"slices"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

const switchType = "github.com/chucklehead-dev/gotogether/connect.Switch"

// A RoundTrip is a call one model action's code makes into shared state:
// a method of an interface, or a method of another module's type, whose
// first parameter is a context.Context (a store, a client, a peer
// service).
type RoundTrip struct {
	Call string // the callee: pkgpath.(T).Method, or pkgpath.Func
	At   string // file:line of the call
	In   string // the function making it, as Name names it
}

// A Split is one connect action whose driver step reaches more than one
// round trip: the model takes the action in one step, so no trace
// interleaves another action between them, and a bug there (a read of a
// budget, then a reservation against it) passes every connect test
// (patterns skill §2).
type Split struct {
	Action string
	Driver string // the driver function, as Name names it, and the case's position
	At     string
	Trips  []RoundTrip
}

// RoundTrips loads the packages matching patterns (relative to dir),
// their tests included, finds each connect.Switch case of a driver (the
// action named by its key, a function literal or a function as its
// value), and lists those whose code reaches more than one round trip.
// Reachability follows calls through the modules' own code (static
// callees, and VTA's edges for dynamic calls), stopping at a round trip:
// the store's own implementation of it is not counted again. A round
// trip made from a _test.go file (a fault the driver injects, a seed it
// writes) is the environment's and not counted; the code it calls is
// still followed. It is a static
// over-approximation: two calls on exclusive branches both count, and a
// call in a loop counts once. Read each Split's calls before splitting
// the model's action.
func RoundTrips(dir string, patterns ...string) ([]Split, error) {
	cfg := &packages.Config{Mode: packages.LoadAllSyntax | packages.NeedModule, Dir: dir, Tests: true}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	var errs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			errs = append(errs, e.Error())
		}
	})
	if len(errs) > 0 {
		return nil, fmt.Errorf("actionmap: loading %v: %s", patterns, strings.Join(errs, "; "))
	}
	prog, _ := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()
	// own: the packages of the loaded patterns' modules; a call into any
	// other module that takes a context is a round trip
	modules := map[string]bool{}
	for _, p := range pkgs {
		if p.Module != nil {
			modules[p.Module.Path] = true
		}
	}
	own := map[*types.Package]bool{}
	for _, p := range prog.AllPackages() {
		if inModules(p.Pkg.Path(), modules) {
			own[p.Pkg] = true
		}
	}
	all := ssautil.AllFunctions(prog)
	cg := vta.CallGraph(all, cha.CallGraph(prog))
	cg.DeleteSyntheticNodes()

	seen := map[string]bool{} // a test package's variants list a case more than once
	var out []Split
	for _, p := range pkgs {
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || !isSwitch(p.TypesInfo.TypeOf(lit)) {
					return true
				}
				for _, el := range lit.Elts {
					kv, ok := el.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					tv := p.TypesInfo.Types[kv.Key]
					if tv.Value == nil || tv.Value.Kind() != constant.String {
						continue
					}
					action := constant.StringVal(tv.Value)
					fn := handler(p, prog, f, kv.Value)
					if fn == nil {
						continue
					}
					pos := p.Fset.Position(kv.Pos())
					key := fmt.Sprintf("%s@%s:%d", action, pos.Filename, pos.Line)
					if seen[key] {
						continue
					}
					seen[key] = true
					trips := tripsFrom(prog, cg, fn, own)
					if len(trips) > 1 {
						out = append(out, Split{Action: action, Driver: Name(fn), At: fmt.Sprintf("%s:%d", pos.Filename, pos.Line), Trips: trips})
					}
				}
				return true
			})
		}
	}
	slices.SortFunc(out, func(a, b Split) int { return strings.Compare(a.At, b.At) })
	return out, nil
}

func inModules(path string, modules map[string]bool) bool {
	for m := range modules {
		if path == m || strings.HasPrefix(path, m+"/") {
			return true
		}
	}
	return false
}

func isSwitch(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path()+"."+n.Obj().Name() == switchType
}

// handler is the SSA function a Switch case's value denotes: a function
// literal, a function, or a method value (d.deposit). connect.Skip and
// other functions of other modules are none.
func handler(p *packages.Package, prog *ssa.Program, file *ast.File, v ast.Expr) *ssa.Function {
	switch e := ast.Unparen(v).(type) {
	case *ast.FuncLit:
		// the literal's own function (ssautil.AllFunctions lists no
		// function of a test variant's unexported types)
		path, _ := astutil.PathEnclosingInterval(file, e.Body.Lbrace, e.Body.Lbrace)
		if pkg := prog.Package(p.Types); pkg != nil {
			return ssa.EnclosingFunction(pkg, path)
		}
	case *ast.Ident:
		if f, ok := p.TypesInfo.Uses[e].(*types.Func); ok {
			return prog.FuncValue(f)
		}
	case *ast.SelectorExpr:
		if sel := p.TypesInfo.Selections[e]; sel != nil {
			if f, ok := sel.Obj().(*types.Func); ok {
				return prog.FuncValue(f)
			}
		} else if f, ok := p.TypesInfo.Uses[e.Sel].(*types.Func); ok {
			return prog.FuncValue(f)
		}
	}
	return nil
}

// tripsFrom are the round trips reachable from fn, each call site once,
// in the order of their positions.
func tripsFrom(prog *ssa.Program, cg *callgraph.Graph, fn *ssa.Function, own map[*types.Package]bool) []RoundTrip {
	found := map[token.Pos]RoundTrip{}
	visited := map[*ssa.Function]bool{}
	var visit func(f *ssa.Function)
	visit = func(f *ssa.Function) {
		if f == nil || visited[f] || !isOwn(f, own) {
			return
		}
		visited[f] = true
		for _, b := range f.Blocks {
			for _, ins := range b.Instrs {
				call, ok := ins.(ssa.CallInstruction)
				if !ok {
					continue
				}
				if callee, ok := roundTrip(call.Common(), own); ok {
					// the test's own calls (a fault the driver injects, a
					// seed it writes) are the environment's, not the code's
					if at := position(prog, call.Pos()); !strings.Contains(at, "_test.go:") {
						found[call.Pos()] = RoundTrip{Call: callee, At: at, In: Name(f)}
					}
					continue // the store's own implementation is the round trip, not more of them
				}
				// a static call is followed by its callee (the call graph
				// has no node for a test variant's functions, a driver's
				// closures among them), a dynamic one by the graph's edges
				if callee := call.Common().StaticCallee(); callee != nil {
					visit(callee)
				} else if n := cg.Nodes[f]; n != nil {
					for _, e := range n.Out {
						if e.Site == call {
							visit(e.Callee.Func)
						}
					}
				}
			}
		}
		for _, anon := range f.AnonFuncs {
			visit(anon)
		}
	}
	visit(fn)
	keys := slices.Sorted(maps.Keys(found))
	out := make([]RoundTrip, 0, len(keys))
	for _, k := range keys {
		out = append(out, found[k])
	}
	slices.SortStableFunc(out, func(a, b RoundTrip) int { return strings.Compare(a.At, b.At) })
	return out
}

// roundTrip names a call into shared state: its first parameter a
// context.Context, and its callee an interface's method or a method of
// another module's type (qenv's and qobs's seams aside, which are the
// simulation's, not shared state).
func roundTrip(c *ssa.CallCommon, own map[*types.Package]bool) (string, bool) {
	sig := c.Signature()
	if sig == nil || sig.Params().Len() == 0 || !isContext(sig.Params().At(0).Type()) {
		return "", false
	}
	if c.IsInvoke() {
		recv := types.TypeString(c.Value.Type(), func(p *types.Package) string { return p.Path() })
		return recv + "." + c.Method.Name(), true
	}
	// a method of another module's type (a client's, a pool's): a plain
	// function taking a context there builds a request or a command
	// (http.NewRequestWithContext, exec.CommandContext) and goes nowhere
	f := c.StaticCallee()
	if f == nil || isOwn(f, own) || f.Signature.Recv() == nil {
		return "", false
	}
	pkg := ""
	if f.Pkg != nil {
		pkg = f.Pkg.Pkg.Path()
	} else if o := f.Object(); o != nil && o.Pkg() != nil {
		pkg = o.Pkg().Path()
	}
	for _, seam := range []string{"github.com/chucklehead-dev/gotogether/qenv", "github.com/chucklehead-dev/gotogether/qobs", "github.com/chucklehead-dev/gotogether/qrun", "context", "log/slog"} {
		if pkg == seam {
			return "", false
		}
	}
	return Name(f), true
}

func isContext(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "context" && n.Obj().Name() == "Context"
}

func position(prog *ssa.Program, pos token.Pos) string {
	p := prog.Fset.Position(pos)
	return fmt.Sprintf("%s:%d", p.Filename, p.Line)
}

// SplitReport formats splits, one action per paragraph, each round trip
// with where it is made.
func SplitReport(splits []Split) string {
	var b strings.Builder
	for _, s := range splits {
		fmt.Fprintf(&b, "%s (%s): one model step makes %d round trips, which no connect trace interleaves; split the action at each, or run the code in qenv (patterns §2):\n", s.Action, s.At, len(s.Trips))
		for _, t := range s.Trips {
			fmt.Fprintf(&b, "  %s at %s (in %s)\n", t.Call, t.At, t.In)
		}
	}
	return b.String()
}

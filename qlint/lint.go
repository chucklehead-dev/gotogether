// Package qlint inspects bindings, recording sites, and simulation integration.
// Inspect produces one structured report for CLI, editor, and agent consumers;
// SimFile and WovenFile also serve the qanalysis analyzers. It is apart from
// qenv so gosim does not translate go/ast and go/parser with simulated code.
package qlint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// A Finding is code in a simulation that escapes it.
type Finding struct {
	// Code identifies the rule independently of its message or source spelling.
	Code string
	Pos  string // file:line:col
	What string
	Fix  string
	// At is Pos in the file set the file was parsed with, for analyzers.
	At token.Pos

	file      string
	line, col int
}

func sortFindings(fs []Finding) {
	sort.Slice(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.file != b.file {
			return a.file < b.file
		}
		if a.line != b.line {
			return a.line < b.line
		}
		return a.col < b.col
	})
}

func (f Finding) String() string { return f.Pos + ": " + f.What + ": " + f.Fix }

// Sim scans the Go files under dir that import qenv (simulation code),
// tests included (a System's Run closure lives in a test), for code that
// escapes the simulation:
//   - goroutines started with `go`: outside the scheduler, so runs are
//     not reproducible, and a step they record has no run (a strict run
//     fails on it);
//   - the wall clock (time.Now, Since, Until, Sleep, After, AfterFunc, Tick,
//     NewTimer, NewTicker): virtual time does not pass for it;
//   - steps recorded with context.Background() or TODO(): they find their
//     run through the goroutine (qrun), but the task's context
//     says so directly, and keeps working if the code moves to a goroutine
//     of its own;
//   - math/rand's (and math/rand/v2's, crypto/rand's) package functions:
//     their source is seeded at random, so a run does not replay (a
//     generator from rand.New with a seed the run chose is fine);
//   - a select with two or more communication cases: when several are
//     ready Go picks one at random, which no chooser makes.
//
// The wall clock's and the random packages' functions are found as
// values too (now := time.Now, a field set to rand.IntN): a call of the
// value escapes as a call of the function would. Without type
// information (Sim, SimFile) a function is found by its package's import
// name (time.Now); with it (SimFileTyped, the analyzers) by what the name
// denotes, so a dot import is seen and a local variable named time is not.
//
// A grist application's code is checked too: the non-test files that
// import grist (its decisions, folds, handlers and effects run in a
// simulation under gristsim, and on every node of the real tier), each
// finding saying what grist offers in its place (an event's Meta.Time
// for the wall clock, a command's arguments for a random value, a
// processor for a goroutine).
//
// A line with a //qenv:ok comment is exempt, for code that runs outside
// the simulation on purpose (a real server next to a simulated client).
func Sim(dir string) ([]Finding, error) {
	var out []Finding
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != dir && (d.Name() == "vendor" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		out = append(out, SimFile(fset, f)...)
		return nil
	})
	sortFindings(out)
	return out, err
}

const qenvPath = "github.com/chucklehead-dev/gotogether/qenv"

// gristPath is grist's package: a file importing it is an application's
// code, which gristsim runs in a simulation (its decisions, folds,
// handlers and effects) and the real tier serves.
const gristPath = "github.com/chucklehead-dev/gotogether/grist"

// Read-only.
var wallClock = map[string]string{
	"Now": "qenv.Now(ctx) or Sim.Now", "Since": "qenv.Since(ctx, m) on a qenv.Mono(ctx) reading (Now(ctx).Sub is wall arithmetic, which a clock jump moves)",
	"Until": "qenv.WaitFor(ctx, ready, d), or an interval on qenv.Mono(ctx)",
	"Sleep": "qenv.Sleep(ctx, d)", "After": "qenv.Sleep, or a Signal with a deadline", "AfterFunc": "qenv.Go with qenv.Sleep",
	"Tick": "a loop with qenv.Sleep", "NewTimer": "qenv.Sleep, or a Signal with a deadline", "NewTicker": "a loop with qenv.Sleep",
}

// importName is a package's name as its import path gives it: its last
// segment, but for a major version's ("math/rand/v2" is rand).
func importName(p string) string {
	segs := strings.Split(p, "/")
	name := segs[len(segs)-1]
	if len(segs) > 1 && len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
		name = segs[len(segs)-2]
	}
	return name
}

// randConstructors are the random packages' functions that make a
// generator from a seed given: deterministic if the seed is.
// Read-only.
var randConstructors = map[string]bool{"New": true, "NewSource": true, "NewPCG": true, "NewChaCha8": true, "NewZipf": true}

// randFuncs are the random packages' functions that draw, by name: a
// value of one, without type information, is told from a type's name
// (rand.Rand) by this list.
// Read-only.
var randFuncs = map[string]bool{}

func init() {
	for p, names := range map[string]string{
		"math/rand":    "ExpFloat64 Float32 Float64 Int Int31 Int31n Int63 Int63n Intn NormFloat64 Perm Read Seed Shuffle Uint32 Uint64",
		"math/rand/v2": "ExpFloat64 Float32 Float64 Int Int32 Int32N Int64 Int64N IntN N NormFloat64 Perm Shuffle Uint Uint32 Uint32N Uint64 Uint64N UintN",
		"crypto/rand":  "Int Prime Read Text",
	} {
		for n := range strings.FieldsSeq(names) {
			randFuncs[p+"."+n] = true
		}
	}
}

// SimFile is Sim's check of one parsed file (with comments): nothing
// unless the file imports qenv, or is a grist application's (a non-test
// file importing grist).
func SimFile(fset *token.FileSet, f *ast.File) []Finding { return simFile(fset, f, nil, false) }

// SimFileTyped is SimFile with the file's type information: a function
// is known by the object its name denotes, not by its package's import
// name (a dot import's Now(), a function value through it).
func SimFileTyped(fset *token.FileSet, f *ast.File, info *types.Info) []Finding {
	return simFile(fset, f, info, false)
}

// simFile is SimFile (SimFileTyped with info); with all, of any file: the
// core's own simulation code (qenv, its doubles, qload) imports no qenv
// to say so.
func simFile(fset *token.FileSet, f *ast.File, info *types.Info, all bool) []Finding {
	names := map[string]string{} // import path -> local name
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		name := importName(p)
		if im.Name != nil {
			name = im.Name.Name
		}
		names[p] = name
	}
	_, sim := names[qenvPath]
	_, imp := names[gristPath]
	// app: a grist application's own code, not a simulation's: its
	// non-test files (its tests drive a real tier, or gristsim, which
	// runs the simulation itself; one importing qenv is a simulation's)
	app := imp && !sim && !strings.HasSuffix(fset.Position(f.Package).Filename, "_test.go")
	if !all && (!sim && !app || sim && f.Name.Name == "qenv") {
		return nil
	}
	exempt := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "//qenv:ok") {
				exempt[fset.Position(c.Pos()).Line] = true
			}
		}
	}
	var out []Finding
	add := func(n ast.Node, code, what, fix string) {
		pos := fset.Position(n.Pos())
		if !exempt[pos.Line] {
			out = append(out, Finding{Code: code, Pos: fmt.Sprintf("%s:%d:%d", pos.Filename, pos.Line, pos.Column), What: what, Fix: fix,
				At: n.Pos(), file: pos.Filename, line: pos.Line, col: pos.Column})
		}
	}
	// funcOf is the package-level function e names (pkg.F, or F through a dot
	// import when typed): by type information if there is any, else by
	// the import's local name
	funcOf := func(e ast.Expr) (pkgPath, name string, ok bool) {
		if info != nil {
			var id *ast.Ident
			switch x := e.(type) {
			case *ast.Ident:
				id = x
			case *ast.SelectorExpr:
				id = x.Sel
			default:
				return "", "", false
			}
			f, ok := info.Uses[id].(*types.Func)
			if !ok || f.Pkg() == nil || f.Type().(*types.Signature).Recv() != nil {
				return "", "", false
			}
			return f.Pkg().Path(), f.Name(), true
		}
		s, ok := e.(*ast.SelectorExpr)
		if !ok {
			return "", "", false
		}
		id, ok := s.X.(*ast.Ident)
		if !ok {
			return "", "", false
		}
		for p, local := range names {
			if local == id.Name {
				return p, s.Sel.Name, true
			}
		}
		return "", "", false
	}
	sel := func(e ast.Expr, pkgPath string) (string, bool) {
		p, name, ok := funcOf(e)
		return name, ok && p == pkgPath
	}
	// escapes is why calling pkgPath.name leaves the simulation, and the fix
	escapes := func(pkgPath, name string) (what, fix string) {
		switch pkgPath {
		case "math/rand", "math/rand/v2", "crypto/rand":
			if !randConstructors[name] && app {
				return pkgPath + "." + name + " draws from a source seeded at random in a grist application, so a run does not replay and no model can compute it",
					"take the value from the command's arguments (the caller chooses it, and the step records it), or derive it from what the decision read"
			}
			if !randConstructors[name] {
				return pkgPath + "." + name + " draws from a source seeded at random, so a run does not replay",
					"draw from the run's chooser (Sim.Choose(kind, n)), or from rand.New with a seed the run chose"
			}
		case "time":
			if wallClock[name] != "" && app {
				return "time." + name + " reads the wall clock in a grist application, which gristsim runs in virtual time and no model can compute",
					"use the event's Meta.Time (the time it was appended, the same on every node and every replay); elsewhere " + wallClock[name] + ", which is the clock outside a simulation"
			}
			if wallClock[name] != "" {
				return "time." + name + " reads the wall clock, which does not move in a simulation", "use " + wallClock[name]
			}
		}
		return "", ""
	}
	escapeCode := func(path string) string {
		if path == "time" {
			return "simulation.wall_clock"
		}
		return "simulation.random"
	}
	// called are the call expressions' functions, and selectors' names:
	// what is left of a flagged function's uses are its values
	called, selName := map[ast.Expr]bool{}, map[*ast.Ident]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			called[ast.Unparen(n.Fun)] = true
		case *ast.SelectorExpr:
			selName[n.Sel] = true
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		if e, ok := n.(ast.Expr); ok && !called[e] {
			if id, ok := e.(*ast.Ident); !ok || !selName[id] {
				// without types a selector may name a type (rand.Rand): only
				// the functions known by name are values
				if p, name, ok := funcOf(e); ok && (info != nil || p == "time" || randFuncs[p+"."+name]) {
					if what, fix := escapes(p, name); what != "" {
						add(n, escapeCode(p), p+"."+name+" used as a value: a call of it "+strings.TrimPrefix(what, p+"."+name+" "),
							fix+" (in a func that calls it, where a function value is wanted)")
					}
				}
			}
		}
		switch n := n.(type) {
		case *ast.GoStmt:
			if app {
				add(n, "simulation.goroutine", "goroutine started with `go` in a grist application: its work is no block's, recorded nowhere and outside a simulation",
					"make it a processor (grist.NewProcessor) on the event that starts it, or start it with qenv.Go(ctx, name, fn)")
				break
			}
			add(n, "simulation.goroutine", "goroutine started with `go`, outside the simulation", "start it with qenv.Go(ctx, name, fn)")
		case *ast.SelectStmt:
			cases := 0
			for _, c := range n.Body.List {
				if cc, ok := c.(*ast.CommClause); ok && cc.Comm != nil {
					cases++
				}
			}
			if cases >= 2 {
				add(n, "simulation.select", fmt.Sprintf("select with %d cases: when several are ready Go picks one at random, which no run replays", cases),
					"wait for one thing at a time (qenv.Wait, a qenv.Signal), or mark it //qenv:ok if at most one case can be ready at once")
			}
		case *ast.CallExpr:
			if p, name, ok := funcOf(ast.Unparen(n.Fun)); ok {
				if what, fix := escapes(p, name); what != "" {
					add(n, escapeCode(p), what, fix)
				}
			}
			if fn, ok := sel(n.Fun, "github.com/chucklehead-dev/gotogether/qobs"); ok && (fn == "Record" || fn == "Invoke") && len(n.Args) > 0 {
				if c, ok := n.Args[0].(*ast.CallExpr); ok {
					if bg, ok := sel(c.Fun, "context"); ok && (bg == "Background" || bg == "TODO") {
						add(n, "simulation.context", "step recorded with context."+bg+"()", "record through the task's context, or Sim.Context() outside tasks")
					}
				}
			}
		}
		return true
	})
	return out
}

// syncMethods are the sync.Mutex, RWMutex and WaitGroup methods qenv/weave
// rewrites, when called.
// Read-only.
var syncMethods = map[string]bool{"Lock": true, "Unlock": true, "TryLock": true, "RLock": true, "RUnlock": true,
	"TryRLock": true, "Add": true, "Done": true, "Wait": true}

// Woven scans the Go files under dir, tests excluded, for what
// qenv/weave cannot bring into a simulation:
//   - a go statement not marked //qenv:go: its goroutine runs outside the
//     simulation;
//   - a lock or wait group method used as a value (st.mu.RUnlock passed or
//     returned, not called): weaving rewrites calls, so it would run the
//     real method on an object whose simulated state it never sees. Wrap
//     it in a function literal that calls it;
//   - a sync.Mutex, RWMutex or WaitGroup embedded in a struct: Orchestrion
//     matches a call by its receiver expression's type, so a promoted call
//     (t.Lock() on a struct embedding sync.Mutex) is not woven, and takes
//     the real lock. Name the field (mu sync.Mutex) and call t.mu.Lock().
//
// Method values are found by name (no type information), so a method of
// another type with one of those names is reported too: mark a deliberate
// one //qenv:ok.
func Woven(dir string) ([]Finding, error) {
	var out []Finding
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != dir && (d.Name() == "vendor" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		out = append(out, WovenFile(fset, f)...)
		return nil
	})
	sortFindings(out)
	return out, err
}

// WovenFile is Woven's check of one parsed file (with comments).
func WovenFile(fset *token.FileSet, f *ast.File) []Finding {
	marked := map[int]string{} // line -> directive on it
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			for _, d := range []string{"//qenv:go", "//qenv:ok"} {
				if c.Text == d || strings.HasPrefix(c.Text, d+" ") {
					marked[fset.Position(c.Pos()).Line] = d
				}
			}
		}
	}
	var out []Finding
	add := func(n ast.Node, code, what, fix string) {
		pos := fset.Position(n.Pos())
		out = append(out, Finding{Code: code, Pos: fmt.Sprintf("%s:%d:%d", pos.Filename, pos.Line, pos.Column), What: what, Fix: fix,
			At: n.Pos(), file: pos.Filename, line: pos.Line, col: pos.Column})
	}
	called := map[ast.Expr]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			called[c.Fun] = true
		}
		return true
	})
	syncName := ""
	for _, im := range f.Imports {
		if im.Path.Value == `"sync"` {
			syncName = "sync"
			if im.Name != nil {
				syncName = im.Name.Name
			}
		}
	}
	embedsSync := func(e ast.Expr) (string, bool) {
		if st, ok := e.(*ast.StarExpr); ok {
			e = st.X
		}
		sel, ok := e.(*ast.SelectorExpr)
		if !ok || syncName == "" {
			return "", false
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name != syncName {
			return "", false
		}
		switch sel.Sel.Name {
		case "Mutex", "RWMutex", "WaitGroup":
			return sel.Sel.Name, true
		}
		return "", false
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Field:
			if t, ok := embedsSync(n.Type); ok && len(n.Names) == 0 && marked[fset.Position(n.Pos()).Line] != "//qenv:ok" {
				add(n, "weave.embedded_sync", "embedded sync."+t+": calls promoted through the struct are not woven (weaving matches the receiver's type)",
					"name the field (mu sync."+t+") and call it through the name")
			}
		case *ast.GoStmt:
			line := fset.Position(n.Pos()).Line
			if marked[line-1] != "//qenv:go" && marked[line] != "//qenv:ok" {
				add(n, "weave.goroutine", "go statement not marked //qenv:go: its goroutine runs outside the simulation",
					"mark it //qenv:go (the call must be a function literal), or //qenv:ok if it should stay outside")
			}
		case *ast.SelectorExpr:
			if syncMethods[n.Sel.Name] && !called[n] && marked[fset.Position(n.Pos()).Line] != "//qenv:ok" {
				add(n, "weave.method_value", "method value "+n.Sel.Name+" is not woven (weaving rewrites calls)",
					"wrap it in a function literal that calls it: func() { x."+n.Sel.Name+"() }")
			}
		}
		return true
	})
	return out
}

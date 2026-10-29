// Package qanalysis has gotogether's checks as go/analysis passes:
// what escapes a qenv simulation (Sim), what qenv/weave cannot rewrite
// (Weave), //quint:action annotations against their binding (Action),
// fresh contexts where the function has one of its own (Ctx), and a
// grist effect calling a dependency past the seam grist records (Raw).
// They run in a multichecker (cmd/quintvet, also usable as `go vet
// -vettool`) and in golangci-lint as a module plugin (package golangci).
//
// Sim and Weave wrap the checks of `gotogether lint -sim` and `-weave`
// (package qlint), so the CLI and the analyzers report the same findings.
// This is its own module because the core keeps to OTel, yaml.v3 and
// Hegel, and x/tools is none of them.
package qanalysis

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/types"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qlint"
)

// All are the analyzers, for a multichecker.
var All = []*analysis.Analyzer{Sim, Weave, Action, Ctx, Raw}

// Sim reports code in files that import qenv that escapes the simulation:
// go statements, the wall clock, steps recorded with a background context
// (qlint.SimFileTyped). Test files are checked, as by `gotogether lint -sim`: a
// System's Run closure lives in one. With the package's types it knows a
// function by what its name denotes, so it finds what the CLI's syntactic
// check cannot: a function value or call through a dot import, and no
// false finding for a local variable that shadows an import's name.
// Where a context is in scope it suggests the fix (simFixes): a `go`
// statement started with qenv.Go, time.Now and time.Sleep as qenv's.
var Sim = &analysis.Analyzer{
	Name: "quintsim",
	Doc:  "report code that escapes a qenv simulation (go statements, the wall clock, steps recorded with context.Background)",
	URL:  "https://github.com/chucklehead-dev/gotogether/tree/main/qanalysis",
	Run: func(pass *analysis.Pass) (any, error) {
		for _, f := range pass.Files {
			for _, x := range qlint.SimFileTyped(pass.Fset, f, pass.TypesInfo) {
				pass.Report(analysis.Diagnostic{Pos: x.At, Message: x.What + ": " + x.Fix, SuggestedFixes: simFixes(pass, f, x.At)})
			}
		}
		return nil, nil
	},
}

// Weave reports what qenv/weave cannot bring into a simulation: unmarked
// go statements, lock methods used as values, embedded sync types
// (qlint.WovenFile). Run it on packages built with qenv/weave's aspects.
// It suggests the fix where one is mechanical (weaveFixes): //qenv:go
// above a function literal's go statement, a method value called from a
// function literal.
var Weave = &analysis.Analyzer{
	Name: "quintweave",
	Doc:  "report what qenv/weave cannot rewrite (unmarked go statements, sync method values, embedded sync types)",
	URL:  "https://github.com/chucklehead-dev/gotogether/tree/main/qanalysis",
	Run: func(pass *analysis.Pass) (any, error) {
		for _, f := range pass.Files {
			if strings.HasSuffix(pass.Fset.File(f.Pos()).Name(), "_test.go") {
				continue
			}
			for _, x := range qlint.WovenFile(pass.Fset, f) {
				pass.Report(analysis.Diagnostic{Pos: x.At, Message: x.What + ": " + x.Fix, SuggestedFixes: weaveFixes(pass, f, x.At)})
			}
		}
		return nil, nil
	},
}

// Action checks //quint:action annotations. Without a binding: each names
// its action, and its expressions use only the function's parameters,
// receiver and named results (they are evaluated on return, where its
// locals are out of scope). With -binding: each names an action of the
// binding's model and records every field the binding's templates for it
// use (binding.LintAnnotations), and every literal action passed to
// qobs.Record or Recorder.Record is one of the model's (binding.LintRecords;
// loading the model runs quint).
var Action = &analysis.Analyzer{
	Name: "quintaction",
	Doc:  "check //quint:action annotations: the action is named, expressions use only parameters, the receiver and named results, and (with -binding) the binding's model has the action and its fields are recorded, as it has every literal qobs.Record action",
	URL:  "https://github.com/chucklehead-dev/gotogether/tree/main/qanalysis",
	Run:  runAction,
}

var bindingPath, quintBin string

func init() {
	Action.Flags.StringVar(&bindingPath, "binding", "", "binding file to check annotations against (empty: no binding checks)")
	Action.Flags.StringVar(&quintBin, "quint", "quint", "quint binary, to load the binding's model")
}

// notExpressions are the directive's keys that are not Go expressions.
var notExpressions = map[string]bool{"when": true}

func runAction(pass *analysis.Pass) (any, error) {
	var lint func(binding.Annotation) []string
	var model *binding.Model
	if bindingPath != "" {
		b, m, err := loadBinding(bindingPath, quintBin)
		if err != nil {
			return nil, err
		}
		lint = func(a binding.Annotation) []string {
			errs, _ := b.LintAnnotations(m, []binding.Annotation{a})
			return errs
		}
		model = m
	}
	for _, f := range pass.Files {
		if model != nil {
			checkRecords(pass, model, f)
		}
		for _, a := range binding.AnnotationsOf(pass.Fset, f) {
			if a.Action == "" {
				pass.Reportf(a.At, "//quint:action on %s without action:NAME: name the model action the step is", a.Func)
				continue
			}
			c := directive(f, a)
			checkScope(pass, c, a)
			if lint != nil {
				for _, e := range lint(a) {
					var fixes []analysis.SuggestedFix
					if _, ok := model.Actions[a.Action]; !ok {
						fixes = renameAction(c, a.Action, model.NearestAction(a.Action))
					} else if m := unrecorded.FindStringSubmatch(e); m != nil {
						fixes = recordField(pass, c, a, m[1])
					}
					pass.Report(analysis.Diagnostic{Pos: a.At, Message: strings.TrimPrefix(e, a.Pos+": "), SuggestedFixes: fixes})
				}
			}
		}
	}
	return nil, nil
}

// checkRecords reports the literal actions of f's qobs.Record and
// Recorder.Record calls that model lacks. The scan is gotogether lint's
// (binding.RecordsOf); types confirm each call is qobs's.
func checkRecords(pass *analysis.Pass, m *binding.Model, f *ast.File) {
	for _, r := range binding.RecordsOf(pass.Fset, f) {
		if !isQobsRecord(typeutil.Callee(pass.TypesInfo, r.Call)) {
			continue
		}
		for _, e := range binding.LintRecords(m, []binding.RecordCall{r}) {
			pass.Report(analysis.Diagnostic{Pos: r.At, Message: strings.TrimPrefix(e, r.Pos+": "), SuggestedFixes: recordFix(m, r)})
		}
	}
}

// isQobsRecord reports whether obj is qobs.Record or (*qobs.Recorder).Record,
// or their Invoke.
func isQobsRecord(obj types.Object) bool {
	fn, ok := obj.(*types.Func)
	return ok && (fn.Name() == "Record" || fn.Name() == "Invoke") && fn.Pkg() != nil && fn.Pkg().Path() == "github.com/chucklehead-dev/gotogether/qobs"
}

// checkScope reports the names in a's expressions that are not in the
// function's scope (parameters, receiver, results) or an enclosing one
// (package, universe), declared before the body: go/types puts the
// body's top-level locals in the parameters' scope, and the advice
// cannot see them.
func checkScope(pass *analysis.Pass, c *ast.Comment, a binding.Annotation) {
	scope := pass.TypesInfo.Scopes[a.Decl.Type]
	if scope == nil || a.Decl.Body == nil {
		return
	}
	keys := slices.Sorted(maps.Keys(a.Keys))
	fixed := map[string]bool{}
	for _, k := range keys {
		if notExpressions[k] {
			continue
		}
		e, err := parser.ParseExpr(a.Keys[k])
		if err != nil {
			pass.Reportf(a.At, "//quint:action %s: %s:%s is not a Go expression: %v (quote a value with spaces: %s:\"f(a, b)\")", a.Action, k, a.Keys[k], err, k)
			continue
		}
		for _, name := range freeNames(e) {
			if _, obj := scope.LookupParent(name, a.Decl.Body.Lbrace); obj == nil {
				var fixes []analysis.SuggestedFix
				if !fixed[name] { // one fix per name: two keys using one local name it once
					fixes = scopeFix(pass, c, a, k, name)
					fixed[name] = fixes != nil
				}
				pass.Report(analysis.Diagnostic{Pos: a.At, SuggestedFixes: fixes,
					Message: fmt.Sprintf("//quint:action %s: %s uses %s, which is not a parameter, the receiver or a named result of %s: the step is recorded on return, where locals are out of scope; name the result or pass it in", a.Action, k, name, a.Func)})
			}
		}
	}
}

// freeNames are the identifiers e refers to by name: not selected fields
// or methods, not composite literal keys, not "_".
func freeNames(e ast.Expr) []string {
	seen := map[string]bool{}
	var out []string
	skip := map[*ast.Ident]bool{}
	ast.Inspect(e, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			skip[x.Sel] = true
		case *ast.KeyValueExpr:
			if id, ok := x.Key.(*ast.Ident); ok {
				skip[id] = true
			}
		case *ast.FuncLit:
			return false // its own scope
		case *ast.Ident:
			if !skip[x] && x.Name != "_" && !seen[x.Name] {
				seen[x.Name] = true
				out = append(out, x.Name)
			}
		}
		return true
	})
	return out
}

type loaded struct {
	b   *binding.Binding
	m   *binding.Model
	err error
}

var (
	bindingsMu sync.Mutex
	bindings   = map[string]*loaded{}
)

// loadBinding loads a binding and its model once per process: the
// analyzer runs per package, and loading the model runs quint.
func loadBinding(path, quint string) (*binding.Binding, *binding.Model, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	bindingsMu.Lock()
	defer bindingsMu.Unlock()
	if l, ok := bindings[abs]; ok {
		return l.b, l.m, l.err
	}
	l := &loaded{}
	if l.b, l.err = binding.Load(abs); l.err == nil {
		l.m, l.err = binding.LoadModel(quint, l.b.SpecPath(), l.b.Module)
	}
	if l.err != nil {
		l.err = fmt.Errorf("quintaction: -binding %s: %w", path, l.err)
	}
	bindings[abs] = l
	return l.b, l.m, l.err
}

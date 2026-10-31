package binding

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/nearest"
	"github.com/chucklehead-dev/gotogether/internal/quintnames"
)

// qobsPath is the import path of the recording package.
const qobsPath = "github.com/chucklehead-dev/gotogether/qobs"

// RecordCall is an explicit qobs.Record or Recorder.Record call (or their
// Invoke) whose action is a string literal, found in Go source.
type RecordCall struct {
	// Path, Line and Column are captured directly from the source file set.
	Path         string
	Line, Column int
	Pos          string        // file:line
	At           token.Pos     // Pos in the file set the file was parsed with
	Call         *ast.CallExpr // the call
	Func         string        // the enclosing function ("" at package level)
	Action       string
	// Method is set for x.Record(...) on a value, taken for a
	// *qobs.Recorder by its name and arguments alone (no types): a caller
	// with types confirms it.
	Method bool
}

// ScanRecords finds the literal qobs.Record actions in the Go files under
// dir, tests included and testdata (fixtures, wrong on purpose) skipped.
func ScanRecords(dir string) ([]RecordCall, error) {
	var out []RecordCall
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "vendor" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) && path != dir {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		out = append(out, RecordsOf(fset, f)...)
		return nil
	})
	return out, err
}

// RecordsOf are the calls in one parsed file that record a step with a
// literal action: qobs.Record(ctx, "a", err, ...), and x.Record(ctx, "a",
// err, ...) on any value (a *qobs.Recorder). Only files that import qobs
// are scanned, and an action that is not a literal (a constant, a
// variable) is not seen.
func RecordsOf(fset *token.FileSet, f *ast.File) []RecordCall {
	// The file's package names: qtest.Record(t, "b.yaml", ...) is another
	// package's Record, not a recorder's.
	pkg, imported := "", map[string]bool{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		name := importName(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		imported[name] = true
		if p == qobsPath {
			pkg = name
		}
	}
	if pkg == "" || pkg == "_" {
		return nil
	}
	var out []RecordCall
	scan := func(fn string, n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// Record(ctx, action, err, kv...) or Invoke(ctx, action, kv...)
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !(sel.Sel.Name == "Record" && len(call.Args) >= 3 || sel.Sel.Name == "Invoke" && len(call.Args) >= 2) {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			action, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			id, isIdent := sel.X.(*ast.Ident)
			if isIdent && id.Name != pkg && imported[id.Name] {
				return true
			}
			pos := fset.Position(lit.Pos())
			out = append(out, RecordCall{
				Path: pos.Filename, Line: pos.Line, Column: pos.Column, Pos: pos.String(), At: lit.Pos(), Call: call,
				Func: fn, Action: action, Method: !isIdent || id.Name != pkg,
			})
			return true
		})
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			scan(fn.Name.Name, fn)
		} else {
			scan("", d)
		}
	}
	return out
}

// importName is the name a package is usually declared with: its path's
// last element, without a major version (yaml.v3, /v2) or a go- prefix.
func importName(path string) string {
	elems := strings.Split(path, "/")
	name := elems[len(elems)-1]
	if len(elems) > 1 && len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
		name = elems[len(elems)-2]
	}
	if i := strings.Index(name, ".v"); i > 0 {
		name = name[:i]
	}
	return strings.TrimPrefix(name, "go-")
}

// LintRecords checks literal Record actions against the model: each names
// one of its actions.
func LintRecords(m *Model, recs []RecordCall) []string {
	return lintMessages(lintRecordFindings(m, recs))
}

func lintRecordFindings(m *Model, recs []RecordCall) []LintFinding {
	var errs []LintFinding
	for _, r := range recs {
		if _, ok := m.Actions[r.Action]; ok {
			continue
		}
		in := ""
		if r.Func != "" {
			in = r.Func + ": "
		}
		errs = append(errs, sourceFinding("source.action.unknown", "error", fmt.Sprintf("%sRecord(ctx, %q, ...): %s", in, r.Action, m.notAnAction(r.Action)), r.Path, r.Line, r.Column, r.Action, "Use an action declared by the model.", r.Pos))
	}
	return errs
}

// recordedFields are the literal field names a Record (or Invoke) call
// records, observations (obs.*) left out: its key-value arguments' keys.
func recordedFields(r RecordCall) []string {
	if r.Call == nil {
		return nil
	}
	first := 3 // Record(ctx, action, err, kv...)
	if sel, ok := r.Call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Invoke" {
		first = 2 // Invoke(ctx, action, kv...)
	}
	var out []string
	for i := first; i < len(r.Call.Args); i += 2 {
		lit, ok := r.Call.Args[i].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		if k, err := strconv.Unquote(lit.Value); err == nil && !strings.HasPrefix(k, "obs.") {
			out = append(out, k)
		}
	}
	return out
}

// lintReservedFields finds the steps that record a field under a name
// Quint keeps for itself (item, to) whose model's parameter is renamed
// (item_) and not mapped: the parameter would read a field item_ no
// step records. The fix is the mapping.
func (b *Binding) lintReservedFields(m *Model, recs []RecordCall) []string {
	return lintMessages(b.lintReservedFindings(m, recs))
}

func (b *Binding) lintReservedFindings(m *Model, recs []RecordCall) []LintFinding {
	var errs []LintFinding
	for _, r := range recs {
		ps, ok := m.Actions[r.Action]
		if !ok {
			continue
		}
		for _, f := range recordedFields(r) {
			if !quintnames.Reserved(f) {
				continue
			}
			p := quintnames.Rename(f)
			if !slices.ContainsFunc(ps, func(q Param) bool { return q.Name == p }) || b.Actions[r.Action].Args[p] != "" {
				continue
			}
			errs = append(errs, sourceFinding("source.parameter.reserved", "error", fmt.Sprintf("Record(ctx, %q, ...) records the field %s, a Quint built-in's name, which the model's parameter cannot take: its %s reads a field %s by default, which no step records: map it in the binding (actions: %s: args: %s: '{{.%s}}')",
				r.Action, f, p, p, r.Action, p, f), r.Path, r.Line, r.Column, r.Action, "Map the renamed Quint parameter to the recorded field in the binding.", r.Pos))
		}
	}
	return errs
}

// LintSource checks annotations (LintAnnotations) and literal Record
// actions (LintRecords) together, so a bound action that one of them
// emits is not reported as emitted by neither.
func (b *Binding) LintSource(m *Model, anns []Annotation, recs []RecordCall) (errs, warns []string) {
	return splitLintMessages(LintSourceFindings([]Bound{{Binding: b, Model: m}}, anns, recs))
}

// A Bound is a binding with its model, one of the several a source tree
// records for (LintSources).
type Bound struct {
	Binding *Binding
	Model   *Model
}

// LintSources is LintSource for a source tree that records for several
// bindings (a system checked against two models: its leadership and its
// clients' history): an annotation or a literal Record is checked
// against the bindings whose models have its action, and is an error
// only when none has it; a bound action is warned of when nothing in the
// tree emits it. With one binding it is LintSource.
func LintSources(bs []Bound, anns []Annotation, recs []RecordCall) (errs, warns []string) {
	return splitLintMessages(LintSourceFindings(bs, anns, recs))
}

// LintSourceFindings checks all source sites against the union of bound models.
// Source locations come from scanner positions; no message text is parsed.
func LintSourceFindings(bs []Bound, anns []Annotation, recs []RecordCall) []LintFinding {
	var out []LintFinding
	if len(bs) == 0 {
		return []LintFinding{{Code: "source.binding.missing", Severity: "error", Message: "source checks require at least one binding", Fix: "Supply a binding and its model for source checks.", Confidence: "model_metadata"}}
	}
	owners := func(action string) []Bound {
		var os []Bound
		for _, b := range bs {
			if _, ok := b.Model.Actions[action]; ok {
				os = append(os, b)
			}
		}
		return os
	}
	none := func(action string) string {
		if len(bs) == 1 {
			return bs[0].Model.notAnAction(action)
		}
		var mods []string
		for _, b := range bs {
			mods = append(mods, b.Model.Module)
		}
		return fmt.Sprintf("%s is an action of none of %s", action, strings.Join(mods, ", "))
	}
	emitted := map[string]bool{}
	for _, a := range anns {
		emitted[a.Action] = true
		os := owners(a.Action)
		if a.Action != "" && len(os) == 0 {
			out = append(out, sourceFinding("source.action.unknown", "error", fmt.Sprintf("%s: %s", a.Func, none(a.Action)), a.Path, a.Line, a.Column, a.Action, "Use an action declared by one of the bound models.", a.Pos))
			continue
		}
		if len(os) == 0 {
			os = bs[:1]
		}
		for _, o := range os {
			fs, _ := o.Binding.lintAnnotationFindings(o.Model, []Annotation{a})
			out = append(out, fs...)
		}
	}
	for _, r := range recs {
		emitted[r.Action] = true
		os := owners(r.Action)
		for _, o := range os {
			out = append(out, o.Binding.lintReservedFindings(o.Model, []RecordCall{r})...)
		}
		if len(os) == 0 {
			in := ""
			if r.Func != "" {
				in = r.Func + ": "
			}
			out = append(out, sourceFinding("source.action.unknown", "error", fmt.Sprintf("%sRecord(ctx, %q, ...): %s", in, r.Action, none(r.Action)), r.Path, r.Line, r.Column, r.Action, "Use an action declared by one of the bound models.", r.Pos))
		}
	}
	for _, b := range bs {
		for _, name := range slices.Sorted(maps.Keys(b.Binding.Actions)) {
			if emitted[name] {
				continue
			}
			msg := fmt.Sprintf("action %s is bound but neither a //quint:action nor a literal qobs.Record emits it (an action recorded through a constant or a variable is not seen)", name)
			if len(bs) > 1 {
				msg = b.Model.Module + ": " + msg
			}
			out = append(out, LintFinding{Code: "source.action.unemitted", Severity: "warning", Message: msg, Symbol: name, Fix: "Add a recording site, remove the unused binding, or verify a dynamic action name manually.", Confidence: "syntax"})
		}
	}
	return out
}

// stepActions are m's actions a step may name: not init or step (the
// state machine), not a helper (part of another).
func (m *Model) stepActions() []string {
	var names []string
	for _, a := range m.ActionNames() {
		if a == "init" || a == "step" || m.Helpers[a] {
			continue
		}
		names = append(names, a)
	}
	return names
}

// NearestAction is the action of m a step may name that is closest to
// name, a likely misspelling of it ("" when none is near): the one a
// lint's "did you mean" names, and an analyzer's fix writes.
func (m *Model) NearestAction(name string) string {
	return nearest.Name(name, m.stepActions())
}

// notAnAction says that name is not an action of m, with the closest
// action if one is near, and the list.
func (m *Model) notAnAction(name string) string {
	msg := fmt.Sprintf("%s is not an action of %s", name, m.Module)
	if best := m.NearestAction(name); best != "" {
		msg += fmt.Sprintf(" (did you mean %s?)", best)
	}
	return msg + "; its actions: " + strings.Join(m.stepActions(), ", ")
}

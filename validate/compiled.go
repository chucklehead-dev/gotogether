package validate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/formats"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A compiled model (gotogether compile; validate -compiled) is what the
// Go evaluator needs to judge a binding's runs with no quint and no Node
// at validate time: quint's typecheck of the model (the binding's plan
// and lint read it) and quint's JSON IR of the binding's evaluation
// module (binding.EvalModule), which the evaluator reads, with what it
// was compiled from. Its rules (DESIGN §64):
//   - the evaluator alone judges: a pass is its pass and a failure its
//     failure, with no sample of its passes checked by quint (there is
//     none to ask), and every result says so (JudgedBy "evaluator", an
//     info issue naming the artifact);
//   - a run the evaluator cannot decide (qeval.Unsupported: a fold whose
//     result depends on quint's order, a choice inside a bound action, a
//     non-literal argument) is no verdict, "needs quint", never guessed;
//     so is a run that needs quint's replay (hidden actions, a resume);
//   - the artifact is refused when stale: another binding than the one
//     it was compiled from, a source of the model present here whose hash
//     is not the artifact's, an evaluation module this gotogether writes
//     otherwise (the binding's meaning changed), a quint older than the
//     toolkit reads, or a later format than this gotogether reads.
//
// A source that is absent (a canary's container carries the binding and
// the artifact, not the model) is not checked: the report says so.

// CompiledFormat is the format Compile writes and LoadCompiled reads.
const CompiledFormat = "gotogether.model/1"

// minQuint is the oldest quint whose IR the evaluator reads (AGENTS.md:
// quint 0.32 or later). Read-only.
var minQuint = [3]int{0, 32, 0}

// compiledEvalDir is where, beside the model's main file, the
// evaluation module is written for the import its text names: one
// directory down, so the import is "../<spec>" wherever the model is.
const compiledEvalDir = ".gotogether-compiled"

// A CompiledModel is a binding's model compiled for the Go evaluator
// (Compile), as its file holds it.
type CompiledModel struct {
	// Format is CompiledFormat.
	Format string `json:"format"`
	// Quint is `quint --version` of the quint that compiled it.
	Quint string `json:"quint"`
	// Gotogether is the core version that compiled it, as doctor reports
	// it ("" when the build does not say).
	Gotogether string `json:"gotogether,omitempty"`
	// Binding is the binding file it was compiled from, its path
	// relative to the artifact's directory.
	Binding File `json:"binding"`
	// Sources are the model's files (its main file, then its imports),
	// each path relative to the binding's directory.
	Sources []File `json:"sources"`
	// Module is the binding's module.
	Module string `json:"module"`
	// Typecheck is quint's typecheck JSON of the model.
	Typecheck json.RawMessage `json:"typecheck"`
	// Eval is the binding's evaluation module, IR quint's compile of it.
	Eval *binding.EvalModule `json:"eval"`
	IR   json.RawMessage     `json:"ir"`
}

// Compile compiles bindingPath's model for the Go evaluator with the
// quint of opt.Quint: its typecheck and its evaluation module's IR, with
// the binding's and the sources' hashes. Write the result with Write.
// It needs quint; LoadCompiled and NewCompiledChecker do not.
func Compile(bindingPath string, opt Options) (*CompiledModel, error) {
	b, err := binding.Load(bindingPath)
	if err != nil {
		return nil, err
	}
	if err := b.CheckSpec(); err != nil {
		return nil, err
	}
	quint := opt.Quint
	if quint == "" {
		quint = "quint"
	}
	qv := quintVersion(quint)
	if qv == "" {
		return nil, fmt.Errorf("compile: %s --version failed: compiling needs quint (npm i -g @informalsystems/quint; gotogether doctor says what is installed)", quint)
	}
	spec := b.SpecPath()
	tmp, err := os.MkdirTemp("", "gotogether-compile-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	tcPath := filepath.Join(tmp, "typecheck.json")
	var stderr bytes.Buffer
	cmd := quintcmd.Command(context.Background(), quint, filepath.Dir(spec), "typecheck", filepath.Base(spec), "--out", tcPath)
	cmd.Stdout, cmd.Stderr = &stderr, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("compile: quint typecheck %s: %w: %s", spec, err, strings.TrimSpace(stderr.String()))
	}
	tc, err := os.ReadFile(tcPath)
	if err != nil {
		return nil, err
	}
	m, err := binding.ParseTypecheck(tc, b.Module)
	if err != nil {
		return nil, err
	}
	if errs := b.Lint(m); len(errs) > 0 {
		return nil, fmt.Errorf("binding %s does not fit %s: %v", bindingPath, b.Module, errs)
	}
	// the module, written where its import names the model (one directory
	// below the spec, as NewCompiledChecker writes it again), under a name
	// of this process's own
	dir, err := os.MkdirTemp(filepath.Dir(spec), compiledEvalDir+"-")
	if err != nil {
		return nil, fmt.Errorf("compile: the evaluation module is written beside the model, for its import: %w", err)
	}
	defer os.RemoveAll(dir)
	em, err := b.EvalModule(m, filepath.Join(dir, "eval.qnt"))
	if err != nil {
		return nil, fmt.Errorf("compile: the binding has no evaluation module, so the Go evaluator cannot judge its runs (%v): validate them with quint", err)
	}
	qnt := filepath.Join(dir, "eval.qnt")
	if err := os.WriteFile(qnt, []byte(em.Text), 0o644); err != nil {
		return nil, err
	}
	// its stdout to a file, as compileEval's: a pipe loses what quint has
	// not flushed when it exits
	irf, err := os.Create(filepath.Join(tmp, "ir.json"))
	if err != nil {
		return nil, err
	}
	var cerr bytes.Buffer
	cmd = quintcmd.Command(context.Background(), quint, dir, "compile", "--target", "json", "--main", em.Name, "--init", em.Init, "--step", em.Init, qnt)
	cmd.Stdout, cmd.Stderr = irf, &cerr
	err = cmd.Run()
	irf.Close()
	if err != nil {
		return nil, fmt.Errorf("compile: quint compile of the evaluation module: %w: %s", err, strings.TrimSpace(cerr.String()))
	}
	ir, err := os.ReadFile(irf.Name())
	if err != nil {
		return nil, err
	}
	if _, err := qeval.Load(ir); err != nil {
		return nil, fmt.Errorf("compile: the Go evaluator cannot read quint's IR: %w", err)
	}
	bf, err := hashed(bindingPath)
	if err != nil {
		return nil, err
	}
	sources, err := relSources(spec, b.Dir)
	if err != nil {
		return nil, err
	}
	return &CompiledModel{Format: CompiledFormat, Quint: qv, Gotogether: coreVersion(), Binding: *bf, Sources: sources, Module: b.Module,
		Typecheck: tc, Eval: em, IR: json.RawMessage(bytes.TrimSpace(ir))}, nil
}

// relSources are spec's files (specFiles), each path relative to dir.
func relSources(spec, dir string) ([]File, error) {
	f, imports, err := specFiles(spec)
	if err != nil {
		return nil, err
	}
	var out []File
	for _, s := range append([]File{*f}, imports...) {
		rel, err := filepath.Rel(dir, s.Path)
		if err != nil {
			return nil, err
		}
		out = append(out, File{Path: filepath.ToSlash(rel), SHA256: s.SHA256})
	}
	return out, nil
}

// Write writes the compiled model to path, its binding's path relative
// to path's directory.
func (cm *CompiledModel) Write(path string) error {
	out := *cm
	if abs, err := filepath.Abs(path); err == nil && filepath.IsAbs(cm.Binding.Path) {
		if rel, err := filepath.Rel(filepath.Dir(abs), cm.Binding.Path); err == nil {
			out.Binding.Path = filepath.ToSlash(rel)
		}
	}
	data, err := json.Marshal(&out)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// LoadCompiled reads a compiled model (Compile's), refusing another
// format or a later major than this gotogether reads, and a quint older
// than the evaluator reads.
func LoadCompiled(path string) (*CompiledModel, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cm CompiledModel
	if err := json.Unmarshal(data, &cm); err != nil {
		return nil, fmt.Errorf("compiled model %s: %w: compile it again (gotogether compile -binding B -o %s)", path, err, path)
	}
	if _, err := formats.Model.Check(cm.Format); err != nil {
		return nil, fmt.Errorf("compiled model %s: %w", path, err)
	}
	if !QuintSupported(cm.Quint) {
		return nil, fmt.Errorf("compiled model %s: compiled with quint %q, older than the %d.%d the Go evaluator reads: compile it again with a newer quint (gotogether compile)", path, cm.Quint, minQuint[0], minQuint[1])
	}
	if cm.Eval == nil || len(cm.IR) == 0 || len(cm.Typecheck) == 0 {
		return nil, fmt.Errorf("compiled model %s holds no model (no typecheck, evaluation module or IR): compile it again (gotogether compile)", path)
	}
	return &cm, nil
}

// QuintSupported reports whether quint version v (`quint --version`'s)
// is one gotogether reads: 0.32 or later.
func QuintSupported(v string) bool { return quintAtLeast(v, minQuint) }

// CacheDir is the directory of the cache of quint's typechecks and
// compiles across processes, by content ("" when GOTOGETHER_CACHE=off):
// a CI job keeps it between runs (docs/ci.md).
func CacheDir() string { return cacheRoot() }

// quintAtLeast reports whether quint version v ("0.33.0") is min or later.
func quintAtLeast(v string, min [3]int) bool {
	parts := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".", 3)
	if len(parts) < 2 {
		return false
	}
	var got [3]int
	for i, p := range parts {
		p, _, _ = strings.Cut(p, "-")
		n, err := strconv.Atoi(p)
		if err != nil {
			return false
		}
		got[i] = n
	}
	for i := range got {
		if got[i] != min[i] {
			return got[i] > min[i]
		}
	}
	return true
}

// compiledState is a compiled checker's: the artifact, its evaluator,
// and what its loading noted (sources not checked).
type compiledState struct {
	path  string
	model *CompiledModel
	ev    *evaluator
	notes []string
}

// noQuint is a compiled checker's quint: no binary, so a check that
// would ask quint is a tool error naming why, never a verdict.
const noQuint = "gotogether-validate-compiled-runs-no-quint"

// ErrNeedsQuint is why a compiled checker refuses what only quint can
// judge (a search, ITF input).
var ErrNeedsQuint = errors.New("needs quint: a compiled model (validate -compiled) is judged by the Go evaluator alone")

// NewCompiledChecker is a checker of bindingPath's runs by the compiled
// model at compiledPath alone: no quint process, ever. It refuses a stale
// artifact (the rules above CompiledFormat), naming the fix.
func NewCompiledChecker(bindingPath, compiledPath string, opt Options) (*Checker, error) {
	cm, err := LoadCompiled(compiledPath)
	if err != nil {
		return nil, err
	}
	b, err := binding.Load(bindingPath)
	if err != nil {
		return nil, err
	}
	recompile := fmt.Sprintf("compile it again (gotogether compile -binding %s -o %s)", bindingPath, compiledPath)
	bf, err := hashed(bindingPath)
	if err != nil {
		return nil, err
	}
	if bf.SHA256 != cm.Binding.SHA256 {
		return nil, fmt.Errorf("compiled model %s is stale: it was compiled from another binding than %s (sha256 %s, the artifact's %s): %s",
			compiledPath, bindingPath, short12(bf.SHA256), short12(cm.Binding.SHA256), recompile)
	}
	if cm.Module != b.Module {
		return nil, fmt.Errorf("compiled model %s is of module %s, the binding's is %s: %s", compiledPath, cm.Module, b.Module, recompile)
	}
	var notes []string
	for _, s := range cm.Sources {
		p := filepath.Join(b.Dir, filepath.FromSlash(s.Path))
		h, err := hashFile(p)
		switch {
		case errors.Is(err, os.ErrNotExist):
			notes = append(notes, fmt.Sprintf("%s is not here: the compiled model is taken as compiled from it at sha256 %s, unchecked", s.Path, short12(s.SHA256)))
		case err != nil:
			return nil, err
		case h != s.SHA256:
			return nil, fmt.Errorf("compiled model %s is stale: %s changed since it was compiled (sha256 %s, the artifact's %s): %s",
				compiledPath, s.Path, short12(h), short12(s.SHA256), recompile)
		}
	}
	m, err := binding.ParseTypecheck(cm.Typecheck, b.Module)
	if err != nil {
		return nil, fmt.Errorf("compiled model %s: its typecheck: %w: %s", compiledPath, err, recompile)
	}
	if errs := b.Lint(m); len(errs) > 0 {
		return nil, fmt.Errorf("binding %s does not fit %s: %v", bindingPath, b.Module, errs)
	}
	// the evaluation module this gotogether writes for the binding: one
	// another wrote (an older toolkit's, its vals or expects otherwise)
	// is another check
	em, err := b.EvalModule(m, filepath.Join(filepath.Dir(b.SpecPath()), compiledEvalDir, "eval.qnt"))
	if err != nil {
		return nil, fmt.Errorf("compiled model %s: the binding has no evaluation module (%v)", compiledPath, err)
	}
	if em.Text != cm.Eval.Text {
		return nil, fmt.Errorf("compiled model %s is stale: this gotogether (%s) writes the binding's evaluation module otherwise than the one that compiled it (%s): %s",
			compiledPath, orUnknown(coreVersion()), orUnknown(cm.Gotogether), recompile)
	}
	qm, err := qeval.Load(cm.IR)
	if err != nil {
		return nil, fmt.Errorf("compiled model %s: %w: %s", compiledPath, err, recompile)
	}
	opt.Quint = noQuint
	cs := &compiledState{path: compiledPath, model: cm, ev: &evaluator{em: cm.Eval, m: qm}, notes: notes}
	return &Checker{Binding: b, Model: m, Options: opt, scratch: &scratch{}, compiled: cs}, nil
}

func short12(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func orUnknown(v string) string {
	if v == "" {
		return "version unknown"
	}
	return v
}

// Compiled is the compiled model c judges by, nil for a checker that
// asks quint.
func (c *Checker) Compiled() *CompiledModel {
	if c.compiled == nil {
		return nil
	}
	return c.compiled.model
}

// checkCompiled is Check by the compiled model alone: each actor's plan
// judged by the Go evaluator, a run it cannot decide no verdict.
func (c *Checker) checkCompiled(ctx context.Context, steps []qtrace.Step) (*Report, error) {
	traces, issues := reconstruct(c.Binding, steps)
	cs := c.compiled
	msg := fmt.Sprintf("judged by the Go evaluator alone, by the compiled model %s (compiled with quint %s): no quint process, and no sample of its passes judged by quint", cs.path, cs.model.Quint)
	issues = append(issues, qtrace.Issue{Severity: "info", Kind: "compiled", Msg: msg})
	for _, n := range cs.notes {
		issues = append(issues, qtrace.Issue{Severity: "warning", Kind: "compiled", Msg: n})
	}
	rep := &Report{Issues: issues}
	e := cs.ev.replaying()
	for _, t := range traces {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		o, err := c.dirFor(c.withinITF(c.Options))
		if err != nil {
			return nil, err
		}
		if o, err = o.resolve(); err != nil {
			return nil, err
		}
		var r *Result
		if len(c.Binding.Hidden) > 0 || c.resumes(t) {
			r = newResult(c.Binding, t, &binding.Plan{})
			needsQuint(r, "the binding's hidden actions or its resume are searched with quint confirming")
		} else {
			p, err := c.Binding.Plan(c.Model, t)
			if err != nil {
				return rep, fmt.Errorf("actor %s: %w", t.Actor, err)
			}
			r = newResult(c.Binding, t, p)
			if err := c.judgeCompiled(e, t, p, r, o); err != nil {
				return rep, fmt.Errorf("actor %s: %w", t.Actor, err)
			}
			c.explainResult(p, r, o)
			c.judgeWithin(t, r)
		}
		r.byEvaluator, r.compiled = true, true
		judgeLost(c.Binding, r, t.Lost)
		judgeAbsent(c.Binding, r, t.Lost, t.Gaps, nil, nil)
		rep.Actors = append(rep.Actors, r)
	}
	return rep, nil
}

// needsQuint makes r no verdict: the evaluator could not decide it.
func needsQuint(r *Result, why string) {
	r.Conforms, r.Failure, r.noVerdict = NotEvaluated, nil, "needs quint"
	r.Invariants = nil
	r.Issues = append(r.Issues, qtrace.Issue{Severity: "warning", Kind: "compiled",
		Msg: "no verdict: needs quint: the Go evaluator cannot decide this run (" + firstLine(why) + "): validate it without -compiled, with quint"})
}

// judgeCompiled fills r, trace t's plan p's, with the evaluator's
// verdict: a pass as passed fills it; a failure where the evaluator's
// replay found it, in quint's terms (the call rejected and whether it
// was disabled or its expect false, the invariants that fail and after
// which call, an eventual val false at the settle), the model's state
// there the evaluator's; a refusal no verdict.
func (c *Checker) judgeCompiled(e *evaluator, t *qtrace.Trace, p *binding.Plan, r *Result, o Options) error {
	v, err := e.judgeRun(c, t, p, true)
	if err != nil {
		needsQuint(r, err.Error())
		return nil
	}
	if v.pass {
		if !e.passed(c.Binding, r, p, v, o, observedBase(t)) {
			return fmt.Errorf("the evaluator's ITF of a passing run could not be written under %s", o.Dir)
		}
		return nil
	}
	fillPass(c.Binding, r, p) // the steps and calls; the verdicts below
	states, path := c.compiledStates(e, p, v.states, filepath.Join(o.Dir, observedBase(t)+"_eval.itf.json"))
	if v.conforms {
		r.ITF = path // as quint's replay of a conforming run: its bounded responses are judged on it
	}
	callAt := func(i int) (string, *binding.Call) {
		switch {
		case i >= 0:
			return p.Calls[i].Quint, &p.Calls[i]
		case p.Start != nil:
			return p.Start.Quint, p.Start
		}
		return "init", nil
	}
	failure := func(kind string, i int, state int) *Failure {
		q, call := callAt(i)
		f := &Failure{Kind: kind, Index: i, Call: q, Quint: "the Go evaluator (validate -compiled): no quint message"}
		if call != nil {
			f.Expect, f.ExpectTemplate, f.Step, f.Note = call.Expect, call.ExpectTemplate, call.Step, call.Note
		}
		if state >= 0 && state < len(states) {
			f.State, f.StateAfter = states[state], kind != "disabled"
		}
		return f
	}
	if !v.conforms {
		r.Conforms = Fail
		switch {
		case v.at < 0: // the start: init's expect, or the snapshot's
			r.Failure = failure("disabled", -1, -1)
		case len(v.states) > v.at+1: // the step was taken: its expect is false after it
			r.Failure = failure("state", v.at, v.at+1)
		default:
			r.Failure = failure("disabled", v.at, v.at)
		}
		for i := range r.Invariants {
			r.Invariants[i].Verdict, r.Invariants[i].Failure = NotEvaluated, nil
		}
		return nil
	}
	consts, err := e.constants(p)
	if err != nil {
		needsQuint(r, err.Error())
		return nil
	}
	// each invariant's first failing state, as each of quint's
	// invariant runs reports its own; one the evaluator refuses in a
	// state its walk did not reach is no verdict
	for i := range r.Invariants {
		inv := &r.Invariants[i]
		if inv.Eventually {
			ev := strings.TrimPrefix(inv.Name, "eventually ")
			x, err := e.m.Eval(consts, v.states[len(v.states)-1], e.em.Eventually[ev])
			switch {
			case err != nil:
				inv.Verdict, inv.cause = NotEvaluated, "needs quint"
			case x != qeval.Bool(true):
				inv.Verdict, inv.Failure = Fail, failure("invariant", len(p.Calls)-1, len(v.states)-1)
			}
			continue
		}
		for pos, s := range v.states {
			x, err := e.m.Eval(consts, s, e.em.Invariants[inv.Name])
			if err != nil {
				inv.Verdict, inv.cause = NotEvaluated, "needs quint"
				break
			}
			if x != qeval.Bool(true) {
				inv.Verdict, inv.Failure = Fail, failure("invariant", pos-1, pos)
				break
			}
		}
	}
	if r.OK() {
		// the walk failed it, and no check fails on its states: the
		// evaluator contradicts itself, which is no verdict to give
		return fmt.Errorf("the Go evaluator failed the run (%s) and passes every check on its states: an evaluator bug (internal/qeval); validate with quint meanwhile", v)
	}
	return nil
}

// compiledStates are the evaluator's states of plan as a failure shows
// them, written to path as the ITF quint's replay would be and read
// back, so the variables are named as quint's are; path "" when it could
// not be written (the failure is shown without the model's state).
func (c *Checker) compiledStates(e *evaluator, p *binding.Plan, states []qeval.State, path string) ([]map[string]itf.Value, string) {
	if len(states) == 0 || e.writeITF(path, p, states) != nil {
		return nil, ""
	}
	tr, err := itf.ReadFile(path)
	if err != nil {
		return nil, ""
	}
	out := make([]map[string]itf.Value, len(tr.States))
	for i, s := range tr.States {
		out[i] = s.Vars
	}
	return out, path
}

// coreVersion is the core module's version in this build, as doctor
// reports it: the module's version, else its commit; "" when the build
// does not say.
func coreVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	if bi.Main.Path == coreModulePath {
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			return bi.Main.Version
		}
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				return "commit " + short12(s.Value)
			}
		}
		return ""
	}
	for _, d := range bi.Deps {
		if d.Path == coreModulePath {
			if d.Replace != nil {
				return "replaced by " + d.Replace.Path
			}
			return d.Version
		}
	}
	return ""
}

// coreModulePath is the core's module path.
const coreModulePath = "github.com/chucklehead-dev/gotogether"

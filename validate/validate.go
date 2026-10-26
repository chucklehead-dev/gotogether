// Package validate checks reconstructed traces against a Quint model: it
// generates a replay module from a binding (package binding), runs it with
// `quint test`, and turns quint's verdict back into a structured result that
// points at the offending observed step.
package validate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/replay"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Options configures a validation run.
type Options struct {
	Quint   string // quint binary; default "quint"
	Backend string // quint test --backend; default $QUINT_BACKEND, else "rust" (a CheckBatch chunk of several traces: "typescript")
	Dir     string // where generated modules and ITF go; default a temp dir
	// NoITF skips writing the ITF of conforming runs (Result.ITF stays
	// empty). The ITF holds the model's full state at every step, which for
	// a model with a large state is much of the cost of a check.
	NoITF bool
	// VerdictsOnly makes a check report its verdicts and where each failed,
	// nothing more: no ITF and no model state at a failure, so the runs
	// that fail are replayed on the backend the others were (TypeScript for
	// a CheckBatch chunk) rather than on Rust for its ITF. For searches
	// that judge many candidates (Judge's Linearize,
	// ResolvePending) and read only where each failed.
	VerdictsOnly bool
	// Eval is how a search judges its candidates: "go" (the Go evaluator,
	// its conclusions confirmed by quint), "quint" (quint alone) or
	// "shadow" (both, failing on any disagreement); "" is
	// GOTOGETHER_EVAL's, else "go". An option, so tests of each run in
	// parallel.
	Eval string
	// detailBackend, if set, replays the runs that failed (the detail
	// phase): a chunk's TypeScript backend writes no ITF for a failing
	// test, so the model's state at the failure is Rust's to report
	detailBackend string
}

// Verdict of one check.
type Verdict string

const (
	Pass         Verdict = "pass"
	Fail         Verdict = "fail"
	NotEvaluated Verdict = "n/a" // e.g. invariants of a trace that does not conform
)

// Result is the outcome of validating one actor's trace.
type Result struct {
	Actor string
	Steps int // replayed steps, including inferred ones and the snapshot it starts from

	// Conformance: is the observed trace a behaviour of the model, with the
	// observed state projections holding after each step?
	Conforms Verdict
	// On failure: which step, and why. Kind is "disabled" (the model cannot
	// take this step from the state reached: QNT513) or "state" (the step
	// was taken but the observed state projection does not hold: QNT508).
	Failure *Failure

	// Invariants, each evaluated in every state of the replay.
	Invariants []InvariantResult

	// Issues from reconstruction (gaps, cycles, overlaps). A trace with an
	// "error" issue was validated anyway, but a pass means little.
	Issues []qtrace.Issue

	// Pending are the operations invoked (qobs.Invoke) and never completed
	// (qtrace.Trace.Pending): no step of the replay. qtest.Completes judges
	// them; OK does not, since a crashed caller's call is pending by right.
	Pending []qtrace.Step

	// Lost are the lost tails of the recorders of its steps
	// (qtrace.Trace.Lost; a search's, of the history it ordered): the
	// failures that may depend on them are no verdict (NotEvaluated), and
	// its eventual vals were not judged.
	Lost []qtrace.Lost

	// Settled: the trace recorded that its system settled
	// (qtrace.Trace.Settled), so the binding's eventual properties were
	// judged; otherwise they are not among Invariants.
	Settled bool

	// Constants are the instance checked against ("NAME = expr"), printed
	// with a failure: a run that fails at its first step is most often
	// checked against the wrong instance.
	Constants []string

	Module string // the generated .qnt
	ITF    string // ITF of the replayed trace, when it conforms
	Calls  []binding.Call
	Output string // quint's output, verbatim

	// noVerdict says why a check that failed is no verdict, when it is
	// not a search's budget: "lost steps" (judgeLost)
	noVerdict string
}

type Failure struct {
	Note   string // for ITF replays: which state, and what was observed
	Kind   string
	Index  int    // into Calls
	Call   string // the Quint step
	Expect string // the post-state check of the step, if any
	// ExpectTemplate is the binding's expect that rendered Expect
	ExpectTemplate string
	Step           *qtrace.Step
	Quint          string // quint's error message
	// State is the model's state at the failure: before the step for a
	// step the model cannot take, after it for a failed expect or
	// invariant (StateAfter). Variables as quint reports them.
	State      map[string]itf.Value
	StateAfter bool
	// noState says why State is empty, where the report would show it
	noState string
	// window is when a within failure's response was due (its trigger's
	// time and the bound): a response lost before it would have met it
	window time.Time
}

// failures are r's failures: its conformance's and its invariants'.
func (r *Result) failures() []*Failure {
	var out []*Failure
	if r.Failure != nil {
		out = append(out, r.Failure)
	}
	for _, i := range r.Invariants {
		if i.Failure != nil {
			out = append(out, i.Failure)
		}
	}
	return out
}

// InvariantResult is one invariant's, or eventual val's, verdict in a replay.
type InvariantResult struct {
	Name    string
	Verdict Verdict
	Failure *Failure
	// Eventually: an eventual val (binding's Eventually), judged in the
	// last state of a settled trace, named EventuallyName(val)
	Eventually bool
	// Within: a bounded-response obligation (binding's Within), judged on
	// the replay's timed states, named by binding.Within.Name
	Within bool
}

// EventuallyName is how a result names an eventual val (binding's
// Eventually) among its invariants: "eventually <val>". Violates takes it
// so: qtest.Violates(validate.EventuallyName("outboxEmpty")).
func EventuallyName(val string) string { return "eventually " + val }

// OK is true when the trace conforms and every invariant holds, and
// reconstruction reported no errors.
func (r *Result) OK() bool {
	if r.Conforms != Pass {
		return false
	}
	for _, i := range r.Invariants {
		if i.Verdict != Pass {
			return false
		}
	}
	for _, i := range r.Issues {
		if i.Severity == "error" {
			return false
		}
	}
	return true
}

// validateTrace checks one actor's trace.
func validateTrace(ctx context.Context, b *binding.Binding, m *binding.Model, t *qtrace.Trace, opt Options) (*Result, error) {
	opt, err := opt.resolve()
	if err != nil {
		return nil, err
	}
	p, err := b.Plan(m, t)
	if err != nil {
		return nil, err
	}
	return validatePlan(ctx, b, t, p, opt)
}

// validatePlan checks one actor's trace as p plans it.
func validatePlan(ctx context.Context, b *binding.Binding, t *qtrace.Trace, p *binding.Plan, opt Options) (*Result, error) {
	opt, err := opt.resolve()
	if err != nil {
		return nil, err
	}
	base := "observed_" + sanitize(t.Actor)
	res := newResult(b, t, p)
	return res, evaluate(ctx, replay.Spec(b, p), [][]binding.Call{p.Calls}, []*binding.Call{p.Start}, base, opt, []*Result{res})[0]
}

// newResult is a trace's result before its replay. A binding's eventual
// vals are judged only in a trace that recorded a settle last: one that
// did not says so, or a run that never settled (no Settle, a step after
// it, its error dropped) would pass with them unjudged and no word.
func newResult(b *binding.Binding, t *qtrace.Trace, p *binding.Plan) *Result {
	res := &Result{Actor: t.Actor, Issues: append(startIssues(b, t), p.Issues...), Pending: t.Pending, Settled: t.Settled, Constants: p.ConstantList()}
	if len(b.Eventually) > 0 && !t.Settled {
		res.Issues = append(res.Issues, notJudged(b.Eventually, t.Lost))
	}
	return res
}

func (opt Options) resolve() (Options, error) {
	if opt.Quint == "" {
		opt.Quint = "quint"
	}
	if opt.Backend == "" {
		opt.Backend = os.Getenv("QUINT_BACKEND")
	}
	if opt.Backend == "" {
		opt.Backend = "rust"
	}
	if opt.Dir == "" {
		d, err := os.MkdirTemp("", "gotogether-")
		if err != nil {
			return opt, err
		}
		opt.Dir = d
	}
	if err := os.MkdirAll(opt.Dir, 0o755); err != nil {
		return opt, err
	}
	if abs, err := filepath.Abs(opt.Dir); err == nil {
		opt.Dir = abs
	}
	return opt, nil
}

// evaluate replays runs of one instance (one run, or a batch) and fills
// their results, in at most two quint processes. The first replays each
// run once with every check fused (replay.FastTests): it decides every
// run that passes. Only the runs that fail it are replayed again, with a
// conformance run and a run per invariant (replay.DetailTests), which say
// which check failed and where. Modules go to <Dir>/<base>.qnt and
// <base>_detail.qnt. starts are where each run starts (nil: init).
func evaluate(ctx context.Context, ms replay.ModuleSpec, runs [][]binding.Call, starts []*binding.Call, base string, opt Options, res []*Result) []error {
	errs := make([]error, len(runs))
	fail := func(idx []int, err error) {
		for _, i := range idx {
			errs[i] = err
		}
	}
	all := make([]int, len(runs))
	for i := range all {
		all[i] = i
	}
	// phase writes the module of the given runs and test set, and runs it.
	phase := func(set replay.TestSet, file string, idx []int, opt Options) ([]*replay.Generated, string, string, error) {
		m := ms
		m.Tests = set
		qnt := filepath.Join(opt.Dir, file+".qnt")
		sub := make([][]binding.Call, len(idx))
		m.Start, m.Starts = nil, make([]*binding.Call, len(idx))
		for j, i := range idx {
			sub[j], m.Starts[j] = runs[i], starts[i]
		}
		gs, err := replay.RenderBatch(m, sub, qnt)
		if err != nil {
			return nil, "", "", err
		}
		if err := os.WriteFile(qnt, []byte(gs[0].Text), 0o644); err != nil {
			return nil, "", "", err
		}
		var names []string
		for _, g := range gs {
			if g.Tests.All != "" {
				names = append(names, g.Tests.All)
			}
			if g.Tests.Conforms != "" {
				names = append(names, g.Tests.Conforms)
			}
			for _, n := range g.Tests.Invariants {
				names = append(names, n)
			}
			for _, n := range g.Tests.Eventually {
				names = append(names, n)
			}
		}
		out, err := quintTest(ctx, qnt, gs[0].Module, file, opt, names)
		return gs, qnt, out, err
	}
	detailed := all
	if len(ms.Invariants) > 0 {
		gs, qnt, out, err := phase(replay.FastTests, base, all, opt)
		if err != nil {
			fail(all, err)
			return errs
		}
		verdicts, _ := parseQuintTest(out, qnt, gs[0])
		detailed = nil
		for i, g := range gs {
			evFailed := false
			for _, n := range g.Tests.Eventually {
				evFailed = evFailed || res[i].Settled && verdicts[n] != Pass
			}
			if verdicts[g.Tests.All] != Pass || evFailed {
				detailed = append(detailed, i)
				continue
			}
			r := res[i]
			r.Steps, r.Module, r.Calls, r.Output, r.Conforms = g.Steps(), qnt, g.Calls, out, Pass
			r.Invariants = nil
			for _, inv := range ms.Invariants {
				r.Invariants = append(r.Invariants, InvariantResult{Name: inv, Verdict: Pass})
			}
			for _, ev := range ms.Eventually {
				if r.Settled {
					r.Invariants = append(r.Invariants, InvariantResult{Name: EventuallyName(ev), Verdict: Pass, Eventually: true})
				}
			}
			if !opt.NoITF {
				itf := filepath.Join(opt.Dir, base+"_"+g.Tests.All+".itf.json")
				if err := annotateITF(itf, g); err == nil {
					r.ITF = itf
				}
			}
		}
		if len(detailed) == 0 {
			return errs
		}
	}
	file := base
	if len(ms.Invariants) > 0 {
		file = base + "_detail"
	}
	// The detail run writes ITF regardless of NoITF: failures are rare, and
	// the model's state at the failure goes into the report.
	withITF := opt
	if !opt.VerdictsOnly {
		withITF.NoITF = false
		if opt.detailBackend != "" {
			withITF.Backend = opt.detailBackend
		}
	}
	gs, qnt, out, err := phase(replay.DetailTests, file, detailed, withITF)
	if err != nil {
		fail(detailed, err)
		return errs
	}
	pattern := filepath.Join(opt.Dir, file+"_{test}.itf.json")
	for j, i := range detailed {
		errs[i] = fill(gs[j], out, qnt, pattern, ms.Invariants, ms.Eventually, res[i])
		if withITF.Backend == "typescript" { // its failing tests write no ITF: say so where the state would be
			for _, f := range res[i].failures() {
				if f.State == nil {
					f.noState = "not reported: the TypeScript backend writes no ITF for a failing test (QUINT_BACKEND=rust shows it)"
				}
			}
		}
	}
	return errs
}

// quintTest runs the named tests of a written module with `quint test`,
// returning quint's output. ITF goes to <Dir>/<base>_<test>.itf.json,
// unless opt.NoITF.
func quintTest(ctx context.Context, qnt, module, base string, opt Options, tests []string) (string, error) {
	args := []string{"test", filepath.Base(qnt), "--main", module, "--backend", opt.Backend, "--max-samples", "1",
		"--match", "^(" + strings.Join(tests, "|") + ")$"}
	if !opt.NoITF {
		args = append(args, "--out-itf", filepath.Join(opt.Dir, base+"_{test}.itf.json"))
	}
	cmd := exec.CommandContext(ctx, opt.Quint, args...)
	cmd.Dir = opt.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return string(out), fmt.Errorf("quint test: %w", err)
		}
	}
	return string(out), nil
}

// fill turns quint's output into the verdict on one generated run.
func fill(g *replay.Generated, output, qnt, itfPattern string, invariants, eventually []string, res *Result) error {
	res.Steps, res.Module, res.Calls, res.Output = g.Steps(), qnt, g.Calls, output
	verdicts, failures := parseQuintTest(res.Output, qnt, g)
	if _, ok := verdicts[g.Tests.Conforms]; !ok {
		return fmt.Errorf("quint test did not report %s:\n%s", g.Tests.Conforms, res.Output)
	}
	res.Conforms = verdicts[g.Tests.Conforms]
	res.Failure = failures[g.Tests.Conforms]
	// The model's state where each check failed, from the ITF quint writes
	// for a failing run up to its failure.
	for name, f := range failures {
		if verdicts[name] != Fail {
			continue
		}
		tr, err := itf.ReadFile(strings.ReplaceAll(itfPattern, "{test}", name))
		if err != nil {
			continue
		}
		for i := len(tr.States) - 1; i >= 0; i-- {
			if len(tr.States[i].Vars) > 0 {
				f.State, f.StateAfter = tr.States[i].Vars, f.Kind != "disabled"
				break
			}
		}
	}
	// A failed run with no error position is quint failing, not the model
	// rejecting the trace (e.g. the evaluator crashed): never a verdict.
	names := []string{g.Tests.Conforms}
	for _, n := range g.Tests.Invariants {
		names = append(names, n)
	}
	for _, n := range g.Tests.Eventually {
		names = append(names, n)
	}
	for _, name := range names {
		if verdicts[name] == Fail && failures[name] == nil {
			msg := "no error location in quint's output"
			if m := reToolErr.FindString(res.Output); m != "" {
				msg = strings.TrimSpace(m)
			}
			return &ToolError{Module: qnt, Test: name, Msg: msg, Output: res.Output}
		}
	}
	for _, inv := range invariants {
		name := g.Tests.Invariants[inv]
		ir := InvariantResult{Name: inv, Verdict: verdicts[name], Failure: failures[name]}
		if res.Conforms != Pass {
			ir.Verdict, ir.Failure = NotEvaluated, nil
		}
		res.Invariants = append(res.Invariants, ir)
	}
	for _, ev := range eventually {
		if !res.Settled {
			break // a run cut short has work pending by right
		}
		name := g.Tests.Eventually[ev]
		ir := InvariantResult{Name: EventuallyName(ev), Verdict: verdicts[name], Failure: failures[name], Eventually: true}
		if res.Conforms != Pass {
			ir.Verdict, ir.Failure = NotEvaluated, nil
		}
		res.Invariants = append(res.Invariants, ir)
	}
	if res.Conforms == Pass {
		itf := strings.ReplaceAll(itfPattern, "{test}", g.Tests.Conforms)
		if err := annotateITF(itf, g); err == nil {
			res.ITF = itf
		}
	}
	return nil
}

// ToolError is quint failing to evaluate a replay (not a verdict).
type ToolError struct {
	Module, Test, Msg, Output string
}

func (e *ToolError) Error() string {
	return fmt.Sprintf("quint could not evaluate the replay %s (test %s): %s\n%s", e.Module, e.Test, e.Msg, e.Output)
}

var (
	reOK      = regexp.MustCompile(`(?m)^\s+ok (\S+) passed`)
	reFailed  = regexp.MustCompile(`(?m)^\s+\d+\) (\S+) failed`)
	reToolErr = regexp.MustCompile(`(?m)(error: \[QNT\d+\][^\n]*(\n\s+Error:[^\n]*)?)`)
	reBlock   = regexp.MustCompile(`(?m)^\s+\d+\) (\S+):\s*\n\s+(Error \[QNT\d+\]: [^\n]*)\n\s+at ([^\n]*):(\d+):(\d+)`)
)

func parseQuintTest(out, qnt string, g *replay.Generated) (map[string]Verdict, map[string]*Failure) {
	v := map[string]Verdict{}
	f := map[string]*Failure{}
	for _, m := range reOK.FindAllStringSubmatch(out, -1) {
		v[m[1]] = Pass
	}
	for _, m := range reFailed.FindAllStringSubmatch(out, -1) {
		v[m[1]] = Fail
	}
	for _, m := range reBlock.FindAllStringSubmatch(out, -1) {
		name, msg := m[1], m[2]
		line, _ := strconv.Atoi(m[4])
		fl := &Failure{Quint: msg, Index: -1}
		switch {
		case strings.Contains(msg, "QNT513"), strings.Contains(msg, "Cannot continue to"):
			// the action itself evaluated to false (with or without a
			// following .expect): the model cannot take this step
			fl.Kind = "disabled"
		case strings.Contains(msg, "QNT508"):
			fl.Kind = "state"
			if name != g.Tests.Conforms {
				fl.Kind = "invariant"
			}
		default:
			fl.Kind = "error"
		}
		i, ok := g.LineStep[line]
		atStart := g.Start != nil && g.StartLines[line]
		switch {
		case strings.Contains(msg, "QNT511") && len(g.Calls) > 0:
			// "Test ... returned false", located at the run itself: the
			// last call evaluated to false, so the model cannot take it.
			fl.Kind, i, ok = "disabled", len(g.Calls)-1, true
		case strings.Contains(msg, "QNT511") && g.Start != nil:
			// a run of the snapshot alone, false: the snapshot is no state
			fl.Kind, atStart = "disabled", true
		}
		switch {
		case ok:
			fl.Index = i
			fl.Call = g.Calls[i].Quint
			fl.Expect, fl.ExpectTemplate = g.Calls[i].Expect, g.Calls[i].ExpectTemplate
			fl.Step = g.Calls[i].Step
			fl.Note = g.Calls[i].Note
		case atStart:
			// the snapshot the run starts from: disabled, its expect
			// false, or an invariant false in the state it recorded
			fl.Call, fl.Step = g.Start.Quint, g.Start.Step
			fl.Expect, fl.ExpectTemplate = g.Start.Expect, g.Start.ExpectTemplate
		default:
			fl.Call = "init" // init.expect(...) of an invariant run
		}
		f[name] = fl
	}
	return v, f
}

// annotateITF strips the module prefix from variable names (so tools like
// the model's trace.py read it) and records the action taken per state.
func annotateITF(path string, g *replay.Generated) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var t map[string]any
	if err := json.Unmarshal(b, &t); err != nil {
		return err
	}
	short := func(k string) string {
		if i := strings.LastIndex(k, "::"); i >= 0 {
			return k[i+2:]
		}
		return k
	}
	// a name two variables share (a::x, b::x) keeps its qualifier: stripped,
	// one would overwrite the other, whichever came last
	uses := map[string]int{}
	if vars, ok := t["vars"].([]any); ok {
		for _, v := range vars {
			uses[short(fmt.Sprint(v))]++
		}
	}
	strip := func(k string) string {
		if s := short(k); uses[s] <= 1 {
			return s
		}
		return k
	}
	if vars, ok := t["vars"].([]any); ok {
		for i, v := range vars {
			vars[i] = strip(fmt.Sprint(v))
		}
	}
	states, _ := t["states"].([]any)
	for i, s := range states {
		st, ok := s.(map[string]any)
		if !ok {
			continue
		}
		ns := map[string]any{}
		for k, v := range st {
			ns[strip(k)] = v
		}
		// state 0 is init's, or the snapshot's the run starts from; state
		// i the i-th call's
		var c *binding.Call
		switch {
		case i == 0:
			c = g.Start
		case i-1 < len(g.Calls):
			c = &g.Calls[i-1]
		}
		act := "init"
		ns["mbt::nondetPicks"] = map[string]any{}
		if c != nil {
			act = c.Quint
			if p := strings.IndexByte(act, '('); p >= 0 {
				act = act[:p]
			}
			if c.Step != nil {
				ns["mbt::observedSeq"] = map[string]any{"#bigint": strconv.FormatUint(c.Step.Seq, 10)}
			}
			// The arguments, as `quint run --mbt` records nondet picks, so
			// this trace can drive an implementation (package connect).
			picks := map[string]any{}
			for name, expr := range c.Args {
				if v, err := itf.ParseQuint(expr); err == nil {
					picks[name] = itf.Some(v)
				}
			}
			ns["mbt::nondetPicks"] = picks
		}
		ns["mbt::actionTaken"] = act
		states[i] = ns
	}
	out, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, s)
}

// Reason says why the result is not OK, in the claims' words: "rejects
// apply" (a step the model cannot take), "mismatch after apply" (the
// state after it differs), "violates I", "evaluation error at apply"
// (quint failed on the step), "no verdict (linearize budget)", or "issue"
// (a reconstruction error: a gap, a cycle, an overlap); "" when it is OK.
func (r *Result) Reason() string {
	switch {
	case r.OK():
		return ""
	case r.Conforms == NotEvaluated && r.noVerdict != "":
		return "no verdict (" + r.noVerdict + ")"
	case r.Conforms == NotEvaluated:
		return "no verdict (linearize budget)"
	case r.Conforms != Pass:
		action := "a step"
		if f := r.Failure; f != nil && f.Step != nil {
			action = f.Step.Action
		} else if f != nil && f.Call != "" { // an inferred step (a restart): the model's call
			action, _, _ = strings.Cut(f.Call, "(")
		}
		switch {
		case r.Failure != nil && r.Failure.Kind == "state":
			return "mismatch after " + action
		case r.Failure != nil && r.Failure.Kind == "error":
			return "evaluation error at " + action // quint could not evaluate the step, not a verdict on it
		}
		return "rejects " + action
	}
	for _, inv := range r.Invariants {
		if inv.Verdict == NotEvaluated && r.noVerdict != "" {
			return "no verdict (" + r.noVerdict + ")"
		}
		if inv.Verdict != Pass {
			return "violates " + inv.Name
		}
	}
	return "issue"
}

// Summary renders a result for humans.
func (r *Result) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "actor %s: %d steps\n", r.Actor, r.Steps)
	for _, i := range r.Issues {
		fmt.Fprintf(&b, "  issue: %s\n", i)
	}
	fmt.Fprintf(&b, "  conformance: %s\n", strings.ToUpper(string(r.Conforms)))
	for _, p := range r.Pending {
		fmt.Fprintf(&b, "  invoked, never completed: %s (process %s)\n", p.String(), p.Process)
	}
	if f := r.Failure; f != nil {
		switch {
		case r.Conforms == NotEvaluated && r.noVerdict != "": // a failure after a lost tail (judgeLost)
			fmt.Fprintf(&b, "    no verdict (%s): the order checked, where it failed (an issue above says why that is no verdict):\n", r.noVerdict)
		case r.Conforms == NotEvaluated: // a search cut short (qtest.Linearize): its furthest order, not a verdict
			fmt.Fprintf(&b, "    no verdict: the order checked is the one that got furthest, where it failed:\n")
		}
		fmt.Fprintf(&b, "%s", f.describe("    "))
	}
	for _, i := range r.Invariants {
		v := strings.ToUpper(string(i.Verdict))
		switch {
		case i.Verdict == NotEvaluated && r.Conforms == NotEvaluated:
			v = "not checked: the run's conformance has no verdict"
		case i.Verdict == NotEvaluated && r.noVerdict != "" && i.Failure != nil:
			v = "no verdict (" + r.noVerdict + ")"
		case i.Verdict == NotEvaluated:
			v = "not checked: the run does not conform, so the model's states after the failure are not the run's"
		}
		fmt.Fprintf(&b, "  invariant %s: %s\n", i.Name, v)
		if i.Failure != nil {
			fmt.Fprintf(&b, "%s", i.Failure.describe("    "))
		}
	}
	if !r.OK() && len(r.Constants) > 0 {
		fmt.Fprintf(&b, "  instance: %s\n", strings.Join(r.Constants, ", "))
	}
	fmt.Fprintf(&b, "  module: %s\n", r.Module)
	if r.ITF != "" {
		fmt.Fprintf(&b, "  itf: %s\n", r.ITF)
	}
	return b.String()
}

func (f *Failure) describe(pfx string) string {
	var b strings.Builder
	start := f.Index < 0 && f.Step != nil // the snapshot step the replay starts from
	switch {
	case start && f.Kind == "disabled":
		fmt.Fprintf(&b, "%sthe snapshot step the trace starts from is no state of the model: its action is disabled with the values it recorded\n", pfx)
	case start && f.Kind == "state":
		fmt.Fprintf(&b, "%sin the state the snapshot step recorded, what it observed does not hold\n", pfx)
	case start && f.Kind == "invariant":
		fmt.Fprintf(&b, "%sviolated in the state the snapshot step recorded, where the trace starts\n", pfx)
	default:
		f.describeKind(&b, pfx)
	}
	return f.describeRest(&b, pfx)
}

// describeKind says what kind of failure f is, and where.
func (f *Failure) describeKind(b *strings.Builder, pfx string) {
	switch f.Kind {
	case "disabled":
		fmt.Fprintf(b, "%sstep %d is not a transition of the model from the state reached\n", pfx, f.Index+1)
	case "state":
		fmt.Fprintf(b, "%safter step %d the model's state does not match what was observed\n", pfx, f.Index+1)
	case "invariant":
		if f.Index < 0 {
			fmt.Fprintf(b, "%sviolated in the initial state\n", pfx)
		} else {
			fmt.Fprintf(b, "%sviolated after step %d\n", pfx, f.Index+1)
		}
	case "within":
		// the obligation's story: when it opened, and what came of it
		fmt.Fprintf(b, "%s%s\n", pfx, f.Note)
	default:
		fmt.Fprintf(b, "%sfailed at step %d\n", pfx, f.Index+1)
	}
}

// describeRest is the rest of f's description, after its kind: the call,
// the expect, the step observed, the model's state.
func (f *Failure) describeRest(b *strings.Builder, pfx string) string {
	if f.Call != "" {
		fmt.Fprintf(b, "%s  quint:    %s\n", pfx, f.Call)
	}
	if f.Expect != "" && f.Kind == "state" {
		// as rendered, the observation is in it ("count == 3"): the
		// template says which part is the model's
		fmt.Fprintf(b, "%s  expect:   %s\n", pfx, f.Expect)
		if t := f.ExpectTemplate; t != "" && t != f.Expect {
			fmt.Fprintf(b, "%s  binding:  %s\n", pfx, t)
		}
	}
	if f.Step == nil && f.Note != "" && f.Kind != "within" {
		fmt.Fprintf(b, "%s  observed: %s\n", pfx, f.Note)
	}
	if s := f.Step; s != nil {
		// the step as recorded: its own sequence number, and where it came
		// from (a log line, a span), which differ from the step's number
		// in the run when steps were skipped or interleave other actors'
		where := fmt.Sprintf("seq %d", s.Seq)
		if s.Source != "" {
			where += ", " + s.Source
		}
		fmt.Fprintf(b, "%s  observed: %s (%s)\n", pfx, s.String(), where)
		if len(s.Obs) > 0 {
			fmt.Fprintf(b, "%s  obs:      %s\n", pfx, obsString(s.Obs))
		}
		if s.Error != "" {
			fmt.Fprintf(b, "%s  error:    %s\n", pfx, s.Error)
		}
	}
	if f.Kind == "error" {
		// quint's own message only where the report has no reading of it:
		// a disabled step, a state mismatch and an invariant are said above
		fmt.Fprintf(b, "%s  %s\n", pfx, f.Quint)
	}
	if f.noState != "" && f.Kind != "error" {
		fmt.Fprintf(b, "%s  model state: %s\n", pfx, f.noState)
	}
	if len(f.State) > 0 {
		when := "before the step"
		if f.StateAfter {
			when = "after the step"
		}
		fmt.Fprintf(b, "%s  model state %s:\n", pfx, when)
		names := make([]string, 0, len(f.State))
		for k := range f.State {
			if !strings.HasPrefix(k, "mbt::") {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		for i, k := range names {
			if i == 12 {
				fmt.Fprintf(b, "%s    ... and %d more variables\n", pfx, len(names)-12)
				break
			}
			v := f.State[k].String()
			if len(v) > 240 {
				v = v[:240] + " ..."
			}
			fmt.Fprintf(b, "%s    %s = %s\n", pfx, k, v)
		}
	}
	return b.String()
}

func obsString(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%v", k, m[k])
	}
	return strings.Join(parts, ", ")
}

// HistorySVG draws a run's history (qtrace.HistorySVG): recorded is what
// the run recorded, judged the order this report checked (a search's
// Steps), and the step its first failing actor failed at is drawn red.
func (r *Report) HistorySVG(recorded, judged []qtrace.Step) []byte {
	failedAt := -1
	for _, a := range r.Actors {
		for _, f := range a.failures() {
			if f.Step == nil {
				continue
			}
			for i, s := range judged {
				if s.Recorder == f.Step.Recorder && s.Seq == f.Step.Seq {
					failedAt = i
					break
				}
			}
			if failedAt >= 0 {
				return qtrace.HistorySVG(recorded, judged, failedAt)
			}
		}
	}
	return qtrace.HistorySVG(recorded, judged, failedAt)
}

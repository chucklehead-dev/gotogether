// Package validate checks reconstructed traces against a Quint model: it
// generates a replay module from a binding (package binding), runs it with
// `quint test`, and turns quint's verdict back into a structured result that
// points at the offending observed step.
package validate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/replay"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// Options configures a validation run.
type Options struct {
	// ChoiceLimit bounds expanded states in a finite model-choice search.
	// Zero uses DefaultChoiceLimit; exhaustion gives no verdict.
	ChoiceLimit int
	Quint       string // quint binary; default "quint"
	Backend     string // quint test --backend; default $QUINT_BACKEND, else "rust" (a CheckBatch chunk of several traces: "typescript")
	Dir         string // where generated modules and ITF go; default a temp dir
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
	// EvalSample is the deterministic confirmation policy for Go-evaluator
	// passes. Nil uses GOTOGETHER_EVAL_SAMPLE (default 1/50). Plans using
	// unconfirmed evaluator operations raise a nonzero rate to at least 1/5.
	EvalSample *EvaluationSampling
	// detailBackend, if set, replays the runs that failed (the detail
	// phase): a chunk's TypeScript backend writes no ITF for a failing
	// test, so the model's state at the failure is Rust's to report
	detailBackend string
	// NoEnvBackend makes Backend "" validate's default, not
	// QUINT_BACKEND's: a replay of a run judged on the default
	// (cli.Replay).
	NoEnvBackend bool
}

// envBackend is the backend chosen: Backend, else QUINT_BACKEND's
// (unless a replay ignores it); "" is validate's default.
func (opt Options) envBackend() string {
	if opt.Backend == "" && !opt.NoEnvBackend {
		return os.Getenv("QUINT_BACKEND")
	}
	return opt.Backend
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
	// Choices discloses finite internal-choice search and selected-path confirmation.
	Choices       *ChoiceSearch
	historySearch *receipt.Search
	inputScope    string
	Actor         string
	// Partition is the entity the result judges, "<argument>=<value>"
	// (Actor is "<actor> <argument>=<value>"), when the binding partitions
	// the log by an argument (binding.Partition); "" else.
	Partition string
	Steps     int // replayed steps, including inferred ones and the snapshot it starts from

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

	// Hot is a trace that recorded no settle going round a cycle of the
	// model's states with an eventual val false all the while (Checker.
	// MarkHot: qtest marks a run that did not settle, validate every
	// unsettled trace): evidence, no verdict, so OK ignores it. It says
	// what repeated (a retry storm, a livelock), not that it would
	// forever: progress the model does not keep (a counter that wraps)
	// repeats a state too, and the cycle's fairness is unchecked.
	Hot *HotCycle

	// Gaps are the holes in the seqs of the recorders of its steps
	// (qtrace.Trace.Gaps; a search's, of the history it ordered), and
	// Collected their watermarks (qtrace.Trace.Collected): what Absent's
	// causes are read from.
	Gaps      []qtrace.Gap
	Collected []qtrace.Watermark

	// Absent are the steps the run was expected to show and does not,
	// each with its cause (judgeAbsent): a bounded response not met in
	// time or not judged, an eventual val not judged or false at the
	// settle, a call never completed. Only a Defect's is a FAIL (a within's
	// or an eventual val's among Invariants; a call's, of qtest.Completes
	// and Finished); one Dropped or Uncollected is no verdict.
	Absent []Absence

	// Constants are the instance checked against ("NAME = expr"), printed
	// with a failure: a run that fails at its first step is most often
	// checked against the wrong instance.
	Constants []string

	Module string // the generated .qnt
	ITF    string // ITF of the replayed trace, when it conforms
	Calls  []binding.Call
	Output string // quint's output, verbatim

	// noVerdict says why a check that failed is no verdict, when it is
	// not a search's budget: "lost steps" (judgeLost), "dropped steps"
	// (judgeGaps)
	noVerdict string
	// resumed: replayed from a resume's witness start, whose obligations
	// triggered already (a bounded response's) began in the lost prefix
	resumed bool
	// withins are the bounded responses judgeWithin found not met or not
	// judged, for Absent
	withins []obligation
	// byEvaluator: the Go evaluator passed it, with no quint replay
	// (decide.go); sampled: the evaluator passed it and quint judged it
	// too, a sample of its passes (GOTOGETHER_EVAL_SAMPLE)
	byEvaluator, sampled bool
	// compiled: judged by a compiled model (validate -compiled), the
	// evaluator alone
	compiled bool
	// Unfinished, set by a check that judges the finish (a recorded
	// run's -require-finish, validate/cli), is why a result that
	// passes is no finished run ("incomplete (no settle)", "no verdict
	// (ITF trace)"): Reason gives it when nothing failed, and a run's
	// verdict reads it (a "no verdict" reason has none, any other fails).
	// OK is the checks' alone and does not read it.
	Unfinished string
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
	// Why is why the model cannot take the step, for a step it rejects
	// (Kind "disabled"), by the Go evaluator: an addition to quint's
	// report, never a verdict (explain.go). nil when no evaluator asked.
	Why *Explanation
	// Changes, for a mismatch or an invariant violated after a step, are
	// what the step changed in the model's state, from the state before
	// it to State (the Go evaluator's diff, I20); nil when it cannot say.
	// A rejected step's are its Why's: what the step the model expects
	// instead would change.
	Changes []Change
	// noState says why State is empty, where the report would show it
	noState string
	// window is when a within failure's response was due (its trigger's
	// time and the bound): a response lost before it would have met it
	window time.Time
}

// failures are r's failures: its conformance's and its invariants',
// those that FAIL first (as Reason: an invariant violated before a gap
// is r's failing step, ahead of another's no verdict after it).
func (r *Result) failures() []*Failure {
	var out, none []*Failure
	if r.Failure != nil {
		if r.Conforms == Fail {
			out = append(out, r.Failure)
		} else {
			none = append(none, r.Failure)
		}
	}
	for _, i := range r.Invariants {
		switch {
		case i.Failure != nil && i.Verdict == Fail:
			out = append(out, i.Failure)
		case i.Failure != nil:
			none = append(none, i.Failure)
		}
	}
	return append(out, none...)
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
	// cause says why a failure is no verdict, when it is one (Verdict
	// NotEvaluated, its Failure kept): "lost steps", "dropped steps",
	// "uncollected steps", "another admitted start". Each invariant has
	// its own: one result's may differ (a within downgraded by a gap
	// beside another by an uncollected window).
	cause string
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

// validatePlan checks one actor's trace as p plans it.
func validatePlan(ctx context.Context, b *binding.Binding, t *qtrace.Trace, p *binding.Plan, opt Options) (*Result, error) {
	return validatePlanIn(ctx, b, t, p, opt, newResult(b, t, p), false)
}

// validatePlanIn is validatePlan into res, newResult's, with only the
// detail replay when detail is set (the Go evaluator rejected the plan:
// the fused replay would fail too).
func validatePlanIn(ctx context.Context, b *binding.Binding, t *qtrace.Trace, p *binding.Plan, opt Options, res *Result, detail bool) (*Result, error) {
	opt, err := opt.resolve()
	if err != nil {
		return nil, err
	}
	return res, evaluate(ctx, replay.Spec(b, p), [][]binding.Call{p.Calls}, []*binding.Call{p.Start}, observedBase(t), opt, []*Result{res}, []bool{detail})[0]
}

// observedBase names an actor's replay files: observed_<actor>.
func observedBase(t *qtrace.Trace) string { return "observed_" + sanitize(t.Actor) }

// newResult is a trace's result before its replay. A binding's eventual
// vals are judged only in a trace that recorded a settle last: one that
// did not says so, or a run that never settled (no Settle, a step after
// it, its error dropped) would pass with them unjudged and no word.
func newResult(b *binding.Binding, t *qtrace.Trace, p *binding.Plan) *Result {
	return newResultWith(b, t, p, p.ConstantList())
}

// newResultWith is newResult with the instance's constants listed
// already (a partition's, its whole's).
func newResultWith(b *binding.Binding, t *qtrace.Trace, p *binding.Plan, constants []string) *Result {
	res := &Result{Actor: t.Actor, Issues: append(startIssues(b, t), p.Issues...), Pending: t.Pending, Settled: t.Settled, Constants: constants,
		Gaps: t.Gaps, Collected: t.Collected}
	if len(b.Eventually) > 0 && !t.Settled {
		res.Issues = append(res.Issues, notJudged(b.Eventually, t.Lost))
	}
	return res
}

func (opt Options) resolve() (Options, error) {
	if opt.ChoiceLimit < 0 {
		return opt, fmt.Errorf("choice limit must be nonnegative (0 uses the default)")
	}
	if opt.ChoiceLimit == 0 {
		opt.ChoiceLimit = DefaultChoiceLimit
	}
	if opt.Quint == "" {
		opt.Quint = "quint"
	}
	opt.Backend = opt.envBackend()
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
// <base>_detail.qnt. starts are where each run starts (nil: init). A run
// whose detail is set is replayed in the detail module only: the Go
// evaluator rejected it, so the fused replay would fail it too (nil: none).
func evaluate(ctx context.Context, ms replay.ModuleSpec, runs [][]binding.Call, starts []*binding.Call, base string, opt Options, res []*Result, detail []bool) []error {
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
	var fast []int // the runs the fused replay judges first
	for _, i := range all {
		if i >= len(detail) || !detail[i] {
			fast = append(fast, i)
		}
	}
	if len(ms.Invariants) > 0 && len(fast) > 0 {
		gs, qnt, out, err := phase(replay.FastTests, base, fast, opt)
		if err != nil {
			fail(fast, err)
			return errs
		}
		verdicts, _ := parseQuintTest(out, qnt, gs[0])
		detailed = nil
		for _, i := range all {
			if i < len(detail) && detail[i] {
				detailed = append(detailed, i)
			}
		}
		for j, g := range gs {
			i := fast[j]
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
		slices.Sort(detailed)
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
		errs[i] = fill(gs[j], out, qnt, pattern, ms.Invariants, ms.Eventually, res[i], func(path string) (*itf.Trace, error) {
			return readQuintITF(opt.Quint, ms.Spec, ms.Module, path)
		})
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

// quintSlots bounds the quint test processes of the whole process, not
// of one CheckBatch: process-wide. Parallel tests' explorations each ran
// GOMAXPROCS of them, and a batch module of 64 runs takes quint about a
// gigabyte: fleetrollout's nightly (500 seeds, three explorations at
// once) reached 13 GB of quint and ended its runner. The processes are
// CPU-bound, so more of them at once than CPUs gained nothing.
var quintSlots = make(chan struct{}, runtime.GOMAXPROCS(0))

// quintTest runs the named tests of a written module with `quint test`,
// returning quint's output. ITF goes to <Dir>/<base>_<test>.itf.json,
// unless opt.NoITF.
func quintTest(ctx context.Context, qnt, module, base string, opt Options, tests []string) (string, error) {
	args := []string{"test", filepath.Base(qnt), "--main", module, "--backend", opt.Backend, "--max-samples", "1",
		"--match", "^(" + strings.Join(tests, "|") + ")$"}
	if !opt.NoITF {
		args = append(args, "--out-itf", filepath.Join(opt.Dir, base+"_{test}.itf.json"))
	}
	select {
	case quintSlots <- struct{}{}:
		defer func() { <-quintSlots }()
	case <-ctx.Done():
		return "", fmt.Errorf("quint test: %w", ctx.Err())
	}
	cmd := quintcmd.Command(ctx, opt.Quint, opt.Dir, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil { // killed: its output is no verdict
		return string(out), fmt.Errorf("quint test: %w", ctx.Err())
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return string(out), fmt.Errorf("quint test: %w", err)
		}
	}
	return string(out), nil
}

// fill turns quint's output into the verdict on one generated run.
func fill(g *replay.Generated, output, qnt, itfPattern string, invariants, eventually []string, res *Result, read func(string) (*itf.Trace, error)) error {
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
		tr, err := read(strings.ReplaceAll(itfPattern, "{test}", name))
		if err != nil {
			if _, ok := errors.AsType[*itf.AmbiguousError](err); ok {
				f.noState = "not read: " + err.Error()
			}
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
	checkpoint := false
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
			if c.Checkpoint {
				ns["gotogether::checkpoint"], checkpoint = true, true
			}
			act = c.Quint
			if p := strings.IndexByte(act, '('); p >= 0 {
				act = act[:p]
			}
			if c.Action != "" {
				act = c.Action // (a resume's start is a literal action, named by the resume)
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
	if checkpoint {
		vars, _ := t["vars"].([]any)
		t["vars"] = append(vars, "gotogether::checkpoint")
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
// (quint failed on the step), "no verdict (linearize budget)", "no
// verdict (dropped steps)" (a failure after a gap of the actor's
// recorders, which a dropped step may explain, or recorded steps that
// pass beside one: only a failure before every gap is a FAIL), or "issue"
// (another reconstruction error: a cycle, an overlap); "" when it is OK.
// A failure anywhere wins over no verdict: an invariant violated before
// every gap or cut is "violates I" though another check has none.
func (r *Result) Reason() string {
	switch {
	case r.OK():
		return r.Unfinished
	case r.Conforms == Fail:
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
	// a failure anywhere is a FAIL, whatever else has no verdict: an
	// invariant violated before every gap is a defect, though another's
	// violation after one is no verdict
	for _, inv := range r.Invariants {
		if inv.Verdict == Fail {
			return "violates " + inv.Name
		}
	}
	switch {
	case r.Conforms == NotEvaluated && r.noVerdict != "":
		return "no verdict (" + r.noVerdict + ")"
	case r.Conforms == NotEvaluated:
		return "no verdict (linearize budget)"
	}
	for _, inv := range r.Invariants {
		switch {
		case inv.Verdict == NotEvaluated && inv.cause != "":
			return "no verdict (" + inv.cause + ")"
		case inv.Verdict == NotEvaluated && r.noVerdict != "":
			return "no verdict (" + r.noVerdict + ")"
		case inv.Verdict != Pass:
			return "violates " + inv.Name
		}
	}
	for _, i := range r.Issues {
		if i.Severity == "error" && i.Kind != "gap" {
			return "issue"
		}
	}
	if len(r.Gaps) > 0 { // the recorded steps pass, and the dropped ones are unknown
		return "no verdict (dropped steps)"
	}
	return "issue"
}

// JudgedBy says who gave r's verdict: "quint", "evaluator" (the Go
// evaluator passed it, with no quint replay) or "both" (it passed, and
// quint judged it too: a sample of its passes, GOTOGETHER_EVAL_SAMPLE;
// a disagreement is an error issue of r's, its verdict quint's).
func (r *Result) JudgedBy() string {
	switch {
	case r.byEvaluator:
		return "evaluator"
	case r.sampled:
		return "both"
	}
	return "quint"
}

// Summary renders a result for humans. A failing record is named once,
// by RecordID, with its wall time and recorder (I20); VerboseSummary
// adds its other indices and the model's whole state.
func (r *Result) Summary() string { return r.summary(false) }

// VerboseSummary is Summary with each failing record's place in the
// replay and its recorder's seq, every example of the actions enabled
// instead, every change, and the model's state in full (validate -v).
func (r *Result) VerboseSummary() string { return r.summary(true) }

func (r *Result) summary(verbose bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "actor %s: %d steps\n", r.Actor, r.Steps)
	if r.Choices != nil {
		fmt.Fprintf(&b, "  %s\n", r.Choices.String())
	}
	for _, i := range r.Issues {
		fmt.Fprintf(&b, "  issue: %s\n", i)
	}
	fmt.Fprintf(&b, "  conformance: %s\n", strings.ToUpper(string(r.Conforms)))
	for _, p := range r.Pending {
		fmt.Fprintf(&b, "  invoked, never completed: %s (process %s)\n", p.String(), p.Process)
	}
	if f := r.Failure; f != nil {
		switch {
		case r.Conforms == NotEvaluated && r.noVerdict != "": // a failure after a lost tail or a gap (judgeLost, judgeGaps)
			fmt.Fprintf(&b, "    no verdict (%s): the order checked, where it failed (an issue above says why that is no verdict):\n", r.noVerdict)
		case r.Conforms == NotEvaluated: // a search cut short (qtest.Linearize): its furthest order, not a verdict
			fmt.Fprintf(&b, "    no verdict: the order checked is the one that got furthest, where it failed:\n")
		}
		fmt.Fprintf(&b, "%s", f.describe("    ", verbose))
	}
	// the invariants a failure leaves unchecked, in one line: one per
	// invariant buried the blast radius under a screen of "not checked"
	var unchecked []string
	why := ""
	for _, i := range r.Invariants {
		v := strings.ToUpper(string(i.Verdict))
		switch {
		case i.Verdict == NotEvaluated && r.Conforms == NotEvaluated && i.Failure == nil:
			unchecked, why = append(unchecked, i.Name), "the run's conformance has no verdict"
			continue
		case i.Verdict == NotEvaluated && r.Conforms == NotEvaluated:
			v = "not checked: the run's conformance has no verdict"
		case i.Verdict == NotEvaluated && i.cause != "" && i.Failure != nil:
			v = "no verdict (" + i.cause + ")"
		case i.Verdict == NotEvaluated && r.noVerdict != "" && i.Failure != nil:
			v = "no verdict (" + r.noVerdict + ")"
		case i.Verdict == NotEvaluated && i.Failure == nil:
			unchecked, why = append(unchecked, i.Name), "the run does not conform, so the model's states after the failure are not the run's"
			continue
		case i.Verdict == NotEvaluated:
			v = "not checked: the run does not conform, so the model's states after the failure are not the run's"
		}
		fmt.Fprintf(&b, "  invariant %s: %s\n", i.Name, v)
		if i.Failure != nil {
			fmt.Fprintf(&b, "%s", i.Failure.describe("    ", verbose))
		}
	}
	switch len(unchecked) {
	case 0:
	case 1:
		fmt.Fprintf(&b, "  invariant %s: not checked: %s\n", unchecked[0], why)
	default:
		fmt.Fprintf(&b, "  invariants not checked (%d: %s): %s\n", len(unchecked), strings.Join(unchecked, ", "), why)
	}
	if r.Hot != nil {
		fmt.Fprintf(&b, "  hot cycle (evidence, no verdict: the run did not settle): %s\n", r.Hot)
	}
	if len(r.Absent) > 0 {
		fmt.Fprintf(&b, "  absent:\n")
		for _, a := range r.Absent {
			fmt.Fprintf(&b, "    %s: %s: %s\n", a.Name, a.Cause.label(), a.Why)
			if a.Diagnostic != nil {
				for _, line := range strings.Split(a.Diagnostic.String(), "\n") {
					fmt.Fprintf(&b, "      %s\n", line)
				}
			}
		}
	}
	if !r.OK() && len(r.Constants) > 0 {
		fmt.Fprintf(&b, "  instance: %s\n", strings.Join(r.Constants, ", "))
	}
	// a failing actor's module, to replay it by hand; a passing one's is
	// none to look at, and the CLI removes its temporary directory
	// (gotogether validate printed a path that was gone)
	if r.Module != "" && !r.OK() { // (a search's verdict, with no replay of its own)
		fmt.Fprintf(&b, "  module: %s\n", r.Module)
	}
	if r.byEvaluator && r.compiled {
		fmt.Fprintf(&b, "  judged by the Go evaluator alone, by a compiled model (validate -compiled): no quint replay, none to sample it against\n")
	} else if r.byEvaluator {
		fmt.Fprintf(&b, "  judged by the Go evaluator, with no quint replay (GOTOGETHER_EVAL=quint replays it with quint)\n")
	}
	if r.sampled {
		fmt.Fprintf(&b, "  judged by quint and by the Go evaluator, which passed it: a sample of its passes (GOTOGETHER_EVAL_SAMPLE)\n")
	}
	if r.ITF != "" {
		fmt.Fprintf(&b, "  itf: %s\n", r.ITF)
	}
	return b.String()
}

func (f *Failure) describe(pfx string, verbose bool) string {
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
	return f.describeRest(&b, pfx, verbose)
}

// At names where f is, as a report names it: its record ("record
// day.jsonl:12", RecordID), else the replay's step ("step 3": an
// inferred one, a restart's, has no record).
func (f *Failure) At() string {
	if f.Step != nil {
		return "record " + RecordID(f.Step)
	}
	return fmt.Sprintf("step %d", f.Index+1)
}

// describeKind says what kind of failure f is, and where.
func (f *Failure) describeKind(b *strings.Builder, pfx string) {
	switch f.Kind {
	case "disabled":
		fmt.Fprintf(b, "%s%s is not a transition of the model from the state reached\n", pfx, f.At())
	case "state":
		fmt.Fprintf(b, "%safter %s the model's state does not match what was observed\n", pfx, f.At())
	case "invariant":
		if f.Index < 0 {
			fmt.Fprintf(b, "%sviolated in the initial state\n", pfx)
		} else {
			fmt.Fprintf(b, "%sviolated after %s\n", pfx, f.At())
		}
	case "within":
		// the obligation's story: when it opened, and what came of it
		fmt.Fprintf(b, "%s%s\n", pfx, f.Note)
	default:
		fmt.Fprintf(b, "%sfailed at %s\n", pfx, f.At())
	}
}

// describeRest is the rest of f's description, after its kind: the
// record and the step observed, the call, the expect, why the model
// cannot take it, what the step changed, the model's state.
func (f *Failure) describeRest(b *strings.Builder, pfx string, verbose bool) string {
	if s := f.Step; s != nil {
		// the record once, by one index, with what on-call looks up next:
		// when, who (the process, the pod), the trace
		fmt.Fprintf(b, "%s  record:   %s\n", pfx, recordWhere(s, f.Index, verbose))
		fmt.Fprintf(b, "%s  observed: %s\n", pfx, s.String())
		if s.Ref != "" { // the driver's own record of the step: where a person looks next
			fmt.Fprintf(b, "%s  ref:      %s\n", pfx, s.Ref)
		}
		if len(s.Obs) > 0 {
			fmt.Fprintf(b, "%s  obs:      %s\n", pfx, obsString(s.Obs))
		}
		if s.Error != "" {
			fmt.Fprintf(b, "%s  error:    %s\n", pfx, s.Error)
		}
	}
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
	if f.Kind == "error" {
		// quint's own message only where the report has no reading of it:
		// a disabled step, a state mismatch and an invariant are said above
		fmt.Fprintf(b, "%s  %s\n", pfx, f.Quint)
	}
	diffed := len(f.Changes) > 0 || f.Why != nil && f.Why.Expected != ""
	if f.Why != nil {
		b.WriteString(f.Why.render(pfx+"  ", verbose))
	}
	if len(f.Changes) > 0 {
		writeChanges(b, pfx+"  ", "the step changed, in the model's state:", f.Changes, verbose)
	}
	if f.noState != "" && f.Kind != "error" {
		fmt.Fprintf(b, "%s  model state: %s\n", pfx, f.noState)
	}
	if len(f.State) > 0 {
		when := "before the step"
		if f.StateAfter {
			when = "after the step"
		}
		names := make([]string, 0, len(f.State))
		for k := range f.State {
			if !strings.HasPrefix(k, "mbt::") {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		if diffed && !verbose {
			// the diff says what moved: the whole state is -v's
			fmt.Fprintf(b, "%s  model state %s: %s (-v prints it)\n", pfx, when, fmt.Sprintf("%d %s", len(names), plural(len(names), "variable", "variables")))
			return b.String()
		}
		fmt.Fprintf(b, "%s  model state %s:\n", pfx, when)
		for i, k := range names {
			if i == 12 && !verbose {
				fmt.Fprintf(b, "%s    ... and %d more variables (-v prints them)\n", pfx, len(names)-12)
				break
			}
			v := f.State[k].String()
			if !verbose {
				v = cutValue(v, 240)
			}
			fmt.Fprintf(b, "%s    %s = %s\n", pfx, k, v)
		}
	}
	return b.String()
}

// cutValue is a value's text cut to about n bytes at an element's end
// (after a ", " separator), not mid-value, saying how much is left.
func cutValue(v string, n int) string {
	if len(v) <= n {
		return v
	}
	cut := strings.LastIndex(v[:n], ", ")
	if cut < n/2 {
		cut = n
	}
	return fmt.Sprintf("%s, ... (%d more characters; -v prints it whole)", v[:cut], len(v)-cut)
}

func obsString(m map[string]any) string {
	keys := slices.Sorted(maps.Keys(m))
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

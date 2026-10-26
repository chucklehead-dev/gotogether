package validate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/replay"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// resumeConfirm is how many starts quint confirms a resume's failure
// over, one run of the steps from each: past it, the failure is the Go
// evaluator's alone, and the report says so.
const resumeConfirm = 200

// resumeLost is the lost prefix of tr's first step's recorder, when b's
// resume judges tr from the states it admits: a resume is named, the
// trace starts with no snapshot step, and its first recorder lost its
// head. Another recorder's lost prefix is no resume's: its lost steps
// may have come after the trace's first (it stays an error).
func resumeLost(b *binding.Binding, tr *qtrace.Trace) (qtrace.Lost, bool) {
	if b.Resume == "" || len(tr.Steps) == 0 || b.SnapshotFirst(tr) {
		return qtrace.Lost{}, false
	}
	first := tr.Steps[0]
	for _, l := range tr.Lost {
		if l.Prefix && l.Recorder == first.Recorder {
			return l, true
		}
	}
	return qtrace.Lost{}, false
}

// resumes reports whether c judges tr from its resume's states.
func (c *Checker) resumes(tr *qtrace.Trace) bool {
	_, ok := resumeLost(c.Binding, tr)
	return ok
}

// checkResume judges tr from every state c's resume admits that keeps
// the invariants checked (a start that violates one is not the trace's
// to blame, as init's would not be): it conforms if some start explains
// the steps, by the Go evaluator's search (judgeHidden, with any hidden
// actions). quint confirms: a pass by replaying the steps from its
// witness start, and a failure by replaying them from every start (at
// most resumeConfirm), none of which may pass. With hidden actions quint
// can replay nothing, and the verdict is the evaluator's, as theirs is.
func (c *Checker) checkResume(ctx context.Context, tr *qtrace.Trace, plan *binding.Plan, res *Result, h *hiddenSearch, l qtrace.Lost) (*Result, error) {
	a := c.Binding.Resume
	all, err := h.e.m.Successors(h.consts, nil, h.e.em.Actions[a])
	if err != nil {
		return nil, fmt.Errorf("resume %s: the Go evaluator: %w", a, err)
	}
	var starts []qeval.State
	for _, s := range all {
		keep := true
		for _, inv := range c.Binding.Invariants {
			v, err := h.e.m.Eval(h.consts, s, h.e.em.Invariants[inv])
			if err != nil {
				return nil, err
			}
			keep = keep && v == qeval.Bool(true)
		}
		if keep {
			starts = append(starts, s)
		}
	}
	noVerdict := func(msg string) (*Result, error) {
		res.Conforms = NotEvaluated
		res.Issues = append(res.Issues, qtrace.Issue{Severity: "warn", Kind: "resume", Msg: msg})
		for _, inv := range c.Binding.Invariants {
			res.Invariants = append(res.Invariants, InvariantResult{Name: inv, Verdict: NotEvaluated})
		}
		return res, nil
	}
	switch {
	case len(starts) == 0:
		return noVerdict(fmt.Sprintf("no verdict: resume %s admits no state that keeps the invariants (%d states, every one violating one): widen it, or record a snapshot step when a log starts",
			a, len(all)))
	case len(starts) > hiddenCap:
		return noVerdict(fmt.Sprintf("no verdict: resume %s admits %d states, past %d: narrow resume, or record a snapshot step when a log starts", a, len(starts), hiddenCap))
	}
	if i := slices.Index(res.Issues, l.Issue()); i >= 0 {
		res.Issues[i] = qtrace.Issue{Severity: "info", Kind: "lost-prefix", Msg: fmt.Sprintf(
			"%s: judged from the states %s admits (%d of them); a pass means some admitted state explains the rest", l, a, len(starts))}
	}
	h.starts = starts
	res, w, err := c.judgeHidden(tr, plan, res, h)
	if err != nil {
		return nil, err
	}
	if len(c.Binding.Hidden) > 0 {
		return res, nil // quint cannot replay hidden actions: the evaluator's verdict, which judgeHidden said
	}
	if w >= 0 {
		return c.confirmWitness(ctx, tr, plan, res, h, starts[w])
	}
	for _, rule := range c.Binding.Within {
		res.Invariants = append(res.Invariants, InvariantResult{Name: rule.Name(), Verdict: NotEvaluated, Within: true})
	}
	if passed(res) { // no verdict (a search past its cap): nothing to confirm
		return res, nil
	}
	if len(starts) > resumeConfirm {
		res.Issues = append(res.Issues, qtrace.Issue{Severity: "info", Kind: "resume", Msg: fmt.Sprintf(
			"the Go evaluator's verdict alone: quint confirms one over at most %d starts, and %s admits %d (narrow it to have quint confirm)", resumeConfirm, a, len(starts))})
		return res, nil
	}
	passes, err := c.replayStarts(ctx, tr, plan, h, starts)
	if err != nil {
		return nil, err
	}
	for i, ok := range passes {
		if ok {
			return nil, fmt.Errorf("resume %s: the Go evaluator finds no admitted state explains actor %s's steps, and quint's replay from %s passes: "+
				"a bug of the evaluator (internal/qeval) or of the search (validate's hiddenSearch); check with a snapshot step meanwhile", a, nameOf(tr.Actor), describeState(h, starts[i]))
		}
	}
	res.Issues = append(res.Issues, qtrace.Issue{Severity: "info", Kind: "resume", Msg: fmt.Sprintf(
		"quint confirms: from none of the %d states %s admits do the steps pass", len(starts), a)})
	return res, nil
}

// confirmWitness replays tr's steps with quint from start, the state a
// search's passing path began in, and takes quint's verdict (its module
// and ITF in the report, its bounded responses judged on it); one that
// does not pass is the evaluator's error, never a verdict.
func (c *Checker) confirmWitness(ctx context.Context, tr *qtrace.Trace, plan *binding.Plan, res *Result, h *hiddenSearch, start qeval.State) (*Result, error) {
	p := *plan
	p.Start = &binding.Call{Action: c.Binding.Resume, Quint: literalStart(h, start), Note: "the resume's witness: " + describeState(h, start)}
	o, err := c.dirFor(c.withinITF(c.Options))
	if err != nil {
		return nil, err
	}
	r, err := validatePlan(ctx, c.Binding, tr, &p, o)
	if err != nil {
		return nil, err
	}
	if r.Conforms != Pass || !passed(r) || slices.ContainsFunc(r.Invariants, func(i InvariantResult) bool { return i.Verdict != Pass }) {
		return nil, fmt.Errorf("resume %s: the Go evaluator's witness for actor %s, %s, does not pass in quint's replay: "+
			"a bug of the evaluator (internal/qeval) or of the search (validate's hiddenSearch); check with a snapshot step meanwhile\n%s", c.Binding.Resume, nameOf(tr.Actor), describeState(h, start), r.Summary())
	}
	res.Conforms, res.Failure, res.Invariants = r.Conforms, r.Failure, r.Invariants
	res.Module, res.ITF, res.Calls, res.Output, res.Steps = r.Module, r.ITF, r.Calls, r.Output, len(r.Calls)
	res.Issues = append(res.Issues, qtrace.Issue{Severity: "info", Kind: "resume", Msg: fmt.Sprintf(
		"the steps pass from %s, a state %s admits (quint confirms)", describeState(h, start), c.Binding.Resume)})
	res.resumed = true
	c.judgeWithin(tr, res)
	for i := range res.Invariants {
		if iv := &res.Invariants[i]; iv.Within && iv.Verdict == Fail && len(h.starts) > 1 {
			// one start's: another the resume admits may meet it (with one
			// start, the witness is every start)
			iv.Verdict, res.noVerdict = NotEvaluated, "another admitted start"
			res.Issues = append(res.Issues, qtrace.Issue{Severity: "warn", Kind: "within", Msg: fmt.Sprintf(
				"%s: not judged: it fails from the witness start, and another state %s admits may meet it", iv.Name, c.Binding.Resume)})
		}
	}
	return res, nil
}

// replayStarts replays tr's steps with quint from each of starts in one
// module, every check fused, and says which pass (their eventual vals
// too, of a settled trace).
func (c *Checker) replayStarts(ctx context.Context, tr *qtrace.Trace, plan *binding.Plan, h *hiddenSearch, starts []qeval.State) ([]bool, error) {
	o, err := c.dirFor(c.Options)
	if err == nil && o.Dir == "" {
		o.Dir, err = os.MkdirTemp("", "gotogether-resume-")
	}
	if err != nil {
		return nil, err
	}
	chosen := o.Backend != "" || os.Getenv("QUINT_BACKEND") != ""
	if o, err = o.resolve(); err != nil {
		return nil, err
	}
	if !chosen {
		o.Backend = "typescript" // one process for every run (CheckBatch's reason)
	}
	o.NoITF = true
	ms := replay.Spec(c.Binding, plan)
	ms.Tests, ms.Start = replay.FastTests, nil
	runs := make([][]binding.Call, len(starts))
	for i, s := range starts {
		runs[i] = plan.Calls
		ms.Starts = append(ms.Starts, &binding.Call{Action: c.Binding.Resume, Quint: literalStart(h, s)})
	}
	qnt := filepath.Join(o.Dir, "resume_starts.qnt")
	gs, err := replay.RenderBatch(ms, runs, qnt)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(qnt, []byte(gs[0].Text), 0o644); err != nil {
		return nil, err
	}
	var names []string
	for _, g := range gs {
		names = append(names, g.Tests.All, g.Tests.Conforms)
		for _, n := range g.Tests.Eventually {
			names = append(names, n)
		}
	}
	names = slices.DeleteFunc(names, func(n string) bool { return n == "" })
	out, err := quintTest(ctx, qnt, gs[0].Module, "resume_starts", o, names)
	if err != nil {
		return nil, err
	}
	passes := make([]bool, len(gs))
	for i, g := range gs {
		verdicts, _ := parseQuintTest(out, qnt, g)
		main := g.Tests.All
		if main == "" {
			main = g.Tests.Conforms
		}
		v, ok := verdicts[main]
		if !ok {
			return nil, &ToolError{Module: qnt, Test: main, Msg: "quint did not report it", Output: out}
		}
		passes[i] = v == Pass
		for _, n := range g.Tests.Eventually {
			if tr.Settled && verdicts[n] != Pass {
				passes[i] = false
			}
		}
	}
	return passes, nil
}

// literalStart is an action assigning state's values, a replay's start:
// a resume's admitted state as Quint.
func literalStart(h *hiddenSearch, state qeval.State) string {
	var parts []string
	for _, v := range h.e.m.Vars {
		parts = append(parts, fmt.Sprintf("%s' = %s", shortName(v), qeval.String(state[v])))
	}
	return "all { " + strings.Join(parts, ", ") + " }"
}

// describeState is state as a report says it: "balance = 50, ops = 3".
func describeState(h *hiddenSearch, state qeval.State) string {
	var parts []string
	for _, v := range h.e.m.Vars {
		parts = append(parts, fmt.Sprintf("%s = %s", shortName(v), qeval.String(state[v])))
	}
	return strings.Join(parts, ", ")
}

// shortName is a variable's name as the replay module sees it (the
// model imported unqualified).
func shortName(v string) string { return v[strings.LastIndex(v, ":")+1:] }

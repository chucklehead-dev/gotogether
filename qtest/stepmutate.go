package qtest

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The fates of an observation mutant (StepMutation.Fate).
const (
	// FateKilled: the mutant's verdict is not the original's: it fails
	// where the original conformed (a rejection, a mismatch, a
	// violation), or it is no finished run where the original finished.
	FateKilled = "killed"
	// FateSurvived: the mutant's verdict is the original's: the check
	// does not read what it changed.
	FateSurvived = "survived"
	// FateUndecided: the mutant has no verdict (a search out of its
	// budget, a lost tail's eventual val, a failure a dropped step may
	// explain: the gap op's): neither killed nor survived.
	FateUndecided = "undecided"
	// FateMalformed: the mutant was refused before a verdict, as a real
	// log like it is (a tool error, validate's exit 2, never a FAIL):
	// Reconstruct refused it with an error issue (an overlap, a cycle, a
	// completion before its invocation; a gap is the verdict's, dropped
	// steps), or the binding could not plan it (a field it reads
	// removed, a process change it has no restart for: "refused by the
	// binding"). No check of the model's judged it, so it counts in no
	// score and meets no claim: a refusal says the binding needs the
	// field, not that a run without it fails. qtrace.Mutations leaves out
	// the mutants it can see Reconstruct would refuse; one that is anyway
	// is reported with its issue.
	FateMalformed = "malformed"
)

// A StepMutation is one observation mutant judged (MutateSteps): the
// mutant, and what its verdict made of it.
type StepMutation struct {
	qtrace.StepMutant
	// Fate is FateKilled, FateSurvived, FateUndecided or FateMalformed.
	Fate string
	// Why is a killed or undecided mutant's verdict, as a report's result
	// line says it ("mismatch after deposit", "violates noOverdraft"), or
	// the binding's refusal.
	Why string
	// Unread marks a corruption of a field the binding reads for none of
	// the step's parameters or expects (binding.Binding.UsedFields): its
	// survival says so already.
	Unread bool
	// Skipped is the mutated step's outcome when the binding skips it
	// (its outcomes map it to skip: "error"), "" otherwise: a skipped step
	// is no model call, so no check reads what a mutant does to it, and
	// its survival says so already.
	Skipped string
	// Report is its verdict; nil when the binding refused it.
	Report *validate.Report
}

func (m StepMutation) String() string {
	s := m.Fate + ": " + m.StepMutant.String()
	if m.Why != "" {
		s += " (" + m.Why + ")"
	}
	if m.Unread {
		s += " (a field the binding does not read)"
	}
	if m.Skipped != "" && m.Fate == FateSurvived {
		s += fmt.Sprintf(" (this step is skipped by the binding's outcomes: %s: skip, so no check reads it; model the refusal if it should be judged)", m.Skipped)
	}
	return s
}

// A StepMutationReport is what judging a log's observation mutants found
// (MutateSteps, JudgeStepMutants).
type StepMutationReport struct {
	// Original is the log as given, judged: it conforms.
	Original *Run
	// Mutants are the mutants judged, in qtrace.Mutations' order.
	Mutants []StepMutation
	// actions and fields are the original's, for a claim naming neither
	actions, fields map[string]bool
}

// StepMutations narrows MutateSteps' mutants (qtrace.MutateOps,
// qtrace.MutateLimit): a long log's every mutation is a check each.
func StepMutations(opts ...qtrace.MutationOption) Option {
	return option(optStepMutations, func(c *settings) { c.stepMutations = append(c.stepMutations, opts...) })
}

// What MutateSteps ignores, and why.
// Read-only.
var mutateStepsIgnores = map[optionID]string{
	optGlobal:          validateIgnores[optGlobal],
	optRecorderOptions: validateIgnores[optRecorderOptions],
	optSkipIfEmpty:     validateIgnores[optSkipIfEmpty],
	optDriver:          "MutateSteps writes no manifest: Validate the run for one",
	optArtifacts:       "MutateSteps writes no manifest: Validate the run for one",
}

// MutateSteps judges each observation mutant of steps (qtrace.Mutations:
// a step dropped, duplicated, swapped with its neighbour, a field
// corrupted, retimed, moved to another actor, lost leaving a gap) as
// Validate judges the steps themselves, with the same options and
// searches, sharing verdicts with every check of the test binary. It asks which recorded fields the
// model and the binding constrain: a mutant whose verdict is the
// original's survived, and what it changed is something no check reads.
// The original must conform (it fails t otherwise): a failing run's
// mutants fail too, and say nothing. State what must be killed, and what
// survives by right, with the report's Expect:
//
//	rep := qtest.MutateSteps(t, "bank.binding.yaml", steps)
//	rep.Expect(t, qtest.Killed(qtest.StepOp("corrupt").Action("deposit").Field("obs.balance").Change("+1")))
//
// A survivor is a question, as a code mutant's is: is the field needed,
// or is the binding (an expect) or the model missing the check that
// would read it? Some survive by right: a trailing probe the model does
// not need dropped, an idempotent read duplicated, an observation the
// binding does not compare corrupted (Unread). StepMutations narrows the
// mutants.
func MutateSteps(t testing.TB, bindingPath string, steps []qtrace.Step, opts ...Option) *StepMutationReport {
	t.Helper()
	c := config(t, opts)
	c.refuse(t, "MutateSteps", mutateStepsIgnores)
	o := c.Options
	o.Dir = filepath.Join(c.Dir, "quint")
	cc, inst, _ := instanceChecker(t, bindingPath, o, c)
	rep, err := judgeStepMutants(cc, inst, steps, c.searchOptions(), c.stepMutations)
	if err != nil {
		t.Fatalf("gotogether: MutateSteps: %v", err)
	}
	rep.Original.checks, rep.Original.name = checkedNames(cc), t.Name()
	t.Logf("gotogether: %s", rep)
	return rep
}

// JudgeStepMutants is MutateSteps outside a test: steps' observation
// mutants judged by c with the searches opt, for a tool (gotogether
// validate -mutate-steps). An error is a tool's, or an original that
// does not conform.
func JudgeStepMutants(c *validate.Checker, steps []qtrace.Step, opt validate.SearchOptions, mopts ...qtrace.MutationOption) (*StepMutationReport, error) {
	// a key of this checker's alone: verdicts shared by its mutants, not
	// with another instance's
	return judgeStepMutants(c, fmt.Sprintf("judge %p|", c), steps, opt, mopts)
}

func judgeStepMutants(cc *validate.Checker, inst string, steps []qtrace.Step, opt validate.SearchOptions, mopts []qtrace.MutationOption) (*StepMutationReport, error) {
	ctx := context.Background()
	var cached int
	check := cachedCheck(cc, inst, &cached)
	js, errs := cc.JudgeBatch(ctx, [][]qtrace.Step{steps}, opt, check)
	if errs[0] != nil {
		return nil, errs[0]
	}
	orig := js[0].Report
	original := &Run{Steps: js[0].Steps, Recorded: steps, Report: orig, checker: cc, search: opt, searched: opt.Linearize || opt.ResolvePending}
	if !orig.OK() {
		return nil, fmt.Errorf("the original steps do not conform (%s): observation mutants ask what a conforming run's checks constrain, "+
			"and a failing run's mutants fail too. Fix the run or the model first:\n%s", reasonOf(orig), orig.Summary())
	}
	rep := &StepMutationReport{Original: original, actions: map[string]bool{}, fields: map[string]bool{}}
	for i := range steps {
		s := &steps[i]
		if !s.Model() {
			continue
		}
		rep.actions[s.Action] = true
		for k := range s.Args {
			rep.fields[k] = true
		}
		for k := range s.Obs {
			rep.fields["obs."+k] = true
		}
	}
	var used map[string][]string
	if cc.Model != nil {
		used = cc.Binding.UsedFields(cc.Model)
	}
	// a mutant the binding cannot plan (a field it reads removed, a
	// process change it has no restart for) is refused before quint, as
	// a real log like it is a tool error: malformed, not killed (it once
	// counted as a kill, a score for a check no model made)
	var todo [][]qtrace.Step
	var at []int
	for _, m := range qtrace.Mutations(steps, mopts...) {
		sm := StepMutation{StepMutant: m}
		if m.Op == "corrupt" && used != nil {
			sm.Unread = !slices.Contains(used[m.Action], m.Field)
		}
		if s := &steps[m.Index]; s.Model() && cc.Binding.Skips(s) {
			sm.Skipped = cmp.Or(s.Outcome, "ok")
		}
		if err := refused(cc, m.Steps); err != nil {
			// its first line: the fix that follows is the binding's
			// for a field missing by accident, not by a mutant's design
			why, _, _ := strings.Cut(err.Error(), "\n")
			sm.Fate, sm.Why = FateMalformed, "refused by the binding: "+why
		} else {
			todo = append(todo, m.Steps)
			at = append(at, len(rep.Mutants))
		}
		rep.Mutants = append(rep.Mutants, sm)
	}
	if len(todo) > 0 {
		js, errs := cc.JudgeBatch(ctx, todo, opt, check)
		for k, i := range at {
			m := &rep.Mutants[i]
			if errs[k] != nil {
				return nil, fmt.Errorf("mutant %s: %w", m.StepMutant, errs[k])
			}
			m.Report = js[k].Report
			m.Fate, m.Why = stepFate(orig, m.Report)
		}
	}
	return rep, nil
}

// refused is the binding's error planning steps, if any: what Check
// would fail with before a verdict.
func refused(cc *validate.Checker, steps []qtrace.Step) error {
	traces, _ := qtrace.Reconstruct(steps)
	for _, tr := range traces {
		if _, err := cc.Binding.Plan(cc.Model, tr); err != nil {
			return fmt.Errorf("actor %s: %w", tr.Actor, err)
		}
	}
	return nil
}

// stepFate is a mutant's fate by its verdict r, the original's (orig)
// being a conforming one: malformed if Reconstruct refused it; killed if
// it fails, or does not finish where the original did; undecided if it
// has no verdict, or survives with bounds the original's had judged left
// unjudged (its times go back along the order checked: its survival says
// nothing of them).
func stepFate(orig, r *validate.Report) (fate, why string) {
	if is := malformed(r); is != "" {
		return FateMalformed, is
	}
	switch {
	case verdictless(r):
		return FateUndecided, reasonOf(r)
	case !r.OK():
		return FateKilled, reasonOf(r)
	}
	if orig.Finished().Verdict == validate.Pass {
		switch f := r.Finished(); f.Verdict {
		case validate.Fail:
			return FateKilled, f.Reason
		case validate.NotEvaluated:
			return FateUndecided, f.Reason
		}
	}
	if w := withinUnjudged(r); w != "" && withinUnjudged(orig) == "" {
		return FateUndecided, w
	}
	return FateSurvived, ""
}

// withinUnjudged is why r's bounded-response obligations were not
// judged, if they were not ("": judged, or none).
func withinUnjudged(r *validate.Report) string {
	for _, a := range r.Actors {
		for _, is := range a.Issues {
			if is.Kind == "within" && strings.Contains(is.Msg, "not judged") {
				return is.Msg
			}
		}
	}
	return ""
}

// reconstructKinds are the kinds of the error issues Reconstruct
// raises: a log a recorder could not have written, whatever the model.
// Read-only.
// A gap is not one: a recorder may drop a step (the gap op), and the
// verdict reads it as dropped steps, no verdict (validate.Result.Reason).
var reconstructKinds = map[string]bool{"lost-prefix": true, "duplicate": true, "invocation": true, "overlap": true, "cycle": true}

// malformed is the first error issue Reconstruct raised in r, if any
// ("": none): the original conforms, so it has none of them.
func malformed(r *validate.Report) string {
	issues := slices.Clone(r.Issues)
	for _, a := range r.Actors {
		issues = append(issues, a.Issues...)
	}
	for _, is := range issues {
		if is.Severity == "error" && reconstructKinds[is.Kind] {
			return "refused by Reconstruct: " + is.String()
		}
	}
	return ""
}

// reasonOf is a report's reason, as validate's result line says it.
func reasonOf(r *validate.Report) string {
	if v := validate.VerdictOf(r.Actors, nil); v.Reason != "" {
		return v.Reason
	}
	if r.Unsettled != "" {
		return "unsettled"
	}
	return "fails"
}

// counts are a report's mutants by op and fate.
func (r *StepMutationReport) counts() (ops []string, n map[string]map[string]int) {
	n = map[string]map[string]int{}
	for _, m := range r.Mutants {
		if n[m.Op] == nil {
			n[m.Op] = map[string]int{}
			ops = append(ops, m.Op)
		}
		n[m.Op][m.Fate]++
	}
	return ops, n
}

// Killed are the mutants killed.
func (r *StepMutationReport) Killed() []StepMutation { return r.ofFate(FateKilled) }

// Survived are the mutants that survived: what no check reads.
func (r *StepMutationReport) Survived() []StepMutation { return r.ofFate(FateSurvived) }

// Undecided are the mutants with no verdict.
func (r *StepMutationReport) Undecided() []StepMutation { return r.ofFate(FateUndecided) }

// Malformed are the mutants refused before a verdict, by Reconstruct or
// the binding (FateMalformed).
func (r *StepMutationReport) Malformed() []StepMutation { return r.ofFate(FateMalformed) }

func (r *StepMutationReport) ofFate(f string) []StepMutation {
	var out []StepMutation
	for _, m := range r.Mutants {
		if m.Fate == f {
			out = append(out, m)
		}
	}
	return out
}

// Score is the share of decided mutants killed.
func (r *StepMutationReport) Score() float64 {
	k, s := len(r.Killed()), len(r.Survived())
	if k+s == 0 {
		return 1
	}
	return float64(k) / float64(k+s)
}

// String is the report's table, by op, and its survivors and undecided
// mutants, as Mutate's report lists its survivors.
func (r *StepMutationReport) String() string {
	var b strings.Builder
	k, s, u, x := len(r.Killed()), len(r.Survived()), len(r.Undecided()), len(r.Malformed())
	fmt.Fprintf(&b, "observation mutants: %d, %d killed (%.0f%%), %d survived", len(r.Mutants), k, 100*r.Score(), s)
	if u > 0 {
		fmt.Fprintf(&b, ", %d undecided (no verdict)", u)
	}
	if x > 0 {
		fmt.Fprintf(&b, ", %d malformed (refused before a verdict, by Reconstruct or the binding: in no score)", x)
	}
	b.WriteString("\n")
	ops, n := r.counts()
	fmt.Fprintf(&b, "  %-8s %7s %7s %9s %10s\n", "op", "mutants", "killed", "survived", "undecided")
	for _, op := range ops {
		c := n[op]
		fmt.Fprintf(&b, "  %-8s %7d %7d %9d %10d", op, c[FateKilled]+c[FateSurvived]+c[FateUndecided]+c[FateMalformed], c[FateKilled], c[FateSurvived], c[FateUndecided])
		if c[FateMalformed] > 0 {
			fmt.Fprintf(&b, " (%d malformed)", c[FateMalformed])
		}
		b.WriteString("\n")
	}
	for _, m := range r.Mutants {
		if m.Fate != FateKilled {
			fmt.Fprintf(&b, "  %s\n", m)
		}
	}
	return b.String()
}

// A StepSelector names observation mutants: by op (StepOp), and
// narrowed by the step's action, the field corrupted and how, and the
// step's index in the log.
type StepSelector struct {
	op, action, field, change string
	index                     *int
}

// StepOp selects the mutants of op ("drop", "dup", "swap", "corrupt",
// "retime", "reactor", and "gap", made only when asked for:
// qtrace.MutationOps). An unknown op panics, naming the ops.
func StepOp(op string) StepSelector {
	if !slices.Contains(qtrace.MutationOps, op) {
		panic(fmt.Sprintf("qtest: StepOp(%q): no such mutation; the ops are %s", op, strings.Join(qtrace.MutationOps, ", ")))
	}
	return StepSelector{op: op}
}

// Action narrows s to mutants of a step of action (for a swap, its
// earlier step).
func (s StepSelector) Action(action string) StepSelector { s.action = action; return s }

// Field narrows s to corruptions of field, as a binding names it
// ("amount", "obs.balance").
func (s StepSelector) Field(field string) StepSelector { s.field = field; return s }

// Change narrows s to corruptions made so: "+1", "-1", "flip", "empty",
// "change" or "remove".
func (s StepSelector) Change(change string) StepSelector { s.change = change; return s }

// Index narrows s to mutants of the log's step i (0-based, as
// StepMutant.Index).
func (s StepSelector) Index(i int) StepSelector { s.index = &i; return s }

func (s StepSelector) matches(m StepMutation) bool {
	return m.Op == s.op && (s.action == "" || m.Action == s.action) && (s.field == "" || m.Field == s.field) &&
		(s.change == "" || m.Change == s.change) && (s.index == nil || m.Index == *s.index)
}

func (s StepSelector) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s mutants", s.op)
	if s.action != "" {
		fmt.Fprintf(&b, " of %s", aStep(s.action))
	}
	if s.index != nil {
		fmt.Fprintf(&b, " (step %d)", *s.index)
	}
	if s.field != "" {
		fmt.Fprintf(&b, ", field %s", s.field)
	}
	if s.change != "" {
		fmt.Fprintf(&b, ", change %s", s.change)
	}
	return b.String()
}

// A StepClaim is what a test expects of observation mutants
// (StepMutationReport.Expect): that the ones a selector names are all
// killed (Killed) or all survive (Survived).
type StepClaim struct {
	fate string
	sel  StepSelector
}

// Killed claims every mutant sel names is killed: a field the model or
// the binding must constrain, and does.
func Killed(sel StepSelector) StepClaim { return StepClaim{FateKilled, sel} }

// Survived claims every mutant sel names survives: an equivalent one,
// stated (a trailing probe the model does not need, an idempotent read
// duplicated, an observation the binding does not compare).
func Survived(sel StepSelector) StepClaim { return StepClaim{FateSurvived, sel} }

func (c StepClaim) String() string { return c.sel.String() + " " + c.fate }

// Expect fails t unless every claim holds: each names at least one
// mutant (a selector that names none, a misspelled action or field,
// would hold for nothing), and every one it names has its fate. An
// undecided mutant meets no claim (raise the search's budget), nor does
// a malformed one (Reconstruct or the binding refused it: no question
// of the model's).
func (r *StepMutationReport) Expect(t testing.TB, claims ...StepClaim) {
	t.Helper()
	for _, c := range claims {
		var named, wrong []string
		for _, m := range r.Mutants {
			if !c.sel.matches(m) {
				continue
			}
			named = append(named, m.String())
			if m.Fate != c.fate {
				wrong = append(wrong, "  "+m.String())
			}
		}
		if len(named) == 0 {
			t.Errorf("gotogether: want %s: no mutant is one (%s)", c, r.unknown(c.sel))
			continue
		}
		if len(wrong) > 0 {
			hint := "the model or the binding does not constrain it: add the check that reads it (an expect, an invariant), or state it Survived"
			if c.fate == FateSurvived {
				hint = "a check reads it: it is no equivalent; claim it Killed"
			}
			t.Errorf("gotogether: want %s, but %d of %d are not (%s):\n%s", c, len(wrong), len(named), hint, strings.Join(wrong, "\n"))
		}
	}
}

// unknown says why a selector named no mutant: an action or field no
// step has, or the ops the report has.
func (r *StepMutationReport) unknown(s StepSelector) string {
	switch {
	case s.action != "" && !r.actions[s.action]:
		return fmt.Sprintf("no step is of action %q; the log's: %s", s.action, strings.Join(slices.Sorted(maps.Keys(r.actions)), ", "))
	case s.field != "" && !r.fields[s.field]:
		return fmt.Sprintf("no step has field %q; the log's: %s", s.field, strings.Join(slices.Sorted(maps.Keys(r.fields)), ", "))
	}
	ops, _ := r.counts()
	return fmt.Sprintf("the report's ops: %s; narrowed by StepMutations, or a selector too narrow", strings.Join(ops, ", "))
}

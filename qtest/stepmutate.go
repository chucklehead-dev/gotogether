package qtest

import (
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

// The fates of an observation mutant (StepMutation.Fate): validate's.
const (
	// FateKilled: the mutant's verdict is not the original's: it fails
	// where the original conformed, or it is no finished run where the
	// original finished.
	FateKilled = validate.FateKilled
	// FateSurvived: the mutant's verdict is the original's: the check
	// does not read what it changed.
	FateSurvived = validate.FateSurvived
	// FateUndecided: the mutant has no verdict: neither killed nor
	// survived.
	FateUndecided = validate.FateUndecided
	// FateMalformed: the mutant was refused before a verdict, as a real
	// log like it is (validate.FateMalformed): in no score, meeting no
	// claim.
	FateMalformed = validate.FateMalformed
)

// A StepMutation is one observation mutant judged (MutateSteps): the
// mutant, and what its verdict made of it (validate.StepMutation).
type StepMutation = validate.StepMutation

// A StepMutationReport is what MutateSteps found: validate's report of
// the mutants (Mutants, Killed, Survived, Undecided, Malformed, Score,
// String: validate.StepMutants'; outside a test,
// validate.Checker.MutateSteps makes one) with the original as a run of
// the test's, and the claims a test states of them (Expect).
type StepMutationReport struct {
	*validate.StepMutants
	// Original is the log as given, judged as Validate judges it: it
	// conforms. (StepMutants.Original is the same judgment, validate's.)
	Original *Run
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
	rep, err := judgeStepMutants(t.Context(), cc, inst, steps, c.searchOptions(), c.stepMutations)
	if err != nil {
		t.Fatalf("gotogether: MutateSteps: %v", err)
	}
	rep.Original.checks, rep.Original.name = checkedNames(cc), t.Name()
	t.Logf("gotogether: %s", rep)
	return rep
}

func judgeStepMutants(ctx context.Context, cc *validate.Checker, inst string, steps []qtrace.Step, opt validate.SearchOptions, mopts []qtrace.MutationOption) (*StepMutationReport, error) {
	var cached int
	sm, err := cc.MutateSteps(ctx, steps, opt, cachedCheck(cc, inst, &cached), mopts...)
	if err != nil {
		return nil, err
	}
	o := sm.Original
	original := &Run{Steps: o.Steps, Recorded: steps, Report: o.Report, checker: cc, search: opt, searched: opt.Linearize || opt.ResolvePending}
	rep := &StepMutationReport{StepMutants: sm, Original: original, actions: map[string]bool{}, fields: map[string]bool{}}
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
	return rep, nil
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
// "change", a list's "shorten" or "grow", a map's "drop", or "remove".
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

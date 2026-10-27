package validate

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/qtrace"
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

// A StepMutation is one observation mutant judged (Checker.MutateSteps): the
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
	Report *Report
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

// StepMutants are a log's observation mutants judged
// (Checker.MutateSteps): the original's judgment, and each mutant's fate.
type StepMutants struct {
	// Original is the log as given, judged: it conforms.
	Original *Judgment
	// Mutants are the mutants judged, in qtrace.Mutations' order.
	Mutants []StepMutation
}

// MutateSteps judges each observation mutant of steps (qtrace.Mutations,
// narrowed by mopts: a step dropped, duplicated, swapped with its
// neighbour, a field corrupted, retimed, moved to another actor, lost
// leaving a gap) as Judge judges the steps themselves, with the searches
// opt, the orders checked by check (nil: CheckBatch; qtest's shares
// verdicts with every check of the test binary). It asks which recorded
// fields the model and the binding constrain: a mutant whose verdict is
// the original's survived, and what it changed is something no check
// reads. An error is a tool's, or an original that does not conform: a
// failing run's mutants fail too, and say nothing.
func (c *Checker) MutateSteps(ctx context.Context, steps []qtrace.Step, opt SearchOptions, check BatchCheck, mopts ...qtrace.MutationOption) (*StepMutants, error) {
	js, errs := c.JudgeBatch(ctx, [][]qtrace.Step{steps}, opt, check)
	if errs[0] != nil {
		return nil, errs[0]
	}
	orig := js[0].Report
	if !orig.OK() {
		return nil, fmt.Errorf("the original steps do not conform (%s): observation mutants ask what a conforming run's checks constrain, "+
			"and a failing run's mutants fail too. Fix the run or the model first:\n%s", orig.Reason(), orig.Summary())
	}
	rep := &StepMutants{Original: js[0]}
	var used map[string][]string
	if c.Model != nil {
		used = c.Binding.UsedFields(c.Model)
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
		if s := &steps[m.Index]; s.Model() && c.Binding.Skips(s) {
			sm.Skipped = cmp.Or(s.Outcome, "ok")
		}
		if err := refused(c, m.Steps); err != nil {
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
		js, errs := c.JudgeBatch(ctx, todo, opt, check)
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
func refused(c *Checker, steps []qtrace.Step) error {
	traces, _ := reconstruct(c.Binding, steps)
	for _, tr := range traces {
		if _, err := c.Binding.Plan(c.Model, tr); err != nil {
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
func stepFate(orig, r *Report) (fate, why string) {
	if is := malformed(r); is != "" {
		return FateMalformed, is
	}
	switch {
	case verdictless(r):
		return FateUndecided, r.Reason()
	case !r.OK():
		return FateKilled, r.Reason()
	}
	if orig.Finished().Verdict == Pass {
		switch f := r.Finished(); f.Verdict {
		case Fail:
			return FateKilled, f.Reason
		case NotEvaluated:
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
func withinUnjudged(r *Report) string {
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
// verdict reads it as dropped steps, no verdict (Result.Reason).
var reconstructKinds = map[string]bool{"lost-prefix": true, "duplicate": true, "invocation": true, "overlap": true, "cycle": true}

// malformed is the first error issue Reconstruct raised in r, if any
// ("": none): the original conforms, so it has none of them.
func malformed(r *Report) string {
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

// counts are a report's mutants by op and fate.
func (r *StepMutants) counts() (ops []string, n map[string]map[string]int) {
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
func (r *StepMutants) Killed() []StepMutation { return r.ofFate(FateKilled) }

// Survived are the mutants that survived: what no check reads.
func (r *StepMutants) Survived() []StepMutation { return r.ofFate(FateSurvived) }

// Undecided are the mutants with no verdict.
func (r *StepMutants) Undecided() []StepMutation { return r.ofFate(FateUndecided) }

// Malformed are the mutants refused before a verdict, by Reconstruct or
// the binding (FateMalformed).
func (r *StepMutants) Malformed() []StepMutation { return r.ofFate(FateMalformed) }

func (r *StepMutants) ofFate(f string) []StepMutation {
	var out []StepMutation
	for _, m := range r.Mutants {
		if m.Fate == f {
			out = append(out, m)
		}
	}
	return out
}

// Score is the share of decided mutants killed.
func (r *StepMutants) Score() float64 {
	k, s := len(r.Killed()), len(r.Survived())
	if k+s == 0 {
		return 1
	}
	return float64(k) / float64(k+s)
}

// String is the report's table, by op, and its survivors and undecided
// mutants, as qtest's Mutate report lists its survivors.
func (r *StepMutants) String() string {
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
	if _, ok := n["corrupt"]; ok && r.Original != nil {
		if u := qtrace.UncorruptedFields(r.Original.Steps); len(u) > 0 {
			fmt.Fprintf(&b, "  %d fields only removed, never corrupted (null, a struct, an empty list or map): %s\n", len(u), strings.Join(u, ", "))
		}
	}
	for _, m := range r.Mutants {
		if m.Fate != FateKilled {
			fmt.Fprintf(&b, "  %s\n", m)
		}
	}
	return b.String()
}

// verdictless reports whether the run has no verdict at all: no actor
// FAILs, and an actor is undecided, or one that conforms and violates
// nothing has an invariant with no verdict (a lost tail's, hidden
// actions past their cap) or a gap in its recorders' seqs. A mutant is
// not killed by such a run, whose failure is not known. (qtest's
// verdictless, for its mutants' runs, is the same judgment.)
func verdictless(r *Report) bool {
	for _, a := range r.Actors {
		if a.Conforms == Fail {
			return false // a FAIL anywhere is the run's: a mutant it fails is killed
		}
		for _, inv := range a.Invariants {
			if inv.Verdict == Fail {
				return false
			}
		}
	}
	for _, a := range r.Actors {
		if a.Conforms == NotEvaluated {
			return true
		}
	}
	for _, a := range r.Actors {
		if a.Conforms != Pass {
			continue
		}
		unknown := false
		for _, inv := range a.Invariants {
			unknown = unknown || inv.Verdict == NotEvaluated
		}
		// dropped steps beside recorded ones that pass: what was dropped
		// is unknown ("no verdict (dropped steps)")
		if unknown || len(a.Gaps) > 0 {
			return true
		}
	}
	return false
}

package qtest

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qmut"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

// A MutationReport is what exploring the woven mutants found.
type MutationReport struct {
	profiles   []runprofile.Profile
	executions []runprofile.Execution
	// Unreached are mutants whose code no run of the baseline reached:
	// coverage's business, not the model's.
	Unreached []qmut.Mutant
	// Killed are mutants some run of whose exploration the model rejected,
	// or that broke a run (an error, a panic).
	Killed []qmut.Mutant
	// Survived are mutants reached and explored that every run survived:
	// code the model does not constrain. Each is a question: is the code
	// needed, or is the model missing what it is for?
	Survived []qmut.Mutant
	// Undecided are mutants no run of which failed and some of whose runs
	// have no verdict (a linearization out of its budget): neither killed
	// nor survived. Raise the budget, or shorten the runs.
	Undecided []qmut.Mutant
	// Baseline is the exploration with every mutant off.
	Baseline *Exploration `json:"-"`

	// survivors are the explorations of the mutants that survived, by
	// id: PerMutant's claims, judged in Expect, kill those that miss one
	survivors map[int]*Exploration
}

// Evidence is the baseline's evidence with the mutants' verdicts: the
// report a mutation run leaves.
func (r *MutationReport) Evidence() *Evidence {
	e := &Evidence{FailingBy: map[string]int{}}
	if r.Baseline != nil {
		e = r.Baseline.Evidence()
	}
	e.Mutants = r
	for _, p := range r.profiles {
		e.Profiles = append(e.Profiles, p.Clone())
	}
	for _, execution := range r.executions {
		e.Executions = append(e.Executions, execution.Clone())
	}
	return e
}

// retainExecution keeps configuration and observed limits of mutant runs.
// Coverage and conformance counts in aggregate evidence remain the baseline's.
func (r *MutationReport) retainExecution(x *Exploration) {
	if x.profile != nil {
		r.profiles = append(r.profiles, x.profile.Clone())
	}
	for _, run := range x.Runs {
		if run.hasExecution() {
			r.executions = append(r.executions, run.execution(x.ref()+":"+run.ID()))
		}
	}
}

// markOf is a mutant's mark by function, change and code.
func markOf(m qmut.Mutant) string {
	fn := m.Func[strings.LastIndex(m.Func, "/")+1:] // the package's path out
	if _, f, ok := strings.Cut(fn, "."); ok {
		fn = f // and its name
	}
	return fmt.Sprintf("%s: %s: %s", fn, m.Op, m.Code)
}

// marks reports whether mark e names m.
func marks(e string, m qmut.Mutant) bool {
	pos := fmt.Sprintf("%s:%d", filepath.Base(m.File), m.Line)
	return e == pos || e == pos+" "+m.Op || e == markOf(m)
}

func (c MutationClaim) marked(m qmut.Mutant) bool {
	for _, e := range c.equivalent {
		if marks(e, m) {
			return true
		}
	}
	return false
}

// Expect fails t unless the mutation run meets every claim, naming each
// surviving mutant and how to mark it, and each mark that matched no
// survivor (stale after the code changed). PerMutant's claims must hold
// of the baseline, and kill each survivor whose exploration misses one:
// Killed and Survived say so after Expect. NoSurvivors names every
// equivalent mutant at once; several are one with all their marks.
func (r *MutationReport) Expect(t testing.TB, cs ...MutationClaim) {
	t.Helper()
	var c MutationClaim
	for _, x := range cs {
		c.equivalent = append(c.equivalent, x.equivalent...)
		c.per = append(c.per, x.per...)
	}
	if len(c.per) > 0 {
		if r.Baseline != nil {
			r.Baseline.Expect(t, c.per...)
		}
		// a survivor whose exploration misses a claim is killed by it,
		// judged as the baseline was (verdict)
		var survived []qmut.Mutant
		for _, m := range r.Survived {
			if x := r.survivors[m.ID]; x != nil && !x.meets(c.per) {
				r.Killed = append(r.Killed, m)
				continue
			}
			survived = append(survived, m)
		}
		r.Survived = survived
	}
	// a mark by function and code names every mutant of that text in the
	// function (two "if err != nil" in one): one marked equivalent would
	// excuse the other, and any later one alike, so it must be one
	all := append(append(append(append([]qmut.Mutant(nil), r.Survived...), r.Killed...), r.Unreached...), r.Undecided...)
	for _, e := range c.equivalent {
		var at []string
		for _, m := range all {
			if e == markOf(m) {
				at = append(at, fmt.Sprintf("%s:%d", filepath.Base(m.File), m.Line))
			}
		}
		if len(at) > 1 {
			t.Errorf("gotogether: NoSurvivors(%q) names %d mutants (%s): mark the equivalent one by its position, %q", e, len(at), strings.Join(at, ", "), at[0])
		}
	}
	var survived []string
	used := map[string]bool{}
	for _, m := range r.Survived {
		if c.marked(m) {
			for _, e := range c.equivalent {
				if marks(e, m) {
					used[e] = true
				}
			}
			continue
		}
		survived = append(survived, fmt.Sprintf("  %s (if it is equivalent, mark it: NoSurvivors(%q))", m, markOf(m)))
	}
	var stale []string
	for _, e := range c.equivalent {
		if !used[e] {
			stale = append(stale, e)
		}
	}
	if len(survived) > 0 {
		t.Errorf("gotogether: %d mutants survived every run: code the model does not constrain, or a check missing:\n%s", len(survived), strings.Join(survived, "\n"))
	}
	if len(r.Undecided) > 0 {
		var us []string
		for _, m := range r.Undecided {
			us = append(us, "  "+m.String())
		}
		t.Errorf("gotogether: %d mutants undecided: no run failed, and some had no verdict (a search out of its budget): raise the budget (qtest.LinearizeBudget) or shorten the runs:\n%s",
			len(us), strings.Join(us, "\n"))
	}
	if len(stale) > 0 {
		t.Errorf("gotogether: marked equivalent but no survivor there (killed now, or the code changed): %s", strings.Join(stale, ", "))
	}
}

// Score is the share of reached mutants killed, of those decided.
func (r *MutationReport) Score() float64 {
	n := len(r.Killed) + len(r.Survived)
	if n == 0 {
		return 1
	}
	return float64(len(r.Killed)) / float64(n)
}

func (r *MutationReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "mutants: %d reached, %d killed (%.0f%%), %d survived, %d unreached",
		len(r.Killed)+len(r.Survived), len(r.Killed), 100*r.Score(), len(r.Survived), len(r.Unreached))
	if len(r.Undecided) > 0 {
		fmt.Fprintf(&b, ", %d undecided (no verdict)", len(r.Undecided))
	}
	b.WriteString("\n")
	for _, m := range r.Survived {
		fmt.Fprintf(&b, "  survived: %s\n", m)
	}
	for _, m := range r.Undecided {
		fmt.Fprintf(&b, "  undecided: %s\n", m)
	}
	return b.String()
}

// Mutate explores the system once per woven mutant (qweave -mutate; with
// none woven it skips, or fails under GOTOGETHER_STRICT=1: Optional): a baseline exploration with every mutant off,
// which must conform and says which mutants' code is reached, then each
// reached mutant with cfg's seeds. A mutant is killed
// when some run is rejected by the model, errs or panics, or when no run
// records a step; one that survives these is killed in Expect by a
// PerMutant claim it misses: what the baseline shows besides conforming
// (qtest.Takes("done").InAtLeast(0.9): a step whose absence no single
// run's model check can see). Verdicts are shared across the
// explorations: most mutants' runs repeat the baseline's steps.
// A workload prerequisite that was not reached refuses the mutation assessment:
// its history is unjudged, not a counterexample or a mutant kill. Establish the
// prerequisite reliably before assessing the mutant's behavior.
//
//	sys.Mutate(t, cfg).Expect(t, qtest.PerMutant(done), qtest.NoSurvivors(marks...))
func (s System) Mutate(t testing.TB, cfg ExploreConfig) *MutationReport {
	t.Helper()
	all := qmut.Mutants()
	if len(all) == 0 {
		skipping(t, "no mutants woven: run with qweave -mutate")
	}
	spec := exploreSpec{ExploreConfig: cfg, opts: s.opts(), rates: s.FaultRates()}
	reach := &qmut.Reach{}
	base := explore(t, s.Binding, spec, func(c qenv.Chooser) error {
		return s.Run(qenv.WithContext(c, qmut.WithReach(qenv.ContextOf(c), reach)))
	})
	base.Expect(t, Conforms())
	rep := &MutationReport{Baseline: base, survivors: map[int]*Exploration{}}
	for _, m := range all {
		if !reach.Reached(m.ID) {
			rep.Unreached = append(rep.Unreached, m)
			continue
		}
		var broke atomic.Bool
		mcfg := spec
		mcfg.baseline = base.ref() // the baseline's regressions are the mutants' too
		mut := m.String()
		mcfg.opts = append(append([]Option(nil), mcfg.opts...), func(c *settings) { c.mutant = mut }) // its evidence names it
		x := explore(t, s.Binding, mcfg, mutantScenario(s.Run, m.ID, &broke))
		rep.retainExecution(x)
		// killed: a run broke, or the model rejects one, or none recorded a
		// step where the baseline's did; undecided: no run failed, and some
		// have no verdict (a search out of budget), which once counted as a
		// kill
		switch {
		case broke.Load() || empty(x.Runs) || decidedFailing(x.Runs):
			rep.Killed = append(rep.Killed, m)
		case anyUndecided(x.Runs):
			rep.Undecided = append(rep.Undecided, m)
		default:
			rep.Survived = append(rep.Survived, m)
			rep.survivors[m.ID] = x
		}
	}
	return rep
}

// mutantScenario preserves ordinary application errors/panics as kills, while
// refusing to assess behavior whose workload prerequisite never held. Keeping
// the marker on the returned error lets the normal execution-failure path emit
// unjudged evidence instead of manufacturing a model verdict.
func mutantScenario(run func(qenv.Chooser) error, id int, broke *atomic.Bool) func(qenv.Chooser) error {
	return func(c qenv.Chooser) (err error) {
		refuse := func(cause error) error {
			return fmt.Errorf("gotogether: mutant %d not assessed: workload prerequisite was not reached; establish the prerequisite before assessing mutation behavior: %w", id, cause)
		}
		defer func() {
			if p := recover(); p != nil {
				if cause, ok := p.(error); ok && workloadNotReached(cause) {
					err = refuse(cause)
					return
				}
				broke.Store(true)
			}
		}()
		if cause := run(qenv.WithContext(c, qmut.WithMutant(qenv.ContextOf(c), id))); cause != nil {
			if workloadNotReached(cause) {
				return refuse(cause)
			}
			broke.Store(true)
		}
		return nil
	}
}

func workloadNotReached(err error) bool {
	var missing interface{ WorkloadNotReached() bool }
	return errors.As(err, &missing) && missing.WorkloadNotReached()
}

// decidedFailing reports whether a run failed with a verdict: not OK,
// and no actor without one.
func decidedFailing(runs []*Run) bool {
	for _, r := range runs {
		if !r.Report.OK() && !verdictless(r.Report) {
			return true
		}
	}
	return false
}

func anyUndecided(runs []*Run) bool {
	for _, r := range runs {
		if verdictless(r.Report) {
			return true
		}
	}
	return false
}

// meets reports whether the exploration meets every claim, without
// failing anything: a survivor's PerMutant claims, judged as the
// baseline's were (verdict).
func (x *Exploration) meets(claims []Claim) bool {
	reps := x.reports()
	for _, c := range claims {
		if !x.verdict(c, reps).ok {
			return false
		}
	}
	return true
}

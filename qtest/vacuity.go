package qtest

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A Vacuity is an invariant that held in every run that checked it only
// because its antecedent never did: `settled implies ...` in runs that
// never settled says nothing.
type Vacuity struct {
	Invariant  string `json:"invariant"`
	Antecedent string `json:"antecedent"`
	Runs       int    `json:"runs"` // the runs where the invariant held, none of which met the antecedent
}

func (v Vacuity) String() string {
	return fmt.Sprintf("%s: %s held in none of the %d runs where it passed", v.Invariant, v.Antecedent, v.Runs)
}

// witnessChunk is how many passing runs are checked for an antecedent at
// a time: the first chunk usually witnesses every one.
const witnessChunk = 8

// Vacuous returns the invariants that held vacuously in every run: their
// antecedent (Model.Antecedents, or the binding's antecedents) never held
// in any run where the invariant passed. The runs are checked for a
// witness in chunks, stopping when every antecedent has one.
//
// An antecedent quint cannot evaluate gives no verdict: Vacuous returns
// nil, and NotVacuous fails with the error.
func (x *Exploration) Vacuous() []Vacuity {
	return x.vacuous().found
}

// vacuityResult is what the search for vacuous invariants found: the
// vacuous ones, the antecedents it judged, and why it could not judge
// them (err).
type vacuityResult struct {
	found      []Vacuity
	antecedent int
	err        error
}

func (x *Exploration) vacuous() vacuityResult {
	if x.vacuity == nil {
		v := x.findVacuous()
		x.vacuity = &v
	}
	return *x.vacuity
}

// vacuityVerdict is NotVacuous's verdict: it fails when an invariant held
// vacuously, when an antecedent cannot be evaluated, and when no
// invariant has one (it would hold for nothing).
func (x *Exploration) vacuityVerdict() claimVerdict {
	v := x.vacuous()
	fail := func(msg string) claimVerdict {
		return claimVerdict{say: func(testing.TB) string { return fmt.Sprintf("want %s; %s", vacuityClaim{}, msg) }}
	}
	switch {
	case v.err != nil:
		return fail(fmt.Sprintf("the antecedents could not be evaluated, so no verdict: %v. If quint names the antecedent, fix it (the binding's antecedents, or the invariant's `A implies B`); otherwise quint itself failed (a crash, a timeout): run the test again", v.err))
	case v.antecedent == 0:
		return fail("no invariant the runs check has an antecedent (a val `A implies B`, or the binding's antecedents), so the claim would hold for nothing: name one in the binding, or drop the claim")
	case len(v.found) > 0:
		var d []string
		for _, f := range v.found {
			d = append(d, f.String())
		}
		return fail(fmt.Sprintf("held vacuously: %s (the runs never reached what the invariant is about: widen the scenario, or raise its faults)", strings.Join(d, "; ")))
	}
	return claimVerdict{ok: true}
}

func (x *Exploration) findVacuous() vacuityResult {
	if x.checker == nil || x.checker.Model == nil {
		return vacuityResult{err: fmt.Errorf("the exploration has no model to evaluate antecedents with")}
	}
	// the runs whose records were released were looked at before (witnessReleased)
	witnessed := maps.Clone(x.witnessed)
	if witnessed == nil {
		witnessed = map[string]bool{}
	}
	ante, invs, err := x.lookForWitnesses(slices.DeleteFunc(slices.Clone(x.Runs), func(r *Run) bool { return r.released }), witnessed)
	if err == nil {
		err = x.witnessErr
	}
	switch {
	case err != nil:
		return vacuityResult{antecedent: len(invs), err: err} // no verdict, rather than a wrong one
	case len(ante) == 0:
		return vacuityResult{}
	}
	passed := passedIn(x.Runs)
	var out []Vacuity
	for _, inv := range invs {
		if !witnessed[inv] && passed[inv] > 0 {
			out = append(out, Vacuity{Invariant: inv, Antecedent: ante[inv], Runs: passed[inv]})
		}
	}
	return vacuityResult{found: out, antecedent: len(invs)}
}

// witnessReleased looks for the antecedents' witnesses in runs whose
// records are about to be released (releaseRecords), the passing runs
// of a batch, for those no earlier batch witnessed: Vacuous reads them
// from x.witnessed, not from runs it can no longer check. An exploration
// whose invariants have no antecedent checks nothing.
func (x *Exploration) witnessReleased(runs []*Run) {
	if x.witnessErr != nil || x.checker == nil || x.checker.Model == nil {
		return
	}
	if x.witnessed == nil {
		x.witnessed = map[string]bool{}
	}
	passing := slices.DeleteFunc(slices.Clone(runs), func(r *Run) bool { return r.Err != nil || r.Report == nil || !r.Report.OK() })
	if _, _, err := x.lookForWitnesses(passing, x.witnessed); err != nil {
		x.witnessErr = err
	}
}

// passedIn counts, for each invariant, the runs where it passed.
func passedIn(runs []*Run) map[string]int {
	passed := map[string]int{}
	for _, r := range runs {
		ok := map[string]bool{}
		for _, a := range r.Report.Actors {
			for _, ir := range a.Invariants {
				if ir.Verdict == validate.Pass {
					ok[ir.Name] = true
				}
			}
		}
		for name := range ok {
			passed[name]++
		}
	}
	return passed
}

// lookForWitnesses checks runs, in chunks, for a witness of each
// antecedent witnessed does not have yet, in the runs where its
// invariant passed, adding those it finds; it stops when every one has
// one. It returns the invariants' antecedents, by invariant, and their
// names in order.
func (x *Exploration) lookForWitnesses(runs []*Run, witnessed map[string]bool) (ante map[string]string, invs []string, err error) {
	ch := x.checker
	if err := ch.VerifySource(); err != nil {
		return nil, nil, err
	}
	defer func() {
		if verr := ch.VerifySource(); verr != nil {
			err = verr
		}
	}()
	ante = map[string]string{}
	for _, inv := range ch.Binding.Invariants {
		if a, ok := ch.Binding.Antecedents[inv]; ok {
			ante[inv] = a
		} else if a, ok := ch.Model.Antecedents[inv]; ok {
			ante[inv] = a
		}
	}
	if len(ante) == 0 {
		return ante, nil, nil
	}
	invs = slices.Sorted(maps.Keys(ante))
	// the runs where an invariant not witnessed yet passed
	var order []int
	for i, r := range runs {
		for _, a := range r.Report.Actors {
			if slices.ContainsFunc(a.Invariants, func(ir validate.InvariantResult) bool {
				return ir.Verdict == validate.Pass && ante[ir.Name] != "" && !witnessed[ir.Name]
			}) {
				order = append(order, i)
				break
			}
		}
	}
	if len(order) == 0 {
		return ante, invs, nil
	}
	// a checker whose invariants are the antecedents' negations: one that
	// fails in a run is witnessed there
	defs, names := validate.AntecedentWitnesses(ch.Binding, ch.Model, invs)
	witnessNames := slices.Sorted(maps.Keys(names))
	var wc *validate.Checker
	if ch.Compiled() != nil {
		// the compiled model carries every candidate invariant's witness:
		// judged by the Go evaluator, no quint
		c, err := ch.WithWitnesses(witnessNames...)
		if err != nil {
			return ante, invs, err
		}
		wc = c
	} else {
		b := *ch.Binding
		b.Defs = b.Defs + defs
		b.Invariants, b.Eventually = witnessNames, nil // the witnesses alone: an eventual failure is no witness
		// This is a derived diagnostic probe, not the loaded source judgment.
		// Keep its generated predicates out of the original checker and source
		// provenance; only the witness booleans below escape this scope. The original
		// source inventory is guarded before and after every exit from this function.
		wc = &validate.Checker{Binding: &b, Model: ch.Model, Options: ch.Options}
		wc.Options.NoITF = true
		dir, err := os.MkdirTemp("", "gotogether-vacuity-")
		if err != nil {
			return ante, invs, err
		}
		defer os.RemoveAll(dir)
		defer wc.Close()
		wc.Options.Dir = dir
	}
	all := func() bool {
		for _, inv := range invs {
			if !witnessed[inv] {
				return false
			}
		}
		return true
	}
	for s := 0; s < len(order) && !all(); s += witnessChunk {
		chunk := order[s:min(s+witnessChunk, len(order))]
		steps := make([][]qtrace.Step, len(chunk))
		for j, i := range chunk {
			steps[j] = runs[i].Steps
		}
		reps, errs := wc.CheckBatch(context.Background(), steps, 0)
		for j := range chunk {
			if errs[j] != nil {
				return ante, invs, errs[j]
			}
			for _, a := range reps[j].Actors {
				for _, ir := range a.Invariants {
					if ir.Verdict == validate.Fail {
						witnessed[names[ir.Name]] = true
					}
				}
			}
		}
	}
	return ante, invs, nil
}

// NotVacuous is the claim that no invariant held vacuously: each one's
// antecedent held in some run where the invariant was checked. Without
// it, an exploration whose runs never settle conforms to `settled
// implies ...` for nothing. It fails when it cannot judge: an antecedent
// quint cannot evaluate, or no invariant with one.
func NotVacuous() Claim { return vacuityClaim{} }

type vacuityClaim struct{}

func (vacuityClaim) String() string { return "no invariant held vacuously" }

func (vacuityClaim) judge([]*validate.Report) judgment { return judgment{ok: true, show: -1} }

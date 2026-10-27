package qtest

import (
	"context"
	"fmt"
	"maps"
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
	ch := x.checker
	if ch == nil || ch.Model == nil {
		return vacuityResult{err: fmt.Errorf("the exploration has no model to evaluate antecedents with")}
	}
	ante := map[string]string{}
	for _, inv := range ch.Binding.Invariants {
		if a, ok := ch.Binding.Antecedents[inv]; ok {
			ante[inv] = a
		} else if a, ok := ch.Model.Antecedents[inv]; ok {
			ante[inv] = a
		}
	}
	if len(ante) == 0 {
		return vacuityResult{}
	}
	invs := slices.Sorted(maps.Keys(ante))
	// the runs where each invariant passed
	passed := map[string][]int{}
	for i, r := range x.Runs {
		ok := map[string]bool{}
		for _, a := range r.Report.Actors {
			for _, ir := range a.Invariants {
				if ir.Verdict == validate.Pass {
					ok[ir.Name] = true
				}
			}
		}
		for name := range ok {
			passed[name] = append(passed[name], i)
		}
	}
	// a checker whose invariants are the antecedents' negations: one that
	// fails in a run is witnessed there
	b := *ch.Binding
	var defs strings.Builder
	defs.WriteString(b.Defs)
	names := map[string]string{}
	for _, inv := range invs {
		n := "gotogetherNot_" + inv
		names[n] = inv
		fmt.Fprintf(&defs, "\nval %s = not(%s)", n, ante[inv])
	}
	b.Defs = defs.String()
	b.Invariants, b.Eventually = nil, nil // the witnesses alone: an eventual failure is no witness
	for _, inv := range invs {
		b.Invariants = append(b.Invariants, "gotogetherNot_"+inv)
	}
	wc := *ch
	wc.Binding = &b
	wc.Options.NoITF = true
	witnessed := map[string]bool{}
	todo := map[int]bool{}
	for _, inv := range invs {
		for _, i := range passed[inv] {
			todo[i] = true
		}
	}
	order := slices.Sorted(maps.Keys(todo))
	for s := 0; s < len(order) && len(witnessed) < len(invs); s += witnessChunk {
		chunk := order[s:min(s+witnessChunk, len(order))]
		runs := make([][]qtrace.Step, len(chunk))
		for j, i := range chunk {
			runs[j] = x.Runs[i].Steps
		}
		reps, errs := wc.CheckBatch(context.Background(), runs, 0)
		for j := range chunk {
			if errs[j] != nil {
				return vacuityResult{antecedent: len(invs), err: errs[j]} // no verdict, rather than a wrong one
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
	var out []Vacuity
	for _, inv := range invs {
		if !witnessed[inv] && len(passed[inv]) > 0 {
			out = append(out, Vacuity{Invariant: inv, Antecedent: ante[inv], Runs: len(passed[inv])})
		}
	}
	return vacuityResult{found: out, antecedent: len(invs)}
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

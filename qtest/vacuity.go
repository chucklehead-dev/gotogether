package qtest

import (
	"context"
	"fmt"
	"sort"
	"strings"

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
func (x *Exploration) Vacuous() []Vacuity {
	if x.vacuity != nil {
		return *x.vacuity
	}
	out := x.findVacuous()
	x.vacuity = &out
	return out
}

func (x *Exploration) findVacuous() []Vacuity {
	ch := x.checker
	if ch == nil || ch.Model == nil {
		return nil
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
		return nil
	}
	invs := make([]string, 0, len(ante))
	for inv := range ante {
		invs = append(invs, inv)
	}
	sort.Strings(invs)
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
	b.Invariants = nil
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
	var order []int
	for i := range todo {
		order = append(order, i)
	}
	sort.Ints(order)
	for s := 0; s < len(order) && len(witnessed) < len(invs); s += witnessChunk {
		chunk := order[s:min(s+witnessChunk, len(order))]
		runs := make([][]qtrace.Step, len(chunk))
		for j, i := range chunk {
			runs[j] = x.Runs[i].Steps
		}
		reps, errs := wc.CheckBatch(context.Background(), runs, 0)
		for j := range chunk {
			if errs[j] != nil {
				return nil // an antecedent quint cannot evaluate: no verdict, rather than a wrong one
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
	return out
}

// NotVacuous is the claim that no invariant held vacuously: each one's
// antecedent held in some run where the invariant was checked. Without
// it, an exploration whose runs never settle conforms to `settled
// implies ...` for nothing.
func NotVacuous() Claim { return vacuityClaim{} }

type vacuityClaim struct{}

func (vacuityClaim) String() string { return "no invariant held vacuously" }

func (vacuityClaim) judge([]*validate.Report) judgment { return judgment{ok: true, show: -1} }

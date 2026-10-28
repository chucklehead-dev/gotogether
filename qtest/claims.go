package qtest

import (
	"fmt"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate"
)

// The claims, in one place: what each report's Expect takes. Every
// report states its claims in its Expect, none in the call that made it
// (a mutation run's too: PerMutant).
//
//	Exploration.Expect (Explore, Exhaust, Sweep rows, Runs)  Claim: Some, All, None of Expectations;
//	                                                         Reaches, Takes, Exercised, NotVacuous
//	Run.Expect, qhegel's Violation.Expect                    RunClaim: an Expectation, ShrunkTo
//	MutationReport.Expect (Mutate)                           MutationClaim: PerMutant, NoSurvivors
//	StepMutationReport.Expect (StepMutate)                   StepClaim: Killed, Survived
//	ShrunkActions.Expect (ShrinkActions)                     ShrinkClaim: an Expectation, AtMostActions
//
// An Expectation is of one run: Conforms, Rejects, Violates, Scenario,
// Satisfies and grist's gristtest.NoClientErrors alike, and Not of one.
// Over runs it takes its quantifier, Some, All or None, except the four
// whose names say every run: Conforms, Settles, Completes and Finishes
// alone are All of themselves. Any other alone panics with the fix: what
// a bare Rejects meant (some run) and a bare Satisfies (every run) was
// each constructor's, which a reader had to know.

// A Claim is what a test expects of a set of runs (Exploration.Expect,
// Sweep.Expect): that some, every or no run meets expectations (Some,
// All, None), or what the runs reached and took (Reaches, Takes,
// Exercised). Conforms, Settles, Completes and Finishes alone are about
// every run (All); any other Expectation alone panics, naming Some, All
// and None. Several claims are judged one by one, so Expect(t,
// Some(Violates("a")), Some(Violates("b"))) wants each shown by some run,
// not one run showing both (Some(a, b) says that).
type Claim interface {
	judge(reports []*validate.Report) judgment
	String() string
}

// judgment is a claim's verdict: show is the run to print (-1: none).
type judgment struct {
	ok   bool
	show int
	n    int // runs meeting (Some, None) or missing (All) the expectations
	// unjudged are None's runs that meet none of its expectations but have
	// no verdict to meet them with (a search out of budget): None holds of
	// none of them, which once passed it over runs judged by nothing
	unjudged int
}

// judge is e's as a claim over runs: All of it if e stands alone,
// else quantify's panic.
func (e Expectation) judge(rs []*validate.Report) judgment { return quantify(e).judge(rs) }

// quantify is c as a claim over runs: an Expectation alone is All of it
// when its name says every run (Conforms, Settles, Completes, Finishes),
// and a panic naming the fix otherwise. A bare Rejects meant some run and
// a bare Satisfies every run, which only the constructor's doc said.
func quantify(c Claim) Claim {
	e, ok := c.(Expectation)
	switch {
	case !ok:
		return c
	case e.alone:
		return All(e)
	}
	meant := "Some"
	if e.every {
		meant = "All"
	}
	panic(fmt.Sprintf("qtest: %q alone is a claim over runs with no quantifier: write qtest.Some(...) (some run shows it), qtest.All(...) (every run) or qtest.None(...) (no run); "+
		"alone it meant %s(...). Only Conforms, Settles, Completes and Finishes stand alone, their names saying every run", e.desc, meant))
}

// quantifyAll is quantify of each claim.
func quantifyAll(cs []Claim) []Claim {
	out := make([]Claim, len(cs))
	for i, c := range cs {
		out[i] = quantify(c)
	}
	return out
}

type quantified struct {
	kind string // "some", "all", "none"
	es   []Expectation
}

// Some: one run meets every one of es.
func Some(es ...Expectation) Claim { return quantified{"some", es} }

// All: every run meets every one of es.
func All(es ...Expectation) Claim { return quantified{"all", es} }

// None: no run meets any of es: what a bug must not do, or a row where a
// fault cannot happen must never show.
func None(es ...Expectation) Claim { return quantified{"none", es} }

func (q quantified) String() string {
	var d []string
	for _, e := range q.es {
		d = append(d, e.desc)
	}
	switch q.kind {
	case "some":
		return "some run showing " + strings.Join(d, " and ")
	case "all":
		return "every run showing " + strings.Join(d, " and ")
	}
	return "no run showing " + strings.Join(d, ", or ")
}

// meets is the run's value for q's expectations together: all of them
// (yes if each is, no if one is not, else unknown) and any of them (yes
// if one is, unknown if none is and one is unknown, else no).
func (q quantified) meets(r *validate.Report) (all, any tri) {
	all, any = yes, no
	for _, e := range q.es {
		switch e.value(r) {
		case yes:
			any = yes
		case no:
			all = no
		case unknown:
			if all == yes {
				all = unknown
			}
			if any == no {
				any = unknown
			}
		}
	}
	return all, any
}

func (q quantified) judge(rs []*validate.Report) judgment {
	j := judgment{show: -1}
	for i, r := range rs {
		all, any := q.meets(r)
		switch q.kind {
		case "some":
			if all == yes {
				j.n++
				if j.show < 0 {
					j.show = i
				}
			}
		case "all":
			switch all {
			case no:
				j.n++
				if j.show < 0 {
					j.show = i
				}
			case unknown:
				j.unjudged++ // (Completes of a run whose calls may have completed in a lost tail)
			}
		case "none":
			switch {
			case any == yes:
				j.n++
				if j.show < 0 {
					j.show = i
				}
			case any == unknown || undecided(r) && !judgesUndecided(q.es):
				j.unjudged++
			}
		}
	}
	// over no runs, no claim has a verdict: All and None would hold of
	// nothing (All once did), and Some fails for want of a run, not of a
	// witness
	j.ok = (q.kind == "some") == (j.n > 0) && j.unjudged == 0 && len(rs) > 0
	return j
}

// judgesUndecided reports whether every one of es judges a run whose
// conformance has no verdict itself (Finishes: a settled run of a binding
// with no eventual val is missed whatever the search found).
func judgesUndecided(es []Expectation) bool {
	for _, e := range es {
		if !e.undecided {
			return false
		}
	}
	return len(es) > 0
}

// A RunClaim is what a test expects of one run (Run.Expect, a qhegel
// Violation's): an Expectation, or a claim only one run can have
// (ShrunkTo), which an exploration's Expect does not take.
type RunClaim interface {
	Holds(r *Run) bool
	String() string
}

// ShrunkTo claims the run has at most n faults: what Hegel's shrinking of
// a failure must reach, with the claim on its verdict.
//
//	qhegel.Shrink(t, sys).Expect(t, qtest.Rejects("apply"), qtest.ShrunkTo(1))
//
// It is a claim on one run (a Violation, a Run with its choices), not on
// an exploration: it is a RunClaim, not a Claim.
func ShrunkTo(n int) RunClaim { return shrunkTo(n) }

type shrunkTo int

func (n shrunkTo) Holds(r *Run) bool { return len(r.Faults()) <= int(n) }
func (n shrunkTo) String() string    { return fmt.Sprintf("a run shrunk to at most %d fault(s)", int(n)) }

// A MutationClaim is what a test expects of a mutation run
// (MutationReport.Expect): PerMutant, NoSurvivors.
type MutationClaim struct {
	equivalent []string
	per        []Claim
}

// PerMutant claims what the baseline shows besides conforming, which
// kills each mutant whose exploration does not (a step whose absence no
// single run's model check can see):
//
//	sys.Mutate(t, cfg).Expect(t, qtest.PerMutant(qtest.Takes("done").InAtLeast(0.9)), qtest.NoSurvivors())
//
// The baseline missing one fails the test. Its claims are an
// exploration's, quantified as Exploration.Expect's are.
func PerMutant(claims ...Claim) MutationClaim { return MutationClaim{per: quantifyAll(claims)} }

// NoSurvivors claims every reached mutant is killed, but those marked
// equivalent: a mutant the environment makes harmless (a dropped ack that
// is redelivered, a retry's boundary) survives by right. Mark one by its
// function, change and code, as a survivor's report prints it, which
// stays put when the code around it moves:
//
//	qtest.NoSurvivors("(Counter).Add: drop call: qobs.Record(ctx, \"replay\", nil, ...")
//
// or by its position, "file.go:42", or position and change, "file.go:42
// drop call", which a line added above it moves.
func NoSurvivors(equivalent ...string) MutationClaim { return MutationClaim{equivalent: equivalent} }

// A ShrinkClaim is what a test expects of a shrink (ShrunkActions.Expect):
// an Expectation of its minimal run, or a claim on the sequence
// (AtMostActions).
type ShrinkClaim interface {
	String() string
	// holdsShrunk reports whether the claim holds of a shrink to n
	// actions whose run is r, and if not, what it saw.
	holdsShrunk(n int, r *Run) (bool, string)
}

func (e Expectation) holdsShrunk(_ int, r *Run) (bool, string) { return e.Holds(r), "" }

// AtMostActions claims the shrink reached at most n actions.
func AtMostActions(n int) ShrinkClaim { return atMostActions(n) }

type atMostActions int

func (n atMostActions) String() string {
	return fmt.Sprintf("a sequence shrunk to at most %d action(s)", int(n))
}
func (n atMostActions) holdsShrunk(k int, _ *Run) (bool, string) {
	return k <= int(n), fmt.Sprintf("it has %d", k)
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

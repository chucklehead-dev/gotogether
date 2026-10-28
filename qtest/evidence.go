package qtest

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/confidence"
	"github.com/chucklehead-dev/gotogether/internal/evidence"
	"github.com/chucklehead-dev/gotogether/qcover"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Evidence is what an exploration, a sweep or a mutation run shows, in one
// report: the verdicts, the faults reached, the configurations covered, the
// code run and the mutants killed. Each part is what a passing test leaves
// unsaid: that runs conformed says nothing of the faults never tried, the
// code never run or the code the model does not constrain.
type Evidence struct {
	// Format names this format, EvidenceFormat: a reader refuses another.
	Format string `json:"format"`
	// Role is what the exploration was: "faithful" (the system as it
	// is), "mutant" (a System.Mutant's, or Mutate's with a mutant on),
	// "replay" (one run reproduced alone, GOTOGETHER_RUN), "external" (a
	// run's manifest, ManifestEvidence). A suite's
	// evidence counts what the faithful ones tried: a mutant takes paths
	// the system does not.
	Role string `json:"role"`
	// Mutant is the System's Mutant, if any.
	Mutant string `json:"mutant,omitempty"`
	// Package is the test's package, below its module ("qtest"; "_" the
	// root): two packages may have tests of one name.
	Package string `json:"package,omitempty"`
	// Import is the test's package's import path
	// ("github.com/x/repo/examples/hello"): Package alone cannot tell two
	// modules' root packages apart, and evidence -expect names a package
	// by it, or by its end ("examples/hello").
	Import string `json:"import,omitempty"`
	// Test is the exploration (TestX#2), and Spec the binding's spec and
	// module ("/abs/model/m.qnt:m"): a suite's evidence groups by them
	// (gotogether evidence).
	Test    string `json:"test,omitempty"`
	Spec    string `json:"spec,omitempty"`
	Runs    int    `json:"runs"`
	Conform int    `json:"conform"`
	// FailingBy counts the failing runs by reason: "violates I", "rejects a
	// (step the model cannot take)", "mismatch after a", "issue".
	FailingBy map[string]int `json:"failing"`
	Faults    *qenv.Coverage `json:"faults,omitempty"`
	Model     *ModelCoverage `json:"model,omitempty"`  // the model's actions the runs took (nil: no model)
	Rows      int            `json:"rows,omitempty"`   // a sweep's rows (0: not a sweep)
	Exhaust   *Extent        `json:"search,omitempty"` // an exhaustive search's extent (nil: seeded runs)
	// Sample is what seeded runs bound of a failure none showed (nil: a
	// search, a run alone, runs given one by one)
	Sample *SampleBound `json:"sample,omitempty"`
	// PCT is a PCT schedule's guarantee and its conditions (nil: none)
	PCT *PCTBound `json:"pct,omitempty"`
	// Judges is who judged the conforming runs (nil: quint, every one)
	Judges  *Judges         `json:"judges,omitempty"`
	Vacuous []Vacuity       `json:"vacuous,omitempty"` // invariants that held only because their antecedent never did
	Code    *qcover.Summary `json:"code,omitempty"`    // when the code is woven with qweave -cover (nil: not)
	Mutants *MutationReport `json:"mutants,omitempty"` // from System.Mutate, if set
}

// WriteJSON writes the evidence to path as JSON, for tools that read a
// run's report (gotogether evidence, cover, actionmap). Explorations write
// theirs under GOTOGETHER_EVIDENCE (dir/<package>/<test>/x<N>.json), else
// in a kept exploration's directory (GOTOGETHER_KEEP/<test>/x<N>/evidence.json).
func (e *Evidence) WriteJSON(path string) error {
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// EvidenceFormat is the format WriteJSON writes and ReadEvidence reads.
// Bumped when a field's name or meaning changes (qenv.Coverage's and
// qcover.Summary's, which it embeds, too), so old files are refused, not
// misread.
const EvidenceFormat = evidence.Format

// Evidence roles (Evidence.Role).
const (
	RoleFaithful = evidence.RoleFaithful
	RoleMutant   = evidence.RoleMutant
	RoleReplay   = evidence.RoleReplay
	// RoleExternal is a run gotogether did not drive, as gotogether
	// evidence lists its manifest (validate.Manifest): Validate's,
	// Record's, validate -manifest's. Not counted in what the suite tried.
	RoleExternal = evidence.RoleExternal
)

// ReadEvidence reads an Evidence written by WriteJSON (an exploration's,
// under GOTOGETHER_EVIDENCE or GOTOGETHER_KEEP). A file of another format,
// or none (written before the format was), is refused with the fix.
func ReadEvidence(path string) (*Evidence, error) {
	var e Evidence
	if err := evidence.Decode(path, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// IsEvidence reports whether the JSON file at path is evidence of any
// format version: what a tool walking a directory reads, and skips the
// rest (ITF traces, corpus entries, summaries).
func IsEvidence(path string) bool { return evidence.Is(path) }

// ManifestEvidence is a run's manifest as evidence (RoleExternal): one
// run, its verdict, its test (or the manifest's name) and package, so
// gotogether evidence lists it, and -expect finds the check that wrote
// it.
func ManifestEvidence(m *validate.Manifest) *Evidence {
	v := evidence.OfManifest(m)
	return &Evidence{Format: v.Format, Role: v.Role, Package: v.Package, Import: v.Import, Test: v.Test, Spec: v.Spec, Runs: v.Runs, Conform: v.Conform, FailingBy: v.FailingBy}
}

// Evidence is the exploration's report.
func (x *Exploration) Evidence() *Evidence {
	e := evidenceOf(x.Runs)
	e.Test, e.Spec = x.ref(), x.model
	e.Format, e.Role, e.Mutant, e.Package, e.Import = EvidenceFormat, x.role(), x.mutation, pkgDir(), importPath()
	e.Faults = x.faultCoverage()
	if x.steps != nil {
		e.Model = modelCoverage(x.Runs, x.steps)
	}
	if x.hasCode() {
		e.Code = x.codeCoverage()
	}
	e.Vacuous = x.Vacuous()
	e.Sample, e.PCT, e.Judges = x.sampleBound(), x.pctBound(), x.judges()
	if x.search != nil {
		e.Exhaust = &Extent{Delays: x.search.bound.Delays, Faults: x.search.bound.Faults, Complete: x.search.left == 0 && x.alone == nil, Left: x.search.left}
	}
	return e
}

// Evidence is the sweep's report over every row's runs.
func (s *SweepResult) Evidence() *Evidence {
	all := &Exploration{}
	for _, x := range s.Runs {
		if x != nil {
			all.Runs = append(all.Runs, x.Runs...)
			if all.actions == nil {
				all.actions, all.steps, all.test, all.model, all.mutation = x.actions, x.steps, x.test, x.model, x.mutation
			}
		}
	}
	e := all.Evidence()
	e.Rows = len(s.Rows)
	return e
}

func (x *Exploration) hasCode() bool {
	for _, r := range x.Runs {
		if r.Code != nil {
			return true
		}
	}
	return false
}

func evidenceOf(runs []*Run) *Evidence {
	e := &Evidence{Runs: len(runs), FailingBy: map[string]int{}}
	for _, r := range runs {
		if r.Report.OK() {
			e.Conform++
			continue
		}
		e.FailingBy[reason(r.Report)]++
	}
	return e
}

// reason names why a run failed, by its first failing actor.
func reason(rep *validate.Report) string {
	for _, a := range rep.Actors {
		if r := a.Reason(); r != "" {
			return r
		}
	}
	if rep.Unsettled != "" {
		return "does not settle"
	}
	if len(rep.Actors) == 0 {
		for _, i := range rep.Issues {
			if i.Severity == "error" {
				return "issue"
			}
		}
		return "no steps" // nothing to reconstruct: no "reconstruction issue"
	}
	return "issue"
}

func (e *Evidence) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "evidence: %d runs", e.Runs)
	if e.Rows > 0 {
		fmt.Fprintf(&b, ", %d rows", e.Rows)
	}
	if sr := e.Exhaust; sr != nil && sr.Complete {
		fmt.Fprintf(&b, "\n  search    complete within %s and %s: every run within that bound, none beyond it", plural(sr.Delays, "delay"), plural(sr.Faults, "fault"))
	} else if sr != nil {
		fmt.Fprintf(&b, "\n  search    truncated: at least %d runs within %s and %s left (MaxRuns)", sr.Left, plural(sr.Delays, "delay"), plural(sr.Faults, "fault"))
	}
	if s := e.Sample; s != nil {
		fmt.Fprintf(&b, "\n  sample    %d seeded runs, no proof: %s", s.Runs, confidence.Sentence(s.Runs, "run", "a failure"))
	}
	if p := e.PCT; p != nil && p.Probability > 0 {
		fmt.Fprintf(&b, "\n  pct       depth %d, Steps %d: a bug of depth up to %d found per run with probability at least %.2g (%d tasks, %d steps at most)", p.Depth, p.Steps, p.Depth, p.Probability, p.MaxTasks, p.MaxSteps)
	} else if p != nil {
		fmt.Fprintf(&b, "\n  pct       depth %d, Steps %d: void, runs took up to %d scheduling steps (set Steps to it)", p.Depth, p.Steps, p.MaxSteps)
	}
	fmt.Fprintf(&b, "\n  verdicts  %d conform", e.Conform)
	if j := e.Judges; j != nil {
		fmt.Fprintf(&b, " (%d by the Go evaluator alone, %d of its passes confirmed by quint)", j.Evaluator, j.Confirmed)
	}
	reasons := slices.Sorted(maps.Keys(e.FailingBy))
	for _, r := range reasons {
		fmt.Fprintf(&b, " · %d %s", e.FailingBy[r], r)
	}
	b.WriteByte('\n')
	for _, v := range e.Vacuous {
		fmt.Fprintf(&b, "  vacuity   %s\n", v)
	}
	if m := e.Model; m != nil {
		fmt.Fprintf(&b, "  model     %d of %d actions taken", len(m.Taken)-countNot(m.Taken), len(m.Taken)-countNot(m.Taken)+len(m.Never))
		if len(m.Never) > 0 {
			fmt.Fprintf(&b, "; never: %s", strings.Join(m.Never, ", "))
		}
		b.WriteByte('\n')
	}
	if c := e.Faults; c != nil {
		faulted := 0
		for _, p := range c.Points {
			if p.Faulted() > 0 {
				faulted++
			}
		}
		pairs := c.Pairs()
		fmt.Fprintf(&b, "  faults    %d of %d kinds faulted", faulted, len(c.Points))
		if len(pairs) > 0 {
			fmt.Fprintf(&b, ", pairs %d/%d together, %d one way", len(pairs)-len(c.Untried()), len(pairs), len(c.OneWay()))
		}
		if u := c.Unreached(); len(u) > 0 {
			fmt.Fprintf(&b, "; unreached: %s", kinds(u))
		}
		if u := c.Unfaulted(); len(u) > 0 {
			fmt.Fprintf(&b, "; never faulted: %s", kinds(u))
		}
		b.WriteByte('\n')
		if u := c.Untried(); len(u) > 0 {
			fmt.Fprintf(&b, "            never together (each faulted, never in one run): %s\n", pairList(u, false))
		}
		if o := c.OneWay(); len(o) > 0 {
			fmt.Fprintf(&b, "            one way (together, but always in this order): %s\n", pairList(o, true))
		}
	}
	if s := e.Code; s != nil {
		fmt.Fprintf(&b, "  code      %.1f%% of statements, %d blocks never run, %d outcome pairs ran the same code\n",
			100*s.Ratio(), len(s.Uncovered()), len(s.Indistinct()))
	}
	if m := e.Mutants; m != nil {
		fmt.Fprintf(&b, "  mutants   %d reached, %d killed (%.0f%%), %d survive, %d unreached, %d undecided\n",
			len(m.Killed)+len(m.Survived), len(m.Killed), 100*m.Score(), len(m.Survived), len(m.Unreached), len(m.Undecided))
	}
	return b.String()
}

// pairList names pairs ("a+b"), up to eight; ordered, as the order they
// ran in ("a, then b").
func pairList(ps []qenv.PairCoverage, ordered bool) string {
	var out []string
	for i, p := range ps {
		if i == 8 {
			out = append(out, fmt.Sprintf("and %d more", len(ps)-8))
			break
		}
		switch {
		case !ordered:
			out = append(out, p.A+"+"+p.B)
		case p.BFirst == 0:
			out = append(out, p.A+", then "+p.B)
		default:
			out = append(out, p.B+", then "+p.A)
		}
	}
	return strings.Join(out, "; ")
}

func kinds(ps []*qenv.PointCoverage) string {
	var ks []string
	for _, p := range ps {
		ks = append(ks, p.Kind)
	}
	return strings.Join(ks, ", ")
}

// countNot counts the actions of taken that are not the model's own
// (init, step): replays take init, which no workflow asks about.
func countNot(taken map[string]int) int {
	n := 0
	for _, a := range []string{"init", "step"} {
		if taken[a] > 0 {
			n++
		}
	}
	return n
}

// role is what the exploration was (Evidence.Role).
func (x *Exploration) role() string {
	switch {
	case x.alone != nil: // a run reproduced alone: it repeats a run counted where it was found
		return RoleReplay
	case x.mutant || x.mutation != "":
		return RoleMutant
	}
	return RoleFaithful
}

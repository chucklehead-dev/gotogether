package qtest

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

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
	// Test is the test that explored, and Spec the binding's spec and
	// module ("/abs/model/m.qnt:m"): a suite's evidence groups by them
	// (gotogether evidence).
	Test    string `json:"test,omitempty"`
	Spec    string `json:"spec,omitempty"`
	Runs    int    `json:"runs"`
	Conform int    `json:"conform"`
	// FailingBy counts the failing runs by reason: "violates I", "rejects a
	// (step the model cannot take)", "mismatch after a", "issue".
	FailingBy map[string]int  `json:"failing"`
	Faults    *qenv.Coverage  `json:"faults,omitempty"`
	Model     *ModelCoverage  `json:"model,omitempty"`   // the model's actions the runs took (nil: no model)
	Rows      int             `json:"rows,omitempty"`    // a sweep's rows (0: not a sweep)
	Search    *Search         `json:"search,omitempty"`  // an exhaustive search's extent (nil: seeded runs)
	Vacuous   []Vacuity       `json:"vacuous,omitempty"` // invariants that held only because their antecedent never did
	Code      *qcover.Summary `json:"code,omitempty"`    // when the code is woven with qweave -cover (nil: not)
	Mutants   *MutationReport `json:"mutants,omitempty"` // from System.Mutate, if set
}

// WriteJSON writes the evidence to path as JSON, for tools that read a
// run's report (gotogether evidence, cover, actionmap). Explorations write
// theirs under GOTOGETHER_KEEP: evidence.json in the test's directory, and
// evidence-2.json and on for a test's later explorations.
func (e *Evidence) WriteJSON(path string) error {
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ReadEvidence reads an Evidence written by WriteJSON (an exploration's
// evidence.json under GOTOGETHER_KEEP).
func ReadEvidence(path string) (*Evidence, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var e Evidence
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("%s: %w (want the JSON of a qtest.Evidence)", path, err)
	}
	return &e, nil
}

// Evidence is the exploration's report.
func (x *Exploration) Evidence() *Evidence {
	e := evidenceOf(x.Runs)
	e.Test, e.Spec = x.test, x.model
	e.Faults = x.faultCoverage()
	if x.actions != nil {
		e.Model = modelCoverage(x.Runs, x.actions)
	}
	if x.hasCode() {
		e.Code = x.codeCoverage()
	}
	e.Vacuous = x.Vacuous()
	if x.search != nil {
		e.Search = &Search{Delays: x.search.bound.Delays, Faults: x.search.bound.Faults, Complete: x.search.left == 0, Left: x.search.left}
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
				all.actions, all.test, all.model = x.actions, x.test, x.model
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
		if a.OK() {
			continue
		}
		if a.Conforms != validate.Pass {
			action := "a step"
			if a.Failure != nil && a.Failure.Step != nil {
				action = a.Failure.Step.Action
			}
			if a.Failure != nil && a.Failure.Kind == "state" {
				return "mismatch after " + action
			}
			return "rejects " + action
		}
		for _, inv := range a.Invariants {
			if inv.Verdict != validate.Pass {
				return "violates " + inv.Name
			}
		}
		return "issue"
	}
	return "issue"
}

func (e *Evidence) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "evidence: %d runs", e.Runs)
	if e.Rows > 0 {
		fmt.Fprintf(&b, ", %d rows", e.Rows)
	}
	if sr := e.Search; sr != nil && sr.Complete {
		fmt.Fprintf(&b, "\n  search    complete: every run within %d delays and %d faults", sr.Delays, sr.Faults)
	} else if sr != nil {
		fmt.Fprintf(&b, "\n  search    truncated: at least %d runs within %d delays and %d faults left (MaxRuns)", sr.Left, sr.Delays, sr.Faults)
	}
	fmt.Fprintf(&b, "\n  verdicts  %d conform", e.Conform)
	reasons := make([]string, 0, len(e.FailingBy))
	for r := range e.FailingBy {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
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
		fmt.Fprintf(&b, "  mutants   %d reached, %d killed (%.0f%%), %d survive, %d unreached\n",
			len(m.Killed)+len(m.Survived), len(m.Killed), 100*m.Score(), len(m.Survived), len(m.Unreached))
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

package qtest

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate"
)

// RequireQuint skips the test when the quint CLI is not installed.
func RequireQuint(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not installed (npm i -g @informalsystems/quint)")
	}
}

// An Expectation is what a test expects of a verdict: it returns nil when
// the report meets it, else why not.
type Expectation struct {
	desc  string
	check func(*validate.Report) bool
	every bool // as a Claim alone: every run (Conforms), not some run
	// the actions and invariants it names, checked against the model: a
	// misspelled name would make None(...) hold vacuously
	actions, invariants []string
	// violates is Violates' invariant, for At
	violates string
}

func (e Expectation) String() string { return e.desc }

// Holds reports whether the expectation holds of run r: the one judgment
// Run.Expect makes, for a package judging runs it made (qhegel's
// Violation, whose "nothing found" is a conforming run).
func (e Expectation) Holds(r *Run) bool { return r.Report != nil && e.check(r.Report) }

// Conforms: every actor's run is a behaviour of the model and satisfies
// its invariants.
func Conforms() Expectation {
	return Expectation{"conformance, with every invariant holding", func(r *validate.Report) bool { return r.OK() }, true, nil, nil, ""}
}

// Settles: the system settled (qenv.Sim.Settle found it quiet), whatever
// its steps; a run that does not settle does not conform either.
// Over runs, None(Settles()) states a livelock.
func Settles() Expectation {
	return Expectation{"a system that settles", func(r *validate.Report) bool { return r.Unsettled == "" }, true, nil, nil, ""}
}

// Completes: every operation recorded at its invocation (qobs.Invoke)
// completed, whatever the run's verdict: every call was answered. A caller
// that crashes leaves its call pending by right, so state it of runs
// whose callers do not crash (the core's clients under
// qenv.Sim.SettleCore); Some(validate's pending call) is None(Completes()).
func Completes() Expectation {
	return Expectation{"every invoked operation completing", func(r *validate.Report) bool {
		for _, a := range r.Actors {
			if len(a.Pending) > 0 {
				return false
			}
		}
		return true
	}, true, nil, nil, ""}
}

func failed(r *validate.Report, kind, action string) bool {
	for _, a := range r.Actors {
		f := a.Failure
		if a.Conforms == validate.NotEvaluated {
			continue // no verdict (Linearize out of budget): not a rejection
		}
		if f != nil && f.Kind == kind && (action == "" || (f.Step != nil && f.Step.Action == action) || strings.HasPrefix(f.Call, action+"(") || f.Call == action) {
			return true
		}
	}
	return false
}

// Rejects: the model cannot take an observed step of this action (an
// implementation bug, or a wrong binding). "" matches any action.
func Rejects(action string) Expectation {
	return Expectation{desc: fmt.Sprintf("%s the model cannot take", aStep(action)), check: func(r *validate.Report) bool { return failed(r, "disabled", action) }, actions: named(action)}
}

// Mismatch: after a step of this action, the observed state differs from
// the model's (the binding's expect fails). "" matches any action.
func Mismatch(action string) Expectation {
	return Expectation{desc: fmt.Sprintf("a state mismatch after %s", aStep(action)), check: func(r *validate.Report) bool { return failed(r, "state", action) }, actions: named(action)}
}

// Violates: the run is a behaviour of the model, and the model's invariant
// fails on it: a property the design (or this binding's view of it)
// does not guarantee, seen in a real run. At narrows it to the step it
// first fails after.
func Violates(invariant string) Expectation {
	return violatesAt(invariant, "")
}

func violatesAt(invariant, action string) Expectation {
	desc := fmt.Sprintf("a conforming run that violates %s", invariant)
	if action != "" {
		desc += " after " + aStep(action)
	}
	return Expectation{desc: desc, check: func(r *validate.Report) bool {
		for _, a := range r.Actors {
			if a.Conforms != validate.Pass {
				continue
			}
			for _, inv := range a.Invariants {
				if inv.Name == invariant && inv.Verdict == validate.Fail &&
					(action == "" || inv.Failure != nil && inv.Failure.Step != nil && inv.Failure.Step.Action == action) {
					return true
				}
			}
		}
		return false
	}, invariants: []string{invariant}, actions: named(action), violates: invariant}
}

// At narrows Violates(invariant) to a run where the invariant first fails
// after a step of action: Violates("exactlyOnce").At("perform").
func (e Expectation) At(action string) Expectation {
	if e.violates == "" {
		panic(fmt.Sprintf("qtest: %s.At(%q): At narrows Violates(invariant) only; Rejects and Mismatch name their action already", e, action))
	}
	return violatesAt(e.violates, action)
}

// AnyOf: the report meets at least one of es.
func AnyOf(es ...Expectation) Expectation {
	e := Expectation{desc: describeOr(es), check: func(r *validate.Report) bool {
		for _, e := range es {
			if e.check(r) {
				return true
			}
		}
		return false
	}}
	for _, x := range es {
		e.actions = append(e.actions, x.actions...)
		e.invariants = append(e.invariants, x.invariants...)
	}
	return e
}

func named(action string) []string {
	if action == "" {
		return nil
	}
	return []string{action}
}

// unknownNames says which actions and invariants es name that the model
// (actions) or the checked invariants do not have; nil sets skip a check.
func unknownNames(es []Expectation, actions, invariants map[string]bool) []string {
	var out []string
	for _, e := range es {
		for _, a := range e.actions {
			if actions != nil && !actions[a] {
				out = append(out, fmt.Sprintf("action %q (the model's: %s)", a, strings.Join(keysOf(actions), ", ")))
			}
		}
		for _, i := range e.invariants {
			if invariants != nil && !invariants[i] {
				out = append(out, fmt.Sprintf("invariant %q (checked: %s)", i, strings.Join(keysOf(invariants), ", ")))
			}
		}
	}
	return out
}

// reportInvariants are the invariants a report checked (nil: none known).
func reportInvariants(r *validate.Report) map[string]bool {
	var m map[string]bool
	for _, a := range r.Actors {
		for _, inv := range a.Invariants {
			if m == nil {
				m = map[string]bool{}
			}
			m[inv.Name] = true
		}
	}
	return m
}

func describeOr(es []Expectation) string {
	var s []string
	for _, e := range es {
		s = append(s, e.desc)
	}
	return strings.Join(s, ", or ")
}

// aStep is "a step", "a ship step", "an apply step".
func aStep(action string) string {
	switch {
	case action == "":
		return "a step"
	case strings.ContainsRune("aeiouAEIOU", rune(action[0])):
		return "an " + action + " step"
	}
	return "a " + action + " step"
}

// A Claim is what a test expects of a set of runs (Exploration.Expect,
// Sweep.Expect): that some, every or no run meets expectations. An
// Expectation alone is a claim about some run (Some), except Conforms,
// which is about every run (All); several claims are judged one by one,
// so Expect(t, Violates("a"), Violates("b")) wants each shown by some
// run, not one run showing both (Some(a, b) says that).
type Claim interface {
	judge(reports []*validate.Report) judgment
	String() string
}

// judgment is a claim's verdict: show is the run to print (-1: none).
type judgment struct {
	ok   bool
	show int
	n    int // runs meeting (Some, None) or missing (All) the expectations
}

func (e Expectation) judge(rs []*validate.Report) judgment {
	if e.every {
		return All(e).judge(rs)
	}
	return Some(e).judge(rs)
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

func (q quantified) meets(r *validate.Report) (all, any bool) {
	all = true
	for _, e := range q.es {
		ok := r != nil && e.check(r)
		all = all && ok
		any = any || ok
	}
	return all, any
}

func (q quantified) judge(rs []*validate.Report) judgment {
	j := judgment{show: -1}
	for i, r := range rs {
		all, any := q.meets(r)
		switch q.kind {
		case "some":
			if all {
				j.n++
				if j.show < 0 {
					j.show = i
				}
			}
		case "all":
			if !all {
				j.n++
				if j.show < 0 {
					j.show = i
				}
			}
		case "none":
			if any {
				j.n++
				if j.show < 0 {
					j.show = i
				}
			}
		}
	}
	j.ok = (q.kind == "some") == (j.n > 0)
	return j
}

// expectReport fails t unless the report meets every expectation,
// printing the report.
func expectReport(t testing.TB, r *validate.Report, es ...Expectation) {
	t.Helper()
	if r == nil {
		t.Fatalf("gotogether: no report")
	}
	if u := unknownNames(es, nil, reportInvariants(r)); len(u) > 0 {
		t.Fatalf("gotogether: the expectation names %s: a misspelled name never matches", strings.Join(u, "; "))
	}
	for _, e := range es {
		if !e.check(r) {
			t.Fatalf("gotogether: want %s, got:\n%s", e, r.Summary())
		}
	}
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

// Expect fails t unless the run meets every claim: its report each
// Expectation, its faults ShrunkTo. A failure prints the report and names
// the run's step log and model-level ITF (Record's and Validate's). It is
// how a test states what a mutant must look like:
//
//	qtest.Record(t, "bank.binding.yaml", payday).Expect(t, qtest.Conforms())
func (r *Run) Expect(t testing.TB, cs ...RunClaim) {
	t.Helper()
	var es []Expectation
	for _, c := range cs {
		if e, ok := c.(Expectation); ok {
			es = append(es, e)
		}
	}
	if r.Report != nil && r.StepLog != "" {
		for _, e := range es {
			if !e.check(r.Report) {
				itfs := make([]string, 0, len(r.ITF))
				for _, k := range keysOf(r.ITF) {
					itfs = append(itfs, r.ITF[k])
				}
				t.Fatalf("gotogether: want %s, got:\n%s  step log: %s\n  model-level ITF: %s",
					e, r.Report.Summary(), r.StepLog, strings.Join(itfs, ", "))
			}
		}
	}
	expectReport(t, r.Report, es...)
	for _, c := range cs {
		if _, ok := c.(Expectation); !ok && !c.Holds(r) {
			t.Fatalf("gotogether: want %s, got %d faults: %v", c, len(r.Faults()), r.Faults())
		}
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

package qtest

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
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
	// tri, if set, is the expectation's value of a run when check's two
	// do not say it: met, missed, or unknown (a call that may have
	// completed in a lost tail), which no claim over it holds by
	tri func(*validate.Report) tri
	// why, if set, says why a run misses the expectation or has no verdict
	// for it, and the fix: a failure prints it (Finishes names the part
	// missing)
	why func(*validate.Report) string
}

// tri is a three-valued verdict of an expectation on a run.
type tri int8

const (
	no tri = iota
	yes
	unknown
)

// value is e's verdict on r: met, missed, or unknown. Unknown: no report,
// e's own (tri), or, for one naming an invariant, a conforming actor
// without a verdict for it (a lost tail's, hidden actions past their
// cap).
func (e Expectation) value(r *validate.Report) tri {
	switch {
	case r == nil:
		return unknown
	case e.tri != nil:
		return e.tri(r)
	case e.check(r):
		return yes
	case invariantUnknown(e.invariants, r):
		return unknown
	}
	return no
}

func (e Expectation) String() string { return e.desc }

// Holds reports whether the expectation holds of run r (not when the run
// has no verdict for it): the one judgment
// Run.Expect makes, for a package judging runs it made (qhegel's
// Violation, whose "nothing found" is a conforming run).
func (e Expectation) Holds(r *Run) bool { return r.Report != nil && e.value(r.Report) == yes }

// Conforms: every actor's run is a behaviour of the model and satisfies
// its invariants.
func Conforms() Expectation {
	return Expectation{desc: "conformance, with every invariant holding", check: func(r *validate.Report) bool { return r.OK() }, every: true}
}

// Settles: the system settled, whatever its steps: the run recorded a
// settle (qenv.Sim.Settle found it quiet, or qobs.Settled) with nothing
// after it. A run that never settles (no Settle, a Settle whose error was
// dropped, the native runtime, which cannot tell) does not; one that
// does not settle does not conform either. Over runs, None(Settles())
// states a livelock.
func Settles() Expectation {
	return Expectation{desc: "a system that settles", check: settled, every: true}
}

// settled reports whether the run recorded a settle last, and settled
// when asked to.
func settled(r *validate.Report) bool {
	if r.Unsettled != "" {
		return false
	}
	for _, a := range r.Actors {
		if a.Settled {
			return true
		}
	}
	return false
}

// Completes: every operation recorded at its invocation (qobs.Invoke)
// completed, whatever the run's verdict: every call was answered. A caller
// that crashes leaves its call pending by right, so state it of runs
// whose callers do not crash (the core's clients under
// qenv.Sim.SettleCore); Some(validate's pending call) is None(Completes()).
//
// A call whose completion may be in a recorder's lost tail (invoked before
// its cut) is neither: a run with one is no verdict for it.
func Completes() Expectation {
	completes := func(r *validate.Report) tri {
		v := yes
		for _, a := range r.Actors {
			for _, p := range a.Pending {
				if !maybeCompleted(a, p) {
					return no // one certainly pending: missed, whatever the others
				}
				v = unknown
			}
		}
		return v
	}
	return Expectation{desc: "every invoked operation completing", check: func(r *validate.Report) bool { return completes(r) == yes }, every: true, tri: completes}
}

// Finishes: the run is a finished journey, not a prefix of one. It
// conforms, it settled (Settles), every eventual val of the binding was
// judged at the settle and holds, and every invoked call completed
// (Completes). Conforms alone passes a prefix: three steps of a five-step
// workflow, cut short, are a behaviour of the model. A binding with no
// eventual val says nothing of where a journey ends, so no run finishes
// by it: state the journey's end as an eventual val on a ghost flag its
// last step sets. A run with no verdict for a part (a lost tail, which
// may hold the settle, the end or a call's completion; a search out of
// its budget) neither meets nor misses it, unless another part is
// certainly missing. A failure names the part missing and the fix.
func Finishes() Expectation {
	value := func(r *validate.Report) tri { v, _ := finishes(r); return v }
	return Expectation{desc: "a finished run (conforming, settled, every eventual val judged and true, every invoked call completed)",
		check: func(r *validate.Report) bool { return value(r) == yes }, every: true, tri: value,
		why: func(r *validate.Report) string { _, why := finishes(r); return why }}
}

// finishes is Finishes' value of r, and why it is not met: a part
// certainly missing decides (the first found is named), else a part with
// no verdict leaves it unknown.
func finishes(r *validate.Report) (tri, string) {
	if r.Unsettled != "" {
		return no, "the system did not settle (" + r.Unsettled + ")"
	}
	for _, is := range r.Issues {
		if is.Severity == "error" {
			return no, "the steps could not be put in order (" + is.String() + ")"
		}
	}
	if len(r.Actors) == 0 {
		return no, "the run recorded no step"
	}
	var unknowns []string
	settledRun, lostTail, eventuals := false, false, 0
	for _, a := range r.Actors {
		switch a.Conforms {
		case validate.Fail:
			return no, fmt.Sprintf("actor %s does not conform (%s)", a.Actor, a.Reason())
		case validate.NotEvaluated:
			unknowns = append(unknowns, fmt.Sprintf("actor %s's conformance has no verdict", a.Actor))
		}
		for _, is := range a.Issues {
			if is.Severity == "error" {
				return no, fmt.Sprintf("actor %s's steps could not be put in order (%s)", a.Actor, is)
			}
		}
		for _, inv := range a.Invariants {
			switch {
			case inv.Verdict == validate.Fail && inv.Eventually:
				return no, fmt.Sprintf("%s is false where actor %s settled: the run stopped short of the journey's end (a prefix of it)", inv.Name, a.Actor)
			case inv.Verdict == validate.Fail:
				return no, fmt.Sprintf("actor %s violates %s", a.Actor, inv.Name)
			case inv.Verdict == validate.NotEvaluated && a.Conforms == validate.Pass:
				unknowns = append(unknowns, fmt.Sprintf("%s has no verdict in actor %s", inv.Name, a.Actor))
			}
			if inv.Eventually {
				eventuals++
			}
		}
		for _, p := range a.Pending {
			if !maybeCompleted(a, p) {
				return no, fmt.Sprintf("call %s (process %s) was invoked and never completed: answer every call before the settle, and record its completion (Call.Done)", p.String(), p.Process)
			}
			unknowns = append(unknowns, fmt.Sprintf("call %s may have completed in a lost tail", p.String()))
		}
		settledRun = settledRun || a.Settled
		lostTail = lostTail || len(a.Lost) > 0
	}
	switch {
	case !settledRun && lostTail:
		unknowns = append(unknowns, "a recorder lost its tail, which may hold the settle")
	case !settledRun:
		return no, "the run recorded no settle last, so its eventual vals were not judged: record qobs.Settled after the driver's calls are answered and the collector drained " +
			"(Sim.Settle in a simulation), with nothing recorded after it"
	case eventuals == 0 && len(unknowns) == 0:
		return no, "the binding states no eventual val, so nothing says where the journey ends: add the journey's end as an eventual val on a ghost flag its last step sets (eventually: [ended])"
	}
	if len(unknowns) > 0 {
		return unknown, strings.Join(unknowns, "; ")
	}
	return yes, ""
}

// explain is why r misses e, or has no verdict for it, as a failure
// appends it ("": e says nothing more than its description).
func (e Expectation) explain(r *validate.Report) string {
	if e.why == nil || r == nil {
		return ""
	}
	if w := e.why(r); w != "" {
		return " (" + w + ")"
	}
	return ""
}

// maybeCompleted reports whether pending call p's completion may be in a
// lost tail of a's: it was invoked before the cut of some recorder of a's
// steps (a search re-sequences the caller's recorder: any lost tail is
// taken as its).
func maybeCompleted(a *validate.Result, p qtrace.Step) bool {
	for _, l := range a.Lost {
		if !p.Time.After(l.At.Add(qtrace.RealTimeSlack)) {
			return true
		}
	}
	return false
}

// Pending: a call of this action was invoked (qobs.Invoke) and never
// completed ("" matches any): Some(Pending("put")) states a call left
// unanswered, None(Pending("")) the same as All(Completes()). A call whose
// completion may be in a lost tail is neither pending nor not.
func Pending(action string) Expectation {
	pending := func(r *validate.Report) tri {
		v := no
		for _, a := range r.Actors {
			for _, p := range a.Pending {
				switch {
				case action != "" && p.Action != action:
				case !maybeCompleted(a, p):
					return yes // one certainly pending: met, whatever the others
				default:
					v = unknown
				}
			}
		}
		return v
	}
	return Expectation{desc: fmt.Sprintf("%s invoked and never completed", aCall(action)), check: func(r *validate.Report) bool { return pending(r) == yes },
		actions: named(action), tri: pending}
}

// aCall names an action's call in a description.
func aCall(action string) string {
	if action == "" {
		return "a call"
	}
	return "a call of " + action
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
	desc := fmt.Sprintf("a violation of %s", invariant)
	if action != "" {
		desc += " after " + aStep(action)
	}
	desc += " (the run conforming)"
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
	// three-valued: met if any part is, unknown if none is and some part
	// is unknown, else missed
	anyOf := func(r *validate.Report) tri {
		v := no
		for _, e := range es {
			switch e.value(r) {
			case yes:
				return yes
			case unknown:
				v = unknown
			}
		}
		return v
	}
	e := Expectation{desc: describeOr(es), check: func(r *validate.Report) bool { return anyOf(r) == yes }, tri: anyOf}
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
	// unjudged are None's runs that meet none of its expectations but have
	// no verdict to meet them with (a search out of budget): None holds of
	// none of them, which once passed it over runs judged by nothing
	unjudged int
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
			case any == unknown || undecided(r):
				j.unjudged++
			}
		}
	}
	j.ok = (q.kind == "some") == (j.n > 0) && j.unjudged == 0
	return j
}

// invariantUnknown reports whether r has no verdict for one of
// invariants (or a within, or an eventual val), in an actor that
// conforms: one a lost tail left unjudged (validate.Result.Lost), or
// hidden actions past their cap. That the run does not violate it is not
// known. (An actor that does not conform violates no invariant, by
// Violates' definition.)
func invariantUnknown(invariants []string, r *validate.Report) bool {
	for _, name := range invariants {
		for _, a := range r.Actors {
			if a.Conforms != validate.Pass {
				continue
			}
			found := false
			for _, inv := range a.Invariants {
				if inv.Name == name {
					found = true
					if inv.Verdict == validate.NotEvaluated {
						return true
					}
				}
			}
			// an eventual val a lost tail left unjudged is no result at
			// all: the run might have settled, with the val false
			if !found && len(a.Lost) > 0 && strings.HasPrefix(name, validate.EventuallyName("")) {
				return true
			}
		}
	}
	return false
}

// undecided reports whether some actor of the run has no verdict on its
// conformance (a search out of its budget, hidden actions past their
// cap): what the run did is not known, so no claim that it did not do
// something holds of it.
func undecided(r *validate.Report) bool {
	for _, a := range r.Actors {
		if a.Conforms == validate.NotEvaluated {
			return true
		}
	}
	return false
}

// verdictless reports whether the run has no verdict at all: undecided,
// or an actor that conforms, violates nothing, and has an invariant with
// no verdict (a lost tail's, hidden actions past their cap). A mutant is
// not killed by such a run, whose failure is not known.
func verdictless(r *validate.Report) bool {
	if undecided(r) {
		return true
	}
	for _, a := range r.Actors {
		if a.Conforms != validate.Pass {
			continue
		}
		unknown, failed := false, false
		for _, inv := range a.Invariants {
			unknown = unknown || inv.Verdict == validate.NotEvaluated
			failed = failed || inv.Verdict == validate.Fail
		}
		if unknown && !failed {
			return true
		}
	}
	return false
}

// expectReport fails t unless the report meets every expectation,
// printing the report.
func expectReport(t testing.TB, r *validate.Report, checks map[string]bool, es ...Expectation) {
	t.Helper()
	if r == nil {
		t.Fatalf("gotogether: no report")
	}
	known := checks // the binding's names; a Run made by hand has only its report's
	if known == nil {
		known = reportInvariants(r)
	}
	if u := unknownNames(es, nil, known); len(u) > 0 {
		t.Fatalf("gotogether: the expectation names %s: a misspelled name never matches", strings.Join(u, "; "))
	}
	for _, e := range es {
		switch e.value(r) {
		case unknown:
			t.Fatalf("gotogether: want %s: the run has no verdict for it (a lost tail, a search out of its budget)%s, got:\n%s", e, e.explain(r), r.Summary())
		case no:
			t.Fatalf("gotogether: want %s%s, got:\n%s", e, e.explain(r), r.Summary())
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
			if v := e.value(r.Report); v != yes {
				itfs := make([]string, 0, len(r.ITF))
				for _, k := range keysOf(r.ITF) {
					itfs = append(itfs, r.ITF[k])
				}
				why := e.explain(r.Report)
				if v == unknown {
					why = ": the run has no verdict for it (a lost tail, a search out of its budget)" + why
				}
				t.Fatalf("gotogether: want %s%s, got:\n%s  step log: %s\n  model-level ITF: %s",
					e, why, r.Report.Summary(), r.StepLog, strings.Join(itfs, ", "))
			}
		}
	}
	expectReport(t, r.Report, r.checks, es...)
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

// Runs gathers runs made one by one (Validate's, Record's: a real
// deployment's, each judged from its step logs, its nemesis's faults
// among its steps) so claims over runs are stated of them as of an
// exploration's runs, with the same three-valued judgment and the same
// checks of the claims: names the model and the binding lack fail, as do
// eventual vals no run judged, and no runs at all:
//
//	qtest.Runs(runs...).Expect(t, qtest.Some(qtest.Rejects("put")), qtest.None(qtest.Violates("agreement")))
//
// The runs have no seeds or choices: a failure names a run by its place
// among them, with its report. They must have been checked against one
// binding's instance.
func Runs(runs ...*Run) *Exploration {
	x := &Exploration{Runs: runs, given: true}
	for _, r := range runs {
		switch {
		case r == nil || r.checker == nil:
		case x.checker == nil:
			x.checker = r.checker
		case !sameInstance(x.checker, r.checker):
			x.mixed = true
		}
	}
	if x.checker != nil && x.checker.Model != nil {
		x.actions = map[string]bool{}
		for a := range x.checker.Model.Actions {
			x.actions[a] = true
		}
	}
	return x
}

// sameInstance reports whether two checkers judge one model instance:
// the same binding's spec and module, with the same constants and invariants.
func sameInstance(a, b *validate.Checker) bool {
	return a == b || a.Binding.SpecPath() == b.Binding.SpecPath() && a.Binding.Module == b.Binding.Module &&
		fmt.Sprint(a.Binding.Constants, a.Binding.Invariants) == fmt.Sprint(b.Binding.Constants, b.Binding.Invariants)
}

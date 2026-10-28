package qtest

import (
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/nearest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// RequireQuint skips the test when the quint CLI is not installed, and
// fails it under GOTOGETHER_STRICT=1 (Optional).
func RequireQuint(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("quint"); err != nil {
		skipping(t, "quint not installed (npm i -g @informalsystems/quint)")
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
	// undecided: its value of a run whose conformance has no verdict is
	// its own (tri), not unknown for that alone
	undecided bool
	// scenario, if set, is a Scenario's: a failing claim says per run how
	// far it followed it, and whether the model can (scenarioMisses)
	scenario *scenario
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
// its invariants. A run with no verdict (validate.Report.Verdict's
// NotEvaluated: a search out of its budget, a lost tail, dropped steps)
// neither meets nor misses it: All(Conforms()) fails of it as
// unjudged, not as a run that does not conform.
func Conforms() Expectation {
	conforms := func(r *validate.Report) tri {
		switch {
		case r.OK():
			return yes
		case r.Unsettled == "" && len(r.Actors) > 0 && r.Verdict().Verdict == validate.NotEvaluated:
			return unknown
		}
		return no
	}
	return Expectation{desc: "conformance, with every invariant holding", check: func(r *validate.Report) bool { return r.OK() }, every: true, tri: conforms}
}

// Settles: the system settled, whatever its steps: the run recorded a
// settle (qenv.Sim.Settle found it quiet, or qobs.Settled) with nothing
// after it. A run that never settles (no Settle, a Settle whose error was
// dropped, the native runtime, which cannot tell) does not; one that
// does not settle does not conform either. Over runs, None(Settles())
// states a livelock. A run that recorded no settle and lost its tail (a
// recorder's lost suffix, or a log still being collected) may have
// settled out of sight: no verdict for it, whatever its conformance.
func Settles() Expectation {
	return Expectation{desc: "a system that settles", check: func(r *validate.Report) bool { return settles(r) == yes }, every: true, tri: settles, undecided: true}
}

// settles is Settles' value of r: yes if the run recorded a settle last
// (and settled when asked to), unknown if it did not and a recorder's
// tail is out of sight, else no.
func settles(r *validate.Report) tri {
	if r.Unsettled != "" {
		return no
	}
	hidden := false
	for _, a := range r.Actors {
		if a.Settled {
			return yes
		}
		for _, l := range a.Lost {
			hidden = hidden || !l.Prefix
		}
		hidden = hidden || len(a.Collected) > 0
	}
	if hidden {
		return unknown
	}
	return no
}

// Completes: every operation recorded at its invocation (qobs.Invoke)
// completed, whatever the run's verdict: every call was answered. A caller
// that crashes leaves its call pending by right, so state it of runs
// whose callers do not crash (the core's clients under
// qenv.Sim.SettleCore); Some(validate's pending call) is None(Completes()).
//
// A call whose completion may be out of sight is neither: in a
// recorder's lost tail (invoked before its cut) or a gap after it, or
// not collected yet (its recorders state a watermark and the run no
// settle they reach: validate.Result.PendingCause). A run with one is no
// verdict for it.
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
	return Expectation{desc: "every invoked operation completing", check: func(r *validate.Report) bool { return completes(r) == yes }, every: true, tri: completes,
		why: pendingWhy("")}
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
		check: func(r *validate.Report) bool { return value(r) == yes }, every: true, tri: value, undecided: true,
		why: func(r *validate.Report) string { _, why := finishes(r); return why }}
}

// finishes is Finishes' value of r, and why it is not met: validate's
// Report.Finished, the judgment `gotogether validate -require-finish`
// makes too.
func finishes(r *validate.Report) (tri, string) {
	f := r.Finished()
	switch f.Verdict {
	case validate.Pass:
		return yes, ""
	case validate.NotEvaluated:
		return unknown, f.Why
	}
	return no, f.Why
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

// Undecided: the run has no verdict for e (a lost tail, which may hold
// the settle or a call's answer; a search out of its budget), and e's
// explanation of it names every one of because ("lost its tail"): the
// claim a test makes of a run that must neither pass nor fail e, without
// walking its report. A run that meets e, or misses it, misses this.
func Undecided(e Expectation, because ...string) Expectation {
	desc := "no verdict for " + e.desc
	if len(because) > 0 {
		desc += " (naming " + strings.Join(because, ", ") + ")"
	}
	value := func(r *validate.Report) tri {
		if e.value(r) != unknown {
			return no
		}
		why := e.explain(r)
		for _, b := range because {
			if !strings.Contains(why, b) {
				return no
			}
		}
		return yes
	}
	return Expectation{desc: desc, check: func(r *validate.Report) bool { return value(r) == yes }, every: true,
		actions: e.actions, invariants: e.invariants, tri: value, undecided: true,
		why: func(r *validate.Report) string {
			switch e.value(r) {
			case yes:
				return "it meets it"
			case no:
				return "it misses it" + e.explain(r)
			}
			return "its explanation is" + e.explain(r)
		}}
}

// maybeCompleted reports whether pending call p of a may have completed
// out of sight: in steps its recorders dropped (a lost tail, a gap after
// it), or not collected yet (a watermark, no settle), as a's Absent says
// (validate.Result.PendingCause). Only a Defect's call is certainly
// pending.
func maybeCompleted(a *validate.Result, p qtrace.Step) bool {
	return a.PendingCause(p) != validate.Defect
}

// Absent: the run was expected to show a step and does not, for this
// cause (validate.Dropped, Uncollected or Defect, as the report's absent
// section says it, validate.Result.Absent): name is a bounded response's
// (binding.Within.Name), an eventual val's (validate.EventuallyName) or a
// pending call's action. It states why a step is missing without
// walking the report: a production trace's late window is
// Absent(name, validate.Uncollected), never a FAIL, and the same window
// with its watermark past it Absent(name, validate.Defect), a FAIL.
func Absent(name string, cause validate.Cause) Expectation {
	check := func(r *validate.Report) bool {
		for _, a := range r.Absent() {
			if a.Name == name && a.Cause == cause {
				return true
			}
		}
		return false
	}
	// two-valued: the absent section decides it, whatever verdict the
	// obligation has (an uncollected within has none, and is Absent)
	value := func(r *validate.Report) tri {
		if check(r) {
			return yes
		}
		return no
	}
	e := Expectation{desc: fmt.Sprintf("%s absent, %s", name, cause), check: check, tri: value,
		why: func(r *validate.Report) string {
			var ws []string
			for _, a := range r.Absent() {
				if a.Name == name {
					ws = append(ws, fmt.Sprintf("it is %s: %s", a.Cause, a.Why))
				}
			}
			if len(ws) == 0 {
				return "the run shows no absence of it"
			}
			return strings.Join(ws, "; ")
		}}
	if strings.HasPrefix(name, "within ") || strings.HasPrefix(name, validate.EventuallyName("")) {
		e.invariants = []string{name}
	} else {
		e.actions = named(name)
	}
	return e
}

// Pending: a call of this action was invoked (qobs.Invoke) and never
// completed ("" matches any): Some(Pending("put")) states a call left
// unanswered, None(Pending("")) the same as All(Completes()). A call whose
// completion may be out of sight (dropped or uncollected, as Completes
// says) is neither pending nor not.
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
		actions: named(action), tri: pending, why: pendingWhy(action)}
}

// pendingWhy says what the report's absent section says of the run's
// pending calls of action ("": any): why each is pending, or may not be.
func pendingWhy(action string) func(*validate.Report) string {
	return func(r *validate.Report) string {
		var ws []string
		for _, a := range r.Absent() {
			if a.Kind == "pending" && (action == "" || a.Name == action) {
				ws = append(ws, fmt.Sprintf("%s: %s", a.Cause, a.Why))
			}
		}
		return strings.Join(ws, "; ")
	}
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
	}, invariants: []string{invariant}, actions: named(action), violates: invariant,
		// what the absent section says of it: why a within or eventual
		// val has no verdict (Undecided(Violates(w), "uncollected"))
		why: func(r *validate.Report) string {
			var ws []string
			for _, a := range r.Absent() {
				if a.Name == invariant {
					ws = append(ws, fmt.Sprintf("%s: %s", a.Cause, a.Why))
				}
			}
			return strings.Join(ws, "; ")
		}}
}

// At narrows Violates(invariant) to a run where the invariant first fails
// after a step of action: Violates("exactlyOnce").At("perform").
func (e Expectation) At(action string) Expectation {
	if e.violates == "" {
		panic(fmt.Sprintf("qtest: %s.At(%q): At narrows Violates(invariant) only; Rejects and Mismatch name their action already", e, action))
	}
	return violatesAt(e.violates, action)
}

// issueKinds are the kinds of error qtrace.Reconstruct (and Linearize)
// report, which HasIssue names.
var issueKinds = []string{"cycle", "duplicate", "gap", "invocation", "lost-prefix", "overlap", "realtime"} // read-only

// HasIssue: the run's steps carry a reconstruction error of this kind
// (qtrace.Issue.Kind: "duplicate" for one recorder numbering two
// different records, "cycle", "overlap", "gap", "invocation",
// "lost-prefix", "realtime"), and the report fails on it alone: every
// actor that is not OK has such an error, so the run has no verdict of
// the model's to read. It states a defect of the input, not of the code
// (two pods recording as one node: grist's pidIdentity mutant). A kind
// no reconstruction reports panics, naming the kinds: a misspelled one
// would make None(HasIssue(...)) hold of every run.
func HasIssue(kind string) Expectation {
	if !slices.Contains(issueKinds, kind) {
		panic(fmt.Sprintf("qtest: HasIssue(%q): no reconstruction error of that kind; the kinds are %s", kind, strings.Join(issueKinds, ", ")))
	}
	has := func(is []qtrace.Issue) bool {
		return slices.ContainsFunc(is, func(i qtrace.Issue) bool { return i.Severity == "error" && i.Kind == kind })
	}
	return Expectation{desc: fmt.Sprintf("a %s error of the steps' reconstruction, and no failure without one", kind), check: func(r *validate.Report) bool {
		runWide := has(r.Issues)
		found := runWide
		for _, a := range r.Actors {
			mine := has(a.Issues)
			if !a.OK() && !mine && !runWide {
				return false // a verdict where the run has none
			}
			found = found || mine
		}
		return found
	}}
}

// Satisfies: f finds nothing wrong with the run's report (nil), where f
// judges what no other expectation names: a framework's claim over its
// steps (grist's gristtest.NoClientErrors over its clients' calls). Its
// error says why a run misses it, and a failure prints it; an error
// wrapping NoVerdict says the report cannot judge it (what it judges was
// not recorded, or not by this binding), and no claim holds by such a
// run: None(e) would otherwise hold of reports that judge nothing. As a
// claim alone it is about every run, as Conforms is; Some, All and None
// quantify it as any other.
func Satisfies(desc string, f func(r *validate.Report) error) Expectation {
	value := func(r *validate.Report) tri {
		switch err := f(r); {
		case err == nil:
			return yes
		case errors.Is(err, NoVerdict):
			return unknown
		}
		return no
	}
	return Expectation{desc: desc, check: func(r *validate.Report) bool { return f(r) == nil }, every: true, tri: value,
		why: func(r *validate.Report) string {
			if err := f(r); err != nil {
				return err.Error()
			}
			return ""
		}}
}

// NoVerdict is what a Satisfies function's error wraps when the report
// cannot judge its expectation (fmt.Errorf("%w: ...", NoVerdict)): the
// run is unjudged for it, not missing it.
var NoVerdict = errors.New("no verdict")

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
	// the parts' explanations, each part's own: a Finishes among them
	// still names the part it misses
	why := func(r *validate.Report) string {
		var ws []string
		for _, e := range es {
			if e.why != nil && e.value(r) != yes {
				if w := e.why(r); w != "" {
					ws = append(ws, w)
				}
			}
		}
		return strings.Join(ws, "; ")
	}
	e := Expectation{desc: describeOr(es), check: func(r *validate.Report) bool { return anyOf(r) == yes }, tri: anyOf, why: why}
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
// (actions) or the checked invariants do not have, each as why it never
// matches, with the name it most likely meant; nil sets skip a check. of
// names whose invariants they are ("the run", "the runs").
func unknownNames(es []Expectation, actions, invariants map[string]bool, of string) []string {
	var out []string
	for _, e := range es {
		// a name of the other kind is said first: a misspelling's
		// suggestion would be of a name the claim did not mean
		for _, a := range e.actions {
			switch {
			case actions == nil || actions[a]:
			case invariants[a]:
				out = append(out, fmt.Sprintf("%s is an invariant, not an action (Violates(%q) names an invariant; Rejects and Mismatch an action: its actions: %s)", a, a, strings.Join(slices.Sorted(maps.Keys(actions)), ", ")))
			default:
				out = append(out, fmt.Sprintf("%s is not an action of the model (%sits actions: %s)", a, didYouMean(a, actions), strings.Join(slices.Sorted(maps.Keys(actions)), ", ")))
			}
		}
		for _, i := range e.invariants {
			switch {
			case invariants == nil || invariants[i]:
			case actions[i]:
				out = append(out, fmt.Sprintf("%s is an action, not an invariant (Rejects(%q) or Mismatch(%q) name an action; Violates an invariant: checked: %s)", i, i, i, strings.Join(slices.Sorted(maps.Keys(invariants)), ", ")))
			case invariants[validate.EventuallyName(i)]:
				// an eventual val's verdict is named apart from an
				// invariant's of the same val
				out = append(out, fmt.Sprintf("%s is an eventual val of %s, judged after a settle: name it Violates(validate.EventuallyName(%q)), %q (checked: %s)", i, of, i, validate.EventuallyName(i), strings.Join(slices.Sorted(maps.Keys(invariants)), ", ")))
			default:
				out = append(out, fmt.Sprintf("%s is not an invariant of %s (%schecked: %s)", i, of, didYouMean(i, invariants), strings.Join(slices.Sorted(maps.Keys(invariants)), ", ")))
			}
		}
	}
	return out
}

// didYouMean is "did you mean x?; " for the name of names nearest to
// name, or "" if none is near.
func didYouMean(name string, names map[string]bool) string {
	if best := nearest.Name(name, slices.Sorted(maps.Keys(names))); best != "" {
		return "did you mean " + best + "?; "
	}
	return ""
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

// decidedFail reports whether an actor of r FAILs: its conformance, or
// an invariant, failed with a verdict (validate's Reason "rejects",
// "mismatch", "violates"), whatever another actor or check has none of.
func decidedFail(r *validate.Report) bool {
	for _, a := range r.Actors {
		if a.Conforms == validate.Fail {
			return true
		}
		for _, inv := range a.Invariants {
			if inv.Verdict == validate.Fail {
				return true
			}
		}
	}
	return false
}

// verdictless reports whether the run has no verdict at all: no actor
// FAILs (decidedFail), and it is undecided,
// or an actor that conforms, violates nothing, and has an invariant with
// no verdict (a lost tail's, hidden actions past their cap) or a gap in
// its recorders' seqs. A mutant is
// not killed by such a run, whose failure is not known.
func verdictless(r *validate.Report) bool {
	if decidedFail(r) {
		return false // a FAIL anywhere is the run's: a mutant it fails is killed
	}
	if undecided(r) {
		return true
	}
	for _, a := range r.Actors {
		if a.Conforms != validate.Pass {
			continue
		}
		unknown := false
		for _, inv := range a.Invariants {
			unknown = unknown || inv.Verdict == validate.NotEvaluated
		}
		// dropped steps beside recorded ones that pass: what was dropped
		// is unknown (validate's "no verdict (dropped steps)")
		if unknown || len(a.Gaps) > 0 {
			return true
		}
	}
	return false
}

// expectReport fails t unless the report meets every expectation,
// printing the report. Names the model (actions, nil: unknown) or the
// checks lack fail first, as names, not as the report's verdicts.
func expectReport(t testing.TB, r *validate.Report, actions, checks map[string]bool, es ...Expectation) {
	t.Helper()
	if r == nil {
		t.Fatalf("gotogether: no report")
	}
	known := checks // the binding's names; a Run made by hand has only its report's
	if known == nil {
		known = reportInvariants(r)
	}
	if u := unknownNames(es, actions, known, "the run"); len(u) > 0 {
		t.Fatalf("gotogether: want %s: %s; a misspelled name never matches", describeAll(es), strings.Join(u, "; "))
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
// the run's step log and model-level ITF (Record's and Validate's),
// copied out of the test's temporary directory, which is removed when the
// test ends. It is how a test states what a mutant must look like:
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
	var actions map[string]bool
	if r.checker != nil && r.checker.Model != nil {
		actions = map[string]bool{}
		for a := range r.checker.Model.Actions {
			actions[a] = true
		}
	}
	if r.Report != nil && r.StepLog != "" {
		// a name the run cannot have is said as one, before any verdict:
		// "want a violation of nonNegativ" read as the code's failure
		known := r.checks
		if known == nil {
			known = reportInvariants(r.Report)
		}
		if u := append(unknownNames(es, actions, known, "the run"), unknownArgs(es, r.checker)...); len(u) > 0 {
			t.Fatalf("gotogether: want %s: %s; a misspelled name never matches", describeAll(es), strings.Join(u, "; "))
		}
		for _, e := range es {
			if v := e.value(r.Report); v != yes {
				itfs := make([]string, 0, len(r.ITF))
				for _, k := range slices.Sorted(maps.Keys(r.ITF)) {
					itfs = append(itfs, r.ITF[k])
				}
				why := e.explain(r.Report)
				if v == unknown {
					why = ": the run has no verdict for it (a lost tail, a search out of its budget)" + why
				}
				t.Fatalf("gotogether: want %s%s, got:\n%s", e, why, r.keep.out(fmt.Sprintf("%s  step log: %s\n  model-level ITF: %s\n",
					r.Report.Summary(), r.StepLog, strings.Join(itfs, ", "))))
			}
		}
	}
	expectReport(t, r.Report, actions, r.checks, es...)
	for _, c := range cs {
		if _, ok := c.(Expectation); !ok && !c.Holds(r) {
			t.Fatalf("gotogether: want %s, got %d faults: %v", c, len(r.Faults()), r.Faults())
		}
	}
	if r.Report != nil && r.StepLog != "" {
		// Record's and Validate's run, as an exploration's summary says
		// its own: a passing Record was silent, and a newcomer suspected
		// nothing had been checked
		t.Logf("gotogether: %s", r.summary(es))
	}
}

// summary is a run's verdict in a line, once its claims held: what it
// recorded and what was checked, or its catch.
func (r *Run) summary(es []Expectation) string {
	steps := 0
	for _, s := range r.Steps {
		if s.Model() {
			steps++
		}
	}
	of := ""
	if n := len(r.Report.Actors); n > 1 {
		of = fmt.Sprintf(" of %d actors", n)
	}
	if !r.Report.OK() {
		return fmt.Sprintf("as expected, %s: %s", describeAll(es), r.Brief())
	}
	n := fmt.Sprintf("%d steps", steps)
	if steps == 1 {
		n = "1 step"
	}
	line := fmt.Sprintf("the run conforms: %s%s", n, of)
	if invs := slices.Sorted(maps.Keys(reportInvariants(r.Report))); len(invs) > 0 {
		line += "; invariants held: " + strings.Join(invs, ", ")
	}
	return line
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
		x.actions, x.steps = map[string]bool{}, stepActions(x.checker.Model, x.checker.Binding)
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

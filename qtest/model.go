package qtest

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/validate"
)

// ModelCoverage says which of the model's actions the runs took: an
// exploration that never took an action says nothing about it, however
// many runs conform.
type ModelCoverage struct {
	// Taken counts, for each model action, the runs that took it.
	Taken map[string]int `json:"taken"`
	// Never are the model's actions no run took (init and step aside).
	Never []string `json:"never,omitempty"`
}

// takenCalls are the calls a run's replay took: every actor's calls up
// to its failure (a step the model could not take was not taken; a step
// whose state did not match was).
func takenCalls(r *Run) []binding.Call {
	if r == nil || r.Report == nil {
		return nil
	}
	var out []binding.Call
	for _, a := range r.Report.Actors {
		calls := a.Calls
		if f := a.Failure; f != nil && a.Conforms != validate.Pass && f.Index >= 0 && f.Index <= len(calls) {
			end := f.Index
			if f.Kind == "state" && end < len(calls) {
				end++
			}
			calls = calls[:end]
		}
		out = append(out, calls...)
	}
	return out
}

// taken returns the model actions a run's replay took (takenCalls).
func taken(r *Run) map[string]bool {
	out := map[string]bool{}
	for _, c := range takenCalls(r) {
		if c.Action != "" {
			out[c.Action] = true
		}
	}
	return out
}

// outcomeOf is a call's step's recorded outcome ("ok" for none); "" for
// an inferred call, which recorded nothing.
func outcomeOf(c binding.Call) string {
	if c.Step == nil {
		return ""
	}
	if c.Step.Outcome == "" {
		return "ok"
	}
	return c.Step.Outcome
}

// takenBy reports whether a run's replay took k: its action, by a step
// of its outcome, with its arguments.
func takenBy(r *Run, k Take) bool {
	for _, c := range takenCalls(r) {
		if k.takes(c) {
			return true
		}
	}
	return false
}

// stepActions are the actions of m a run's step can be: all but init,
// step (the state machine's own) and the frame helpers other actions
// call (binding.Model.Helpers, never a step).
func stepActions(m *binding.Model) map[string]bool {
	out := map[string]bool{}
	for a := range m.Actions {
		if a != "init" && a != "step" && !m.Helpers[a] {
			out[a] = true
		}
	}
	return out
}

func modelCoverage(runs []*Run, actions map[string]bool) *ModelCoverage {
	m := &ModelCoverage{Taken: map[string]int{}}
	for _, r := range runs {
		for a := range taken(r) {
			m.Taken[a]++
		}
	}
	for a := range actions {
		if a != "init" && a != "step" && m.Taken[a] == 0 {
			m.Never = append(m.Never, a)
		}
	}
	sort.Strings(m.Never)
	return m
}

// A Take is a claim that runs took a model action, in at least a share of
// them (Takes): the twin of Reaches for the model's side. A pass in which
// no run cancelled says nothing of cancelling.
type Take struct {
	action  string
	share   float64
	outcome string // a step's recorded outcome the action was taken with ("": any)
	hint    string // how to reach it, said when no run did
	args    []takeArg
}

// a parameter's value a Take wants (With)
type takeArg struct{ param, value string }

// Takes claims that some run took the model action.
func Takes(action string) Take { return Take{action: action} }

// InAtLeast wants the action taken in at least share (0 to 1) of the runs.
func (k Take) InAtLeast(share float64) Take { k.share = share; return k }

// Outcome wants the action taken by a step recorded with outcome o (the
// step's quint.outcome, as the binding's outcomes map it: "ok", a
// refusal's name), not by any: a contrast test claims it reached the
// refusal its mutant removes (Takes("borrow").Outcome("limit")), which a
// borrow that committed does not show.
func (k Take) Outcome(o string) Take { k.outcome = o; return k }

// With wants the action taken with its parameter param at value, a
// Quint literal (`"a"`, `2`, `Set(1, 2)`), compared as a value with the
// argument the binding passed it: Takes("add").With("item", `"a"`).
// Several With want each.
func (k Take) With(param, value string) Take {
	k.args = append(append([]takeArg(nil), k.args...), takeArg{param, value})
	return k
}

// takes reports whether call c takes k: its action, its step's outcome
// if k names one, and each of k's arguments.
func (k Take) takes(c binding.Call) bool {
	if c.Action != k.action || k.outcome != "" && outcomeOf(c) != k.outcome {
		return false
	}
	for _, a := range k.args {
		got, ok := c.Args[a.param]
		if !ok || !sameLiteral(got, a.value) {
			return false
		}
	}
	return true
}

// sameLiteral reports whether two Quint literals are one value (a set's
// order, spaces aside), or, when one is no literal itf reads, the same
// text but for spaces.
func sameLiteral(a, b string) bool {
	x, errA := itf.ParseQuint(a)
	y, errB := itf.ParseQuint(b)
	if errA == nil && errB == nil {
		return x.Equal(y)
	}
	return strings.Join(strings.Fields(a), "") == strings.Join(strings.Fields(b), "")
}

// Hint is how to reach the action, said when no run did (what the
// workload must send, a fault to turn on): a claim built for a whole
// catalog (TakesEach) names the fix of each block it misses.
func (k Take) Hint(how string) Take { k.hint = how; return k }

// Name is the action, then the outcome after a dot if Outcome gave one
// ("borrow.limit"): how TakesEach's lists and its builders' exceptions
// name a Take.
func (k Take) Name() string {
	if k.outcome == "" {
		return k.action
	}
	return k.action + "." + k.outcome
}

func (k Take) what() string {
	w := k.action
	if len(k.args) > 0 {
		parts := make([]string, len(k.args))
		for i, a := range k.args {
			parts[i] = a.param + " = " + a.value
		}
		w += "(" + strings.Join(parts, ", ") + ")"
	}
	if k.outcome == "" {
		return w
	}
	return w + " with outcome " + k.outcome
}

func (k Take) String() string {
	if k.share > 0 {
		return fmt.Sprintf("taking %s in at least %.0f%% of the runs", k.what(), 100*k.share)
	}
	return "some run taking " + k.what()
}

func (k Take) judge([]*validate.Report) judgment { return judgment{show: -1} }

// unknown says why k names nothing of the model ("": it names an
// action, and a parameter of it for each With; m nil: its parameters
// unknown).
func (k Take) unknown(actions map[string]bool, m *binding.Model) string {
	if actions != nil && !actions[k.action] {
		return fmt.Sprintf("%s is not an action of the model (%sits actions: %s); a misspelled name never matches", k.action, didYouMean(k.action, actions), strings.Join(slices.Sorted(maps.Keys(actions)), ", "))
	}
	return k.unknownWith(m)
}

// unknownWith says which parameter of k's With its action lacks in m
// ("": none, or m nil or without the action): a With of no parameter
// never matches, so a claim of it would hold or fail for nothing
// (None of it always holds).
func (k Take) unknownWith(m *binding.Model) string {
	if m == nil {
		return ""
	}
	ps, ok := m.Actions[k.action]
	if !ok {
		return ""
	}
	names := map[string]bool{}
	for _, p := range ps {
		names[p.Name] = true
	}
	for _, a := range k.args {
		if !names[a.param] {
			return fmt.Sprintf("%s has no parameter %s (%ssignature %s); a misspelled parameter never matches", k.action, a.param, didYouMean(a.param, names), m.Signature(k.action))
		}
	}
	return ""
}

// unknownArgs says, for each Take of es's scenarios, which With
// parameter its action lacks in c's model (c nil: unknown).
func unknownArgs(es []Expectation, c *validate.Checker) []string {
	if c == nil || c.Model == nil {
		return nil
	}
	var out []string
	for _, e := range es {
		if e.scenario == nil {
			continue
		}
		for _, k := range e.scenario.steps {
			if u := k.unknownWith(c.Model); u != "" {
				out = append(out, u)
			}
		}
	}
	return out
}

// modelOf is the model the runs were checked against (nil: none known).
func modelOf(runs []*Run) *binding.Model {
	for _, r := range runs {
		if r != nil && r.checker != nil && r.checker.Model != nil {
			return r.checker.Model
		}
	}
	return nil
}

// count is how many runs took k, and how many must.
func (k Take) count(runs []*Run, m *ModelCoverage) (n, need int) {
	need = 1
	if k.share > 0 {
		need = int(k.share*float64(len(runs)) + 0.999999)
	}
	if k.outcome == "" && len(k.args) == 0 {
		return m.Taken[k.action], need
	}
	for _, r := range runs {
		if takenBy(r, k) {
			n++
		}
	}
	return n, need
}

func (k Take) judgeRuns(runs []*Run, actions map[string]bool) string {
	if u := k.unknown(actions, modelOf(runs)); u != "" {
		return k.String() + ": " + u
	}
	m := modelCoverage(runs, actions)
	if n, need := k.count(runs, m); n < need {
		how := "Drive the system to it (more seeds, a longer run, a workload that asks for it) or say why it is not expected"
		if k.hint != "" {
			how = k.hint
		}
		return fmt.Sprintf("want %s; %d of %d runs did (the actions the runs took: %s). %s", k, n, len(runs), takenList(m), how)
	}
	return ""
}

// A TakeEach is the claim TakesEach: every one of its Takes holds.
type TakeEach struct{ takes []Take }

// TakesEach claims that every one of takes holds (each Take's own share
// and outcome), naming every one that does not, each with its Hint: one
// claim for a list of what an exploration must have reached, such as
// every block of an application's catalog by each of its outcomes
// (gristtest.Exercised), where a list of separate claims stops at the
// first missed. Exercised is the model-wide twin, over every action and
// fault; this one asks for the takes it is given, so a model's helpers
// and the outcomes a test does not care for are not wanted.
func TakesEach(takes ...Take) TakeEach { return TakeEach{takes: append([]Take(nil), takes...)} }

func (e TakeEach) String() string {
	names := make([]string, len(e.takes))
	for i, k := range e.takes {
		names[i] = k.Name()
	}
	return "each of " + strings.Join(names, ", ") + " taken"
}

func (e TakeEach) judge([]*validate.Report) judgment { return judgment{show: -1} }

func (e TakeEach) judgeRuns(runs []*Run, actions map[string]bool) string {
	var bad, missed []string
	m := modelCoverage(runs, actions)
	model := modelOf(runs)
	for _, k := range e.takes {
		if u := k.unknown(actions, model); u != "" {
			bad = append(bad, u)
			continue
		}
		if n, need := k.count(runs, m); n < need {
			line := fmt.Sprintf("%s (%d of %d runs", k.what(), n, len(runs))
			if need > 1 {
				line += fmt.Sprintf(", want %d", need)
			}
			line += ")"
			if k.hint != "" {
				line += ": " + k.hint
			}
			missed = append(missed, line)
		}
	}
	switch {
	case len(bad) > 0:
		return fmt.Sprintf("want %s; %s", e, strings.Join(bad, "; "))
	case len(missed) > 0:
		return fmt.Sprintf("want %s; %d of %d not taken:\n  %s\n(the actions the runs took: %s)", e, len(missed), len(e.takes), strings.Join(missed, "\n  "), takenList(m))
	}
	return ""
}

// An Exercise is the claim Exercised: every model action taken, every
// option of every fault kind the runs reached taken, and every nemesis
// they enabled fired, in some run, but those it excepts.
type Exercise struct{ except map[string]bool }

// Exercised claims that the runs exercised everything: every model action
// was taken, every option of every fault kind they reached was taken
// (each by name: a drop does not stand for a duplicate), and every nemesis
// a run enabled fired (each by name: one process's crash does not stand
// for another's), in some run. except names the actions, kinds, options
// ("rpc.payments.lostResponse") and nemeses ("nemesis.crash.payments")
// not expected, a name covering those below it ("nemesis.partition"):
// each a decision the test states.
func Exercised(except ...string) Exercise {
	e := Exercise{except: map[string]bool{}}
	for _, x := range except {
		if strings.HasPrefix(x, "fault.") {
			panic(fmt.Sprintf("qtest: Exercised(%q): name the kind without the \"fault.\" prefix (%q), as reports print it", x, strings.TrimPrefix(x, "fault.")))
		}
		e.except[x] = true
	}
	return e
}

func (e Exercise) String() string {
	const all = "every model action taken, every fault option taken and every nemesis fired"
	if len(e.except) == 0 {
		return all
	}
	ex := slices.Sorted(maps.Keys(e.except))
	return all + ", but " + strings.Join(ex, ", ")
}

func (e Exercise) judge([]*validate.Report) judgment { return judgment{show: -1} }

// excepted reports whether the except list covers name: name itself, or a
// prefix of it at a dot.
func (e Exercise) excepted(name string) bool {
	for x := range e.except {
		if name == x || strings.HasPrefix(name, x+".") {
			return true
		}
	}
	return false
}

func (e Exercise) judgeRuns(runs []*Run, actions map[string]bool) string {
	if actions == nil {
		return fmt.Sprintf("want %s, but the model's actions are unknown (no binding's model)", e)
	}
	var missing []string
	for _, a := range modelCoverage(runs, actions).Never {
		if !e.excepted(a) {
			missing = append(missing, "action "+a+" (never taken)")
		}
	}
	known, never := exercisedFaults(runs)
	for _, n := range never {
		if !e.excepted(n.name) {
			missing = append(missing, n.what)
		}
	}
	for x := range e.except {
		ok := actions[x]
		for _, r := range known {
			ok = ok || r == x || strings.HasPrefix(r, x+".")
		}
		known := ok || knownFault(x, runs) // declared, though no run met it (a sweep row with it off)
		if !known {
			missing = append(missing, x+" (excepted, but neither a model action, a declared fault kind or option, nor a nemesis a run enabled: misspelled?)")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Sprintf("want %s; not exercised: %s. Drive the runs to them (rates, seeds, workload), or except "+
			"them with the reason", e, strings.Join(missing, "; "))
	}
	return ""
}

// unexercised is a fault option or nemesis the runs reached and never
// took: its name as Exercised excepts it, and what the claim says of it.
type unexercised struct{ name, what string }

// exercisedFaults are the names an exception may give (every fault kind
// declared or met, each declared option, each nemesis a run enabled:
// known, so that one exception list serves runs that reach less, a sweep
// row with a kind off), and those the runs reached and never took. A
// declared kind's options count by name, those a choice offered; a kind
// of no declaration (its options unnamed) as a whole; a nemesis by name.
// A heal is not wanted: a cut heals by itself (Network.CutLength), so its
// nemesis rarely fires, and healing is the partition's end, not a fault.
func exercisedFaults(runs []*Run) (known []string, never []unexercised) {
	offers := map[string]int{} // a kind's most options at one choice: a call of a prefix (Only) offers fewer
	for _, r := range runs {
		for _, c := range r.Choices {
			if k := strings.TrimPrefix(c.Kind, "fault."); k != c.Kind {
				offers[k] = max(offers[k], c.N)
			}
		}
	}
	for _, p := range (&Exploration{Runs: runs}).faultCoverage().Points {
		if strings.HasPrefix(p.Kind, "nemesis.") {
			continue // by name, below
		}
		known = append(known, p.Kind)
		for _, name := range p.Names {
			known = append(known, p.Kind+"."+name)
		}
		if p.Reached == 0 {
			continue // a declared kind no run reached is Unreached's
		}
		if len(p.Names) == 0 {
			if p.Faulted() == 0 {
				never = append(never, unexercised{p.Kind, "fault " + p.Kind + " (reached, never faulted)"})
			}
			continue
		}
		for i, name := range p.Names {
			if i+1 >= offers[p.Kind] {
				break // no choice offered it
			}
			if i+1 >= len(p.Injected) || p.Injected[i+1] == 0 {
				never = append(never, unexercised{p.Kind + "." + name, "fault " + p.Kind + " option " + name + " (reached, never taken)"})
			}
		}
	}
	fired := map[string]bool{}
	var offered []string
	seen := map[string]bool{}
	for _, r := range runs {
		for _, c := range r.Choices {
			if strings.HasPrefix(c.Kind, "fault.nemesis") && c.Value > 0 && c.Name != "" {
				fired[c.Name] = true
			}
		}
		for _, n := range r.Nemeses {
			if !seen[n] {
				seen[n] = true
				offered = append(offered, n)
			}
		}
	}
	for _, n := range offered {
		known = append(known, "nemesis."+n)
		if !fired[n] && !strings.HasPrefix(n, "heal.") {
			never = append(never, unexercised{"nemesis." + n, "nemesis " + n + " (enabled, never fired)"})
		}
	}
	return known, never
}

// neverList is the summary's list of the actions no run took: first
// those the binding names (bound: a grist binding names every block of
// the app's catalog), every one of them, then the model's others (the
// helpers other actions call: appends, readsThrough), at most 8. One list
// once buried an app's processor among the helpers no run of it ever
// lacks, and cut it off ("and 1 more").
func neverList(never []string, bound map[string]bool) string {
	var own, other []string
	for _, a := range never {
		if bound[a] {
			own = append(own, a)
		} else {
			other = append(other, a)
		}
	}
	short := func(as []string) string {
		if len(as) <= 8 {
			return strings.Join(as, ", ")
		}
		return fmt.Sprintf("%s and %d more", strings.Join(as[:8], ", "), len(as)-8)
	}
	switch {
	case len(never) == 0:
		return ""
	case len(own) == 0 && len(bound) == 0:
		return " (never: " + short(other) + ")"
	case len(own) == 0:
		return " (never, of the binding's actions: none; of the model's others: " + short(other) + ")"
	case len(other) == 0:
		return " (never, of the binding's actions: " + strings.Join(own, ", ") + ")"
	}
	return " (never, of the binding's actions: " + strings.Join(own, ", ") + "; of the model's others: " + short(other) + ")"
}

func takenList(m *ModelCoverage) string {
	as := slices.Sorted(maps.Keys(m.Taken))
	if len(as) == 0 {
		return "none"
	}
	return strings.Join(as, ", ")
}

package qtest

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Scenario claims (P4): a run whose steps take each of a sequence of
// Takes in turn, any other steps between them: a domain expert's
// acceptance criterion ("a submitted order, verified ok, then opened")
// stated over the model's actions, judged over runs like any
// expectation (Some, All, None), and checked possible in the model on
// its own (Possible) before any code runs.

// a scenario: its name ("" for none) and steps
type scenario struct {
	name  string
	steps []Take
}

// Scenario is the expectation that a run's steps take each of steps in
// turn (the model's actions, as Takes, Outcome and With name them), any
// others between: Scenario(Takes("submit"), Takes("verify").Outcome("ok"),
// Takes("open")). Alone, or under Some, it wants one run that does; All
// every run; None no run. A run that misses it is reported with the
// longest prefix of the steps it took, the step where it stopped, and
// the next step's near misses (taken with another outcome or other
// arguments); a claim no run meets says too whether the model on its
// own can follow it, with a witness. A run's actors' steps are taken in
// the order of their times.
func Scenario(steps ...Take) Expectation { return NamedScenario("", steps...) }

// NamedScenario is Scenario, named in reports as name.
func NamedScenario(name string, steps ...Take) Expectation {
	if len(steps) == 0 {
		panic("qtest.Scenario: no steps: a scenario is at least one Takes")
	}
	for _, k := range steps {
		if k.share > 0 {
			panic(fmt.Sprintf("qtest.Scenario: step %s has InAtLeast, a claim's share over runs: say it of the scenario (Some, All), not of its step", k.what()))
		}
	}
	sc := &scenario{name: name, steps: append([]Take(nil), steps...)}
	var names []string
	for _, k := range steps {
		names = append(names, k.action)
	}
	return Expectation{desc: sc.String(), actions: names, scenario: sc,
		check: func(r *validate.Report) bool { n, _ := sc.match(runCalls(r)); return n == len(sc.steps) },
		why:   func(r *validate.Report) string { return "  " + sc.miss(runCalls(r)) + "\n" }}
}

func (sc *scenario) String() string {
	parts := make([]string, len(sc.steps))
	for i, k := range sc.steps {
		parts[i] = k.what()
	}
	if sc.name != "" {
		return fmt.Sprintf("the scenario %q (%s)", sc.name, strings.Join(parts, " -> "))
	}
	return "the scenario " + strings.Join(parts, " -> ")
}

// runCalls are a run's calls the replay took (takenCalls), its actors'
// merged by their steps' times (an inferred call keeps its place after
// its actor's previous).
func runCalls(r *validate.Report) []binding.Call {
	if r == nil {
		return nil
	}
	type at struct {
		c     binding.Call
		actor int
		i     int
	}
	var all []at
	for ai, a := range r.Actors {
		one := &Run{Report: &validate.Report{Actors: []*validate.Result{a}}}
		for i, c := range takenCalls(one) {
			all = append(all, at{c, ai, i})
		}
	}
	last := map[int]int64{}
	times := make([]int64, len(all))
	for i, x := range all {
		if x.c.Step != nil && !x.c.Step.Time.IsZero() {
			last[x.actor] = x.c.Step.Time.UnixNano()
		}
		times[i] = last[x.actor]
	}
	idx := make([]int, len(all))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return times[idx[a]] < times[idx[b]] })
	out := make([]binding.Call, len(all))
	for i, j := range idx {
		out[i] = all[j].c
	}
	return out
}

// match is how far calls follow the scenario: the longest prefix of its
// steps they take in turn (taking each step at its first call after the
// previous: the longest, for a subsequence) and, for each step matched,
// the index of its call.
func (sc *scenario) match(calls []binding.Call) (n int, at []int) {
	for i, c := range calls {
		if n < len(sc.steps) && sc.steps[n].takes(c) {
			at = append(at, i)
			n++
		}
	}
	return n, at
}

// miss says how a run's calls miss the scenario: the prefix taken, each
// at its step, where it stopped, and the next step's near misses.
func (sc *scenario) miss(calls []binding.Call) string {
	n, at := sc.match(calls)
	if n == len(sc.steps) {
		return fmt.Sprintf("%s: taken in full", sc)
	}
	var b strings.Builder
	if n == 0 {
		fmt.Fprintf(&b, "none of %s taken: no step of the run's %d takes %s", sc, len(calls), sc.steps[0].what())
	} else {
		parts := make([]string, n)
		for i := range n {
			parts[i] = fmt.Sprintf("%s at step %d", sc.steps[i].what(), at[i]+1)
		}
		fmt.Fprintf(&b, "%d of %d steps of %s taken (%s); stuck at %s: no step after step %d of the run's %d takes it",
			n, len(sc.steps), sc, strings.Join(parts, ", "), sc.steps[n].what(), at[n-1]+1, len(calls))
	}
	from := 0
	if n > 0 {
		from = at[n-1] + 1
	}
	next := sc.steps[n]
	var near []string
	for i := from; i < len(calls) && len(near) < 3; i++ {
		if c := calls[i]; c.Action == next.action {
			near = append(near, fmt.Sprintf("step %d %s with outcome %s", i+1, c.Quint, outcomeOf(c)))
		}
	}
	if len(near) > 0 {
		fmt.Fprintf(&b, " (%s taken otherwise: %s)", next.action, strings.Join(near, "; "))
	}
	if next.hint != "" {
		b.WriteString(". " + next.hint)
	}
	return b.String()
}

// scenarios are the scenarios claim c names.
func scenarios(c Claim) []*scenario {
	var es []Expectation
	switch c := c.(type) {
	case Expectation:
		es = []Expectation{c}
	case quantified:
		if c.kind == "none" {
			return nil // a scenario no run may show: what the model allows is beside the point
		}
		es = c.es
	}
	var out []*scenario
	for _, e := range es {
		if e.scenario != nil {
			out = append(out, e.scenario)
		}
	}
	return out
}

// scenarioMisses are the lines a failing claim's scenarios add: per run
// (the first maxRuns), how far it followed each, and whether the model
// on its own can follow it (checker nil: not asked).
func (x *Exploration) scenarioMisses(c Claim) string {
	scs := scenarios(c)
	if len(scs) == 0 {
		return ""
	}
	const maxRuns = 10
	var b strings.Builder
	for _, sc := range scs {
		fmt.Fprintf(&b, "\n%s, per run:", sc)
		for i, r := range x.Runs {
			if i == maxRuns {
				fmt.Fprintf(&b, "\n  (%d runs more)", len(x.Runs)-maxRuns)
				break
			}
			fmt.Fprintf(&b, "\n  %s: %s", x.nameOf(r), sc.miss(runCalls(r.Report)))
		}
		if x.checker != nil {
			fmt.Fprintf(&b, "\n  the model on its own: %s", modelVerdict(x.checker, sc))
		}
	}
	return b.String()
}

// modelVerdict is whether the model on its own follows sc, as a line.
func modelVerdict(c *validate.Checker, sc *scenario) string {
	r, err := scenarioPossible(c, sc)
	switch {
	case err != nil:
		return "not searched (" + err.Error() + ")"
	case r.Found:
		return r.Verdict() + ": the system is to be driven there (the workload, the faults, the seeds)"
	case r.Complete:
		return r.Verdict() + ": the scenario names what the model forbids (a wrong step, or a model too strict)"
	}
	return r.Verdict()
}

// scenarioPossible searches c's model for sc with the Go evaluator: each
// step's action, its arguments, and its outcome where the binding passes
// the step's outcome to a parameter (args: { result: '{{.outcome}}' }),
// as the binding's outcomes map it. An outcome the binding passes to no
// parameter is no search: the model's step would be looked for with any
// outcome, and found where the outcome asked for is impossible.
func scenarioPossible(c *validate.Checker, sc *scenario) (validate.PossibleResult, error) {
	p, err := scenarioAsPossible(c.Binding, sc)
	if err != nil {
		return validate.PossibleResult{}, err
	}
	search := validate.PossibleSearch{}
	if sim := c.Binding.Simulate; sim != nil {
		search.States = sim.SearchStates
		search.Constants = constantsOf(sim.Constants)
	}
	rs, err := c.Possible([]binding.Possible{p}, search)
	if err != nil {
		return validate.PossibleResult{}, err
	}
	return rs[0], nil
}

// scenarioAsPossible is sc as a possible of b's model, as
// scenarioPossible searches it.
func scenarioAsPossible(b *binding.Binding, sc *scenario) (binding.Possible, error) {
	var steps []string
	for _, k := range sc.steps {
		var args []string
		for _, a := range k.args {
			args = append(args, a.param+" = "+a.value)
		}
		if k.outcome != "" {
			p, v, ok := outcomeParam(b, k.action, k.outcome)
			if !ok {
				return binding.Possible{}, fmt.Errorf("step %s: the binding passes %s's outcome to none of its parameters (args: { p: '{{.outcome}}' }), so the model's steps do not say it: "+
					"drop the Outcome to search for the action alone, or claim the scenario of runs only", k.what(), k.action)
			}
			args = append(args, p+" = "+v)
		}
		s := k.action
		if len(args) > 0 {
			s += "(" + strings.Join(args, ", ") + ")"
		}
		steps = append(steps, s)
	}
	return binding.Possible{Name: sc.String(), Steps: steps}, nil
}

// constantsOf are cs as NAME=expr.
func constantsOf(cs map[string]string) []string {
	var out []string
	for k, v := range cs {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// outcomeParam is the parameter of action that b passes a step's outcome
// ({{.outcome}}), and outcome o's value there: the binding's outcome map
// (the action's own first), else o as a string.
func outcomeParam(b *binding.Binding, action, o string) (param, value string, ok bool) {
	ab := b.Actions[action]
	for _, p := range slices.Sorted(maps.Keys(ab.Args)) {
		if strings.ReplaceAll(ab.Args[p], " ", "") == "{{.outcome}}" {
			param, ok = p, true
			break
		}
	}
	if !ok {
		return "", "", false
	}
	if v, found := ab.Outcomes[o]; found {
		if v == "skip" || v == "call" {
			// a skipped step is no model step; a call's is the ok one's
			return "", "", false
		}
		return param, v, true
	}
	if v, found := b.Outcomes[o]; found {
		return param, v, true
	}
	return param, strconv.Quote(o), true
}

// Possible fails t unless the model of binding (a binding file) on its
// own can follow each of scenarios, each searched with the Go evaluator
// from init by the model's step: the model-first check of an acceptance
// criterion (a scenario a model forbids, no conforming run can show).
// Each found is logged with its witness; one not found is a failure
// naming the search's extent (only a complete search says impossible).
func Possible(t testing.TB, binding string, scenarios ...Expectation) {
	t.Helper()
	RequireQuint(t)
	c, err := validate.NewChecker(binding, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, e := range scenarios {
		if e.scenario == nil {
			t.Fatalf("qtest.Possible: %s is no scenario (qtest.Scenario, gristtest.Scenario)", e)
		}
		r, err := scenarioPossible(c, e.scenario)
		switch {
		case err != nil:
			t.Errorf("%s: not searched: %v. Without the Go evaluator's search the model is not checked here: claim the scenario of runs (Explore(...).Expect(t, Some(scenario))), "+
				"and state its actions in turn as a simulate.possible of the binding (steps: [a, b]), which gotogether simulate samples with quint", e.scenario, err)
		case r.Found:
			t.Logf("%s: possible in the model, %s", e.scenario, r.Verdict())
		case r.Complete:
			t.Errorf("%s: the model on its own cannot follow it, %s: a step names what the model forbids, or the model is too strict for any conforming run to show it "+
				"(gotogether nav -binding %s shows each disabled action with why)", e.scenario, r.Verdict(), binding)
		default:
			t.Errorf("%s: %s: raise the binding's simulate.searchStates, or check its steps' order", e.scenario, r.Verdict())
		}
	}
}

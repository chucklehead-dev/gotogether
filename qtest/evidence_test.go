package qtest

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// One report for an exploration: verdicts by reason, faults reached and
// faulted, pairs of kinds together.
func TestEvidence(t *testing.T) {
	t.Parallel()
	ok := &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass}}}
	violates := &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass,
		Invariants: []validate.InvariantResult{{Name: "chargedAtMostOnce", Verdict: validate.Fail}}}}}
	rejects := &validate.Report{Actors: []*validate.Result{{Conforms: validate.Fail,
		Failure: &validate.Failure{Kind: "disabled", Step: &qtrace.Step{Action: "ship"}}}}}
	both := []qenv.Choice{{Kind: "fault.a", N: 2, Value: 1}, {Kind: "fault.b", N: 2, Value: 1}}
	x := &Exploration{Runs: []*Run{
		{Report: ok, Choices: both},
		{Report: violates, Choices: []qenv.Choice{{Kind: "fault.a", N: 2, Value: 1}}},
		{Report: rejects, Choices: []qenv.Choice{{Kind: "fault.c", N: 2}}},
	}}
	s := x.Evidence().String()
	for _, want := range []string{"3 runs", "1 conform · 1 rejects ship · 1 violates chargedAtMostOnce",
		"2 of 3 kinds faulted", "pairs 1/1 together", "never faulted: c"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in:\n%s", want, s)
		}
	}
	t.Log(s)
}

// An exploration's summary counts the model's actions a step can be
// (init, step and frame helpers aside) and names those no run took: a
// count alone left a reader guessing which actions were missing, and
// counted helpers no run can ever take.
func TestSummaryNamesUntakenActions(t *testing.T) {
	t.Parallel()
	m := &binding.Model{Actions: map[string][]binding.Param{"init": nil, "step": nil, "put": nil, "get": nil, "appends": nil, "same": nil},
		Helpers: map[string]bool{"same": true}}
	ok := &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass, Calls: []binding.Call{{Action: "put"}}}}}
	x := &Exploration{Runs: []*Run{{Report: ok}}, steps: stepActions(m), given: true}
	if s, want := x.summary(), "model actions taken 1 of 3 (never: appends, get)"; !strings.Contains(s, want) {
		t.Errorf("summary %q: want %q", s, want)
	}
	if s := x.Evidence().String(); !strings.Contains(s, "1 of 3 actions taken; never: appends, get") {
		t.Errorf("evidence:\n%s\nwant 1 of 3 actions taken, appends and get never", s)
	}
}

// The summary names the untaken actions the binding names apart from the
// model's others, and never cuts the binding's short: a grist app's
// processor once hid among the helpers, after "and 1 more".
func TestSummarySeparatesTheBindingsActions(t *testing.T) {
	t.Parallel()
	never := []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "h1", "h2", "h3", "h4", "h5", "h6", "h7", "h8", "h9"}
	bound := map[string]bool{}
	for _, a := range never[:9] {
		bound[a] = true
	}
	if s, want := neverList(never, bound), " (never, of the binding's actions: a1, a2, a3, a4, a5, a6, a7, a8, a9; of the model's others: h1, h2, h3, h4, h5, h6, h7, h8 and 1 more)"; s != want {
		t.Errorf("neverList:\n%s\nwant\n%s", s, want)
	}
	if s, want := neverList([]string{"get"}, nil), " (never: get)"; s != want {
		t.Errorf("no binding's actions: %q, want %q", s, want)
	}
	if s, want := neverList([]string{"appends"}, map[string]bool{"put": true}), " (never, of the binding's actions: none; of the model's others: appends)"; s != want {
		t.Errorf("%q, want %q", s, want)
	}
}

// Exercised asks for the model's steps: a frame condition (same) no run
// can take is not missing, an untaken step is.
func TestExercisedSkipsFrameConditions(t *testing.T) {
	t.Parallel()
	m := &binding.Model{Actions: map[string][]binding.Param{"init": nil, "step": nil, "put": nil, "get": nil, "same": nil},
		Helpers: map[string]bool{"same": true}}
	run := func(actions ...string) *Run {
		var calls []binding.Call
		for _, a := range actions {
			calls = append(calls, binding.Call{Action: a})
		}
		return &Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass, Calls: calls}}}}
	}
	all := map[string]bool{}
	for a := range m.Actions {
		all[a] = true
	}
	x := &Exploration{Runs: []*Run{run("put"), run("get")}, actions: all, steps: stepActions(m), given: true}
	if v := x.verdict(Exercised(), nil); !v.ok {
		t.Errorf("Exercised over runs taking put and get: %s; want it to hold (same is a frame condition)", v.say(t))
	}
	x.Runs = x.Runs[:1]
	if v := x.verdict(Exercised(), nil); v.ok || !strings.Contains(v.say(t), "action get (never taken)") {
		t.Errorf("Exercised over a run taking put alone held or named the wrong action: %q", v.say(t))
	}
}

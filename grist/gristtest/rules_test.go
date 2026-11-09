package gristtest_test

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

type held struct {
	Item string `json:"item" grist:"tag"`
}

// guarded is an App whose hold command is guarded by a rule refusing a
// third hold of an item, and whose rule on another event guards no
// command (a processor emits it).
func guarded() *grist.App {
	a := grist.New("holds")
	ev := grist.NewEvent[held](a, "held")
	other := grist.NewEvent[held](a, "released")
	full := grist.Rejection("full")
	grist.NewCommand(a, "hold", func(r *grist.Reads, c note) ([]grist.Event, error) {
		return []grist.Event{ev.Of(held(c))}, nil
	}, grist.Appends(ev), grist.Authorize(grist.Authenticated))
	grist.NewRule(a, "two-holds", ev, func(r *grist.Reads, e held) error {
		if len(grist.Events(r, ev, grist.Tag("item", e.Item))) >= 2 {
			return full
		}
		return nil
	}, grist.Refuses(full))
	grist.NewProcessor(a, "release", ev, nil, grist.Emits(other))
	grist.NewRule(a, "quiet", other, func(r *grist.Reads, e held) error { return nil }, grist.Refuses(full))
	return a
}

// The catalog's Exercised wants each refusal a command's rules add, its
// hint naming the rule; RuleMutants makes a generated mutant of each rule guarding a
// command, none of one that guards none.
func TestRules(t *testing.T) {
	a := guarded()
	if err := a.Verify(); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, k := range gristtest.Catalog(a).Takes {
		names = append(names, k.Name())
	}
	runs := qtest.Runs(&qtest.Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass,
		Calls: []binding.Call{{Action: "init"}, {Action: "hold", Step: &qtrace.Step{Action: "hold", Outcome: "ok"}}}}}}})
	if msgs := run(t, func(tb testing.TB) { runs.Expect(tb, gristtest.Catalog(a).Exercised("release")) }); len(msgs) != 1 || !strings.Contains(msgs[0], "no hold was refused full by its rules (two-holds)") {
		t.Errorf("a run without the rule's refusal: %q, want it named with the rule", msgs)
	}
	if got := strings.Join(names, " "); !strings.HasPrefix(got, "hold.ok hold.full ") {
		t.Errorf("the catalog's takes %q: want hold by ok and by its rule's full", got)
	}
	if got := gristtest.RuleMutants(a); len(got) != 1 || got[0] != "drop-two-holds" {
		t.Errorf("generated mutants %v: want [drop-two-holds]", got)
	}
	if _, _, ok := gristtest.DroppedRule(a); ok {
		t.Errorf("DroppedRule in the app's own binary: ok")
	}
}

// A generic Rejects would accept a missing-read failure. A generated rule
// mutant's claim needs the specific guard outcome after an ok decision instead.
func TestDroppedRuleRequiresGuardRefusal(t *testing.T) {
	t.Setenv(qtest.MutantEnv, "drop-two-holds")
	_, caught, ok := gristtest.DroppedRule(guarded())
	if !ok {
		t.Fatal("no generated rule claim")
	}
	makeRun := func() *qtest.Run {
		return &qtest.Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Fail, Failure: &validate.Failure{
			Kind: "disabled", Step: &qtrace.Step{Action: "hold", Outcome: "ok"}, Why: &validate.Explanation{Conditions: []validate.Condition{{Expr: "result == verdict_.outcome", Values: []validate.NamedValue{
				{Name: "result", Value: `"ok"`}, {Name: "verdict_", Value: `{ decided: "ok", outcome: "full", rule: "two-holds", needs: Set() }`},
			}}}},
		}}}}}
	}
	tests := []struct {
		name     string
		edit     func(*validate.Result)
		accepted bool
	}{
		{"guard-refusal", func(*validate.Result) {}, true},
		{"same-refusal-other-rule", func(a *validate.Result) {
			a.Failure.Why.Conditions[0].Values[1].Value = `{ decided: "ok", outcome: "full", rule: "other-guard", needs: Set() }`
		}, false},
		{"missing-rule-origin", func(a *validate.Result) {
			a.Failure.Why.Conditions[0].Values[1].Value = `{ decided: "ok", outcome: "full", needs: Set() }`
		}, false},
		{"missing-verdict", func(a *validate.Result) {
			a.Failure.Why.Conditions[0].Values = a.Failure.Why.Conditions[0].Values[:1]
		}, false},
		{"malformed-verdict", func(a *validate.Result) {
			a.Failure.Why.Conditions[0].Values[1].Value = "{ broken"
		}, false},
		{"duplicate-verdict", func(a *validate.Result) {
			c := &a.Failure.Why.Conditions[0]
			c.Values = append(c.Values, c.Values[1])
		}, false},
		{"empty-rule-origin", func(a *validate.Result) {
			a.Failure.Why.Conditions[0].Values[1].Value = `{ decided: "ok", outcome: "full", rule: "", needs: Set() }`
		}, false},
		{"model-action-name", func(a *validate.Result) { a.Failure.Step.Action = "hold-raw"; a.Failure.Call = "hold(1)" }, true},
		{"read-pinning", func(a *validate.Result) {
			a.Failure.Why.Conditions[0].Expr = "verdict_.needs.forall(n => covers(reads_, n))"
		}, false},
		{"other-command", func(a *validate.Result) { a.Failure.Step.Action = "release" }, false},
		{"recorded-refusal", func(a *validate.Result) { a.Failure.Step.Outcome = "full" }, false},
		{"decision-refusal", func(a *validate.Result) {
			a.Failure.Why.Conditions[0].Values[1].Value = `{ decided: "invalid", outcome: "full", rule: "two-holds", needs: Set() }`
		}, false},
		{"other-refusal", func(a *validate.Result) {
			a.Failure.Why.Conditions[0].Values[1].Value = `{ decided: "ok", outcome: "other", rule: "two-holds", needs: Set() }`
		}, false},
		{"state-mismatch", func(a *validate.Result) { a.Failure.Kind = "state" }, false},
		{"no-explanation", func(a *validate.Result) { a.Failure.Why = nil }, false},
		{"no-verdict", func(a *validate.Result) { a.Conforms = validate.NotEvaluated }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := makeRun()
			tc.edit(r.Report.Actors[0])
			capture := &recTB{TB: t}
			done := make(chan struct{})
			go func() {
				defer close(done)
				qtest.Runs(r).Expect(capture, caught)
			}()
			<-done
			msgs := capture.msgs
			if (len(msgs) == 0) != tc.accepted {
				t.Fatalf("claim errors %v, accepted=%t", msgs, tc.accepted)
			}
			if tc.name == "read-pinning" {
				if msgs := run(t, func(tb testing.TB) { qtest.Runs(r).Expect(tb, qtest.Some(qtest.Rejects("hold"))) }); len(msgs) != 0 {
					t.Fatalf("generic rejection contrast: %v", msgs)
				}
			}
			text := strings.Join(append(append([]string{}, capture.msgs...), capture.logs...), "\n")
			stale := tc.name == "missing-rule-origin" || tc.name == "missing-verdict" || tc.name == "malformed-verdict" || tc.name == "duplicate-verdict"
			if stale && (!strings.Contains(text, "no verdict") || !strings.Contains(text, "regenerate the application model")) {
				t.Fatalf("missing canonical guard data lacks no-verdict regeneration hint: %s", text)
			}
			if (tc.name == "same-refusal-other-rule" || tc.name == "empty-rule-origin" || tc.name == "read-pinning") && strings.Contains(text, "regenerate the application model") {
				t.Fatalf("known different cause presented as stale data: %s", text)
			}
		})
	}
}

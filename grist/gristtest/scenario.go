package gristtest

import (
	"fmt"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// Scenario is an acceptance scenario of a grist app as a claim: the
// commands of steps, each with its arguments and its expected outcome
// (Command.With, Invocation.Refused), taken in turn by a run's steps,
// others between them. It is a qtest.NamedScenario of the catalog's
// actions (each step a qtest.Takes with its outcome and its arguments as
// the model's parameters take them), so it is claimed like any
// expectation (under Some, All or None) and checked possible in
// the model on its own with qtest.Possible:
//
//	fill := gristtest.Scenario("fill an item",
//		Add.With(AddItem{Item: "a", Qty: 3}),
//		Add.With(AddItem{Item: "a", Qty: 3}).Refused(ErrFull))
//	qtest.Model(t, "app.binding.yaml").Expect(t, qtest.Possible(fill)) // the model can
//	system(commits, cfg).Explore(t, qtest.ExploreConfig{}).Expect(t, qtest.Some(fill)) // and a run does
//
// A run that misses it is reported with how far it got, where it
// stopped, and whether the model on its own can follow it.
func Scenario(name string, steps ...grist.Invocation) qtest.Expectation {
	takes := make([]qtest.Take, len(steps))
	for i, inv := range steps {
		k := qtest.Takes(inv.Action).Outcome(inv.Outcome)
		for j, p := range inv.Params {
			k = k.With(p, inv.Values[j])
		}
		takes[i] = k.Hint(fmt.Sprintf("send %s after the steps before it (gristsim Config.Prologue for a fixed start, Config.Gen for its arguments)", inv))
	}
	return qtest.NamedScenario(name, takes...)
}

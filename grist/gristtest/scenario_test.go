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

// A scenario of the catalog's commands, each with its arguments as the
// model's parameters and its outcome, holds of a run whose steps take
// them in turn, and Refused takes only a refusal the command declares.
func TestScenario(t *testing.T) {
	a := grist.New("noting")
	ev := grist.NewEvent[noted](a, "noted")
	dup := grist.Rejection("dup")
	cmd := grist.NewCommand(a, "note", func(r *grist.Reads, c note) ([]grist.Event, error) {
		if r.Exists(ev, grist.Tag("item", c.Item)) {
			return nil, dup
		}
		return []grist.Event{ev.Of(noted{Item: c.Item})}, nil
	}, grist.Refuses(dup), grist.Authorize(grist.Authenticated))
	twice := gristtest.Scenario("noted twice", cmd.With(note{Item: "a"}), cmd.With(note{Item: "a"}).Refused(dup))
	if s := twice.String(); !strings.Contains(s, `the scenario "noted twice" (note(item_ = "a") with outcome ok -> note(item_ = "a") with outcome dup)`) {
		t.Errorf("its name: %s", s)
	}
	step := func(item, outcome string) binding.Call {
		return binding.Call{Action: "note", Quint: "note(" + item + ")", Args: map[string]string{"item_": item},
			Step: &qtrace.Step{Action: "note", Outcome: outcome}}
	}
	run := func(calls ...binding.Call) *qtest.Run {
		return &qtest.Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass, Calls: calls}}}}
	}
	if !twice.Holds(run(step(`"a"`, ""), step(`"b"`, ""), step(`"a"`, "dup"))) {
		t.Errorf("a run noting a, b, then a refused: want the scenario")
	}
	if twice.Holds(run(step(`"b"`, ""), step(`"a"`, "dup"))) || twice.Holds(run(step(`"a"`, "dup"), step(`"a"`, ""))) {
		t.Errorf("another item, or the wrong order: want no scenario")
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "declares no such refusal") {
			t.Errorf("Refused of an undeclared refusal: %v", r)
		}
	}()
	cmd.With(note{Item: "a"}).Refused(grist.Rejection("other"))
}

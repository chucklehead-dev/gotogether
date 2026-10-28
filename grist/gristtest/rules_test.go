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

// Exercised wants each refusal a command's rules add, its hint naming
// the rule; Mutants makes a generated mutant of each rule guarding a
// command, none of one that guards none.
func TestRules(t *testing.T) {
	a := guarded()
	if err := a.Verify(); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, k := range gristtest.Takes(a) {
		names = append(names, k.Name())
	}
	runs := qtest.Runs(&qtest.Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass,
		Calls: []binding.Call{{Action: "init"}, {Action: "hold", Step: &qtrace.Step{Action: "hold", Outcome: "ok"}}}}}}})
	if msgs := run(t, func(tb testing.TB) { runs.Expect(tb, gristtest.Exercised(a, "release")) }); len(msgs) != 1 || !strings.Contains(msgs[0], "no hold was refused full by its rules (two-holds)") {
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

package gristtest_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

type sent struct {
	Key string `json:"key"`
}

// lending is an App of every kind of block: a command with a refusal, a
// query, an at-least-once processor and a site.
func lending() *grist.App {
	a := grist.New("lending")
	ev := grist.NewEvent[noted](a, "noted")
	dup := grist.Rejection("dup")
	grist.NewCommand(a, "note", func(r *grist.Reads, c note) ([]grist.Event, error) {
		if r.Exists(ev, grist.Tag("item", c.Item)) {
			return nil, dup
		}
		return []grist.Event{ev.Of(noted{Item: c.Item})}, nil
	}, grist.Refuses(dup), grist.Authorize(grist.Authenticated))
	grist.NewQuery(a, "count", func(r *grist.Reads, q note) (int, error) { return 0, nil }, grist.Authorize(grist.Authenticated))
	grist.NewProcessor(a, "mail", ev, func(ctx context.Context, r *grist.Reads, e noted, m grist.Meta) grist.Result {
		return grist.Effect(func(context.Context) error { return nil }, grist.AtLeastOnce)
	}, grist.Delivers(grist.AtLeastOnce))
	grist.NewSite[sent](a, "sent")
	return a
}

// Exercised wants every block of the catalog taken, a command by each of
// its outcomes, and names each one missed with how to reach it; its
// exceptions are the names it lists, and one it does not list panics.
func TestExercised(t *testing.T) {
	a := lending()
	var names []string
	for _, k := range gristtest.Takes(a) {
		names = append(names, k.Name())
	}
	if got, want := strings.Join(names, " "), "note.ok note.dup count mailEffect mail sent"; got != want {
		t.Errorf("the catalog's takes %q, want %q", got, want)
	}
	call := func(action, outcome string) binding.Call {
		return binding.Call{Action: action, Step: &qtrace.Step{Action: action, Outcome: outcome}}
	}
	runs := func() *qtest.Exploration {
		return qtest.Runs(&qtest.Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass,
			Calls: []binding.Call{{Action: "init"}, call("note", "ok"), call("count", "ok"), call("mailEffect", "ok"), call("mail", "ok")}}}}})
	}
	msgs := run(t, func(tb testing.TB) { runs().Expect(tb, gristtest.Exercised(a)) })
	if len(msgs) != 1 || !strings.Contains(msgs[0], "2 of 6 not taken") || !strings.Contains(msgs[0], "no note was refused dup") ||
		!strings.Contains(msgs[0], "the sent site recorded nothing") {
		t.Errorf("a run without the refusal and the site's record: %q, want both named with their fixes", msgs)
	}
	if msgs := run(t, func(tb testing.TB) { runs().Expect(tb, gristtest.Exercised(a, "note.dup", "sent")) }); len(msgs) > 0 {
		t.Errorf("both excepted: %q", msgs)
	}
	if msgs := run(t, func(tb testing.TB) { runs().Expect(tb, gristtest.Exercised(a, "note", "sent")) }); len(msgs) > 0 {
		t.Errorf("the command excepted whole: %q", msgs)
	}
	defer func() {
		if msg := fmt.Sprint(recover()); !strings.Contains(msg, `"notes" names no block`) {
			t.Errorf("a misspelled exception: panic %q", msg)
		}
	}()
	gristtest.Exercised(a, "notes")
}

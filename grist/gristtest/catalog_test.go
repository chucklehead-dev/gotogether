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
// query, an at-least-once processor using a declared call, and a site.
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
	send := grist.NewCall[sent, sent](grist.NewDependency(a, "post"), "send")
	grist.NewProcessor(a, "mail", ev, func(ctx context.Context, r *grist.Reads, e noted, m grist.Meta) grist.Result {
		return grist.Effect(func(context.Context) error { return nil }, grist.AtLeastOnce)
	}, grist.Delivers(grist.AtLeastOnce), grist.Uses(send))
	grist.NewSite[sent](a, "sent")
	return a
}

// The catalog's Exercised wants every block taken, a command by each of
// its outcomes, and names each one missed with how to reach it; its
// exceptions are the names it lists, and one it does not list panics.
func TestCatalogExercised(t *testing.T) {
	a := lending()
	cat := gristtest.Catalog(a)
	var names []string
	for _, k := range cat.Takes {
		names = append(names, k.Name())
	}
	if got, want := strings.Join(names, " "), "note.ok note.dup count mailEffect mail postSend.ok postSendServed sent"; got != want {
		t.Errorf("the catalog's takes %q, want %q", got, want)
	}
	call := func(action, outcome string) binding.Call {
		return binding.Call{Action: action, Step: &qtrace.Step{Action: action, Outcome: outcome}}
	}
	runs := func() *qtest.Exploration {
		return qtest.Runs(&qtest.Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass,
			Calls: []binding.Call{{Action: "init"}, call("note", "ok"), call("count", "ok"), call("mailEffect", "ok"), call("mail", "ok"), call("postSend", "ok")}}}}})
	}
	msgs := run(t, func(tb testing.TB) { runs().Expect(tb, cat.Exercised()) })
	if len(msgs) != 1 || !strings.Contains(msgs[0], "3 of 8 not taken") || !strings.Contains(msgs[0], "no note was refused dup") ||
		!strings.Contains(msgs[0], "the sent site recorded nothing") || !strings.Contains(msgs[0], "post applied no send") {
		t.Errorf("a run without the refusal, the site's record and the call's application: %q, want each named with its fix", msgs)
	}
	if msgs := run(t, func(tb testing.TB) { runs().Expect(tb, cat.Exercised("note.dup", "sent", "postSendServed")) }); len(msgs) > 0 {
		t.Errorf("all three excepted: %q", msgs)
	}
	if msgs := run(t, func(tb testing.TB) { runs().Expect(tb, cat.Exercised("note", "sent", "postSendServed")) }); len(msgs) > 0 {
		t.Errorf("the command excepted whole: %q", msgs)
	}
	defer func() {
		if msg := fmt.Sprint(recover()); !strings.Contains(msg, `"notes" names no block`) {
			t.Errorf("a misspelled exception: panic %q", msg)
		}
	}()
	cat.Exercised("notes")
}

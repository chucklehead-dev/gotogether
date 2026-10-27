package grist_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// A command's declared refusals (Refuses) are the ones its decision may
// return: another rejection is the decision's error (recorded with its
// text, not remembered), so the generated model, which has a branch per
// declared refusal, never meets an outcome it lacks. A declared one is a
// refusal as before.
func TestRefusesDeclared(t *testing.T) {
	a := grist.New("counters")
	ev := grist.NewEvent[Created](a, "counter-created")
	errOther := grist.Rejection("other")
	cmd := grist.NewCommand(a, "create-counter", func(r *grist.Reads, c Create) ([]grist.Event, error) {
		if c.Name == "other" {
			return nil, errOther
		}
		return decide(ev)(r, c)
	}, grist.Refuses(errTaken))
	if err := a.Verify(); err != nil {
		t.Fatal(err)
	}
	if got := a.Catalog().Commands[0].Refuses; len(got) != 1 || got[0] != "name-taken" {
		t.Errorf("the catalog's refusals %v: want [name-taken]", got)
	}
	s := newStore(t)
	ctx, steps := recorded(t, s)
	env := func(id string) grist.Envelope { return grist.Envelope{ID: id, Tenant: "t1", Actor: "a"} }
	if _, err := cmd.Execute(ctx, s, env("c1"), Create{Counter: "a", Name: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmd.Execute(ctx, s, env("c2"), Create{Counter: "b", Name: "x"}); !errors.Is(err, errTaken) {
		t.Errorf("a declared refusal: %v, want name-taken", err)
	}
	_, err := cmd.Execute(ctx, s, env("c3"), Create{Counter: "c", Name: "other"})
	var rej *grist.Rejected
	if err == nil || errors.As(err, &rej) || !strings.Contains(err.Error(), "Refuses does not declare") {
		t.Errorf("an undeclared refusal: %v, want the decision's error naming the fix", err)
	}
	// not remembered: the key decides again
	if _, err := cmd.Execute(ctx, s, env("c3"), Create{Counter: "c", Name: "other"}); err == nil || errors.As(err, &rej) {
		t.Errorf("the undeclared refusal's retry: %v, want the error again (no marker)", err)
	}
	for _, st := range steps() {
		if st.Args["id"] == "c3" && (st.Outcome != "error" || st.Error == "") {
			t.Errorf("the undeclared refusal's step %v: want the code's error, not an outcome", st)
		}
	}
	// a non-rejection is a Verify problem
	b := grist.New("other")
	grist.NewCommand(b, "x", func(r *grist.Reads, c Create) ([]grist.Event, error) { return nil, nil }, grist.Refuses(errors.New("plain")))
	if err := b.Verify(); err == nil || !strings.Contains(err.Error(), "is no rejection") {
		t.Errorf("Refuses of a plain error: %v, want a Verify problem", err)
	}
}

// A processor's declared shape (Emits, Delivers) is the result its
// handler must return: another fails the trigger (grist.failed after the
// tries), naming the fix, so the generated model's actions, which follow
// the declaration, judge what runs. Skip and Fail are allowed under any.
func TestShapeDeclared(t *testing.T) {
	cases := []struct {
		name   string
		result func(n *grist.EventType[Noted], m grist.Meta) grist.Result
		decl   func(n *grist.EventType[Noted]) grist.Option
		fails  bool
	}{
		{"emits as declared", func(n *grist.EventType[Noted], m grist.Meta) grist.Result {
			return grist.Emit(n.Of(Noted{Of: int64(m.Pos)}))
		},
			func(n *grist.EventType[Noted]) grist.Option { return grist.Emits(n) }, false},
		{"skip under Delivers", func(n *grist.EventType[Noted], m grist.Meta) grist.Result { return grist.Skip() },
			func(n *grist.EventType[Noted]) grist.Option { return grist.Delivers(grist.AtLeastOnce, n) }, false},
		{"an effect under Emits", func(n *grist.EventType[Noted], m grist.Meta) grist.Result {
			return grist.Effect(func(context.Context) error { return nil }, grist.AtLeastOnce)
		}, func(n *grist.EventType[Noted]) grist.Option { return grist.Emits(n) }, true},
		{"another delivery", func(n *grist.EventType[Noted], m grist.Meta) grist.Result {
			return grist.Effect(func(context.Context) error { return nil }, grist.AtMostOnce, grist.OnSuccess(n.Of(Noted{Of: int64(m.Pos)})))
		}, func(n *grist.EventType[Noted]) grist.Option { return grist.Delivers(grist.AtLeastOnce, n) }, true},
		{"no success event where one is declared", func(n *grist.EventType[Noted], m grist.Meta) grist.Result {
			return grist.Effect(func(context.Context) error { return nil }, grist.AtLeastOnce)
		}, func(n *grist.EventType[Noted]) grist.Option { return grist.Delivers(grist.AtLeastOnce, n) }, true},
		{"a success event where none is declared", func(n *grist.EventType[Noted], m grist.Meta) grist.Result {
			return grist.Effect(func(context.Context) error { return nil }, grist.AtLeastOnce, grist.OnSuccess(n.Of(Noted{Of: int64(m.Pos)})))
		}, func(n *grist.EventType[Noted]) grist.Option { return grist.Delivers(grist.AtLeastOnce) }, true},
		{"an effect as declared", func(n *grist.EventType[Noted], m grist.Meta) grist.Result {
			return grist.Effect(func(context.Context) error { return nil }, grist.AtLeastOnce, grist.OnSuccess(n.Of(Noted{Of: int64(m.Pos)})))
		}, func(n *grist.EventType[Noted]) grist.Option { return grist.Delivers(grist.AtLeastOnce, n) }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := grist.New("procs")
			added := grist.NewEvent[Added](a, "added")
			noted := grist.NewEvent[Noted](a, "noted")
			grist.NewProcessor(a, "note", added, func(ctx context.Context, r *grist.Reads, e Added, m grist.Meta) grist.Result {
				return c.result(noted, m)
			}, c.decl(noted))
			if err := a.Verify(); err != nil {
				t.Fatal(err)
			}
			log, steps := simulate(t, a, added, qenv.NewSeeded(1))
			failed := 0
			for _, e := range log {
				if e.Type == "grist.failed" {
					failed++
				}
			}
			if c.fails != (failed == 3) || failed%3 != 0 {
				t.Errorf("%d of 3 triggers failed: want %v", failed, c.fails)
			}
			for _, s := range steps {
				if c.fails && s.Action == "noteEffect" {
					t.Errorf("an effect step %v: a result contradicting the declaration runs nothing", s)
				}
			}
		})
	}
	// two shapes, and an event of another App, are Verify problems
	a := grist.New("procs")
	added := grist.NewEvent[Added](a, "added")
	foreign := grist.NewEvent[Noted](grist.New("other"), "noted")
	grist.NewProcessor(a, "p", added, func(ctx context.Context, r *grist.Reads, e Added, m grist.Meta) grist.Result { return grist.Skip() },
		grist.Emits(added), grist.Delivers(grist.AtMostOnce))
	grist.NewProcessor(a, "q", added, func(ctx context.Context, r *grist.Reads, e Added, m grist.Meta) grist.Result { return grist.Skip() },
		grist.Delivers(grist.AtLeastOnce, foreign))
	err := a.Verify()
	if err == nil || !strings.Contains(err.Error(), "one result shape") || !strings.Contains(err.Error(), "an event of another App") {
		t.Errorf("Verify: %v, want both problems", err)
	}
	cat := a.Catalog().Processors
	if cat[1].Shape != "effect" || cat[1].Delivery != grist.AtLeastOnce || cat[1].Success != "noted" {
		t.Errorf("the catalog's processor %+v: want its declared effect", cat[1])
	}
}

type paid struct {
	Key    string `json:"key"`
	Amount int    `json:"amount"`
	Note   []int  `json:"note"` // not a scalar: not recorded
}

// A site records its step where the effect is made: the site's Quint
// name, actor the tenant, process the App's, its record's scalar fields
// by JSON name; it is in the catalog.
func TestSiteRecords(t *testing.T) {
	a := grist.New("bank-app")
	site := grist.NewSite[paid](a, "bank-pays")
	if err := a.Verify(); err != nil {
		t.Fatal(err)
	}
	sink := &qobs.MemorySink{}
	ctx := qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))
	site.Record(ctx, "t1", paid{Key: "k", Amount: 3, Note: []int{1}})
	steps := sink.Steps()
	if len(steps) != 1 {
		t.Fatalf("%d steps, want 1", len(steps))
	}
	s := steps[0]
	if s.Action != "bankPays" || s.Actor != "t1" || s.Process != "bank-app" || s.Args["key"] != "k" || s.Args["amount"] != int64(3) || s.Args["note"] != nil {
		t.Errorf("the site's step %+v: want bankPays of t1 at bank-app, key k, amount 3, no note", s)
	}
	if c := a.Catalog().Sites; len(c) != 1 || c[0].Quint != "bankPays" {
		t.Errorf("the catalog's sites %+v", c)
	}
	b := grist.New("x")
	grist.NewSite[int](b, "s")
	if err := b.Verify(); err == nil || !strings.Contains(err.Error(), "no struct") {
		t.Errorf("a site of int: %v, want a Verify problem", err)
	}
}

type mailer interface{ Send(string) string }

type mailerFunc func(string) string

func (f mailerFunc) Send(s string) string { return f(s) }

// A Dep is attached to a context and read from it, typed, its fallback
// where none is; two Deps of one type are two dependencies.
func TestDep(t *testing.T) {
	fallback := mailerFunc(func(string) string { return "none" })
	a := grist.NewDep[mailer]("mailer", fallback)
	b := grist.NewDep[mailer]("other", fallback)
	ctx := context.Background()
	if a.Of(ctx).Send("x") != "none" || a.Attached(ctx) {
		t.Error("an unattached Dep: want its fallback")
	}
	ctx = a.With(ctx, mailerFunc(func(s string) string { return "sent " + s }))
	if a.Of(ctx).Send("x") != "sent x" || !a.Attached(ctx) {
		t.Error("an attached Dep: want the value attached")
	}
	if b.Of(ctx).Send("x") != "none" {
		t.Error("another Dep of the same type sees the first's value: want its own fallback")
	}
}

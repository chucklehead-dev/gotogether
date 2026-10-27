package grist_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qenv"
)

type Untagged struct {
	Counter string `json:"counter"` // not marked grist:"tag"
	By      int    `json:"by"`
}

// A read whose type the App does not register, or whose tag key the type
// has no tag of, would match nothing and condition nothing: the race back
// with no Unconditioned in sight. It is the decision's error, naming the
// fix, and nothing commits.
func TestReadsChecked(t *testing.T) {
	a := grist.New("counters")
	ev := grist.NewEvent[Created](a, "counter-created")
	other := grist.NewEvent[Created](grist.New("other"), "counter-created")
	for _, c := range []struct {
		name string
		read func(r *grist.Reads)
		want string
	}{
		{"the Go field's name as the key", func(r *grist.Reads) { r.Exists(ev, grist.Tag("Name", "a")) }, `counter-created has no tag "Name"`},
		{"a misspelled type", func(r *grist.Reads) { r.Select(grist.Selector{Types: []string{"counter-creatd"}}) }, `read of "counter-creatd", which counters does not register`},
		{"another App's event", func(r *grist.Reads) { r.Exists(other, grist.Tag("name", "a")) }, "an event type of another App"},
		{"another App's event, typed", func(r *grist.Reads) { grist.Events(r, other) }, "an event type of another App"},
	} {
		cmd := grist.NewCommand(a, "create-"+strings.ReplaceAll(strings.ReplaceAll(c.name, " ", "-"), "'", ""), func(r *grist.Reads, cr Create) ([]grist.Event, error) {
			c.read(r)
			return []grist.Event{ev.Of(Created(cr))}, nil
		})
		s := newStore(t)
		_, err := cmd.Execute(context.Background(), s, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "x", Name: "a"})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want the decision's error naming %q", c.name, err, c.want)
		}
		if n := count(t, s, "counter-created"); n != 0 {
			t.Errorf("%s: %d events appended", c.name, n)
		}
	}
}

// A keyed read model keyed by a field that is no tag of its handlers'
// types would fold nothing for every key: Verify refuses it, naming the
// fix.
func TestKeyedByUntaggedRefused(t *testing.T) {
	a := grist.New("counters")
	inc := grist.NewEvent[Untagged](a, "incremented")
	grist.NewKeyedReadModel(a, "totals", 1, grist.Keys("counter"), func() int { return 0 },
		grist.On(inc, func(s int, e Untagged, m grist.Meta) int { return s + e.By }))
	if err := a.Verify(); err == nil || !strings.Contains(err.Error(), `read model "totals" is keyed by "counter", which incremented has no tag of`) {
		t.Errorf("Verify: %v, want the untagged key named", err)
	}
}

// Events is the typed read: the events of one type and tags, decoded, in
// the read set.
func TestTypedEvents(t *testing.T) {
	a := grist.New("counters")
	ev := grist.NewEvent[Created](a, "counter-created")
	var got []Created
	q := grist.NewQuery(a, "names", func(r *grist.Reads, c Create) (int, error) {
		got = grist.Events(r, ev, grist.Tag("name", c.Name))
		return len(got), nil
	})
	s := newStore(t)
	for i, n := range []string{"a", "b", "a"} {
		s.Append(context.Background(), "t1", storeBatch(ev.Of(Created{Counter: string(rune('x' + i)), Name: n})))
	}
	if n, _, err := q.Run(context.Background(), s, grist.Envelope{ID: "q", Tenant: "t1"}, Create{Name: "a"}); err != nil || n != 2 || got[0].Counter != "x" || got[1].Counter != "z" {
		t.Errorf("Events(name=a): %v (%d, %v), want x and z", got, n, err)
	}
}

// A processor's zero Result, or an Effect of a delivery slice 1 does not
// have, is the handler's error: the trigger is checkpointed with
// grist.failed, not silently skipped.
func TestInvalidResults(t *testing.T) {
	for name, res := range map[string]grist.Result{
		"zero":         {},
		"no delivery":  grist.Effect(func(context.Context) error { return nil }, 0),
		"no function":  grist.Effect(nil, grist.AtLeastOnce),
		"bad delivery": grist.Effect(func(context.Context) error { return nil }, grist.AtLeastOnce+7),
	} {
		a, added, _ := procApp(nil)
		grist.NewProcessor(a, "odd", added, func(ctx context.Context, r *grist.Reads, e Added, m grist.Meta) grist.Result { return res })
		log, _ := simulate(t, a, added, qenv.NewSeeded(1))
		failed := 0
		for _, e := range log {
			if e.Type == "grist.failed" && strings.Contains(string(e.Body), `"odd"`) {
				failed++
			}
		}
		if failed != 3 {
			t.Errorf("%s: %d grist.failed events of odd, want one per trigger (3)", name, failed)
		}
	}
}

// Options: one type for every block; an option a block does not take, a
// zero one or an invalid count is a problem Verify names. ConditionTries
// bounds a command's decisions.
func TestOptions(t *testing.T) {
	a := grist.New("opts")
	ev := grist.NewEvent[Created](a, "counter-created")
	grist.NewQuery(a, "q", func(*grist.Reads, Create) (int, error) { return 0, nil }, grist.Unconditioned("advisory"))
	grist.NewCommand(a, "c", func(*grist.Reads, Create) ([]grist.Event, error) { return nil, nil }, grist.Option{}, grist.ConditionTries(0))
	grist.NewProcessor(a, "p", ev, func(context.Context, *grist.Reads, Created, grist.Meta) grist.Result { return grist.Skip() }, grist.TransientTries(3))
	grist.NewCommand(a, "d", func(*grist.Reads, Create) ([]grist.Event, error) { return nil, nil }, grist.Unconditioned(""))
	err := a.Verify()
	for _, want := range []string{`query "q": option Unconditioned applies to command blocks only`, `command "c": a zero grist.Option`,
		`command "c": option ConditionTries: 0: give at least 1`, `processor "p": option TransientTries applies to command and query blocks only`,
		`command "d": option Unconditioned: no reason given`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Verify's error lacks %q:\n%v", want, err)
		}
	}
	b := grist.New("counters")
	cev := grist.NewEvent[Created](b, "counter-created")
	cmd := grist.NewCommand(b, "count", func(r *grist.Reads, c Create) ([]grist.Event, error) {
		r.Select(grist.Selector{Types: []string{"counter-created"}})
		return []grist.Event{cev.Of(Created(c))}, nil
	}, grist.ConditionTries(2))
	s := &racing{Store: newStore(t)}
	s.before = func(int) {
		s.Store.Append(context.Background(), "t1", storeBatch(cev.Of(Created{Counter: "x", Name: "z"})))
	}
	if _, err := cmd.Execute(context.Background(), s, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "y", Name: "a"}); !errors.Is(err, grist.ErrConflict) || s.n != 2+1 {
		t.Errorf("ConditionTries(2): %v after %d appends, want a conflict after 2 tries and its marker", err, s.n)
	}
}

// The Catalog is the registry as data, in registration order.
func TestCatalog(t *testing.T) {
	a, _, _ := procApp(func(context.Context) error { return nil })
	_, _, cmd := app(true)
	_ = cmd
	c := a.Catalog()
	var events []string
	for _, e := range c.Events {
		events = append(events, e.Name)
	}
	if strings.Join(events, " ") != "grist.failed added summed noted" || len(c.Processors) != 2 || c.Processors[1].Quint != "note" || c.Processors[0].EffectTries != 3 {
		t.Errorf("catalog %+v: want grist.failed and the three events, the processors sum and note", c)
	}
	b, _, _ := app(true)
	if cc := b.Catalog(); len(cc.Commands) != 1 || cc.Commands[0].Quint != "createCounter" || cc.Commands[0].Unconditioned == "" || cc.Events[1].Tags[1] != "name" {
		t.Errorf("catalog %+v: want create-counter, unconditioned, of an event tagged counter and name", cc)
	}
}

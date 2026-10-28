package grist_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
)

type lent struct {
	Member string `json:"member" grist:"tag"`
	Book   string `json:"book"`
}

type lend struct {
	Member string `json:"member"`
	Book   string `json:"book"`
}

var (
	errLimit  = grist.Rejection("limit")
	errBanned = grist.Rejection("banned")
)

// lending is an App whose lend command appends a lent event and reads
// nothing: its two rules on lent (a member holds at most two loans, read
// from the member's loans; member "x" is banned, reading nothing) are its
// only reads and its only refusals.
func lending(rules bool) (*grist.App, *grist.EventType[lent], *grist.Command[lend]) {
	a := grist.New("lending")
	ev := grist.NewEvent[lent](a, "lent")
	cmd := grist.NewCommand(a, "lend", func(r *grist.Reads, c lend) ([]grist.Event, error) {
		return []grist.Event{ev.Of(lent(c))}, nil
	}, grist.Appends(ev), grist.Authorize(grist.Authenticated))
	if rules {
		grist.NewRule(a, "loan-limit", ev, func(r *grist.Reads, e lent) error {
			if len(grist.Events(r, ev, grist.Tag("member", e.Member))) >= 2 {
				return errLimit
			}
			return nil
		}, grist.Refuses(errLimit))
		grist.NewRule(a, "ban", ev, func(r *grist.Reads, e lent) error {
			if e.Member == "x" {
				return errBanned
			}
			return nil
		}, grist.Refuses(errBanned))
	}
	return a, ev, cmd
}

// loans are the lent events of member m in s.
func loans(t *testing.T, s grist.Store, m string) int {
	t.Helper()
	evs, _, err := s.Read(context.Background(), "t1", grist.Selector{Types: []string{"lent"}, Tags: []grist.TagPair{{Key: "member", Value: m}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return len(evs)
}

// A rule runs on what a command's decision appends, on the command's
// reads: its rejection is the command's refusal, recorded as the step's
// outcome with the rule's read in the step's read set, and remembered (a
// retry under the id answers it, recording nothing). The rules on one
// event run in registration order, the first refusal winning. Without the
// rules the third loan commits.
func TestRuleRefuses(t *testing.T) {
	for _, rules := range []bool{true, false} {
		a, _, cmd := lending(rules)
		if err := a.Verify(); err != nil {
			t.Fatal(err)
		}
		s := newStore(t)
		ctx, steps := recorded(t, s)
		var errs []error
		for i, id := range []string{"c1", "c2", "c3"} {
			_, err := cmd.Execute(ctx, s, grist.Envelope{ID: id, Tenant: "t1"}, lend{Member: "m", Book: string(rune('a' + i))})
			errs = append(errs, err)
		}
		if !rules {
			if errs[2] != nil || loans(t, s, "m") != 3 {
				t.Errorf("without rules: %v and %d loans; want the third committed", errs, loans(t, s, "m"))
			}
			continue
		}
		if errs[0] != nil || errs[1] != nil || !errors.Is(errs[2], errLimit) || loans(t, s, "m") != 2 {
			t.Fatalf("lends: %v and %d loans; want ok, ok, limit (the rule's) and two loans", errs, loans(t, s, "m"))
		}
		if _, err := cmd.Execute(ctx, s, grist.Envelope{ID: "c3", Tenant: "t1"}, lend{Member: "m", Book: "c"}); !errors.Is(err, errLimit) {
			t.Errorf("the refused lend retried under its id: %v; want its refusal again", err)
		}
		// x at the limit too: the limit rule, registered first, refuses first
		for _, b := range []string{"a", "b"} {
			s.Append(context.Background(), "t1", store.Batch{Events: []grist.Event{lendingEvent("x", b)}})
		}
		if _, err := cmd.Execute(ctx, s, grist.Envelope{ID: "x3", Tenant: "t1"}, lend{Member: "x", Book: "c"}); !errors.Is(err, errLimit) {
			t.Errorf("x's third lend: %v; want limit, the first rule's refusal (ban comes after it)", err)
		}
		if _, err := cmd.Execute(ctx, s, grist.Envelope{ID: "y1", Tenant: "t1"}, lend{Member: "x2", Book: "c"}); err != nil {
			t.Errorf("x2's lend: %v", err)
		}
		got := steps()
		if len(got) != 5 {
			t.Fatalf("%d steps, want 5 (the retry records nothing): %v", len(got), got)
		}
		third := got[2]
		if third.Action != "lend" || third.Outcome != "limit" {
			t.Errorf("the third step: %s outcome %q; want lend refused limit", third.Action, third.Outcome)
		}
		if reads := fmt.Sprint(third.Args["reads"]); reads != `[["lent","member","m"]]` {
			t.Errorf("the refused step's read set %v: want the rule's read of m's loans", third.Args["reads"])
		}
		c := a.Catalog()
		lendBlock := c.Commands[0]
		if strings.Join(lendBlock.Refuses, ",") != "limit,banned" || strings.Join(lendBlock.Rules, ",") != "loan-limit,ban" || strings.Join(lendBlock.Appends, ",") != "lent" {
			t.Errorf("lend in the catalog: refuses %v, rules %v, appends %v; want its rules' refusals added", lendBlock.Refuses, lendBlock.Rules, lendBlock.Appends)
		}
		if len(c.Rules) != 2 || c.Rules[0].Name != "loan-limit" || strings.Join(c.Rules[0].Guards, ",") != "lend" || c.Rules[0].On != "lent" {
			t.Errorf("the catalog's rules: %+v", c.Rules)
		}
	}
}

// lendingEvent is a lent event, as a command appends it.
func lendingEvent(m, b string) grist.Event {
	return grist.Event{Type: "lent", Version: 1, Tags: []grist.TagPair{{Key: "member", Value: m}}, Body: []byte(`{"member":"` + m + `","book":"` + b + `"}`)}
}

// A rule's reads join the command's condition: a loan committed after
// the rule read the member's loans, before the append, fails the append's
// condition, and the command decides again, its rule refusing on fresh
// reads. The decision itself reads nothing, so without the rule's reads
// in the condition the third loan would commit. On memstore and on
// simstore at every isolation level.
func TestRuleReadsConditioned(t *testing.T) {
	t.Run("memstore", func(t *testing.T) {
		_, ev, cmd := lending(true)
		s := &racing{Store: newStore(t)}
		s.Store.Append(context.Background(), "t1", store.Batch{Events: []grist.Event{ev.Of(lent{Member: "m", Book: "a"})}})
		s.before = func(n int) {
			if n == 1 { // another loan, committed after the rule's read
				s.Store.Append(context.Background(), "t1", store.Batch{Events: []grist.Event{ev.Of(lent{Member: "m", Book: "b"})}})
			}
		}
		_, err := cmd.Execute(context.Background(), s, grist.Envelope{ID: "c1", Tenant: "t1"}, lend{Member: "m", Book: "c"})
		if !errors.Is(err, errLimit) || loans(t, s, "m") != 2 || s.n != 2 {
			t.Errorf("%v, %d loans, %d appends; want limit on the second try, two loans", err, loans(t, s, "m"), s.n)
		}
	})
	for _, iso := range []qenv.Isolation{qenv.Serializable, qenv.SnapshotIsolation, qenv.ReadCommitted} {
		t.Run("simstore/"+iso.String(), func(t *testing.T) {
			sim := qenv.New(qenv.NewSeeded(1))
			defer sim.Close()
			sim.Check = true
			_, ev, cmd := lending(true)
			inner := simstore.New(qenv.NewStore(sim, "lending", iso))
			s := &racingStore{Store: inner}
			s.before = func(ctx context.Context, n int) {
				if n == 1 {
					if _, err := inner.Append(ctx, "t1", store.Batch{Events: []grist.Event{ev.Of(lent{Member: "m", Book: "b"})}}); err != nil {
						t.Errorf("the other loan: %v", err)
					}
				}
			}
			var err error
			n := 0
			sim.Spawn("client", func(ctx context.Context) {
				if cerr := inner.CreateTenant(ctx, "t1"); cerr != nil {
					t.Error(cerr)
					return
				}
				inner.Append(ctx, "t1", store.Batch{Events: []grist.Event{ev.Of(lent{Member: "m", Book: "a"})}})
				_, err = cmd.Execute(ctx, s, grist.Envelope{ID: "c1", Tenant: "t1"}, lend{Member: "m", Book: "c"})
				evs, _, rerr := inner.Read(ctx, "t1", grist.Selector{Types: []string{"lent"}}, 0)
				if rerr != nil {
					t.Error(rerr)
				}
				n = len(evs)
			})
			if rerr := sim.Run(time.Minute); rerr != nil {
				t.Fatal(rerr)
			}
			if !errors.Is(err, errLimit) || n != 2 {
				t.Errorf("%v and %d loans; want limit, two loans: the rule's read is in the condition", err, n)
			}
		})
	}
}

// Verify refuses a rule no block can run (on an event no command declares
// in Appends and no processor emits: pavlov's impossible event), one
// without refusals and one on another App's event, each naming the rule;
// a command appending a ruled event without declaring it, one appending
// an event its Appends lacks, and a rule refusing what it does not
// declare, are the code's errors.
func TestRuleVerify(t *testing.T) {
	a := grist.New("lending")
	ev := grist.NewEvent[lent](a, "lent")
	other := grist.NewEvent[Created](a, "created")
	grist.NewCommand(a, "lend", func(r *grist.Reads, c lend) ([]grist.Event, error) { return nil, nil }, grist.Appends(other))
	grist.NewRule(a, "never", ev, func(r *grist.Reads, e lent) error { return nil }, grist.Refuses(errLimit))
	grist.NewRule(a, "mute", other, func(r *grist.Reads, e Created) error { return nil })
	elsewhere := grist.NewEvent[Created](grist.New("other"), "elsewhere")
	grist.NewRule(a, "foreign", elsewhere, func(r *grist.Reads, e Created) error { return nil }, grist.Refuses(errLimit))
	err := a.Verify()
	for _, want := range []string{
		`rule "never" guards "lent", which no block appends`,
		`rule "mute" declares no refusal`,
		`rule "foreign" guards "elsewhere", an event of another App`,
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Verify: %v; want %q", err, want)
		}
	}

	// a processor's Emits is a block appending the event: the rule may run
	b := grist.New("lending")
	bev := grist.NewEvent[lent](b, "lent")
	trig := grist.NewEvent[Created](b, "created")
	grist.NewProcessor(b, "p", trig, func(ctx context.Context, r *grist.Reads, e Created, m grist.Meta) grist.Result { return grist.Skip() }, grist.Emits(bev))
	grist.NewRule(b, "limit", bev, func(r *grist.Reads, e lent) error { return nil }, grist.Refuses(errLimit))
	if err := b.Verify(); err != nil {
		t.Errorf("a rule on an event a processor emits: %v", err)
	}

	c := grist.New("lending")
	cev := grist.NewEvent[lent](c, "lent")
	cother := grist.NewEvent[Created](c, "created")
	undeclared := grist.NewCommand(c, "lend", func(r *grist.Reads, l lend) ([]grist.Event, error) { return []grist.Event{cev.Of(lent(l))}, nil })
	wrong := grist.NewCommand(c, "lend-other", func(r *grist.Reads, l lend) ([]grist.Event, error) { return []grist.Event{cev.Of(lent(l))}, nil }, grist.Appends(cother))
	liar := grist.NewCommand(c, "lend-liar", func(r *grist.Reads, l lend) ([]grist.Event, error) { return []grist.Event{cev.Of(lent(l))}, nil }, grist.Appends(cev))
	grist.NewRule(c, "liar", cev, func(r *grist.Reads, e lent) error { return errBanned }, grist.Refuses(errLimit))
	s := newStore(t)
	for _, x := range []struct {
		cmd  *grist.Command[lend]
		want string
	}{
		{undeclared, "command lend appends \"lent\", which rule liar guards, and declares no Appends"},
		{wrong, "command lend-other appends \"lent\", which its Appends does not declare"},
		{liar, "rule liar refused banned, which its Refuses does not declare"},
	} {
		if _, err := x.cmd.Execute(context.Background(), s, grist.Envelope{ID: x.cmd.Name(), Tenant: "t1"}, lend{Member: "m"}); err == nil || !strings.Contains(err.Error(), x.want) {
			t.Errorf("%s: %v; want %q", x.cmd.Name(), err, x.want)
		}
	}
}

// A processor whose Emit a rule refuses fails its trigger visibly: tried,
// then checkpointed with grist.failed, its step's error naming the rule
// and that processors are not supported yet; the triggers the rule admits
// emit as before. Without the rule every trigger emits.
func TestRuleBlocksProcessor(t *testing.T) {
	for _, rules := range []bool{true, false} {
		a := grist.New("procs")
		added := grist.NewEvent[Added](a, "added")
		summed := grist.NewEvent[Summed](a, "summed")
		grist.NewProcessor(a, "sum", added, func(ctx context.Context, r *grist.Reads, e Added, m grist.Meta) grist.Result {
			return grist.Emit(summed.Of(Summed{Sum: e.By, Count: 1}))
		}, grist.Emits(summed))
		if rules {
			grist.NewRule(a, "small", summed, func(r *grist.Reads, e Summed) error {
				if e.Sum > 1 {
					return errLimit
				}
				return nil
			}, grist.Refuses(errLimit))
		}
		log, steps := simulate(t, a, added, qenv.NewSeeded(1))
		var got []string
		for _, e := range log {
			if e.Type != "added" {
				got = append(got, e.Type)
			}
		}
		want := "summed,summed,summed"
		if rules {
			want = "summed,grist.failed,grist.failed"
		}
		if strings.Join(got, ",") != want {
			t.Errorf("rules %v: the processor appended %v; want %s", rules, got, want)
		}
		blocked := 0
		for _, s := range steps {
			if s.Action == "sum" && strings.Contains(s.Error, "rule small refuses (limit)") && strings.Contains(s.Error, "not supported yet") {
				blocked++
			}
		}
		if rules && blocked != 2 {
			t.Errorf("%d sum steps whose error names the rule; want 2: %v", blocked, steps)
		}
	}
}

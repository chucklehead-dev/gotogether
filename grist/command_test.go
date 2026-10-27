package grist_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

type Created struct {
	Counter string `json:"counter" grist:"tag"`
	Name    string `json:"name" grist:"tag"`
}

type Create struct {
	Counter string `json:"counter"`
	Name    string `json:"name"`
}

var errTaken = grist.Rejection("name-taken")

// decide is grain's create-counter, made safe by its read set.
func decide(created *grist.EventType[Created]) func(r *grist.Reads, c Create) ([]grist.Event, error) {
	return func(r *grist.Reads, c Create) ([]grist.Event, error) {
		if r.Exists(created, grist.Tag("name", c.Name)) {
			return nil, errTaken
		}
		return []grist.Event{created.Of(Created{Counter: c.Counter, Name: c.Name})}, nil
	}
}

// app is a fresh app with one event and the create command (blind if
// unconditioned is set).
func app(unconditioned bool) (*grist.App, *grist.EventType[Created], *grist.Command[Create]) {
	a := grist.New("counters")
	ev := grist.NewEvent[Created](a, "counter-created")
	var opts []grist.Option
	if unconditioned {
		opts = append(opts, grist.Unconditioned("a test of the blind variant"))
	}
	return a, ev, grist.NewCommand(a, "create-counter", decide(ev), opts...)
}

// recorded is a context recording into a fresh recorder, and its steps
// once every commit's step is relayed from s's step log (as a node's
// relay records them), in the order Reconstruct gives them.
func recorded(t *testing.T, s grist.Store) (context.Context, func() []qtrace.Step) {
	t.Helper()
	sink := &qobs.MemorySink{}
	ctx := qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))
	relay, err := grist.NewRelay(s)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, func() []qtrace.Step {
		if err := relay.Flush(ctx); err != nil {
			t.Fatal(err)
		}
		traces, _ := qtrace.Reconstruct(sink.Steps())
		var out []qtrace.Step
		for _, tr := range traces {
			out = append(out, tr.Steps...)
		}
		return out
	}
}

func newStore(t *testing.T) *memstore.Store {
	s := memstore.New("counters")
	if err := s.CreateTenant(context.Background(), "t1"); err != nil {
		t.Fatal(err)
	}
	return s
}

// A store that runs before before each Append (once per call, while
// before is set): a concurrent commit landing between a decision's reads
// and its append.
type racing struct {
	*memstore.Store
	before func(n int) // n: the Append's number, from 1
	n      int
}

func (s *racing) Append(ctx context.Context, t string, b store.Batch) (grist.Position, error) {
	s.n++
	if s.before != nil {
		s.before(s.n)
	}
	return s.Store.Append(ctx, t, b)
}

func names(t *testing.T, s grist.Store) []string {
	evs, _, err := s.Read(context.Background(), "t1", grist.Selector{Types: []string{"counter-created"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		for _, tag := range e.Tags {
			if tag.Key == "name" {
				out = append(out, tag.Value)
			}
		}
	}
	return out
}

// A decision's append is conditioned on its read set: a create of a name
// that another create took between the read and the append fails its
// condition, decides again on fresh reads, and is refused. Unconditioned,
// both commit: grain's race.
func TestConditionedOnReads(t *testing.T) {
	for _, blind := range []bool{false, true} {
		_, ev, cmd := app(blind)
		s := &racing{Store: newStore(t)}
		s.before = func(n int) {
			if n == 1 { // the other create, committed after this one's read
				s.Store.Append(context.Background(), "t1", store.Batch{Events: []grist.Event{ev.Of(Created{Counter: "x", Name: "a"})}})
			}
		}
		_, err := cmd.Execute(context.Background(), s, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "y", Name: "a"})
		got := names(t, s)
		switch {
		case !blind && (!errors.Is(err, errTaken) || len(got) != 1):
			t.Errorf("conditioned: %v and names %v; want name-taken, one create of a", err, got)
		case blind && (err != nil || len(got) != 2):
			t.Errorf("unconditioned: %v and names %v; want two creates of a (the race, on purpose)", err, got)
		}
	}
}

// Reads are pinned at their first read: a later read of the same Reads
// does not see what committed after it, and the append's condition then
// fails (decide again).
func TestReadsPinned(t *testing.T) {
	a := grist.New("counters")
	ev := grist.NewEvent[Created](a, "counter-created")
	s := newStore(t)
	var seen []int
	tries := 0
	cmd := grist.NewCommand(a, "count", func(r *grist.Reads, c Create) ([]grist.Event, error) {
		tries++
		first := len(r.Select(grist.Selector{Types: []string{"counter-created"}}))
		if tries == 1 { // a commit while the decision runs
			s.Append(context.Background(), "t1", store.Batch{Events: []grist.Event{ev.Of(Created{Counter: "x", Name: "z"})}})
		}
		seen = append(seen, first, len(r.Select(grist.Selector{Types: []string{"counter-created"}})))
		return []grist.Event{ev.Of(Created{Counter: c.Counter, Name: c.Name})}, nil
	})
	if _, err := cmd.Execute(context.Background(), s, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "y", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if want := []int{0, 0, 1, 1}; !equal(seen, want) || tries != 2 {
		t.Errorf("reads saw %v in %d tries; want %v in 2 (pinned at 0, then decided again at 1)", seen, tries, want)
	}
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Idempotency: a command retried under its id answers the first
// attempt's receipt and appends nothing (a lost answer); under its id
// with other arguments it is refused.
func TestIdempotency(t *testing.T) {
	_, _, cmd := app(false)
	s := newStore(t)
	ctx, steps := recorded(t, s)
	env := grist.Envelope{ID: "c1", Tenant: "t1"}
	r1, err := cmd.Execute(ctx, s, env, Create{Counter: "x", Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := cmd.Execute(ctx, s, env, Create{Counter: "x", Name: "a"})
	if err != nil || r2.Pos != r1.Pos || len(r2.Events) != 1 || r2.Events[0] != r1.Events[0] {
		t.Errorf("the retry: %+v (%v), want the first attempt's receipt %+v", r2, err, r1)
	}
	if _, err := cmd.Execute(ctx, s, env, Create{Counter: "x", Name: "b"}); !errors.Is(err, grist.ErrKeyReused) {
		t.Errorf("the id with other arguments: %v, want grist.ErrKeyReused", err)
	}
	if got := names(t, s); len(got) != 1 {
		t.Errorf("names %v: the retry appended", got)
	}
	var outcomes []string
	for _, st := range steps() {
		outcomes = append(outcomes, st.Outcome)
	}
	if strings.Join(outcomes, " ") != "ok idempotency-key-reused" {
		t.Errorf("steps %v: want the commit and the refusal of the reused key, nothing for the retry", outcomes)
	}
}

// A commit whose answer is lost is answered from its marker on the
// retry, and transient errors are retried apart from the condition's
// tries.
func TestTransientRetried(t *testing.T) {
	_, _, cmd := app(false)
	s := &flaky{Store: newStore(t), transient: 3}
	ctx, steps := recorded(t, s)
	rec, err := cmd.Execute(ctx, s, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "x", Name: "a"})
	if err != nil || rec.Pos != 1 {
		t.Fatalf("after 3 transient errors and a lost answer: %+v, %v; want the receipt of position 1", rec, err)
	}
	if got := names(t, s.Store); len(got) != 1 || len(steps()) != 1 {
		t.Errorf("names %v, %d steps: want one commit, recorded once", got, len(steps()))
	}
}

// flaky fails its first transient appends, then commits the next and
// reports its answer lost.
type flaky struct {
	*memstore.Store
	transient int
	lost      bool
}

func (s *flaky) Append(ctx context.Context, t string, b store.Batch) (grist.Position, error) {
	if s.transient > 0 {
		s.transient--
		return 0, errors.New("lock lost: retry")
	}
	h, err := s.Store.Append(ctx, t, b)
	if err == nil && !s.lost {
		s.lost = true
		return 0, errors.New("connection lost after commit")
	}
	return h, err
}

// A condition failing on every try is a conflict: committed as the key's
// answer (one more append, the marker alone, unconditioned) and recorded
// as one; a retry under the key gets the conflict again, deciding
// nothing.
func TestConflictAfterTries(t *testing.T) {
	a := grist.New("counters")
	ev := grist.NewEvent[Created](a, "counter-created")
	cmd := grist.NewCommand(a, "count", func(r *grist.Reads, c Create) ([]grist.Event, error) {
		r.Select(grist.Selector{Types: []string{"counter-created"}})
		return []grist.Event{ev.Of(Created{Counter: c.Counter, Name: c.Name})}, nil
	})
	s := &racing{Store: newStore(t)}
	s.before = func(int) {
		s.Store.Append(context.Background(), "t1", store.Batch{Events: []grist.Event{ev.Of(Created{Counter: "x", Name: "z"})}})
	}
	ctx, steps := recorded(t, s)
	_, err := cmd.Execute(ctx, s, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "y", Name: "a"})
	if !errors.Is(err, grist.ErrConflict) || s.n != 5+1 {
		t.Errorf("%v after %d appends; want grist.ErrConflict after 5 tries and the conflict's marker", err, s.n)
	}
	if st := steps(); len(st) != 1 || st[0].Outcome != "conflict" {
		t.Errorf("steps %v: want one, its outcome conflict", st)
	}
	s.before = nil
	if _, err := cmd.Execute(ctx, s, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "y", Name: "a"}); !errors.Is(err, grist.ErrConflict) || s.n != 6 || len(steps()) != 1 {
		t.Errorf("the retry: %v after %d appends, %d steps; want the conflict again, nothing appended or recorded", err, s.n, len(steps()))
	}
}

// Every step has actor the tenant and process the store's name, the
// command's fields, its id and read set, and its place on the domain
// clock: 2·P for a commit ending at P, 2·P+1 for a refusal through P.
func TestCommandSteps(t *testing.T) {
	_, _, cmd := app(false)
	s := newStore(t)
	ctx, steps := recorded(t, s)
	for i, name := range []string{"a", "a"} {
		cmd.Execute(ctx, s, grist.Envelope{ID: []string{"c1", "c2"}[i], Tenant: "t1"}, Create{Counter: "x", Name: name})
	}
	st := steps()
	if len(st) != 2 {
		t.Fatalf("%d steps, want 2", len(st))
	}
	for i, want := range []struct {
		outcome string
		order   int64
		readPos int64
	}{{"ok", 2, 0}, {"name-taken", 3, 1}} {
		s := st[i]
		if s.Action != "createCounter" || s.Actor != "t1" || s.Process != "counters" || s.Outcome != want.outcome ||
			s.Order["pos"] != want.order || s.Args["readPos"] != want.readPos || s.Args["name"] != "a" || s.Args["id"] != []string{"c1", "c2"}[i] {
			t.Errorf("step %d: %s actor %s process %s outcome %s order %v args %v; want createCounter of t1 at counters, %s, order.pos %d, readPos %d",
				i+1, s.Action, s.Actor, s.Process, s.Outcome, s.Order, s.Args, want.outcome, want.order, want.readPos)
		}
		if s.Args["reads"] != `[["counter-created","name","a"]]` {
			t.Errorf("step %d's read set %v, want the name's query", i+1, s.Args["reads"])
		}
	}
	if st[0].Obs["head"] != int64(1) || st[0].Obs["events"] != `["counter-created"]` || st[0].Obs["strs"] != `["x","a"]` {
		t.Errorf("the commit's observations %v: want head 1 and the created event", st[0].Obs)
	}
}

// Boot verification names the block and the fix: floats and times in
// events, names taken twice or not Quint identifiers, arguments named as
// grist's own step fields.
func TestVerify(t *testing.T) {
	type Bad struct {
		Amount float64   `json:"amount"`
		At     time.Time `json:"at"`
	}
	type Args struct {
		ID string `json:"id"`
	}
	a := grist.New("bad")
	grist.NewEvent[Bad](a, "bad-happened")
	grist.NewEvent[Created](a, "bad-happened")
	grist.NewCommand(a, "to", func(*grist.Reads, Create) ([]grist.Event, error) { return nil, nil })
	grist.NewCommand(a, "do--it", func(*grist.Reads, Args) ([]grist.Event, error) { return nil, nil })
	grist.NewProcessor(a, "note", grist.NewEvent[Created](a, "noted"), func(context.Context, *grist.Reads, Created, grist.Meta) grist.Result { return grist.Skip() })
	grist.NewCommand(a, "note-effect", func(*grist.Reads, Create) ([]grist.Event, error) { return nil, nil })
	err := a.Verify()
	if err == nil {
		t.Fatal("an app of floats, times, a reused name and a reserved one verifies")
	}
	for _, want := range []string{"Bad.Amount is a float64", "Bad.At is a time.Time", `"bad-happened": the name is taken`,
		"maps to to, which Quint reserves", `"do--it"`, `field ID is named "id"`,
		`command "note-effect" maps to the Quint action noteEffect, as "note" does`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Verify's error lacks %q:\n%v", want, err)
		}
	}
	if a2, _, _ := app(false); a2.Verify() != nil {
		t.Errorf("a sound app does not verify: %v", a2.Verify())
	}
}

func TestQuintName(t *testing.T) {
	for in, want := range map[string]string{"create-counter": "createCounter", "average": "average", "show-counter-2": "showCounter2"} {
		if got, err := grist.QuintName(in); err != nil || got != want {
			t.Errorf("QuintName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"to", "", "-a", "a_b", "1st"} {
		if _, err := grist.QuintName(bad); err == nil {
			t.Errorf("QuintName(%q) maps it", bad)
		}
	}
}

// A decision and a query see the actor (Reads.Actor: the envelope's),
// and every step records it as "principal", a commit's, a refusal's and
// a query's alike, so a model can judge a rule over who asks: here only
// alice names. Before, neither the decision nor the steps had it.
func TestActorInDecisionsAndSteps(t *testing.T) {
	a := grist.New("names")
	named := grist.NewEvent[Named](a, "named")
	notAlice := grist.Rejection("not-alice")
	cmd := grist.NewCommand(a, "name", func(r *grist.Reads, c Name) ([]grist.Event, error) {
		if r.Actor() != "alice" {
			return nil, notAlice
		}
		return []grist.Event{named.Of(Named(c))}, nil
	})
	grist.NewQuery(a, "who", func(r *grist.Reads, q struct{}) (Name, error) { return Name{Name: r.Actor()}, nil })
	s := memstore.New("names")
	s.CreateTenant(context.Background(), "t1")
	ctx, steps := recorded(t, s)
	if _, err := cmd.Execute(ctx, s, grist.Envelope{ID: "k1", Tenant: "t1", Actor: "bob"}, Name{Name: "a"}); !errors.Is(err, notAlice) {
		t.Errorf("bob: %v, want not-alice", err)
	}
	if _, err := cmd.Execute(ctx, s, grist.Envelope{ID: "k2", Tenant: "t1", Actor: "alice"}, Name{Name: "a"}); err != nil {
		t.Errorf("alice: %v", err)
	}
	res, _, err := a.Query(ctx, s, grist.Envelope{Tenant: "t1", Actor: "carol"}, "who", []byte(`{}`))
	if err != nil || string(res) != `{"name":"carol"}` {
		t.Errorf("the query saw %s, %v: want carol", res, err)
	}
	got := map[string]any{}
	for _, st := range steps() {
		key := st.Action + ":" + fmt.Sprint(st.Args["id"])
		got[key] = st.Args["principal"]
	}
	if got["name:k1"] != "bob" || got["name:k2"] != "alice" || got["who:<nil>"] != "carol" {
		t.Errorf("principals by step %v: want bob's refusal, alice's commit, carol's query", got)
	}
}

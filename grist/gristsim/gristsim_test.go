package gristsim_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

type Noted struct {
	Key string `json:"topic" grist:"tag"`
}

type Note struct {
	Key string `json:"topic"`
}

type Count struct {
	N int `json:"n"`
}

func app() *grist.App {
	a := grist.New("notes")
	noted := grist.NewEvent[Noted](a, "noted")
	grist.NewCommand(a, "note", func(r *grist.Reads, c Note) ([]grist.Event, error) {
		return []grist.Event{noted.Of(Noted{Key: c.Key})}, nil
	}, grist.Authorize(grist.Authenticated))
	grist.NewQuery(a, "count", func(r *grist.Reads, q Note) (Count, error) {
		return Count{N: len(r.Select(grist.Selector{Types: []string{"noted"}, Tags: []grist.TagPair{grist.Tag("topic", q.Key)}}))}, nil
	}, grist.Authorize(grist.Authenticated))
	return a
}

func run(t *testing.T, cfg gristsim.Config) []qtrace.Step {
	t.Helper()
	sink := &qobs.MemorySink{}
	c := qenv.WithContext(qenv.NewSeeded(1), qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink)))
	if err := gristsim.Run(c, app(), cfg); err != nil {
		t.Fatal(err)
	}
	return sink.Steps()
}

// A run ends in a settle (after the probes, which run every query over
// the alphabet on every tenant), so the binding's eventual vals are
// judged. It records both sides: at the store, every step is the store's,
// of its tenant, the commits relayed from the step log under the tenant's
// own recorder, on simstore and on sqlstore over qsql alike, the clients
// calling over a qenv.Service or the HTTP API; at the clients, every
// command and query is a call (invoked, then done) of actor
// <tenant>/clients.
func TestBothSides(t *testing.T) {
	for _, cfg := range []gristsim.Config{{Check: true}, {Check: true, SQL: true}, {Check: true, HTTP: true}, {Check: true, SQL: true, HTTP: true}} {
		bothSides(t, run(t, cfg))
	}
}

// The HTTP tier refuses an App whose blocks declare no policy: every
// block is denied by default.
func TestHTTPNeedsPolicies(t *testing.T) {
	a := grist.New("open")
	noted := grist.NewEvent[Noted](a, "noted")
	grist.NewCommand(a, "note", func(r *grist.Reads, c Note) ([]grist.Event, error) {
		return []grist.Event{noted.Of(Noted{Key: c.Key})}, nil
	})
	if err := gristsim.Run(qenv.NewSeeded(1), a, gristsim.Config{HTTP: true}); err == nil || !strings.Contains(err.Error(), "Authorize") {
		t.Errorf("an App without policies on the HTTP tier: %v, want NewHandler's refusal", err)
	}
}

func bothSides(t *testing.T, steps []qtrace.Step) {
	t.Helper()
	if len(steps) == 0 || !steps[len(steps)-1].Settled {
		t.Fatalf("the run's last record is no settle: %v", steps[len(steps)-1])
	}
	probes, commits, calls, done := 0, 0, 0, 0
	for _, s := range steps {
		switch {
		case s.Settled:
		case s.Actor == gristsim.ClientsActor("t1") || s.Actor == gristsim.ClientsActor("t2"):
			if s.Invocation {
				calls++
			} else if s.Call != 0 {
				done++
			} else {
				t.Errorf("a client's step %s that is no call's", s.Action)
			}
		case s.Invocation || s.Call != 0:
			t.Errorf("a call of actor %s: want the clients' actors", s.Actor)
		case s.Process != "notes" || (s.Actor != "t1" && s.Actor != "t2"):
			t.Errorf("step %s of actor %s at %s: want a tenant's, at the store", s.Action, s.Actor, s.Process)
		case s.Action == "count" && s.Obs["n"] != nil:
			probes++
		case s.Action == "note":
			if s.Recorder != grist.RecorderID("notes", s.Actor) || s.Thread == "" {
				t.Errorf("commit %v: want it relayed, under %s's recorder, on its thread", s, s.Actor)
			}
			commits++
		}
	}
	if commits == 0 || probes < 6 {
		t.Errorf("%d commits, %d queries: want the clients' notes and a probe per tenant and letter (6)", commits, probes)
	}
	if calls == 0 || calls != done {
		t.Errorf("%d invocations, %d completions: want every call answered (a client retries a lost answer)", calls, done)
	}
}

// Gen draws every field from the alphabet and the integers.
func TestGen(t *testing.T) {
	type Args struct {
		S string
		N int
		B bool
	}
	picks := 0
	v := gristsim.Gen(qreflect[Args](), []string{"x", "y"}, []int{-1, 0, 3}, func(n int) int { picks++; return n - 1 }).(Args)
	if v != (Args{S: "y", N: 3, B: true}) || picks != 3 {
		t.Errorf("Gen = %+v in %d picks, want the last of each", v, picks)
	}
}

func qreflect[T any]() reflect.Type { return reflect.TypeFor[T]() }

// Do runs a command or query in the caller's goroutine and records the
// call as a run's clients do: invoked, then done with its answer, under
// <tenant>/clients, beside the store's own step.
func TestDo(t *testing.T) {
	a := app()
	st := memstore.New("notes")
	st.CreateTenant(context.Background(), "t1")
	sink := &qobs.MemorySink{}
	ctx := qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))
	if err := gristsim.Do(ctx, a, st, grist.Envelope{ID: "n1", Tenant: "t1"}, "note", []byte(`{"topic":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if err := gristsim.Do(ctx, a, st, grist.Envelope{ID: "q1", Tenant: "t1"}, "count", []byte(`{"topic":"x"}`)); err != nil {
		t.Fatal(err)
	}
	var calls []qtrace.Step
	for _, s := range sink.Steps() {
		if s.Actor == gristsim.ClientsActor("t1") {
			calls = append(calls, s)
		}
	}
	if len(calls) != 4 || !calls[0].Invocation || calls[1].Call == 0 || calls[0].Args["id"] != "n1" || calls[3].Obs["n"] != int64(1) {
		t.Errorf("client-side steps %v: want the note and the count each invoked and done, the count answering 1", calls)
	}
}

// A Prologue's calls are made in order, each to each tenant (or the one
// it names), before any client's first call: every client's invocation
// comes after the last prologue call's completion, in the recording.
func TestPrologue(t *testing.T) {
	for _, cfg := range []gristsim.Config{{}, {HTTP: true}} {
		cfg.Prologue = []gristsim.Call{{Name: "note", Args: Note{Key: "p"}}, {Tenant: "t2", Name: "note", Args: []byte(`{"topic":"q"}`)}, {Name: "count", Args: nil}}
		var order []string
		lastPrologue, firstClient := -1, -1
		for i, s := range run(t, cfg) {
			if !s.Invocation && s.Call == 0 {
				continue
			}
			id, _ := s.Args["id"].(string)
			switch {
			case s.Invocation && strings.Contains(id, "-prologue-") && s.Action == "note":
				order = append(order, id+":"+fmtAny(s.Args["topic"]))
			case s.Args["caller"] == "prologue" || strings.Contains(id, "-prologue-"):
			case s.Invocation && firstClient < 0 && !strings.HasPrefix(fmtAny(s.Args["caller"]), "probe"):
				firstClient = i
			}
			if s.Args["caller"] == "prologue" && !s.Invocation {
				lastPrologue = i
			}
		}
		if want := []string{"t1-prologue-1:p", "t2-prologue-1:p", "t2-prologue-2:q"}; !reflect.DeepEqual(order, want) {
			t.Errorf("HTTP %v: the prologue's notes %v, want %v (in order, each to each tenant but the one naming t2)", cfg.HTTP, order, want)
		}
		if lastPrologue < 0 || firstClient < 0 || firstClient < lastPrologue {
			t.Errorf("HTTP %v: the prologue's last completion at %d, the clients' first call at %d: want the clients after it", cfg.HTTP, lastPrologue, firstClient)
		}
	}
	err := gristsim.Run(qenv.NewSeeded(1), app(), gristsim.Config{Prologue: []gristsim.Call{{Name: "nope"}}})
	if err == nil || !strings.Contains(err.Error(), `"nope" is no command or query`) {
		t.Errorf("an unknown name in the prologue: %v, want the Config's error naming it", err)
	}
}

func fmtAny(v any) string { s, _ := v.(string); return s }

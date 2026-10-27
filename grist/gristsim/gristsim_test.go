package gristsim_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
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
	})
	grist.NewQuery(a, "count", func(r *grist.Reads, q Note) (Count, error) {
		return Count{N: len(r.Events(grist.Query{Types: []string{"noted"}, Tags: []grist.TagPair{grist.Tag("topic", q.Key)}}))}, nil
	})
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
// judged; at the commits, every step is the store's, of its tenant.
func TestAtCommits(t *testing.T) {
	steps := run(t, gristsim.Config{Check: true})
	if len(steps) == 0 || !steps[len(steps)-1].Settled {
		t.Fatalf("the run's last record is no settle: %v", steps[len(steps)-1])
	}
	probes, commits := 0, 0
	for _, s := range steps {
		switch {
		case s.Settled:
		case s.Invocation:
			t.Errorf("an invocation record at the commits: %v", s)
		case s.Process != "notes" || (s.Actor != "t1" && s.Actor != "t2"):
			t.Errorf("step %s of actor %s at %s: want a tenant's, at the store", s.Action, s.Actor, s.Process)
		case s.Action == "count" && s.Obs["n"] != nil:
			probes++
		case s.Action == "note":
			commits++
		}
	}
	if commits == 0 || probes < 6 {
		t.Errorf("%d commits, %d queries: want the clients' notes and a probe per tenant and letter (6)", commits, probes)
	}
}

// At the clients, every command and query is a call (invoked, then
// done) and grist records nothing at the store.
func TestAtClients(t *testing.T) {
	steps := run(t, gristsim.Config{Record: gristsim.AtClients})
	calls, done := 0, 0
	for _, s := range steps {
		switch {
		case s.Settled:
		case s.Invocation:
			calls++
		case s.Call != 0:
			done++
		default:
			t.Errorf("a step %s that is no call's: the store recorded it", s.Action)
		}
	}
	if calls == 0 || calls != done {
		t.Errorf("%d invocations, %d completions: want every call answered (a client retries a lost answer)", calls, done)
	}
}

// Gen draws every field from the alphabet and 1 to 3.
func TestGen(t *testing.T) {
	type Args struct {
		S string
		N int
		B bool
	}
	picks := 0
	v := gristsim.Gen(qreflect[Args](), []string{"x", "y"}, func(n int) int { picks++; return n - 1 }).(Args)
	if v != (Args{S: "y", N: 3, B: true}) || picks != 3 {
		t.Errorf("Gen = %+v in %d picks, want the last of each", v, picks)
	}
}

func qreflect[T any]() reflect.Type { return reflect.TypeFor[T]() }

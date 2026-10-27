package gristsim_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

type Shout struct {
	Topic string `json:"topic"`
}

type Heard struct {
	Echo string `json:"echo"`
}

// Ack is an answer with no fields: an acknowledgement, which a default
// double may give.
type Ack struct{}

// echoApp is notes with a processor that shouts each note's topic to an
// echo dependency, at least once, answered with R.
func echoApp[R any]() (*grist.App, *grist.Dependency, *grist.Call[Shout, R]) {
	a := app()
	echo := grist.NewDependency(a, "echo")
	shout := grist.NewCall[Shout, R](echo, "shout", grist.Keyed())
	shouted := grist.NewEvent[Noted](a, "shouted-at")
	grist.NewCommand(a, "shout", func(r *grist.Reads, c Note) ([]grist.Event, error) {
		return []grist.Event{shouted.Of(Noted{Key: c.Key})}, nil
	}, grist.Authorize(grist.Authenticated))
	grist.NewProcessor(a, "shouter", shouted, func(ctx context.Context, r *grist.Reads, e Noted, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			_, err := shout.Do(ctx, Shout{Topic: e.Key})
			return err
		}, grist.AtLeastOnce)
	}, grist.Delivers(grist.AtLeastOnce), grist.Uses(shout))
	return a, echo, shout
}

// A dependency is served as a qenv.Service named after it, whose faults
// are its own (rpc.<dependency>.*), from the run's Config.Doubles; a
// dependency given handlers lacking a call is the run's error naming the
// fix (one given none gets its default double: TestDefaultDouble).
func TestDoublesServed(t *testing.T) {
	a, echo, shout := echoApp[Heard]()
	runs := 0
	doubles := func() gristsim.Doubles {
		runs++
		return gristsim.Doubles{echo: grist.Handlers{shout.Handler(func(ctx context.Context, s Shout) (Heard, error) {
			return Heard{Echo: s.Topic}, nil
		})}}
	}
	sink := &qobs.MemorySink{}
	base := qenv.NewSeeded(3)
	base.Rates = qenv.NoFaults.Set("rpc.echo.response", 0.5)
	c := qenv.WithContext(base, qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink)))
	if err := gristsim.Run(c, a, gristsim.Config{Tenants: []string{"t1"}, Send: []string{"shout"}, Doubles: doubles, Check: true}); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Errorf("Doubles called %d times: want once per run", runs)
	}
	outcomes := map[string]int{}
	served := 0
	for _, s := range sink.Steps() {
		switch {
		case s.Invocation:
		case s.Action == "echoShout":
			outcomes[s.Outcome]++
		case s.Action == "echoShoutServed":
			served++
		}
	}
	if outcomes["ok"] == 0 || outcomes["unknown"] == 0 || served == 0 {
		t.Errorf("caller outcomes %v, %d served: want calls answered, calls whose answer the dependency's own Service lost (rpc.echo.response: unknown), and served records", outcomes, served)
	}
	for _, cfg := range []struct {
		doubles func() gristsim.Doubles
		want    string
	}{
		{func() gristsim.Doubles { return gristsim.Doubles{echo: nil} }, "no handler of call echo/shout"},
	} {
		err := gristsim.Run(qenv.NewSeeded(1), a, gristsim.Config{Tenants: []string{"t1"}, Doubles: cfg.doubles})
		if err == nil || !strings.Contains(err.Error(), cfg.want) {
			t.Errorf("Run: %v, want %q", err, cfg.want)
		}
	}
}

// A dependency the run gives no handlers is served by its default double
// (grist.DefaultHandlers), a Service of its own name as any double: a
// Keyed call applied once per key, answered with its response (one with
// no fields), and a later try of a key (the Service's duplicates and the
// effect's retries after a lost answer) answered from that first answer
// as a replay, never a second served record. A dependency whose call
// answers fields has no default double: the run is an error naming the
// call, its fields and the fix.
func TestDefaultDouble(t *testing.T) {
	if err := gristsim.Run(qenv.NewSeeded(1), func() *grist.App { a, _, _ := echoApp[Heard](); return a }(), gristsim.Config{Tenants: []string{"t1"}}); err == nil ||
		!strings.Contains(err.Error(), "call echo/shout answers a response with fields (echo), which a default double cannot know: give dependency echo handlers") {
		t.Errorf("Run of a call answering fields, no double: %v; want the run's error naming the call, its fields and the fix", err)
	}
	a, _, _ := echoApp[Ack]()
	sink := &qobs.MemorySink{}
	base := qenv.NewSeeded(5)
	base.Rates = qenv.NoFaults.Set("rpc.echo.request", 0.4).Set("rpc.echo.response", 0.4)
	c := qenv.WithContext(base, qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink)))
	cfg := gristsim.Config{Tenants: []string{"t1"}, Send: []string{"shout"}, Check: true}
	if err := gristsim.Run(c, a, cfg); err != nil {
		t.Fatal(err)
	}
	served, replayed := map[string]int{}, 0
	for _, s := range sink.Steps() {
		switch {
		case s.Invocation:
		case s.Action == "echoShoutServed" || s.Action == "echoShoutReplayed":
			if s.Outcome != "ok" {
				t.Errorf("%s %v: want ok", s.Action, s)
			}
			if s.Action == "echoShoutReplayed" {
				replayed++
				if served[s.Args["key"].(string)] == 0 {
					t.Errorf("a replay of key %v before its application", s.Args["key"])
				}
				continue
			}
			served[s.Args["key"].(string)]++
		}
	}
	for k, n := range served {
		if n != 1 {
			t.Errorf("key %s applied %d times: the default double of a Keyed call applies each key once", k, n)
		}
	}
	if len(served) == 0 || replayed == 0 {
		t.Errorf("%d keys applied, %d replays: want calls applied and later tries replayed", len(served), replayed)
	}
}

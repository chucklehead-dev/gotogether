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

// echoApp is notes with a processor that shouts each note's topic to an
// echo dependency, at least once.
func echoApp() (*grist.App, *grist.Dependency, *grist.Call[Shout, Heard]) {
	a := app()
	echo := grist.NewDependency(a, "echo")
	shout := grist.NewCall[Shout, Heard](echo, "shout", grist.Keyed())
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
// dependency without a double, or a call without a handler, is the run's
// error naming the fix.
func TestDoublesServed(t *testing.T) {
	a, echo, shout := echoApp()
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
		{nil, "dependency echo of notes has no double: give Config.Doubles"},
		{func() gristsim.Doubles { return gristsim.Doubles{echo: nil} }, "no handler of call echo/shout"},
	} {
		err := gristsim.Run(qenv.NewSeeded(1), a, gristsim.Config{Tenants: []string{"t1"}, Doubles: cfg.doubles})
		if err == nil || !strings.Contains(err.Error(), cfg.want) {
			t.Errorf("Run: %v, want %q", err, cfg.want)
		}
	}
}

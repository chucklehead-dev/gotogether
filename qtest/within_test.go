package qtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// A node that crashes, heals and elects a leader, the election after
// elect of virtual time: a binding's within (a leader within 3s of a
// heal) holds for a prompt one and is violated by a slow one, which an
// eventual val alone would pass. Violates names the obligation as reports
// do (binding.Within.Name).
func TestWithinBoundsAResponse(t *testing.T) {
	RequireQuint(t)
	b, err := binding.Load("../validate/testdata/within.yaml")
	if err != nil {
		t.Fatal(err)
	}
	name := b.Within[0].Name()
	node := func(elect time.Duration) func(c qenv.Chooser) error {
		return func(c qenv.Chooser) error {
			s := qenv.New(c)
			defer s.Close()
			s.Spawn("node", func(ctx context.Context) {
				qobs.Record(ctx, "crash", nil)
				qenv.Sleep(ctx, time.Second)
				qobs.Record(ctx, "heal", nil)
				qenv.Sleep(ctx, elect)
				qobs.Record(ctx, "elect", nil)
			})
			if err := s.Run(time.Minute); err != nil {
				return err
			}
			return s.Settle(time.Minute)
		}
	}
	sys := System{Binding: "../validate/testdata/within.yaml", Rates: qenv.NoFaults, Run: node(time.Second)}
	sys.Explore(t, ExploreConfig{Seeds: 2}).Expect(t, Conforms())
	sys.Run = node(5 * time.Second)
	sys.Explore(t, ExploreConfig{Seeds: 2}).Expect(t, All(Violates(name)))
}

// Conformance claimed of runs none of which judged a bounded response (a
// node that never elects and records nothing more, its runs not settled)
// fails with the fix: a system that stalls in silence once passed.
func TestWithinUnjudgedFails(t *testing.T) {
	RequireQuint(t)
	stalls := func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		s.Spawn("node", func(ctx context.Context) {
			qobs.Record(ctx, "crash", nil)
			qenv.Sleep(ctx, time.Second)
			qobs.Record(ctx, "heal", nil)
		})
		return s.Run(10 * time.Minute)
	}
	x := System{Binding: "../validate/testdata/within.yaml", Rates: qenv.NoFaults, Run: stalls}.Explore(t, ExploreConfig{Seeds: 1})
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { defer close(done); x.Expect(f, Conforms()) }() //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
	<-done
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "were judged in none of 1 runs") {
		t.Errorf("Conforms of runs that judged no bound: %v", f.errs)
	}
}

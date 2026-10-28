package qenv_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// optionsObserver notes ChoiceOptions at every choice it is asked.
type optionsObserver struct {
	sim  *qenv.Sim
	seen []string
}

func (o *optionsObserver) ObserveSim(s *qenv.Sim) { o.sim = s }

func (o *optionsObserver) Choose(kind string, n int) int {
	opts := o.sim.ChoiceOptions(kind)
	if len(opts) != n {
		panic(fmt.Sprintf("%s: %d options named for %d", kind, len(opts), n))
	}
	o.seen = append(o.seen, kind+"="+strings.Join(opts, ","))
	return n - 1 // the last option: a crash, and the last runnable task
}

// The scheduler names a choice's options without building a closure at
// every step (I40): a scheduling choice's runnable tasks, and a nemesis
// family's "none" then its nemeses, as they are at that choice.
func TestChoiceOptionsNamed(t *testing.T) {
	o := &optionsObserver{}
	s := qenv.New(o)
	defer s.Close()
	s.MaxSteps = 40
	for _, name := range []string{"a", "b"} {
		s.Process(name, true, func(ctx context.Context) {
			for range 3 {
				qenv.Yield(ctx)
			}
		})
	}
	if err := s.Run(time.Minute); err != nil && err != qenv.ErrMaxSteps {
		t.Fatal(err)
	}
	var sched, nem bool
	for _, c := range o.seen {
		kind, opts, _ := strings.Cut(c, "=")
		switch {
		case kind == "sched.task":
			sched = true
			for _, n := range strings.Split(opts, ",") {
				if !strings.HasPrefix(n, "a") && !strings.HasPrefix(n, "b") {
					t.Errorf("a scheduling choice named %q among %s", n, opts)
				}
			}
		case kind == "fault.nemesis.crash":
			nem = true
			if !strings.HasPrefix(opts, "none,") || !slices.Contains(strings.Split(opts, ","), "crash.a") && !slices.Contains(strings.Split(opts, ","), "crash.b") {
				t.Errorf("a nemesis choice named %s", opts)
			}
		}
	}
	if !sched || !nem {
		t.Fatalf("no scheduling or nemesis choice seen: %v", o.seen)
	}
}

// A step recorded with a task's context carries its simulation's time
// wherever it is recorded: the task is found from the context first (no
// goroutine lookup, which costs microseconds), here from a goroutine the
// simulation does not own, where the goroutine's lookup finds nothing.
func TestStepTimeFromTheContextsTask(t *testing.T) {
	mem := &qobs.MemorySink{}
	rec := qobs.NewRecorder(mem)
	s := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(context.Background(), rec)))
	defer s.Close()
	s.Spawn("t", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Hour)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { //qenv:ok a goroutine outside the simulation, waited for, recording with the task's context
			defer wg.Done()
			qobs.Record(ctx, "away", nil)
		}()
		wg.Wait()
	})
	if err := s.Run(2 * time.Hour); err != nil {
		t.Fatal(err)
	}
	steps := mem.Steps()
	if len(steps) != 1 || !steps[0].Time.Equal(qenv.Epoch.Add(time.Hour)) {
		t.Fatalf("steps %v: want one at the simulation's hour, %v", steps, qenv.Epoch.Add(time.Hour))
	}
}

package qtest

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// journalSystem's writer restarts after three writes in a row fail, each
// one in ten: about one run in 250. Its steps observe how many failed in a
// row, the state steering climbs.
func journalSystem() System {
	disk := qenv.DeclareFaults("journal.disk", "fail")
	return System{Binding: "testdata/journal.binding.yaml", Rates: qenv.NoFaults.Set("journal.disk", 0.1), Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		s.Spawn("writer", func(ctx context.Context) {
			written, fails := 0, 0
			for range 6 {
				if s.FaultOf(disk) == "fail" {
					fails++
					qobs.Record(ctx, "writeFail", nil, "obs.written", written, "obs.fails", fails)
					if fails == 3 {
						qobs.Record(ctx, "restart", nil, "obs.written", written)
						return
					}
					continue
				}
				written, fails = written+1, 0
				qobs.Record(ctx, "write", nil, "obs.written", written)
			}
		})
		return s.Run(0)
	}}
}

func restarted(x *Exploration) int {
	n := 0
	for _, r := range x.Runs {
		if slices.ContainsFunc(r.Steps, func(s qtrace.Step) bool { return s.Action == "restart" }) {
			n++
		}
	}
	return n
}

// Steering finds the restart that uniform seeds miss: runs one failure
// deep breed runs that start there. A steered run's reproduce line names
// its saved choices, which run it alone.
func TestSteer(t *testing.T) {
	RequireQuint(t)
	sys := journalSystem()
	plain := sys.Explore(t, ExploreConfig{Seeds: 200})
	steered := sys.Explore(t, ExploreConfig{Seeds: 20, Steer: 180})
	p, s := restarted(plain), restarted(steered)
	t.Logf("restarts: %d of 200 seeds, %d of 20 seeds and 180 steered runs", p, s)
	if s <= p || s < 3 {
		t.Fatalf("steering reached the restart in %d runs, uniform seeds in %d", s, p)
	}
	steered.Expect(t, Conforms())
	var r *Run
	for _, run := range steered.Runs {
		if run.origin.kind == "choices" && slices.ContainsFunc(run.Steps, func(s qtrace.Step) bool { return s.Action == "restart" }) {
			r = run
			break
		}
	}
	if !strings.HasPrefix(r.ID(), "choices=") {
		t.Fatalf("a steered run's reproduce names %q, not its choices", r.ID())
	}
	e, err := readEntry(r.origin.value, steered.ref())
	if err != nil {
		t.Fatal(err)
	}
	alone := runOne(config(t, nil), false, qenv.Replay(e.Choices), sys.Run)
	if !slices.ContainsFunc(alone.Steps, func(s qtrace.Step) bool { return s.Action == "restart" }) {
		t.Errorf("%s replayed did not restart: %v", r.ID(), alone.Steps)
	}
	if _, err := readEntry(r.origin.value, plain.ref()); err == nil {
		t.Error("another exploration's saved run was accepted")
	}
}

// Steer steers seeded runs: with a Chooser factory (a PCT's, a Script's)
// the prefix would not reach the choices it makes, so it is refused.
func TestSteerRefusesAChooser(t *testing.T) {
	RequireQuint(t)
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		journalSystem().Explore(f, ExploreConfig{Seeds: 2, Steer: 2, Chooser: func(seed uint64) qenv.Chooser { return qenv.NewSeeded(seed) }})
	}()
	<-done
	if !f.failed || !strings.Contains(strings.Join(f.errs, "\n"), "Steer with a Chooser") {
		t.Errorf("errors: %v", f.errs)
	}
}

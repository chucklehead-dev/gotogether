package qtest

import (
	"context"
	"testing"

	"github.com/chucklehead-dev/gotogether/connect"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// A System is an application bound to its model: the binding, the options
// of its checks (the model's constants for its instance, ...), and how to
// run it on a chooser. The
// workflows over it share that one definition: explore seeds (Explore),
// follow a model's traces through their faults (FollowTraces), sweep
// configurations (Sweep), and shrink a failure with Hegel
// (qhegel.Shrink).
//
//	sys := qtest.System{Binding: "checkout.binding.yaml", Run: func(c qenv.Chooser) error {
//		_, err := checkout.Run(c, cfg)
//		return err
//	}}
//	sys.Explore(t, qtest.ExploreConfig{Seeds: 100}).Expect(t, qtest.Conforms())
type System struct {
	// Binding is the path of the binding file, relative to the test's
	// package directory (where go test runs).
	Binding string
	// Opts apply to every workflow over the system: the model's constants
	// for this instance (Constants), the invariants checked (Invariants),
	// quint's options (Quint), the recorder's (RecorderOptions).
	Opts []Option
	// Rates are the environment's fault rates for every workflow that
	// draws faults (Explore, Sweep, Mutate, Fuzz, qhegel.Shrink); zero is
	// qenv.DefaultRates. Explore it at other rates with a copy:
	//
	//	quiet := sys
	//	quiet.Rates = qenv.NoFaults
	Rates qenv.Rates
	// Run deploys and runs the application on c: a simulation made from c
	// (qenv.New(c)) records into the run's recorder. An error means the run
	// could not run to its end, not that it violates the model: it fails
	// the test, naming its seed.
	Run func(c qenv.Chooser) error
	// Mutant names the bug this system carries on purpose (a config's
	// mutation, "no-dedupe"): its explorations' evidence has role
	// "mutant", which a suite's report does not count as what the system
	// tried. Empty: the system as it is.
	Mutant string
}

// WithRates is the system at rates r: Explore it, Sweep it, Shrink it at
// other rates than its own.
func (s System) WithRates(r qenv.Rates) System {
	s.Rates = r
	return s
}

// FaultRates are the rates the system's workflows draw faults at: its
// Rates, or qenv.DefaultRates when they are zero. Encode a shrunk failure
// for the fuzz corpus at them: qenv.EncodeChoices(v.Choices,
// sys.FaultRates()).
func (s System) FaultRates() qenv.Rates {
	if s.Rates.Default == 0 && s.Rates.Kinds == nil {
		return qenv.DefaultRates
	}
	return s.Rates
}

func (s System) opts(more ...Option) []Option {
	out := append(append([]Option(nil), s.Opts...), more...)
	if s.Mutant != "" {
		out = append(out, func(c *settings) { c.mutant = s.Mutant })
	}
	return out
}

// Explore runs the system once per seed, each time with a seeded qenv
// chooser (wrapped in a qenv.ChoiceLog, so every run can be replayed) whose
// context carries a fresh step recorder, then checks every run in one
// batch (validate.Checker.CheckBatch, without ITF). It is how a simulated
// system is explored like Maelstrom or Jepsen explore real ones: many
// random schedules and faults, every one judged by the model. State
// expectations with Exploration.Expect.
func (s System) Explore(t testing.TB, cfg ExploreConfig) *Exploration {
	t.Helper()
	cfg.opts, cfg.rates = s.opts(cfg.opts...), s.FaultRates()
	return explore(t, s.Binding, cfg, s.Run)
}

// FollowTraces drives the whole system through each of the model's traces'
// faults (FaultScript): the trace's mapped actions are imposed as
// faults in its order, the system makes every other step itself, and its
// recorded run is checked against the model. A run that cannot follow its
// trace (a scripted fault it never came to) fails the test with the
// faults left. trace configures `quint run` (Dir defaults to a temporary
// directory).
func (s System) FollowTraces(t testing.TB, trace connect.Config, faults FaultMap) *Exploration {
	t.Helper()
	if trace.Dir == "" {
		trace.Dir = t.TempDir()
	}
	files, _, err := connect.GenerateRun(context.Background(), trace)
	if err != nil {
		t.Fatal(err)
	}
	scripts := make([]*qenv.Script, len(files))
	for i, f := range files {
		tr, err := itf.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if scripts[i], err = FaultScript(tr, faults, connect.ReplayOptions{Trace: i, StatePath: trace.StatePath, NondetPath: trace.NondetPath}); err != nil {
			t.Fatal(err)
		}
	}
	x := s.Explore(t, ExploreConfig{Seeds: len(files), Chooser: func(seed uint64) qenv.Chooser { return scripts[seed-1] }})
	for _, r := range x.Runs { // the traces that ran (one, run alone)
		if r.corpus != "" || r.seed < 1 || int(r.seed) > len(scripts) {
			continue
		}
		if rest := scripts[r.seed-1].Remaining(); len(rest) > 0 {
			t.Errorf("gotogether: trace %d: scripted faults never happened: %v (the run did not follow its trace: "+
				"the fault's kind never came up after the steps before it)", r.seed-1, rest)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	return x
}

// Sweep explores each covering row of sw as a subtest named by the row,
// checking its expectation there: it takes a *testing.T, not a
// testing.TB as the other workflows do, for t.Run. The system's Run reads
// the row from its chooser (RowOf) and configures itself from it: one Run
// for every workflow. A row's FaultKind factors that are off are off in
// the chooser.
func (s System) Sweep(t *testing.T, sw SweepConfig) *SweepResult {
	t.Helper()
	sw.Explore.opts, sw.Explore.rates = s.opts(sw.Explore.opts...), s.FaultRates()
	return sweep(t, s.Binding, sw, s.Run)
}

// Fuzz explores the system with Go's coverage-guided fuzzer: each input is
// one run, its bytes the run's choices (qenv.FromBytes at the system's
// Rates: schedules, faults), the model the oracle. Without -fuzz, go test
// runs the seeds and the corpus in testdata/fuzz as regression tests. Seed
// it with runs worth keeping: a shrunk Hegel failure, once fixed, is
// qenv.EncodeChoices(v.Choices, sys.FaultRates()). The empty input, every choice
// nominal, is always a seed. Inputs that repeat a run's steps take its
// verdict without quint.
//
//	func FuzzShop(f *testing.F) { sys.Fuzz(f) }
func (s System) Fuzz(f *testing.F, seeds ...[]byte) {
	f.Helper()
	f.Add([]byte{})
	for _, sd := range seeds {
		f.Add(sd)
	}
	rates := s.FaultRates()
	f.Fuzz(func(t *testing.T, data []byte) {
		s.Explore(t, ExploreConfig{Seeds: 1, Parallel: 1, fuzz: true,
			Chooser: func(uint64) qenv.Chooser { return qenv.FromBytes(data, rates) }}).Expect(t, Conforms())
	})
}

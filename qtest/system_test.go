package qtest

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// failTB records failures instead of failing the test, and stops like t.
type failTB struct {
	testing.TB
	errs   []string
	logs   []string
	failed bool
}

func (f *failTB) Helper() {}
func (f *failTB) Errorf(format string, a ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, a...))
	f.failed = true
}
func (f *failTB) Fatalf(format string, a ...any) { f.Errorf(format, a...); runtime.Goexit() }
func (f *failTB) Fatal(a ...any)                 { f.Errorf("%s", fmt.Sprint(a...)); runtime.Goexit() }
func (f *failTB) FailNow()                       { f.failed = true; runtime.Goexit() }
func (f *failTB) Failed() bool                   { return f.failed }
func (f *failTB) Logf(format string, args ...any) {
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
}

// A system whose Run fails fails the exploration, naming the seed, before
// any run is checked.
func TestSystemRunError(t *testing.T) {
	t.Parallel()
	f := &failTB{TB: t}
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		return errors.New("no database")
	}}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		sys.Explore(f, ExploreConfig{Seeds: 2, Parallel: 1})
	}()
	<-done
	if len(f.errs) != 2 || !strings.Contains(f.errs[0], "seed 1 did not run: no database") {
		t.Errorf("errors %q", f.errs)
	}
}

// A step from a goroutine one of a run's tasks started with `go` fails
// that run, by name, and is not logged process-wide, where a test in
// parallel would see it: whether the task is still running (seed 1) or
// has ended (seed 2: its simulation is still open).
func TestEscapedStepFailsItsRun(t *testing.T) {
	f := &failTB{TB: t}
	mark, _ := hooks.Escaped(0)
	runs := 0 // seeds run in order (Parallel: 1)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		gate, done := make(chan struct{}), make(chan struct{})
		runs++
		ended := runs == 2
		s.Spawn("client", func(ctx context.Context) {
			go func() { //qenv:ok the escape under test
				defer close(done)
				<-gate
				qobs.Record(context.Background(), "deposit", nil, "amount", 10, "obs.balance", 10) //qenv:ok the escape under test
			}()
			if !ended {
				close(gate)
				<-done //qenv:ok the goroutine records while its task runs
			}
		})
		err := s.Run(time.Second)
		if ended {
			close(gate)
			<-done
		}
		return err
	}}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		sys.Explore(f, ExploreConfig{Seeds: 2, Parallel: 1})
	}()
	<-done
	all := strings.Join(f.errs, "\n")
	for _, seed := range []string{"seed 1", "seed 2"} {
		if !strings.Contains(all, seed+" did not run: gotogether: 1 steps were recorded from goroutines the run's tasks started with `go`") {
			t.Errorf("%s: errors %q, want the run failed for its escaped step", seed, f.errs)
		}
	}
	if n, msgs := hooks.Escaped(mark); n != mark {
		t.Errorf("the run's escapes were logged process-wide too: %v", msgs)
	}
}

// A system whose runs differ from the same seed (here a counter outside
// the Chooser) fails the exploration, naming the first difference: none
// of its failures would replay.
func TestNondeterministicSystem(t *testing.T) {
	t.Parallel()
	f := &failTB{TB: t}
	calls := 0
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		calls++
		n := calls
		s.Spawn("client", func(ctx context.Context) {
			qobs.Record(ctx, "deposit", nil, "amount", n, "obs.balance", n)
		})
		return s.Run(time.Second)
	}}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		sys.Explore(f, ExploreConfig{Seeds: 1, Parallel: 1})
	}()
	<-done
	if all := strings.Join(f.errs, "\n"); !strings.Contains(all, "seed 1 ran twice made two runs, first different at step 0: deposit(amount=1") {
		t.Errorf("errors %q, want the difference named", f.errs)
	}
}

// A run whose system does not settle is checked all the same, and is no
// conforming run: Conforms fails naming it, Settles states it alone, and
// None(Settles()) holds of a livelock.
func TestUnsettledRuns(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	sys := func(livelock bool) System {
		return System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
			s := qenv.New(c)
			defer s.Close()
			s.Spawn("client", func(ctx context.Context) {
				qobs.Record(ctx, "deposit", nil, "amount", 10, "obs.balance", 10)
				for livelock {
					qenv.Sleep(ctx, time.Second) // retries forever
				}
			})
			return s.Settle(time.Minute)
		}}
	}
	sys(false).Explore(t, ExploreConfig{Seeds: 2}).Expect(t, Conforms(), Settles())
	x := sys(true).Explore(t, ExploreConfig{Seeds: 2})
	x.Expect(t, None(Settles()))
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		x.Expect(f, Conforms())
	}()
	<-done
	if all := strings.Join(f.errs, "\n"); !strings.Contains(all, "do not settle") || !strings.Contains(all, "client: waiting") {
		t.Errorf("Conforms of an unsettled run: %q, want it named, with its task", f.errs)
	}
}

// A binding's eventual property is judged at the end of a run whose
// system settled: a run that funds the account conforms, one that drains
// it violates "eventually funded"; a run that never settled says nothing
// of it.
func TestEventually(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	sys := func(settle bool, withdraw int) System {
		return System{Binding: "../validate/testdata/funds.binding.yaml", Run: func(c qenv.Chooser) error {
			s := qenv.New(c)
			defer s.Close()
			s.Spawn("client", func(ctx context.Context) {
				qobs.Record(ctx, "deposit", nil, "amount", 10, "obs.balance", 10)
				if withdraw > 0 {
					qobs.Record(ctx, "withdraw", nil, "amount", withdraw, "obs.balance", 10-withdraw)
				}
			})
			if settle {
				return s.Settle(time.Minute)
			}
			return s.Run(time.Minute)
		}}
	}
	funded := validate.EventuallyName("funded")
	held := sys(true, 0).Explore(t, ExploreConfig{Seeds: 1})
	held.Expect(t, Conforms(), All(Settles()))
	if invs := held.Runs[0].Report.Actors[0].Invariants; !slices.ContainsFunc(invs, func(i validate.InvariantResult) bool { return i.Name == funded && i.Verdict == validate.Pass }) {
		t.Errorf("a settled run's eventual property was not judged: %v", invs)
	}
	sys(true, 10).Explore(t, ExploreConfig{Seeds: 1}).Expect(t, All(Violates(funded)))
	sys(false, 10).Explore(t, ExploreConfig{Seeds: 1}).Expect(t, Conforms(), None(Settles())) // never settled: not judged
	// a recorded run is judged as an exploration's: once it records a settle
	scenario := func(settle bool) func(*qobs.Recorder) {
		return func(r *qobs.Recorder) {
			ctx := context.Background()
			r.Record(ctx, "deposit", nil, "amount", 10, "obs.balance", 10)
			r.Record(ctx, "withdraw", nil, "amount", 10, "obs.balance", 0)
			if settle {
				r.Settled(ctx)
			}
		}
	}
	Record(t, "../validate/testdata/funds.binding.yaml", scenario(false)).Expect(t, Conforms())
	Record(t, "../validate/testdata/funds.binding.yaml", scenario(true)).Expect(t, Violates(funded), Settles())
}

// A scenario that fails the test from its run's goroutine (t.Fatal in
// Run) fails the exploration with its seed, instead of a nil run
// crashing it.
func TestScenarioFatal(t *testing.T) {
	t.Parallel()
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		explore(f, "../validate/testdata/account.yaml", ExploreConfig{Seeds: 1, Parallel: 1}, func(qenv.Chooser) error {
			f.Fatalf("the gosim binary is missing")
			return nil
		})
	}()
	<-done
	if !f.failed || !strings.Contains(strings.Join(f.errs, "\n"), "seed 1 did not finish") {
		t.Fatalf("errors: %v", f.errs)
	}
}

// A task's panic is its run's failure, not the test binary's end: the
// exploration names the run, keeps it in the corpus, and shows the task's
// stack, where the panic happened.
func TestRunPanic(t *testing.T) {
	t.Parallel()
	f := &failTB{TB: t}
	corpus := t.TempDir()
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		s.Spawn("checker", func(ctx context.Context) {
			qenv.Yield(ctx)
			if s.Choose("sched.which", 2) == 1 {
				panicInTask()
			}
		})
		return s.Run(time.Second)
	}}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		sys.Explore(f, ExploreConfig{Seeds: 20, Parallel: 4, Corpus: corpus})
	}()
	<-done
	all := strings.Join(f.errs, "\n")
	if !strings.Contains(all, "panicked: the store's history check failed") || !strings.Contains(all, "panicInTask") ||
		!strings.Contains(all, "saved to the corpus") || !strings.Contains(all, "reproduce: GOTOGETHER_RUN='TestRunPanic#1:seed=") {
		t.Fatalf("errors: %s", all)
	}
	if !strings.Contains(all, "the task's stack") {
		t.Errorf("not named the task's stack: %s", all)
	}
	// the exploration stops after naming the runs that did not run (as
	// for a Run error): every panicking run is named, the rest ran
	if n := strings.Count(all, "panicked:"); n == 0 || n == 20 {
		t.Errorf("%d of 20 runs reported as panicked, want some: %s", n, all)
	}

	// the saved entries replay to the same panic, before any seed, in the
	// next run of the test, and say how to rerun them (the corpus, not a
	// seed)
	rerun(t)
	f = &failTB{TB: t}
	done = make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		sys.Explore(f, ExploreConfig{Seeds: 1, Parallel: 1, Corpus: corpus})
	}()
	<-done
	all = strings.Join(f.errs, "\n")
	if !strings.Contains(all, "corpus entry ") || !strings.Contains(all, "panicked: the store's history check failed") ||
		!strings.Contains(all, "GOTOGETHER_RUN='TestRunPanic#1:corpus=") || strings.Contains(all, "seed=0") {
		t.Errorf("the corpus's replay: %s", all)
	}
}

// A scenario that recovers a task's panic and then panics with another
// value is reported with its own stack, not the task's earlier one.
func TestRunPanicNotTheTasks(t *testing.T) {
	t.Parallel()
	f := &failTB{TB: t}
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		func() {
			defer func() { _ = recover() }()
			s := qenv.New(c)
			defer s.Close()
			s.Spawn("checker", func(ctx context.Context) { panicInTask() })
			_ = s.Run(time.Second)
		}()
		panic("the scenario's own")
	}}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		sys.Explore(f, ExploreConfig{Seeds: 1, Parallel: 1})
	}()
	<-done
	all := strings.Join(f.errs, "\n")
	if !strings.Contains(all, "panicked: the scenario's own") || !strings.Contains(all, "the scenario's stack") || strings.Contains(all, "panicInTask") {
		t.Errorf("errors: %s", all)
	}
}

// panicInTask is a named frame for the stack to show.
func panicInTask() { panic("the store's history check failed") }

// A Rates key that covers nothing declared (a misspelling, a kind renamed
// since: the queues' old shared one) fails the exploration with the fix,
// rather than set no rate, silently.
func TestUnknownRatesKey(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's FailNow ends the goroutine (runtime.Goexit)
		defer close(done)
		System{Binding: "../validate/testdata/account.yaml", Run: race, Rates: qenv.DefaultRates.Set("queue.ack", 0.5)}.Explore(f, ExploreConfig{Seeds: 1})
	}()
	<-done
	if !strings.Contains(strings.Join(f.errs, "\n"), `System.Rates key "queue.ack" covers no fault kind`) {
		t.Errorf("errors: %q", f.errs)
	}
	// so does a Bound.Without kind, which would leave the kind on
	f = &failTB{TB: t}
	done = make(chan struct{})
	go func() { //qenv:ok failTB's FailNow ends the goroutine (runtime.Goexit)
		defer close(done)
		System{Binding: "../validate/testdata/account.yaml", Run: race}.Exhaust(f, Bound{Faults: 0, Without: []string{"queue.ack"}})
	}()
	<-done
	if !strings.Contains(strings.Join(f.errs, "\n"), `Bound.Without kind "queue.ack" covers no fault kind`) {
		t.Errorf("errors: %q", f.errs)
	}
}

// An option a workflow ignores fails it, saying why and what to do: an
// exploration records each run on its own (Global) and checks it in its
// recorded order (Linearize); Validate records nothing.
func TestIgnoredOptionsRefused(t *testing.T) {
	RequireQuint(t)
	for _, c := range []struct {
		run  func(f *failTB)
		want string
	}{
		{func(f *failTB) {
			System{Binding: "../validate/testdata/account.yaml", Run: race, Opts: []Option{Global()}}.Explore(f, ExploreConfig{Seeds: 1})
		}, "Global() does nothing in an exploration"},
		{func(f *failTB) { Validate(f, "../validate/testdata/account.yaml", nil, SkipIfEmpty()) }, "SkipIfEmpty() does nothing in Validate"},
	} {
		f := &failTB{TB: t}
		done := make(chan struct{})
		go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
			defer close(done)
			c.run(f)
		}()
		<-done
		if len(f.errs) == 0 || !strings.Contains(f.errs[0], c.want) || !strings.Contains(f.errs[0], "Drop it") {
			t.Errorf("want %q, got %q", c.want, f.errs)
		}
	}
}

// An operation recorded at its invocation whose caller crashes before it
// completes is pending, no step: the run conforms, the report names it,
// and Completes fails of it; with no crash every call completes.
func TestInvokedNeverCompleted(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	sys := func(crash bool) System {
		return System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
			s := qenv.New(c)
			defer s.Close()
			var p *qenv.Process
			p = s.Process("client", true, func(ctx context.Context) {
				qobs.Invoke(ctx, "deposit", "amount", 10).Done(nil, "obs.balance", 10)
				w := qobs.Invoke(ctx, "withdraw", "amount", 3)
				if crash {
					p.Crash() // the caller dies: the withdrawal's outcome is unknown
				}
				w.Done(nil, "obs.balance", 7)
			})
			return s.Run(time.Minute)
		}}
	}
	x := sys(true).Explore(t, ExploreConfig{Seeds: 1})
	x.Expect(t, Conforms(), None(Completes()))
	if sum := x.Runs[0].Report.Summary(); !strings.Contains(sum, "invoked, never completed: withdraw(amount=3)") {
		t.Errorf("the report does not name the pending call:\n%s", sum)
	}
	done := sys(false).Explore(t, ExploreConfig{Seeds: 1})
	done.Expect(t, Conforms(), Completes())
	if n := len(slices.DeleteFunc(slices.Clone(done.Runs[0].Steps), func(s qtrace.Step) bool { return !s.Invocation })); n != 2 {
		t.Errorf("%d invocation records, want one per call: %v", n, done.Runs[0].Steps)
	}
}

// A client history in a simulation: the deposit takes effect at once and
// answers in 10ms, the withdrawal that needs it begins a millisecond later
// and answers first. Steps carry the simulation's time, so their intervals
// are its: in recorded order the withdrawal is rejected, and an
// exploration with Linearize finds the order the intervals allow.
func TestLinearizeInExploration(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	clients := func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		balance := 0
		s.Spawn("depositor", func(ctx context.Context) {
			start := s.Now()
			balance += 60
			seen := balance
			qenv.Sleep(ctx, 10*time.Millisecond)
			qobs.Record(ctx, "deposit", nil, "amount", 60, "obs.balance", seen, "invoked", start)
		})
		s.Spawn("withdrawer", func(ctx context.Context) {
			qenv.Sleep(ctx, time.Millisecond)
			start := s.Now()
			balance -= 50
			seen := balance
			qenv.Sleep(ctx, 4*time.Millisecond)
			qobs.Record(ctx, "withdraw", nil, "amount", 50, "obs.balance", seen, "invoked", start)
		})
		return s.Run(time.Second)
	}
	sys := System{Binding: "../validate/testdata/account.yaml", Run: clients, Rates: qenv.NoFaults}
	sys.Explore(t, ExploreConfig{Seeds: 3}).Expect(t, All(Rejects("withdraw")))
	sys.Opts = []Option{Linearize()}
	x := sys.Explore(t, ExploreConfig{Seeds: 3})
	x.Expect(t, Conforms())
	if s := x.Runs[0].Steps; s[0].Action != "deposit" || !strings.Contains(fmt.Sprint(x.Runs[0].Report.Issues), "an order of its 2 steps conforms") {
		t.Errorf("checked %v: %v", s, x.Runs[0].Report.Issues)
	}
}

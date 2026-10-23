package qtest

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
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

// A scenario that fails the test from its run's goroutine (t.Fatal in
// Run) fails the exploration with its seed, instead of a nil run
// crashing it.
func TestScenarioFatal(t *testing.T) {
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

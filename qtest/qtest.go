// Package qtest records a scenario's model steps and checks them against a
// Quint model inside a Go test: no OTel, no files to manage.
//
//	func TestWithdraw(t *testing.T) {
//		qtest.Record(t, "binding.yaml", func(rec *qobs.Recorder) {
//			acct.Withdraw(ctx, 20) // woven //quint:action steps go to rec too
//			rec.Record(ctx, "deposit", nil, "amount", 5, "obs.balance", 35)
//		}).Expect(t, qtest.Conforms())
//	}
//
// The recorder is installed as the global one for the scenario's duration,
// so annotated code built with `orchestrion go test` records into it.
// Every run leaves a native step log, a model-level ITF per actor, and the
// generated Quint modules in the run's directory (t.TempDir(), or
// $GOTOGETHER_KEEP/<test name> to keep them).
package qtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qcover"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qrun"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// settings are what the Options set, for every workflow (Record, Validate,
// a System's).
type settings struct {
	Options     validate.Options // quint binary, backend (default $QUINT_BACKEND, else rust)
	Dir         string           // artifacts; default $GOTOGETHER_KEEP/<test> or t.TempDir()
	Recorder    []qobs.Option    // e.g. qobs.WithActor("acct-1")
	SkipIfEmpty bool             // skip (not fail) when nothing was recorded, e.g. an unwoven build
	// Constants override the binding's (validate.Checker.WithConstants).
	Constants map[string]string
	// Invariants, if set, replace the binding's list
	// (validate.Checker.WithInvariants); empty and non-nil checks none.
	Invariants []string
	// Global installs Record's recorder as the global one (Global).
	Global bool
	// Linearize checks steps in an order real time allows (Linearize).
	Linearize bool
}

// An Option configures a check: Record's, Validate's, a System's
// workflows (System.Opts) and qhegel's.
type Option func(*settings)

// Quint sets how quint runs: its binary and backend (the default backend
// is $QUINT_BACKEND, else rust). Its Dir is ignored: artifacts go to
// $GOTOGETHER_KEEP/<test>, else a temporary directory.
func Quint(o validate.Options) Option { return func(c *settings) { c.Options = o } }

// RecorderOptions configure the run's recorder, e.g. qobs.WithActor("acct-1").
func RecorderOptions(opts ...qobs.Option) Option {
	return func(c *settings) { c.Recorder = append(c.Recorder, opts...) }
}

// SkipIfEmpty skips the test, rather than failing it, when nothing was
// recorded: an unwoven build of code that records through //quint:action.
func SkipIfEmpty() Option { return func(c *settings) { c.SkipIfEmpty = true } }

// Global makes Record install its recorder as the global one
// (qobs.InstallGlobal) while the scenario runs, for the steps of
// goroutines the scenario starts (a server's handlers, a cluster's
// nodes), which no run can follow. Such tests must not run in parallel:
// a second install fails. Without it only the scenario's goroutine, and
// code recording through its recorder's context, record into the run.
func Global() Option { return func(c *settings) { c.Global = true } }

// Constants checks against another instance of the binding's model: these
// constants override the binding's, e.g. Constants(map[string]string{"STOCK": "1000"}).
func Constants(m map[string]string) Option {
	return func(c *settings) {
		if c.Constants == nil {
			c.Constants = map[string]string{}
		}
		for k, v := range m {
			c.Constants[k] = v
		}
	}
}

// Invariants checks these of the model's invariants instead of the
// binding's list: none with Invariants(), for conformance alone, or a
// design-level property the binding leaves out.
func Invariants(names ...string) Option {
	return func(c *settings) { c.Invariants = append([]string{}, names...) }
}

// checkerFor is the cached checker for a binding, with c's constants and
// invariants.
func checkerFor(t testing.TB, bindingPath string, o validate.Options, c settings) *validate.Checker {
	t.Helper()
	ch := checker(t, bindingPath, o)
	var err error
	if len(c.Constants) > 0 {
		if ch, err = ch.WithConstants(c.Constants); err != nil {
			t.Fatalf("gotogether: %v", err)
		}
	}
	if c.Invariants != nil {
		if ch, err = ch.WithInvariants(c.Invariants...); err != nil {
			t.Fatalf("gotogether: %v", err)
		}
	}
	return ch
}

// Run is one checked run, whichever workflow made it: Record's, one seed of
// an Exploration, a Hegel Violation (embedded). A field a workflow has no
// value for is zero (Record makes no choices; an exploration writes no
// step log).
type Run struct {
	Seed uint64 // an exploration's seed, or an exhaustive search's run number
	// Schedule is an exhaustive search's run (System.Exhaust): the choices
	// it made otherwise than nominal, as choice:option pairs ("17:1,40:2").
	// GOTOGETHER_SCHEDULE runs it alone.
	Schedule string
	// Corpus is the corpus entry the run replayed (ExploreConfig.Corpus),
	// or "" for a seeded run.
	Corpus  string
	Steps   []qtrace.Step
	Choices []qenv.Choice // every choice of the run: qenv.Replay(Choices) repeats it
	// Interrupted are steps of work a crash or the end of the run cut off
	// (qobs.Recorder.Interrupted): not in Steps, not checked; reported with
	// a failing run.
	Interrupted []qtrace.Step
	// Unscripted are the faults of a Script chooser the run never came to
	// (qenv.Script.Remaining): it did not follow its script.
	Unscripted []qenv.Scripted
	// Code is the run's code coverage, when the code is woven (qweave -cover).
	Code *qcover.Counts
	// Err is the scenario's error: the run did not run to its end
	// (System.Run's error).
	Err     error
	Report  *validate.Report
	StepLog string            // Record's native step log (JSONL)
	ITF     map[string]string // Record's: actor -> model-level ITF written in-process
}

// Faults are the run's fault choices that were not nominal.
func (r *Run) Faults() []qenv.Choice { return (&qenv.ChoiceLog{Choices: r.Choices}).Faults() }

// OK reports whether every actor conformed and satisfied the invariants.
func (r *Run) OK() bool { return r.Report != nil && r.Report.OK() }

func checker(t testing.TB, bindingPath string, o validate.Options) *validate.Checker {
	t.Helper()
	c, err := validate.Cached(bindingPath, o) // typechecks the model and lints the binding once
	if err != nil {
		t.Fatalf("gotogether: %v", err)
	}
	return c
}

func config(t testing.TB, opts []Option) settings {
	var c settings
	for _, o := range opts {
		o(&c)
	}
	if c.Dir == "" {
		if keep := os.Getenv("GOTOGETHER_KEEP"); keep != "" {
			c.Dir = filepath.Join(keep, strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
		} else {
			c.Dir = t.TempDir()
		}
	}
	return c
}

// Record runs scenario with a fresh recorder (the scenario's goroutine's,
// and the global one with Global), then validates what it recorded. State
// the expected verdict with Run.Expect; Record itself fails the test only
// on tool errors and steps it could not place.
func Record(t testing.TB, bindingPath string, scenario func(rec *qobs.Recorder), opts ...Option) *Run {
	t.Helper()
	c := config(t, opts)
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := &Run{StepLog: filepath.Join(c.Dir, "steps.jsonl"), ITF: map[string]string{}}
	file, err := qobs.FileSink(run.StepLog)
	if err != nil {
		t.Fatal(err)
	}
	mem := &qobs.MemorySink{}
	rec := qobs.NewRecorder(qobs.Tee{mem, file}, c.Recorder...)
	// The scenario's goroutine is the run's (qrun.Start), whatever a test
	// in parallel does; the global recorder, with Global, is for the
	// goroutines it starts (servers, clusters), which no run can follow.
	// Without it, their steps would be lost: the run is strict, and they
	// fail the test.
	end := qrun.Start(qobs.WithRecorder(context.Background(), rec))
	restore := func() {}
	if c.Global {
		if restore, err = qobs.InstallGlobal(rec); err != nil {
			end()
			t.Fatalf("gotogether: %v", err)
		}
	}
	mark, _ := qobs.Escaped(0)
	func() {
		release := qobs.Strict()
		defer end()
		defer restore()
		defer release()
		scenario(rec)
	}()
	if err := file.Close(); err != nil {
		t.Fatalf("gotogether: step log: %v", err)
	}
	if n, msgs := qobs.Escaped(mark); n > mark && !c.Global {
		// a verdict on a run missing steps would only mislead
		t.Fatalf("gotogether: %d steps were recorded from goroutines outside the scenario's run (the first: %s); "+
			"record through the scenario's context (rec's: qobs.WithRecorder), or pass qtest.Global() for goroutines "+
			"the scenario starts", n-mark, firstOr(msgs))
	}
	for _, is := range rec.Issues() {
		t.Errorf("gotogether: recording: %s", is)
	}
	run.Steps = mem.Steps()
	if len(run.Steps) == 0 {
		if c.SkipIfEmpty {
			t.Skip("gotogether: no steps recorded (build with `orchestrion go test` to weave //quint:action)")
		}
		t.Fatal("gotogether: the scenario recorded no steps")
	}
	return check(t, bindingPath, run, c)
}

// Validate checks steps recorded elsewhere (a step log, a real run's
// sink): state the expected verdict with Run.Expect.
func Validate(t testing.TB, bindingPath string, steps []qtrace.Step, opts ...Option) *Run {
	t.Helper()
	c := config(t, opts)
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := &Run{Steps: steps, StepLog: filepath.Join(c.Dir, "steps.jsonl"), ITF: map[string]string{}}
	f, err := os.Create(run.StepLog)
	if err == nil {
		err = qtrace.WriteJSONL(f, steps)
		f.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	return check(t, bindingPath, run, c)
}

func check(t testing.TB, bindingPath string, run *Run, c settings) *Run {
	t.Helper()
	o := c.Options
	o.Dir = filepath.Join(c.Dir, "quint")
	ch := checkerFor(t, bindingPath, o, c)
	var found []qtrace.Issue
	if c.Linearize { // the order checked, the step log's and the ITF's
		// the search needs conformance only: without invariants each batch
		// is one quint process, not two
		lc, err := ch.WithInvariants()
		if err != nil {
			t.Fatalf("gotogether: %v", err)
		}
		lc.Options.Dir, lc.Options.NoITF = filepath.Join(o.Dir, "linearize"), true
		run.Steps, found = linearize(t, lc, run.Steps)
	}
	// The model-level trace, emitted in-process from the same steps.
	if itfs, _, err := ch.Binding.StepsToITF(run.Steps); err == nil {
		for actor, data := range itfs {
			p := filepath.Join(c.Dir, strings.NewReplacer("/", "_", " ", "_").Replace(actor)+".itf.json")
			if os.WriteFile(p, data, 0o644) == nil {
				run.ITF[actor] = p
			}
		}
	} else {
		t.Logf("gotogether: no model-level ITF: %v", err)
	}
	cc := *ch // per run: its own output directory
	cc.Options.Dir = o.Dir
	rep, err := cc.Check(context.Background(), run.Steps)
	if err != nil {
		t.Fatalf("gotogether: %v", err)
	}
	rep.Issues = append(rep.Issues, found...)
	run.Report = rep
	return run
}

// firstOr is the first of msgs, or a placeholder when none was kept.
func firstOr(msgs []string) string {
	if len(msgs) == 0 {
		return "not kept"
	}
	return msgs[0]
}

// Settings are a list of Options resolved for a test, for packages that
// build their checks on qtest's (qhegel). Configure with Options;
// Settings only read them. They are exported, not internal, because such a
// package may be another module (qhegel is to be one).
type Settings struct{ s settings }

// Resolve applies opts for t: the artifacts' directory defaults to
// $GOTOGETHER_KEEP/<test>, else a temporary directory.
func Resolve(t testing.TB, opts ...Option) Settings { return Settings{config(t, opts)} }

// Dir is where the check's artifacts go (generated modules, step logs).
func (s Settings) Dir() string { return s.s.Dir }

// Recorder are the recorder's options (RecorderOptions).
func (s Settings) Recorder() []qobs.Option { return s.s.Recorder }

// Global reports whether the run's recorder is installed globally (Global).
func (s Settings) Global() bool { return s.s.Global }

// Checker is the binding's checker (cached per binding) with the
// options' constants, invariants and quint options, its modules in
// Dir()/quint.
func (s Settings) Checker(t testing.TB, bindingPath string) *validate.Checker {
	t.Helper()
	o := s.s.Options
	o.Dir = filepath.Join(s.s.Dir, "quint")
	ch := checkerFor(t, bindingPath, o, s.s)
	cc := *ch
	cc.Options = o
	return &cc
}

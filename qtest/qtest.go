// Package qtest checks Go code against a Quint model inside a Go test. Its
// main entry is System: an application deployed in a simulated environment
// (qenv), explored over many seeded runs, each run's recorded steps
// checked against the model, and claims about them expected:
//
//	sys := qtest.System{Binding: "hello.binding.yaml", Run: func(c qenv.Chooser) error {
//		return hello.Run(c, hello.Faithful)
//	}}
//	sys.Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, qtest.Conforms())
//
// A System also runs every schedule within a bound (Exhaust), a covering
// array of configurations (Sweep), fuzzing (Fuzz) and mutants (Mutate).
// Claims are Conforms, Rejects, Mismatch and Violates, over runs Some,
// All and None, and what the runs exercised (Reaches, Exercised).
//
// Record and Validate check one scenario's steps, recorded in the test or
// read from a file, without a simulation:
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
	"fmt"
	"github.com/chucklehead-dev/gotogether/internal/hooks"
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
	tempDir     bool             // Dir is t.TempDir(), removed when the test ends
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
	// linearizeBudget is LinearizeBudget's (0: DefaultLinearizeBudget)
	linearizeBudget int
	// mutant is System.Mutant
	mutant string
	// given are the options given, by name, for the workflows that
	// ignore some to refuse them (refuse)
	given []string
}

// option is an option that records its name, so a workflow that ignores
// it can say so (refuse).
func option(name string, f func(*settings)) Option {
	return func(c *settings) {
		c.given = append(c.given, name)
		f(c)
	}
}

// refuse fails t for every option given that workflow ignores: an option
// that does nothing, silently, reads as a check the test does not make.
// ignored maps each such option's name to why, and what to do instead.
func (c settings) refuse(t testing.TB, workflow string, ignored map[string]string) {
	t.Helper()
	for _, n := range c.given {
		if why, ok := ignored[n]; ok {
			t.Fatalf("gotogether: %s() does nothing in %s: %s. Drop it", n, workflow, why)
		}
	}
}

// What the workflows ignore, and why: explorations (Explore, Exhaust,
// Mutate, Sweep, Fuzz, FollowTraces, and qhegel's runs) record each run
// into its own recorder and check it in its recorded order; Validate
// records nothing.
var (
	whyGlobal = "each run records into its own recorder, and a step from a goroutine of none fails the run (start it with qenv.Go, " +
		"or record through the run's context); Global is Record's, qhegel.Test's and FindViolation's, for goroutines no run can follow"
	whyLinearize = "its runs are checked in the order they were recorded; Linearize is Record's and Validate's, " +
		"for steps recorded where an operation was invoked and returned"
	exploreIgnores = map[string]string{"Global": whyGlobal, "Linearize": whyLinearize, "LinearizeBudget": whyLinearize}
	whyCaseOrder   = "a case's steps are checked in the order it recorded them; Linearize is qtest.Record's and Validate's"
	casesIgnore    = map[string]string{"Linearize": whyCaseOrder, "LinearizeBudget": whyCaseOrder,
		"SkipIfEmpty": "it is qtest.Record's and explorations' (and a System's cases'), for an unwoven build that records nothing"}
	whyUnchecked    = "no case is checked against the model"
	uncheckedIgnore = map[string]string{"Constants": whyUnchecked, "Invariants": whyUnchecked, "Quint": whyUnchecked}
	validateIgnores = map[string]string{
		"Global":          "Validate checks steps already recorded: there is no recorder to install",
		"RecorderOptions": "Validate checks steps already recorded: there is no recorder to configure",
		"SkipIfEmpty":     "it is Record's and explorations', for an unwoven build that records nothing; skip on len(steps) == 0 instead",
	}
)

// An Option configures a check: Record's, Validate's, a System's
// workflows (System.Opts) and qhegel's.
type Option func(*settings)

// Quint sets how quint runs: its binary and backend (the default backend
// is $QUINT_BACKEND, else rust). Its Dir is ignored: artifacts go to
// $GOTOGETHER_KEEP/<test>, else a temporary directory.
func Quint(o validate.Options) Option { return option("Quint", func(c *settings) { c.Options = o }) }

// RecorderOptions configure the run's recorder, e.g. qobs.WithActor("acct-1").
func RecorderOptions(opts ...qobs.Option) Option {
	return option("RecorderOptions", func(c *settings) { c.Recorder = append(c.Recorder, opts...) })
}

// SkipIfEmpty skips the test, rather than failing it, when nothing was
// recorded: an unwoven build of code that records through //quint:action.
func SkipIfEmpty() Option { return option("SkipIfEmpty", func(c *settings) { c.SkipIfEmpty = true }) }

// Global makes Record install its recorder as the global one
// (qobs.InstallGlobal) while the scenario runs, for the steps of
// goroutines the scenario starts (a server's handlers, a cluster's
// nodes), which no run can follow. Such tests must not run in parallel:
// a second install fails. Without it only the scenario's goroutine, and
// code recording through its recorder's context, record into the run. It
// is Record's and qhegel's: an exploration and Validate refuse it.
func Global() Option { return option("Global", func(c *settings) { c.Global = true }) }

// Constants checks against another instance of the binding's model: these
// constants override the binding's, e.g. Constants(map[string]string{"STOCK": "1000"}).
func Constants(m map[string]string) Option {
	return option("Constants", func(c *settings) {
		if c.Constants == nil {
			c.Constants = map[string]string{}
		}
		for k, v := range m {
			c.Constants[k] = v
		}
	})
}

// Invariants checks these of the model's invariants instead of the
// binding's list: none with Invariants(), for conformance alone, or a
// design-level property the binding leaves out.
func Invariants(names ...string) Option {
	return option("Invariants", func(c *settings) { c.Invariants = append([]string{}, names...) })
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

// ID is the run's identity in its exploration, as GOTOGETHER_RUN names it
// after the exploration ("seed=17", "schedule=17:1", "corpus=<file>"),
// or "" for a run of no exploration (Record's, Validate's, Judge's).
func (r *Run) ID() string {
	switch {
	case r.corpus != "":
		return "corpus=" + filepath.Base(r.corpus) // as GOTOGETHER_RUN matches it
	case r.exhaustive:
		return "schedule=" + r.schedule
	case r.seed != 0:
		return fmt.Sprintf("seed=%d", r.seed)
	}
	return ""
}

// Run is one checked run, whichever workflow made it: Record's, one seed of
// an Exploration, a Hegel Violation (embedded). A field a workflow has no
// value for is zero (Record makes no choices; an exploration writes no
// step log).
type Run struct {
	seed uint64 // an exploration's seed, or an exhaustive search's run number
	// schedule is an exhaustive search's run (System.Exhaust): the choices
	// it made otherwise than nominal, as choice:option pairs ("17:1,40:2").
	// GOTOGETHER_RUN runs it alone.
	schedule string
	// exhaustive is a run of an exhaustive search, whose schedule names
	// it ("" for the nominal one)
	exhaustive bool
	// corpus is the corpus entry the run replayed (ExploreConfig.Corpus),
	// or "" for a seeded run.
	corpus  string
	Steps   []qtrace.Step
	Choices []qenv.Choice // every choice of the run: qenv.Replay(Choices) repeats it
	// Nemeses are the nemeses the run's nemesis choices offered, by name
	// (qenv.ChoiceLog.Nemeses): Exercised wants each fired in some run.
	Nemeses []string
	// Interrupted are steps of work a crash or the end of the run cut off
	// (qobs.Recorder.Interrupted): not in Steps, not checked; reported with
	// a failing run.
	Interrupted []qtrace.Step
	// Unscripted are the faults of a Script chooser the run never came to
	// (qenv.Script.Remaining): it did not follow its script.
	Unscripted []qenv.Scripted
	// Code is the run's code coverage, when the code is woven (qweave -cover).
	Code *qcover.Counts
	// savedFrom is a corpus entry's seed, the run it was saved from
	savedFrom uint64
	// scripted is a run a Script chose for: it makes only its own faults;
	// misnamed are what is wrong with its script's faults (Script.Misnamed)
	scripted bool
	misnamed []string
	// diverged is why the run's replay stopped repeating its choices (a
	// corpus entry the code no longer follows), apart from Err
	diverged error
	// Err is the scenario's error: the run did not run to its end
	// (System.Run's error).
	Err error
	// Unsettled is why the system did not settle, when the run had it
	// settle (a qenv.Unsettled from Sim.Settle): no error of the run, an
	// outcome the claims judge. It makes the run's report not OK, so
	// Conforms does not hold of it; Settles states it alone.
	Unsettled string
	// settled: its simulation settled (Sim.Settle found it quiet), so the
	// binding's eventual properties apply to it
	settled  bool
	adjusted *validate.Report // the report adjust made: it is not made again
	Report   *validate.Report
	StepLog  string            // Record's native step log (JSONL)
	ITF      map[string]string // Record's: actor -> model-level ITF written in-process
}

// Faults are the run's fault choices that were not nominal.
func (r *Run) Faults() []qenv.Choice { return (&qenv.ChoiceLog{Choices: r.Choices}).Faults() }

// OK reports whether every actor conformed and satisfied the invariants.
func (r *Run) OK() bool { return r.Report != nil && r.Report.OK() }

// adjust fits the run's report to how the run ended, on a copy (verdicts
// are shared by every run of the same steps, which may have ended
// otherwise): its Unsettled, and the binding's eventual properties only
// if its system settled. A run cut short, or not settled, says nothing of
// what it would have finished.
func (r *Run) adjust() {
	if r.Report == nil || r.adjusted == r.Report {
		return
	}
	rep := *r.Report
	rep.Unsettled = r.Unsettled
	if !r.settled {
		rep.Actors = make([]*validate.Result, len(r.Report.Actors))
		for i, a := range r.Report.Actors {
			b := *a
			b.Invariants = nil
			for _, inv := range a.Invariants {
				if !strings.HasPrefix(inv.Name, validate.EventuallyName("")) {
					b.Invariants = append(b.Invariants, inv)
				}
			}
			rep.Actors[i] = &b
		}
	}
	r.Report = &rep
	r.adjusted = r.Report
}

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
			c.Dir = filepath.Join(keep, safeName(t.Name()))
		} else {
			c.Dir, c.tempDir = t.TempDir(), true
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
	file, err := qobs.FileSink(run.StepLog, qobs.PerStep)
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
	mark, _ := hooks.Escaped(0)
	func() {
		release := hooks.Strict()
		defer end()
		defer restore()
		defer release()
		scenario(rec)
	}()
	if err := file.Close(); err != nil {
		t.Fatalf("gotogether: step log: %v", err)
	}
	if n, msgs := hooks.Escaped(mark); n > mark && !c.Global {
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
	c.refuse(t, "Validate", validateIgnores)
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
	var undecided map[string]bool
	var kept map[string][]qtrace.Issue
	if c.Linearize { // the order checked, the step log's and the ITF's
		// the search needs conformance only: without invariants each batch
		// is one quint process, not two
		lc, err := ch.WithInvariants()
		if err != nil {
			t.Fatalf("gotogether: %v", err)
		}
		lc.Options.Dir, lc.Options.NoITF = filepath.Join(o.Dir, "linearize"), true
		budget := c.linearizeBudget
		if budget == 0 {
			budget = DefaultLinearizeBudget
		}
		run.Steps, found, undecided, kept = linearize(t, lc, run.Steps, budget)
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
	for _, a := range rep.Actors {
		a.Issues = append(a.Issues, kept[a.Actor]...)
		if undecided[a.Actor] && a.Conforms == validate.Fail {
			a.Conforms = validate.NotEvaluated // the search ran out of budget: the order shown is not a verdict
		}
	}
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

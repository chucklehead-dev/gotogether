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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"

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
	// clockSkew is ClockSkew's (0: qtrace.RealTimeSlack)
	clockSkew time.Duration
	// linearizeKey is LinearizeByKey's argument ("": no partition)
	linearizeKey string
	// resolvePending is ResolvePending's
	resolvePending bool
	// mutant is System.Mutant
	mutant string
	// stepMutations are StepMutations', for MutateSteps
	stepMutations []qtrace.MutationOption
	// driver and artifacts are a run's manifest's (Driver, Artifacts)
	driver    *validate.Driver
	artifacts []validate.Artifact
	// given are the options given, by name, for the workflows that
	// ignore some to refuse them (refuse)
	given []optionID
}

// An optionID is an option's identity, with its name as the refusals
// print it: a workflow's ignored options are keyed by it, so a key that
// names no option does not compile (string keys once could hold a
// misspelling that refused nothing).
type optionID struct{ name string }

// The options a workflow may refuse.
// Read-only.
var (
	optConstants       = optionID{"Constants"}
	optGlobal          = optionID{"Global"}
	optInvariants      = optionID{"Invariants"}
	optLinearize       = optionID{"Linearize"}
	optLinearizeBudget = optionID{"LinearizeBudget"}
	optLinearizeByKey  = optionID{"LinearizeByKey"}
	optClockSkew       = optionID{"ClockSkew"}
	optQuint           = optionID{"Quint"}
	optRecorderOptions = optionID{"RecorderOptions"}
	optResolvePending  = optionID{"ResolvePending"}
	optSkipIfEmpty     = optionID{"SkipIfEmpty"}
	optDriver          = optionID{"Driver"}
	optArtifacts       = optionID{"Artifacts"}
	optStepMutations   = optionID{"StepMutations"}
)

// option is an option that records its identity, so a workflow that
// ignores it can say so (refuse).
func option(id optionID, f func(*settings)) Option {
	return func(c *settings) {
		c.given = append(c.given, id)
		f(c)
	}
}

// refuse fails t for every option given that workflow ignores: an option
// that does nothing, silently, reads as a check the test does not make.
// ignored maps each such option's name to why, and what to do instead.
func (c settings) refuse(t testing.TB, workflow string, ignored map[optionID]string) {
	t.Helper()
	for _, id := range c.given {
		if why, ok := ignored[id]; ok {
			t.Fatalf("gotogether: %s() does nothing in %s: %s. Drop it", id.name, workflow, why)
		}
	}
}

// What the workflows ignore, and why: explorations (Explore, Exhaust,
// Mutate, Sweep, Fuzz, FollowTraces) record each run into its own
// recorder, searched as Record's and Validate's are (Linearize,
// ResolvePending); qhegel's cases are checked as they record, in their
// recorded order; Validate records nothing.
// Read-only.
var (
	whyGlobal = "each run records into its own recorder, and a step from a goroutine of none fails the run (start it with qenv.Go, " +
		"or record through the run's context); Global is Record's, qhegel.Test's and FindViolation's, for goroutines no run can follow"
	whyManifest    = "a manifest is of a run gotogether did not drive, Validate's or Record's; an exploration's runs are its own, each reproduced by GOTOGETHER_RUN"
	whyStepMutants = "it narrows MutateSteps' observation mutants; pass the steps to qtest.MutateSteps"
	exploreIgnores = map[optionID]string{optGlobal: whyGlobal, optDriver: whyManifest, optArtifacts: whyManifest, optStepMutations: whyStepMutants}
	whyCaseOrder   = "a case's steps are checked as it records them, in that order; Linearize and ResolvePending are Record's, Validate's and explorations'"
	casesIgnore    = map[optionID]string{optLinearize: whyCaseOrder, optLinearizeBudget: whyCaseOrder, optLinearizeByKey: whyCaseOrder, optClockSkew: whyCaseOrder, optResolvePending: whyCaseOrder,
		optSkipIfEmpty: "it is qtest.Record's and explorations' (and a System's cases'), for an unwoven build that records nothing",
		optDriver:      whyManifest, optArtifacts: whyManifest, optStepMutations: whyStepMutants}
	whyUnchecked    = "no case is checked against the model"
	uncheckedIgnore = map[optionID]string{optConstants: whyUnchecked, optInvariants: whyUnchecked, optQuint: whyUnchecked}
	validateIgnores = map[optionID]string{
		optGlobal:          "Validate checks steps already recorded: there is no recorder to install",
		optRecorderOptions: "Validate checks steps already recorded: there is no recorder to configure",
		optSkipIfEmpty:     "it is Record's and explorations', for an unwoven build that records nothing; skip on len(steps) == 0 instead",
		optStepMutations:   whyStepMutants,
	}
	recordIgnores = map[optionID]string{optStepMutations: whyStepMutants}
)

// An Option configures a check: Record's, Validate's, a System's
// workflows (System.Opts) and qhegel's.
type Option func(*settings)

// Quint sets how quint runs: its binary and backend (the default backend
// is $QUINT_BACKEND, else rust). Its Dir is ignored: artifacts go to
// $GOTOGETHER_KEEP/<test>, else a temporary directory.
func Quint(o validate.Options) Option { return option(optQuint, func(c *settings) { c.Options = o }) }

// RecorderOptions configure the run's recorder, e.g. qobs.WithActor("acct-1").
func RecorderOptions(opts ...qobs.Option) Option {
	return option(optRecorderOptions, func(c *settings) { c.Recorder = append(c.Recorder, opts...) })
}

// SkipIfEmpty skips the test, rather than failing it, when nothing was
// recorded: an unwoven build of code that records through //quint:action.
// Under GOTOGETHER_STRICT=1 the skip fails (Optional): a job that weaves
// must not pass by recording nothing.
func SkipIfEmpty() Option { return option(optSkipIfEmpty, func(c *settings) { c.SkipIfEmpty = true }) }

// Global makes Record install its recorder as the global one
// (qobs.InstallGlobal) while the scenario runs, for the steps of
// goroutines the scenario starts (a cluster's nodes, a server's handlers
// when the server cannot be wrapped), which no run can follow. Such tests
// must not run in parallel: a second install fails. Without it only the
// scenario's goroutine, and code recording through its recorder's context,
// record into the run: an HTTP server's handlers do, in parallel with
// other runs, behind qobshttp.Handler(h, rec). It is Record's and
// qhegel's: an exploration and Validate refuse it.
func Global() Option { return option(optGlobal, func(c *settings) { c.Global = true }) }

// Constants checks against another instance of the binding's model: these
// constants override the binding's, e.g. Constants(map[string]string{"STOCK": "1000"}).
func Constants(m map[string]string) Option {
	return option(optConstants, func(c *settings) {
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
	return option(optInvariants, func(c *settings) { c.Invariants = append([]string{}, names...) })
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
	switch r.origin.kind {
	case "corpus":
		return "corpus=" + filepath.Base(r.origin.value) // as GOTOGETHER_RUN matches it
	case "choices", "schedule":
		return r.origin.kind + "=" + r.origin.value
	case "seed":
		return fmt.Sprintf("seed=%d", r.origin.seed)
	}
	return ""
}

// origin is where a run of an exploration came from, as GOTOGETHER_RUN
// names it: its kind says which of the other fields mean what.
type origin struct {
	// kind is "seed", "schedule" (an exhaustive search's run, System.Exhaust),
	// "corpus" (a corpus entry replayed, ExploreConfig.Corpus), "choices"
	// (a steered run's saved choices, ExploreConfig.Steer), or "" for a run
	// of no exploration
	kind string
	// value is a schedule's choices made otherwise than nominal, as
	// choice:option pairs ("17:1,40:2"; "" for the nominal run), a corpus
	// entry's path, or a steered run's choices file
	value string
	// seed is a seeded run's seed, an exhaustive search's run number, or
	// the seed a corpus entry was saved from
	seed uint64
	// from is the run a steered run was steered from
	from string
}

// Run is one checked run, whichever workflow made it: Record's, one seed of
// an Exploration, a Hegel Violation (embedded). A field a workflow has no
// value for is zero (Record makes no choices; an exploration writes no
// step log).
type Run struct {
	origin origin // where the run came from, in its exploration
	// searched: a search ordered Steps (Recorded is what the run recorded)
	searched bool
	// Steps are the steps judged: as recorded, or the order a search
	// (Linearize, ResolvePending) found, re-sequenced onto a recorder per
	// actor. Recorded are the steps as the run recorded them.
	Steps, Recorded []qtrace.Step
	Choices         []qenv.Choice // every choice of the run: qenv.Replay(Choices) repeats it
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
	// checks are the invariant names its check judges (checkedNames)
	checks map[string]bool
	// checker is the instance it was checked against (Validate's, Record's):
	// Runs judges claims over such runs by its names
	checker *validate.Checker
	// search, driver, artifacts and name are its manifest's (Manifest):
	// the searches it was judged with, its options' driver and artifacts,
	// its test
	search    validate.SearchOptions
	driver    *validate.Driver
	artifacts []validate.Artifact
	name      string
	// pct is what a PCT chooser saw of its guarantee's conditions (nil:
	// none scheduled the run)
	pct *pctRun
	// scripted is a run a Script chose for: it makes only its own faults;
	// misnamed are what is wrong with its script's faults (Script.Misnamed)
	scripted bool
	misnamed []string
	// faults are the fault kinds, options and nemeses the run declared
	// or chose, for the names users give (Rates keys, exceptions)
	faults *hooks.FaultNames
	// notes are what its doubles saw that no step says (hooks.Notes:
	// requests refused before their server listened), for a run that
	// recorded nothing
	notes []string
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
	adjusted  *validate.Report // the report adjust made: it is not made again
	Report    *validate.Report
	StepLog   string            // Record's native step log (JSONL)
	ITF       map[string]string // Record's: actor -> model-level ITF written in-process
	// keep copies the run's files (Record's, Validate's) out of the
	// test's temporary directory when a report names them; nil when they
	// are kept already (GOTOGETHER_KEEP, a Dir option)
	keep *keeper
}

// Faults are the run's fault choices that were not nominal.
func (r *Run) Faults() []qenv.Choice { return (&qenv.ChoiceLog{Choices: r.Choices}).Faults() }

// OK reports whether every actor conformed and satisfied the invariants.
func (r *Run) OK() bool { return r.Report != nil && r.Report.OK() }

// adjust fits the run's report to how the run ended, on a copy (verdicts
// are shared by every run of the same steps, which may have ended
// otherwise): its Unsettled, and for a run that did not settle the cycle
// its model states went round with an eventual val false, if any
// (validate.Checker.MarkHot, cc's: evidence, no verdict). Whether
// eventual properties apply is in the steps (a settle record), so the
// verdict says it.
func (r *Run) adjust(cc *validate.Checker) {
	if r.Report == nil || r.adjusted == r.Report {
		return
	}
	rep := *r.Report
	rep.Unsettled = r.Unsettled
	if r.Unsettled != "" && cc != nil {
		rep.Actors = make([]*validate.Result, len(r.Report.Actors))
		for i, a := range r.Report.Actors {
			c := *a
			rep.Actors[i] = &c
		}
		cc.MarkHot(&rep, r.Steps)
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
	c.refuse(t, "Record", recordIgnores)
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
	end := qrun.Start(qobs.WithRecorder(context.Background(), rec)) //quintctx:ok the run's context: a step recorded after the test is reported late, not cancelled
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
	// the run is judged as the scenario left it: a step recorded later
	// (a handler still running, a goroutine) fails the test, named, when
	// it ends, and after it is logged as escaped, never lost silently
	closeErr := rec.Close() // closes the step log (the Tee's file)
	t.Cleanup(func() {
		if late := rec.Late(); len(late) > 0 {
			t.Errorf("gotogether: %d steps were recorded on the run's recorder after the scenario returned (the first: %s): the run was judged "+
				"without them; wait for the work that records them before the scenario returns (an HTTP server: close it, then qobshttp's "+
				"Recording.Wait, which waits for hijacked connections' handlers too; a goroutine: wait for it)", len(late), late[0].String())
		}
	})
	if closeErr != nil {
		t.Fatalf("gotogether: step log: %v", closeErr)
	}
	if n, msgs := hooks.Escaped(mark); n > mark && !c.Global {
		// a verdict on a run missing steps would only mislead
		t.Fatalf("gotogether: %d steps were recorded from goroutines outside the scenario's run (the first: %s); "+
			"record through the scenario's context (rec's: qobs.WithRecorder), wrap an HTTP server's handler "+
			"(qobshttp.Handler(h, rec)), or pass qtest.Global() for goroutines the scenario starts", n-mark, firstOr(msgs))
	}
	for _, is := range rec.Issues() {
		t.Errorf("gotogether: recording: %s", is)
	}
	run.Steps = mem.Steps()
	run.Recorded = run.Steps
	if len(run.Steps) == 0 {
		if c.SkipIfEmpty {
			skipping(t, "gotogether: no steps recorded (build with `orchestrion go test` to weave //quint:action)")
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
	run := &Run{Steps: steps, Recorded: steps, StepLog: filepath.Join(c.Dir, "steps.jsonl"), ITF: map[string]string{}}
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
	run.Recorded = run.Steps
	cc := *ch // per run: its own output directory
	cc.Options.Dir = o.Dir
	j, err := cc.Judge(t.Context(), run.Steps, c.searchOptions())
	if err != nil {
		t.Fatalf("gotogether: %v", err)
	}
	run.Steps = j.Steps // the order checked, the step log's and the ITF's
	// The model-level trace, emitted in-process from the same steps.
	if itfs, _, err := ch.Binding.StepsToITF(run.Steps); err == nil {
		for actor, data := range itfs {
			p := filepath.Join(c.Dir, strings.NewReplacer("/", "_", " ", "_").Replace(actor)+".itf.json")
			if os.WriteFile(p, data, 0o644) == nil {
				run.ITF[actor] = p
			}
		}
	} else {
		// the ITF is an artifact (Run.ITF: the observed trace as a file, for
		// a reader or a replay), made in-process from literal arguments only;
		// the verdict is quint's, which evaluates every argument, and is not
		// affected
		t.Logf("gotogether: no model-level ITF file (Run.ITF) for this run, the verdict unaffected (quint evaluated every argument; only the file, written for inspection, needs literals): %v", err)
	}
	run.Report = j.Report
	run.checks, run.checker = checkedNames(ch), ch
	run.search, run.driver, run.artifacts, run.name = c.searchOptions(), c.driver, c.artifacts, t.Name()
	if c.tempDir {
		run.keep = &keeper{dir: c.Dir, name: t.Name()}
	}
	writeManifest(t, run)
	return run
}

// checkedNames are the invariant names a check with ch judges: the
// binding's invariants, eventual vals and bounded-response obligations,
// as reports name them. A claim's names are checked against them, not
// against the reports' (none, when no run judged one: a misspelled name
// then held for nothing).
func checkedNames(ch *validate.Checker) map[string]bool {
	m := map[string]bool{}
	for _, inv := range ch.Binding.Invariants {
		m[inv] = true
	}
	for _, ev := range ch.Binding.Eventually {
		m[validate.EventuallyName(ev)] = true
	}
	for _, w := range ch.Binding.Within {
		m[w.Name()] = true
	}
	return m
}

// searchOptions are the searches c asks for (Linearize, LinearizeByKey,
// ResolvePending): none, the steps as recorded.
func (c settings) searchOptions() validate.SearchOptions {
	return validate.SearchOptions{Linearize: c.Linearize, Key: c.linearizeKey, Budget: c.linearizeBudget, ResolvePending: c.resolvePending, Skew: c.clockSkew}
}

// firstOr is the first of msgs, or a placeholder when none was kept.
func firstOr(msgs []string) string {
	if len(msgs) == 0 {
		return "not kept"
	}
	return msgs[0]
}

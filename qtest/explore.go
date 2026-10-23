package qtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/chucklehead-dev/gotogether/qcover"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// An Exploration is many seeded runs of a simulated scenario, checked
// against the model in bulk.
type Exploration struct {
	Runs []*Run
	// Cached counts the runs judged from an earlier verdict on the same
	// steps against the same instance (this exploration's, or any before
	// it in the test binary), without running quint.
	Cached int

	constants map[string]string // the instance checked against, for messages
	id        int               // its number among its test's explorations (ref)
	alone     *runSpec          // the run GOTOGETHER_RUN runs alone, if it names this exploration
	dir       string            // its artifacts' directory
	tempDir   bool              // dir is the test's t.TempDir()
	kept      string            // where a failure's artifacts were copied out of it
	keptOnce  sync.Once
	model     string            // the binding's spec and module ("model/m.qnt:m"), for Evidence
	test      string            // the test's name, for reproducing a seed
	actions   map[string]bool   // the model's, for checking claims' names
	search    *search           // an exhaustive search's bound and extent (System.Exhaust), else nil
	checker   *validate.Checker // the runs' instance, for witnesses (vacuity)
	corpus    string            // the corpus directory (ExploreConfig.Corpus), or ""
	emptyOK   bool              // ExploreConfig.emptyOK
	mutant    bool              // its corpus runs are a baseline's, under a mutant (corpusOf)
	replay    bool              // ExploreConfig.Replay
	mutation  string            // System.Mutant
	fuzz      bool              // its run is a fuzz input's (System.Fuzz)
	mu        sync.Mutex        // guards recheck's report swaps
	vacuity   *vacuityResult    // computed once (Vacuous)
}

// faultCoverage is the runs' fault coverage (qenv.CoverageOf): for each fault
// kind, declared with //quint:fault or a double's, how many runs reached it
// and which options they took. A declared point no run reached is a
// failure path the exploration never tried.
func (x *Exploration) faultCoverage() *qenv.Coverage {
	runs := make([][]qenv.Choice, len(x.Runs))
	for i, r := range x.Runs {
		runs[i] = r.Choices
	}
	return qenv.CoverageOf(runs)
}

// codeCoverage is the runs' code coverage read against their steps
// (collected when the code is woven with qweave -cover): what no run ran, what ran under no step, and
// the outcomes of an action that ran the same code.
func (x *Exploration) codeCoverage() *qcover.Summary {
	runs := make([]*qcover.Counts, len(x.Runs))
	for i, r := range x.Runs {
		runs[i] = r.Code
	}
	return qcover.Summarize(runs)
}

// Failing returns the runs that violate the model, in seed order.
func (x *Exploration) Failing() []*Run {
	var out []*Run
	for _, r := range x.Runs {
		if !r.Report.OK() {
			out = append(out, r)
		}
	}
	return out
}

// ExploreConfig configures Explore.
type ExploreConfig struct {
	// Seeds is the number of runs, with seeds 1..Seeds (default 50).
	// GOTOGETHER_RUN in the environment runs one exploration's run alone,
	// as a failure's report says to reproduce it. GOTOGETHER_SEEDS=n and
	// GOTOGETHER_SEED_OFFSET=k widen the sample (a nightly run): n seeds
	// from k+1, unless Chooser makes the runs (a script per trace).
	Seeds int
	// Rates are the fault rates (default qenv.DefaultRates). Fuzz and
	// qhegel.Shrink take rates as an argument instead: nothing else here
	// applies to them.
	Rates qenv.Rates
	opts  []Option // the System's, and a sweep row's (System.Opts configures)
	// emptyOK returns runs that recorded nothing unchecked, rather than
	// failing: a mutant's (Mutate), which the baseline's steps show is
	// killed by it.
	emptyOK bool
	fuzz    bool // System.Fuzz: the run is the fuzz input's
	// corpusOf is the exploration whose corpus entries these runs replay
	// (a mutant's: its baseline's), else its own; with it a replay that
	// diverges is the mutant's doing, not a stale entry
	corpusOf string
	// Parallel runs this many seeds at once (default GOMAXPROCS; 1: one
	// after another). Each run records into its own recorder, carried by
	// the chooser's context (qenv.WithContext), so the scenario must build
	// its simulation from the chooser it is given and keep no state across
	// runs. A step lands in its run if it is recorded through a context of
	// the run or from a goroutine of the simulation (its tasks, the one
	// that created it); one recorded from a goroutine started with `go`
	// has nowhere to go and fails the exploration (qobs.Strict,
	// qobs.Escaped).
	Parallel int
	// Chooser, if set, makes each seed's chooser instead of a seeded one
	// at Rates: a Script per seed, a Replay of a recorded run, a swarm.
	Chooser func(seed uint64) qenv.Chooser
	// Corpus is a directory of saved runs (their choices, as JSON),
	// replayed before the seeds: a run that once failed, kept as a
	// regression. When a claim fails, the run it shows is saved there. An
	// entry names the exploration that saved it (TestX#2), which alone
	// replays it, so a test's explorations may share the directory; one
	// that no longer replays (the code changed) fails as stale, saying
	// how to find its run again. GOTOGETHER_CORPUS=dir gives every
	// exploration dir/<test name> (a nightly run collecting what it finds).
	Corpus string
	// Replay marks the exploration as a replay of a failure found
	// elsewhere (qhegel.Shrink's): its evidence's role is "replay", which
	// a suite's report does not count as what it tried.
	Replay bool
}

// corpusFormat names a corpus entry's format: an entry of another is
// refused with the fix, never replayed as something else.
const corpusFormat = "gotogether.run/1"

// A corpusEntry is a saved run: its choices replay it (qenv.Replay). It
// names the exploration that saved it (TestX#2), which alone replays it,
// so a test's explorations can share a directory, and the seed it came
// from, which finds it again when it no longer replays.
type corpusEntry struct {
	Format      string        `json:"format"`
	Exploration string        `json:"exploration"`
	Seed        uint64        `json:"seed,omitempty"`
	Choices     []qenv.Choice `json:"choices"`
}

// corpusDir is the exploration's corpus directory, or "".
func corpusDir(t testing.TB, cfg ExploreConfig) string {
	if cfg.Corpus != "" {
		return cfg.Corpus
	}
	if d := os.Getenv("GOTOGETHER_CORPUS"); d != "" {
		return filepath.Join(d, strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
	}
	return ""
}

// readCorpus reads the entries of dir that exploration ref saved, sorted
// by name.
func readCorpus(dir, ref string) (paths []string, entries []corpusEntry, err error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, nil, err
		}
		var e struct {
			corpusEntry
			Test string `json:"test"` // before the format: the test alone
		}
		if err := json.Unmarshal(b, &e); err != nil {
			return nil, nil, fmt.Errorf("corpus entry %s: %w", f, err)
		}
		if e.Format != corpusFormat {
			from := e.Exploration
			if from == "" {
				from = e.Test
			}
			return nil, nil, fmt.Errorf("corpus entry %s has format %q, not %q: it was written by another version of gotogether and cannot be replayed; delete it (seed %d of %s may find its run again)", f, e.Format, corpusFormat, e.Seed, from)
		}
		if e.Exploration != ref {
			continue // another exploration's, in a shared directory
		}
		paths, entries = append(paths, f), append(entries, e.corpusEntry)
	}
	return paths, entries, nil
}

// save writes r's choices to the corpus, named by their hash, and
// returns the file.
func (x *Exploration) save(r *Run) (string, error) {
	b, err := json.MarshalIndent(corpusEntry{Format: corpusFormat, Exploration: x.ref(), Seed: r.Seed, Choices: r.Choices}, "", " ")
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	if err := os.MkdirAll(x.corpus, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(x.corpus, hex.EncodeToString(h[:6])+".json")
	return p, os.WriteFile(p, append(b, '\n'), 0o644)
}

// envUint reads a count from the environment: 0 when unset.
func envUint(name string) (uint64, error) {
	v := os.Getenv(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s=%q: want a number", name, v)
	}
	return n, nil
}

func empty(runs []*Run) bool {
	for _, r := range runs {
		if len(r.Steps) > 0 {
			return false
		}
	}
	return true
}

// explore runs the scenario once per seed and checks every run (System.Explore):
// a run that fails to run (rather than to conform) fails t, naming its seed.
func explore(t testing.TB, bindingPath string, cfg ExploreConfig, scenario func(c qenv.Chooser) error) *Exploration {
	t.Helper()
	if cfg.Seeds == 0 {
		cfg.Seeds = 50
	}
	if cfg.Rates.Default == 0 && cfg.Rates.Kinds == nil {
		cfg.Rates = qenv.DefaultRates
	}
	if cfg.Parallel <= 0 {
		cfg.Parallel = runtime.GOMAXPROCS(0)
	}
	c := config(t, cfg.opts)
	c.refuse(t, "an exploration (Explore, Exhaust, Sweep, Mutate, Fuzz, FollowTraces)", exploreIgnores)
	woven := len(qcover.Blocks()) > 0
	defer qobs.Strict()() // a step with nowhere to go fails the exploration, rather than vanish from its run
	mark, _ := qobs.Escaped(0)
	// a nightly run widens the sample: GOTOGETHER_SEEDS=n runs n seeds,
	// GOTOGETHER_SEED_OFFSET=k starts them after k (fresh seeds each night);
	// a failure's report names its run, which GOTOGETHER_RUN runs alone
	n, offset := cfg.Seeds, uint64(0)
	if v, err := envUint("GOTOGETHER_SEEDS"); err != nil {
		t.Fatalf("gotogether: %v", err)
	} else if v > 0 && cfg.Chooser == nil {
		n = int(v)
	}
	if v, err := envUint("GOTOGETHER_SEED_OFFSET"); err != nil {
		t.Fatalf("gotogether: %v", err)
	} else if cfg.Chooser == nil {
		offset = v
	}
	seeds := make([]uint64, n)
	for i := range seeds {
		seeds[i] = offset + uint64(i+1)
	}
	x := &Exploration{test: t.Name(), id: nextExploration(t), corpus: corpusDir(t, cfg), emptyOK: cfg.emptyOK, tempDir: c.tempDir, fuzz: cfg.fuzz, mutant: cfg.corpusOf != "", replay: cfg.Replay, mutation: c.mutant}
	x.dir = exploreDir(t, c.Dir, x.id)
	spec, alone := runSpecFor(t, x.ref())
	if alone {
		x.alone = &spec
		switch spec.kind {
		case "seed":
			n, _ := strconv.ParseUint(spec.value, 10, 64)
			if cfg.Chooser != nil && (n < 1 || n > uint64(cfg.Seeds)) { // the runs are the Chooser's, 1..Seeds
				t.Fatalf("gotogether: GOTOGETHER_RUN names seed %d, but %s has seeds 1..%d", n, x.ref(), cfg.Seeds)
			}
			seeds = []uint64{n}
		case "corpus":
			seeds = nil
		default:
			t.Fatalf("gotogether: GOTOGETHER_RUN names a %s, but %s explores seeds: want :seed=<n> or :corpus=<file>", spec.kind, x.ref())
		}
	}
	x.Runs = make([]*Run, len(seeds))
	// the corpus's runs, after the seeds' (their numbers stay the same);
	// a seed run alone runs no corpus entry
	var corpusPaths []string
	var corpusRuns []corpusEntry
	if x.corpus != "" && (!alone || spec.kind == "corpus") {
		ref := x.ref()
		if cfg.corpusOf != "" { // a mutant's exploration replays its baseline's entries
			ref = cfg.corpusOf
		}
		var err error
		if corpusPaths, corpusRuns, err = readCorpus(x.corpus, ref); err != nil {
			t.Fatalf("gotogether: %v", err)
		}
		if !alone {
			noteCorpus(t.Name(), x.corpus, ref)
		}
	}
	if alone && spec.kind == "corpus" {
		var ps []string
		var rs []corpusEntry
		for i, p := range corpusPaths {
			if filepath.Base(p) == spec.value {
				ps, rs = append(ps, p), append(rs, corpusRuns[i])
			}
		}
		if len(ps) == 0 {
			t.Fatalf("gotogether: GOTOGETHER_RUN names corpus entry %s, which is not one of %s's in %s (an entry belongs to the exploration its \"exploration\" names)", spec.value, x.ref(), x.corpus)
		}
		corpusPaths, corpusRuns = ps, rs
	}
	x.Runs = append(x.Runs, make([]*Run, len(corpusRuns))...)
	ids := append([]uint64(nil), seeds...)
	for range corpusRuns {
		ids = append(ids, 0)
		seeds = append(seeds, 0)
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Parallel)
	for i, seed := range seeds {
		wg.Add(1)
		sem <- struct{}{}
		go func() { //qenv:ok runs seeds side by side, each its own simulation
			defer func() { <-sem; wg.Done() }()
			var ch qenv.Chooser
			if k := i - (len(seeds) - len(corpusRuns)); k >= 0 {
				rp := qenv.Replay(corpusRuns[k].Choices)
				r := runOne(c, woven, rp, scenario)
				r.Corpus, r.savedFrom = corpusPaths[k], corpusRuns[k].Seed
				if n := len(corpusRuns[k].Choices); r.diverged == nil && r.Err == nil && rp.Left() > 0 {
					// the run took another, shorter path to a clean end: it no
					// longer reaches what it saved. One that failed or panicked
					// first ended by its failure, which is the entry's run not
					// running, not staleness.
					r.diverged = fmt.Errorf("the run ended after %d of the entry's %d choices", n-rp.Left(), n)
					r.Err = r.diverged
				}
				x.Runs[i] = r
				return
			}
			if cfg.Chooser != nil {
				ch = cfg.Chooser(seed)
			} else {
				s := qenv.NewSeeded(seed)
				s.Rates = cfg.Rates
				ch = s
			}
			x.Runs[i] = runOne(c, woven, ch, scenario)
			x.Runs[i].Seed = seed
		}()
	}
	wg.Wait()
	unknownKinds(t, "ExploreConfig.Rates key", keysOf(cfg.Rates.Kinds))
	x.finish(t, bindingPath, c, mark, ids)
	return x
}

// unknownKinds fails t for each name that covers no fault kind, option or
// nemesis declared by the time the runs are over (the doubles declare
// theirs as they are made): it would match nothing, silently. "nemesis"
// is every simulation's default key, so a system with no nemesis is not
// told so.
func unknownKinds(t testing.TB, what string, names []string) {
	t.Helper()
	for _, k := range names {
		if k != "nemesis" && !qenv.KnownFault(k) {
			t.Errorf("gotogether: %s %q covers no fault kind, option or nemesis the system declared, so it matches nothing: "+
				"name a kind as fault coverage prints it (a double's carry its name: \"queue.<name>.ack\"), or a prefix of one (\"queue\")", what, k)
		}
	}
}

// A RunPanic is a run that panicked (Run.Err): a task's panic, which
// Sim.Run re-raised (a self-check under GOTOGETHER_CHECK, a bug in the
// system), or the scenario's own.
type RunPanic struct {
	Value  any
	Stack  string // the panicking task's if InTask, else the scenario's
	InTask bool   // a simulation's task panicked (and Run re-raised it)
}

// Error is the panic's value.
func (p *RunPanic) Error() string { return fmt.Sprintf("panic: %v", p.Value) }

// runOne runs the scenario once on ch, recording its steps into a recorder
// of its own (carried by the chooser's context) and its choices.
func runOne(c settings, woven bool, ch qenv.Chooser, scenario func(c qenv.Chooser) error) *Run {
	mem := &qobs.MemorySink{}
	rec := &qenv.ChoiceLog{C: ch}
	var sink qobs.Sink = mem
	ctx := context.Background()
	var code *qcover.Counts
	if woven { // the code is woven with qweave -cover: count it by step
		code = qcover.NewCounts()
		ctx, sink = qcover.WithCounts(ctx, code), code.Sink(mem)
	}
	r := qobs.NewRecorder(sink, c.Recorder...)
	var task qenv.TaskPanic
	ctx = qenv.WithPanicStack(ctx, &task)
	err := func() (err error) {
		defer func() {
			// a task's panic, re-raised by Sim.Run: the store's, the
			// queue's and the scheduler's self-checks report so. It is the
			// run's failure, not the test binary's end.
			if p := recover(); p != nil {
				rp := &RunPanic{Value: p, Stack: string(debug.Stack())}
				if task.Is(p) { // the task's, re-raised: its own stack
					rp.Stack, rp.InTask = task.Stack, true
				}
				err = rp
			}
		}()
		return scenario(qenv.WithContext(rec, qobs.WithRecorder(ctx, r)))
	}()
	cerr := chooserErr(ch)
	if err == nil {
		err = cerr
	}
	if is := r.Issues(); err == nil && len(is) > 0 {
		err = fmt.Errorf("gotogether: recording: %s", strings.Join(is, "; "))
	}
	run := &Run{Steps: mem.Steps(), Choices: rec.Choices, Nemeses: rec.Nemeses, Interrupted: r.Interrupted(), Code: code, Err: err, diverged: cerr}
	if sc, ok := qenv.As[*qenv.Script](ch); ok {
		run.Unscripted = sc.Remaining()
	}
	return run
}

// keep is a failing run's last lines: where it was saved in the corpus
// (if save and there is one), and how to run it alone.
func (x *Exploration) keep(r *Run, save bool) string {
	var b strings.Builder
	if save && x.corpus != "" && r.Corpus == "" && len(r.Choices) > 0 {
		if p, err := x.save(r); err == nil {
			fmt.Fprintf(&b, "  saved to the corpus: %s (replayed before the seeds from now on; commit it as a regression)\n", p)
		} else {
			fmt.Fprintf(&b, "  not saved to the corpus: %v\n", err)
		}
	}
	if x.test != "" && x.alone == nil {
		fmt.Fprintf(&b, "  reproduce: %s\n", x.reproduce(r))
	}
	return b.String()
}

// name is how messages name a run: its seed, or an exhaustive search's
// schedule.
func (x *Exploration) name(id uint64, corpus string) string {
	switch {
	case x.alone != nil:
		return x.alone.String()
	case corpus != "":
		return "corpus entry " + filepath.Base(corpus)
	case x.search != nil:
		return fmt.Sprintf("schedule %d", id)
	}
	return fmt.Sprintf("seed %d", id)
}

// nameOf is how messages name run r.
func (x *Exploration) nameOf(r *Run) string { return x.name(r.Seed, r.Corpus) }

// finish checks the runs, made with ids (seeds, schedules), against the
// model: a run that failed to run (rather than to conform) fails t,
// naming it.
func (x *Exploration) finish(t testing.TB, bindingPath string, c settings, mark int, ids []uint64) {
	t.Helper()
	if n, msgs := qobs.Escaped(mark); n > mark {
		t.Errorf("gotogether: %d steps were recorded from outside the runs while they ran (a test in parallel may have recorded some); the first:\n%s", n-mark, msgs[0])
	}
	for i, r := range x.Runs {
		if r == nil { // the scenario failed t from its goroutine before the run ended
			t.Errorf("gotogether: %s did not finish: the scenario failed the test (above)", x.name(ids[i], ""))
			continue
		}
		if r.Err != nil {
			var p *RunPanic
			if errors.As(r.Err, &p) { // a self-check's finding, or a bug: keep the run
				where := "the scenario's stack"
				if p.InTask {
					where = "the task's stack"
				}
				replays := "the run replays exactly as recorded"
				if r.diverged != nil {
					replays = fmt.Sprintf("but its replay diverged first (%v): the entry may be stale, and the panic the changed code's", r.diverged)
				}
				t.Errorf("gotogether: %s panicked: %v\n"+
					"  a qenv self-check (GOTOGETHER_CHECK: the store's history, a queue's contract, the scheduler's invariants) or a bug in the system: "+
					"fix the code it names below; %s\n%s  %s:\n%s", x.nameOf(r), p.Value, replays, x.keep(r, true), where, p.Stack)
				continue
			}
			if r.Corpus != "" && r.diverged != nil {
				if x.mutant {
					continue // the mutant changed the run: its claims judge it
				}
				// the entry, not the run, is at fault
				t.Errorf("gotogether: %s is stale: %v\n"+
					"  the code or its fault model changed since it was saved, so its choices no longer replay the run they saved. "+
					"If the bug it caught is fixed, delete it; the seed it came from may find a similar run: GOTOGETHER_RUN='%s:seed=%d' go test -run '^%s$'",
					x.nameOf(r), r.diverged, x.ref(), r.savedFrom, strings.ReplaceAll(x.test, "/", "$/^"))
				continue
			}
			t.Errorf("gotogether: %s did not run: %v", x.nameOf(r), r.Err)
		}
	}
	if t.Failed() {
		// a scenario failed t (t.Fatal from a run's goroutine ends only
		// that run)
		t.FailNow()
	}
	if empty(x.Runs) && x.emptyOK {
		return
	}
	if empty(x.Runs) {
		// an empty run conforms to any model: every run empty is a system
		// that records nothing yet (or an unwoven build), not a pass
		if c.SkipIfEmpty {
			t.Skipf("gotogether: no run recorded a step (SkipIfEmpty)")
		}
		t.Fatalf("gotogether: no run of %d recorded a step: the system's Run deploys nothing that records yet. "+
			"Record each model step where it takes effect (qobs.Record with the run's context, //quint:action, a double), "+
			"or, for a build that may not be woven, give the System qtest.SkipIfEmpty()", len(x.Runs))
	}
	o := c.Options
	o.Dir, o.NoITF = filepath.Join(x.dir, "quint"), true
	ch := checkerFor(t, bindingPath, o, c)
	x.constants = map[string]string{}
	for k, v := range ch.Binding.Constants {
		x.constants[k] = v
	}
	for k, v := range c.Constants {
		x.constants[k] = v
	}
	x.model = ch.Binding.SpecPath() + ":" + ch.Binding.Module
	if ch.Model != nil {
		x.actions = map[string]bool{}
		for a := range ch.Model.Actions {
			x.actions[a] = true
		}
	}
	// a verdict is of steps against one instance: the binding, its
	// constants (a sweep's rows set their own) and the invariants checked
	var inst strings.Builder
	fmt.Fprintf(&inst, "%s|%v|%v\n", bindingPath, sortedKV(x.constants), c.Invariants)
	cc := *ch
	cc.Options = o
	x.checker = &cc
	// runs already judged (same steps) take their verdict from the cache,
	// and runs of the batch with the same steps are checked once
	runs := make([][]qtrace.Step, len(x.Runs))
	for i, r := range x.Runs {
		runs[i] = r.Steps
	}
	keys := make([]string, len(runs))
	var todo [][]qtrace.Step
	first := map[string]int{} // key -> index in todo
	at := map[int]int{}       // run -> index in todo
	for i, steps := range runs {
		keys[i] = inst.String() + qtrace.Key(steps)
		if rep, ok := verdicts.get(keys[i]); ok {
			x.Runs[i].Report = rep
			x.Cached++
			continue
		}
		if j, ok := first[keys[i]]; ok {
			at[i] = j
			x.Cached++
			continue
		}
		first[keys[i]] = len(todo)
		at[i] = len(todo)
		todo = append(todo, steps)
	}
	if len(todo) > 0 {
		reps, errs := cc.CheckBatch(context.Background(), todo, 0)
		for i := range x.Runs { // in seed order: an error names the first seed it hit
			j, ok := at[i]
			if !ok {
				continue
			}
			if errs[j] != nil {
				t.Fatalf("gotogether: %s: %v", x.nameOf(x.Runs[i]), errs[j])
			}
			x.Runs[i].Report = reps[j]
			verdicts.put(keys[i], reps[j])
		}
	}
	// the report, for tools that read it (gotogether evidence, cover,
	// actionmap): under GOTOGETHER_EVIDENCE, else in a kept exploration's
	// own directory; each test's are cleared at its first exploration
	// (nextExploration), so a rerun with fewer explorations leaves none
	// stale
	if dir := evidenceDir(x.test); dir != "" {
		err := os.MkdirAll(dir, 0o755)
		if err == nil {
			err = x.Evidence().WriteJSON(filepath.Join(dir, fmt.Sprintf("x%d.json", x.id)))
		}
		if err != nil {
			t.Logf("gotogether: evidence: %v", err)
		}
	} else if os.Getenv("GOTOGETHER_KEEP") != "" {
		if err := x.Evidence().WriteJSON(filepath.Join(x.dir, "evidence.json")); err != nil {
			t.Logf("gotogether: evidence.json: %v", err)
		}
	}
}

// verdicts keeps the model's verdicts by instance and steps, for the test
// binary: a run whose steps were already judged against the same instance
// is not checked again. Fuzzing needs it (most inputs repeat a run's
// model-level steps, and quint takes a second), as do mutants (most of
// their runs repeat the baseline's) and explorations whose seeds repeat
// runs.
var verdicts verdictCache

type verdictCache struct {
	mu sync.Mutex
	m  map[string]*validate.Report
}

func (v *verdictCache) get(k string) (*validate.Report, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	r, ok := v.m[k]
	return r, ok
}

func (v *verdictCache) put(k string, r *validate.Report) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.m == nil {
		v.m = map[string]*validate.Report{}
	}
	v.m[k] = r
}

// stepsKey is a run's steps as the model sees them: actions, outcomes,
// arguments, observations and order keys, with recorder, process and
// thread ids renumbered by first appearance (they are random per run) and
// times left out.
func sortedKV[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, fmt.Sprintf("%s=%v", k, v))
	}
	sort.Strings(out)
	return out
}

// Expect states what the exploration must show, claim by claim: Some,
// All or None of the runs meeting expectations, or an Expectation alone
// (some run; every run for Conforms). With no claims, every run conforms.
// A claim that holds by a failing run is logged with that run; one that
// does not fails t with the run that shows why (its report and faults,
// its step log saved), or, when no run fails at all, the fault kinds the
// runs never faulted.
func (x *Exploration) Expect(t testing.TB, claims ...Claim) {
	t.Helper()
	if len(claims) == 0 {
		claims = []Claim{Conforms()}
	}
	invariants := map[string]bool{}
	for _, r := range x.Runs {
		for k := range reportInvariants(r.Report) {
			invariants[k] = true
		}
	}
	if len(invariants) == 0 {
		invariants = nil
	}
	for _, c := range claims {
		var es []Expectation
		switch c := c.(type) {
		case Expectation:
			es = []Expectation{c}
		case quantified:
			es = c.es
		}
		for _, e := range es {
			if e.run != nil {
				t.Fatalf("gotogether: %s is a claim on one run (a shrunk Violation, a Run), not on an exploration: use it with that run's Expect", e)
			}
		}
		if u := unknownNames(es, x.actions, invariants); len(u) > 0 {
			t.Fatalf("gotogether: %s names %s: a misspelled name never matches, so the claim would hold or fail for nothing", describeClaim(c), strings.Join(u, "; "))
		}
	}
	if x.alone != nil {
		// one run: judged only by the claims that hold of every run
		var judged []Claim
		var not []string
		for _, c := range claims {
			if universal(c) {
				judged = append(judged, c)
			} else {
				not = append(not, describeClaim(c))
			}
		}
		msg := ""
		if len(not) > 0 {
			msg = "; not judged on one run (about the runs it leaves out): " + strings.Join(not, ", ")
		}
		if len(x.Runs) == 1 {
			t.Logf("gotogether: %s alone (GOTOGETHER_RUN)%s:\n%s", x.alone, msg, x.describe(t, x.Runs[0], false))
		}
		claims = judged
	}
	reps := x.reports()
	failedAny := false
	for _, c := range claims {
		v := x.verdict(c, reps)
		if v.log != nil {
			t.Logf("gotogether: %s", v.log(t))
		}
		if !v.ok {
			failedAny = true
			t.Errorf("gotogether: %s", v.say(t))
		}
	}
	if failedAny {
		t.FailNow()
	}
	t.Logf("gotogether: %s", x.summary())
}

// summary is a passing exploration's evidence in one line: what its runs
// showed and what they tried (Evidence has the rest; the vacuity search,
// which runs quint, only if a claim asked for it).
func (x *Exploration) summary() string {
	e := evidenceOf(x.Runs)
	line := fmt.Sprintf("%s: %d runs, %d conform", x.ref(), e.Runs, e.Conform)
	for _, r := range keysOf(e.FailingBy) {
		line += fmt.Sprintf(", %d %s", e.FailingBy[r], r)
	}
	if x.actions != nil {
		m := modelCoverage(x.Runs, x.actions)
		taken := 0
		for _, n := range m.Taken {
			if n > 0 {
				taken++
			}
		}
		line += fmt.Sprintf("; actions taken %d of %d", taken, taken+len(m.Never))
	}
	cov := x.faultCoverage()
	faulted, reached := 0, 0
	for _, p := range cov.Points {
		if p.Reached > 0 {
			reached++
			if p.Faulted() > 0 {
				faulted++
			}
		}
	}
	line += fmt.Sprintf("; fault kinds faulted %d of %d reached", faulted, reached)
	if x.vacuity != nil && len(x.vacuity.found) > 0 {
		line += fmt.Sprintf("; %d invariants held vacuously", len(x.vacuity.found))
	}
	if x.search != nil {
		line += fmt.Sprintf("; search of %d delays, %d faults", x.search.bound.Delays, x.search.bound.Faults)
		if x.search.left > 0 {
			line += fmt.Sprintf(", %d runs left (truncated)", x.search.left)
		}
	}
	return line
}

// reports are the runs' reports, in order.
func (x *Exploration) reports() []*validate.Report {
	reps := make([]*validate.Report, len(x.Runs))
	for i, r := range x.Runs {
		reps[i] = r.Report
	}
	return reps
}

// A claimVerdict is a claim's judgment of an exploration: whether it holds,
// what Expect says when it does not (say), and what it notes when it
// holds by a run that violates the model (log). Both are given the test
// to describe the run they show with.
type claimVerdict struct {
	ok       bool
	say, log func(t testing.TB) string
}

// verdict judges claim c of the exploration: the one judgment both Expect
// and Mutate's meets use, so a mutant is killed by exactly what fails a
// test.
func (x *Exploration) verdict(c Claim, reps []*validate.Report) claimVerdict {
	if _, ok := c.(vacuityClaim); ok {
		return x.vacuityVerdict()
	}
	if rc, ok := c.(runsClaim); ok {
		msg := rc.judgeRuns(x.Runs, x.actions)
		return claimVerdict{ok: msg == "", say: func(testing.TB) string { return msg }}
	}
	bad := len(x.Failing())
	j := c.judge(reps)
	q, isQ := c.(quantified)
	every := !isQ && c.(Expectation).every || isQ && q.kind == "all"
	switch {
	case j.ok && j.show >= 0 && !every:
		r := x.Runs[j.show]
		if r.Report.OK() {
			return claimVerdict{ok: true}
		}
		return claimVerdict{ok: true, log: func(t testing.TB) string {
			return fmt.Sprintf("%d of %d runs violate the model; %s shows %s:\n%s", bad, len(x.Runs), x.nameOf(r), describeClaim(c), x.describe(t, r, false))
		}}
	case j.ok:
		return claimVerdict{ok: true}
	case j.show >= 0:
		return claimVerdict{say: func(t testing.TB) string {
			if every {
				return fmt.Sprintf("want %s in every run; %d of %d runs do not have it%s; the first:\n%s", describeClaim(c), j.n, len(x.Runs), x.atStart(), x.describe(t, x.Runs[j.show], true))
			}
			return fmt.Sprintf("want %s in no run; %d of %d runs have it%s; the first:\n%s", describeClaim(c), j.n, len(x.Runs), x.atStart(), x.describe(t, x.Runs[j.show], true))
		}}
	case bad == 0:
		return claimVerdict{say: func(testing.TB) string {
			return fmt.Sprintf("all %d runs conform; want %s%s%s", len(x.Runs), c, x.unfaulted(), x.unscripted())
		}}
	default:
		return claimVerdict{say: func(t testing.TB) string {
			return fmt.Sprintf("%d of %d runs violate the model, none as %s%s; the first:\n%s", bad, len(x.Runs), c, x.atStart(), x.describe(t, x.Failing()[0], true))
		}}
	}
}

// describeClaim names what a failing All or None was about.
func describeClaim(c Claim) string {
	if q, ok := c.(quantified); ok {
		var d []string
		for _, e := range q.es {
			d = append(d, e.desc)
		}
		return strings.Join(d, " and ")
	}
	return c.String()
}

// unfaulted names the fault kinds no run faulted: a reason a bug did not
// show.
func (x *Exploration) unfaulted() string {
	var kinds []string
	for _, p := range x.faultCoverage().Unfaulted() {
		kinds = append(kinds, p.Kind)
	}
	if len(kinds) == 0 {
		return ""
	}
	return fmt.Sprintf(" (never faulted: %s; raise their rates or the seeds)", strings.Join(kinds, ", "))
}

// atStart says when every run fails at its first step: the model's
// instance (its constants) more likely disagrees with the scenario's than
// every run has the same bug.
func (x *Exploration) atStart() string {
	if len(x.Runs) < 2 {
		return ""
	}
	for _, r := range x.Runs {
		if r.Report.OK() || !failsAtStart(r.Report) {
			return ""
		}
	}
	var cs []string
	for _, k := range keysOf(x.constants) {
		cs = append(cs, k+"="+x.constants[k])
	}
	return fmt.Sprintf(" (all %d runs fail at step 1: the binding's constants (%s) may not match the scenario's; pass qtest.Constants)", len(x.Runs), strings.Join(cs, ", "))
}

func failsAtStart(rep *validate.Report) bool {
	for _, a := range rep.Actors {
		if a.Failure != nil && a.Failure.Index == 0 {
			return true
		}
	}
	return false
}

// unscripted names the scripted faults runs never came to: a reason a
// scripted bug did not show.
func (x *Exploration) unscripted() string {
	n := 0
	var first []qenv.Scripted
	for _, r := range x.Runs {
		if len(r.Unscripted) > 0 {
			if n == 0 {
				first = r.Unscripted
			}
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" (in %d of %d runs scripted faults never happened, the first's: %v; a fault of a phase that has passed blocks every later one)", n, len(x.Runs), first)
}

func describeAll(es []Expectation) string {
	var s []string
	for _, e := range es {
		s = append(s, e.desc)
	}
	return strings.Join(s, " and ")
}

// describe prints a run for a message; save, for a claim that failed,
// also saves it to the corpus.
func (x *Exploration) describe(t testing.TB, r *Run, save bool) string {
	if save || x.alone != nil {
		x.recheck(t, r)
	}
	var b strings.Builder
	if x.search != nil {
		fmt.Fprintf(&b, "%s, made otherwise %v\n%s", x.nameOf(r), r.deviations(), r.Report.Summary())
	} else {
		fmt.Fprintf(&b, "%s, faults %v\n%s", x.nameOf(r), r.Faults(), r.Report.Summary())
	}
	if len(r.Steps) == 0 {
		fmt.Fprintf(&b, "  the run recorded no steps: its tasks never reached a step (a deadlock, a wait no one ends: print Sim.Tasks() before Close), or it records into another recorder\n")
	}
	if len(r.Unscripted) > 0 {
		fmt.Fprintf(&b, "  scripted faults that never happened: %v\n", r.Unscripted)
	}
	if len(r.Interrupted) > 0 {
		fmt.Fprintf(&b, "  interrupted, not checked (cut off by a crash or the end of the run):\n")
		for _, s := range r.Interrupted {
			fmt.Fprintf(&b, "    %s %v (process %s)\n", s.Action, s.Args, s.Process)
		}
	}
	dir := x.dir // kept with the exploration's other artifacts (keepFiles)
	if dir == "" {
		dir = t.TempDir()
	}
	p := filepath.Join(dir, strings.NewReplacer(" ", "", "/", "_").Replace(x.nameOf(r))+".steps.jsonl")
	if f, err := os.Create(p); err == nil {
		if qtrace.WriteJSONL(f, r.Steps) == nil {
			fmt.Fprintf(&b, "  step log: %s\n", p)
		}
		f.Close()
	}
	b.WriteString(x.keep(r, save))
	if !save && x.alone == nil {
		return b.String() // a passing claim's example: nothing to keep
	}
	return x.keepFiles(b.String())
}

// recheck checks r again into the exploration's own directory when its
// report came from the verdict cache with a module written by another
// exploration (another test's directory may be gone), so a failure's
// report points at a module that is there. The verdict is the same.
func (x *Exploration) recheck(t testing.TB, r *Run) {
	if x.checker == nil || x.dir == "" || r.Report == nil {
		return
	}
	foreign := false
	for _, a := range r.Report.Actors {
		if a.Module != "" && !strings.HasPrefix(a.Module, x.dir+string(filepath.Separator)) {
			foreign = true
		}
	}
	if !foreign {
		return
	}
	ch := *x.checker
	ch.Options.Dir = filepath.Join(x.dir, "recheck", strings.NewReplacer(" ", "", "/", "_").Replace(x.nameOf(r)))
	rep, err := ch.Check(context.Background(), r.Steps)
	x.mu.Lock()
	defer x.mu.Unlock()
	// the same verdict, or the old report stands (a backend may word a
	// failure otherwise; it may not judge otherwise)
	switch {
	case err != nil:
	case rep.OK() == r.Report.OK() && reason(rep) == reason(r.Report):
		r.Report = rep
	default: // a cache key or a backend at fault: say so, keep the verdict the claim was judged by
		t.Logf("gotogether: %s checked again judges otherwise (%s, not %s): the verdict cache or a backend is wrong; the report below is the one judged", x.nameOf(r), reason(rep), reason(r.Report))
	}
}

// chooserErr is why a run's chooser could not make the choices it was
// given: a replay that diverged, bytes of another encoding.
func chooserErr(c qenv.Chooser) error {
	if r, ok := qenv.As[*qenv.Replayer](c); ok && r.Err() != nil {
		return r.Err()
	}
	if f, ok := qenv.As[*forcing](c); ok && f.Err() != nil {
		return f.Err()
	}
	if b, ok := qenv.As[*qenv.Bytes](c); ok && b.Err() != nil {
		return b.Err()
	}
	return nil
}

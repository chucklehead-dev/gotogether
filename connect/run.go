package connect

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/mbtmodel"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/itf"
)

// Config says how to generate traces and how to read them. The zero value
// of every field means quint's default (or the environment's).
type Config struct {
	Spec string // the .qnt file (required), relative to the test's directory
	Main string // --main: the module to run, when the file has several

	// quint run only:
	Init     string // --init (default "init")
	StepName string // --step (default "step")
	MaxSteps int    // --max-steps; $QUINT_MAX_STEPS

	// MaxSamples is --max-samples, and also --n-traces for quint run: how many
	// traces to generate and replay (default 100; $QUINT_MAX_SAMPLES).
	MaxSamples int
	// Seed is quint's --seed (e.g. "0x2a" or "42"). Default $QUINT_SEED, else
	// random; a failure prints the seed that reproduces it.
	Seed string

	Quint   string   // the quint binary (default $QUINT_BIN, else "quint")
	Backend string   // --backend (default $QUINT_BACKEND, else quint's own default)
	Args    []string // extra arguments for quint, e.g. --invariant; sampling counts use MaxSamples
	Dir     string   // where traces are written (default a temp dir; t.TempDir() in Run and Test); one run's: its earlier traces are removed

	// How to read the traces (see ReplayOptions).
	StatePath  []string
	NondetPath []string

	// Require names boolean vals of the model the run's traces must reach:
	// each is evaluated in every state of every trace by the Go evaluator,
	// its count logged ("required x: reached in 3 of 100 traces"), and Run
	// or Test fails when one held in no state of any trace. A rare state
	// the implementation is wrong at needs its traces to reach it: a run
	// that never did proved nothing of it. Run steers to each: the
	// shortest path to it the evaluator finds (within MaxSteps) is
	// replayed first, as subtest witness_<name>, so a state one sample in
	// thousands reaches is tested every run. A val of an instance module
	// (module design { import m(N = 3).* }) is named bare, as in m.
	Require []string

	// Verbose: 0 quiet, 1 logs every step (action and picks), 2 also the
	// model state. Values outside 0–2 are invalid. Default $QUINT_VERBOSE.
	// Failures always show the step.
	Verbose int

	// compiled is the spec's compiled model the run uses instead of quint
	// (compiled.go), set by resolve per GOTOGETHER_COMPILED
	compiled *compiled
	// ignored is the spec's compiled model a run with quint passes over
	// and why (compiled.go), logged once; nil where there is nothing to say
	ignored *ignoredModel
}

// FromEnv fills unset fields from the environment, quint-connect's
// variables (read here at run time, not at compile time) and a few more:
//
//	QUINT_SEED         Seed
//	QUINT_VERBOSE      Verbose
//	QUINT_MAX_SAMPLES  MaxSamples
//	QUINT_MAX_STEPS    MaxSteps
//	QUINT_BACKEND      Backend
//	QUINT_BIN          Quint
//
// Explicit nonzero numeric fields take precedence. Selected environment values
// must be integers; budgets are nonnegative (0 uses the default), and Verbose
// is 0, 1 or 2. Invalid explicit values also return an error.
func (c Config) FromEnv() (Config, error) {
	if c.Seed == "" {
		c.Seed = os.Getenv("QUINT_SEED")
	}
	for _, number := range []struct {
		value *int
		field string
		env   string
		max   int // -1: no upper bound
		want  string
	}{
		{&c.MaxSamples, "MaxSamples", "QUINT_MAX_SAMPLES", -1, "want a nonnegative integer (0 uses the default)"},
		{&c.MaxSteps, "MaxSteps", "QUINT_MAX_STEPS", -1, "want a nonnegative integer (0 uses the default)"},
		{&c.Verbose, "Verbose", "QUINT_VERBOSE", 2, "want 0 (quiet), 1 (steps), or 2 (state)"},
	} {
		source := fmt.Sprintf("Config.%s=%d", number.field, *number.value)
		if raw := os.Getenv(number.env); *number.value == 0 && raw != "" {
			source = fmt.Sprintf("%s=%q", number.env, raw)
			n, err := strconv.Atoi(raw)
			if err != nil {
				return c, fmt.Errorf("connect: %s: %s", source, number.want)
			}
			*number.value = n
		}
		if *number.value < 0 || number.max >= 0 && *number.value > number.max {
			return c, fmt.Errorf("connect: %s: %s", source, number.want)
		}
	}
	for _, arg := range c.Args {
		name, _, _ := strings.Cut(arg, "=")
		switch name {
		case "--max-samples", "--n-traces", "--maxSamples", "--nTraces":
			return c, fmt.Errorf("connect: Config.Args %q overrides trace coverage: use Config.MaxSamples instead", arg)
		}
	}
	if c.Backend == "" {
		c.Backend = os.Getenv("QUINT_BACKEND")
	}
	if c.Quint == "" {
		c.Quint = os.Getenv("QUINT_BIN")
	}
	return c, nil
}

func (c Config) resolve(ctx context.Context) (Config, error) {
	var err error
	c, err = c.FromEnv()
	if err != nil {
		return c, err
	}
	if c.Spec == "" {
		return c, errors.New("connect: Config.Spec is required")
	}
	if c.Quint == "" {
		c.Quint = "quint"
		if runtime.GOOS == "windows" {
			c.Quint = "quint.cmd"
		}
	}
	spec, err := filepath.Abs(c.Spec)
	if err != nil {
		return c, err
	}
	if c.compiled == nil {
		if c.compiled, c.ignored, err = compiledMode(c, spec); err != nil {
			return c, err
		}
	}
	if c.compiled == nil {
		if err := quintcmd.Require(ctx, c.Quint, filepath.Dir(spec)); err != nil {
			if plain := mbtmodel.PlainBeside(CompiledFor(c.Spec, c.Main)); plain != "" {
				return c, fmt.Errorf("%w (%s is a plain compiled model an earlier gotogether kept, which connect no longer finds: compile it again, gotogether compile -spec %s%s, and remove it)", err, plain, c.Spec, mainFlag(c.Main))
			}
			return c, err
		}
	}
	if c.MaxSamples == 0 {
		c.MaxSamples = 100
	}
	if c.Seed == "" {
		var b [4]byte
		_, _ = rand.Read(b[:])
		c.Seed = fmt.Sprintf("0x%x", binary.BigEndian.Uint32(b[:]))
	}
	if c.Dir == "" {
		d, err := os.MkdirTemp("", "quint-connect-")
		if err != nil {
			return c, err
		}
		c.Dir = d
	}
	if abs, err := filepath.Abs(c.Dir); err == nil {
		c.Dir = abs
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return c, err
	}
	return c, nil
}

// QuintError is a failed quint invocation.
type QuintError struct {
	Args   []string
	Output string
	Err    error
}

func (e *QuintError) Error() string {
	return fmt.Sprintf("quint %s: %v\n%s", strings.Join(e.Args, " "), e.Err, e.Output)
}

func (e *QuintError) Unwrap() error { return e.Err }

// GenerateRun runs `quint run --mbt` and returns the trace files, in order,
// and the resolved config (with the seed used). An incomplete batch returns
// its partial files and an error instead of claiming MaxSamples were generated.
// With the spec's compiled model (GOTOGETHER_COMPILED, CompiledFor), the
// Go evaluator's simulation writes them instead, as quint would, with no
// quint process.
func GenerateRun(ctx context.Context, c Config) ([]string, Config, error) {
	c, err := c.resolve(ctx)
	if err != nil {
		return nil, c, err
	}
	if c.compiled != nil {
		files, err := generateGo(ctx, c)
		return files, c, err
	}
	spec, _ := filepath.Abs(c.Spec)
	n := strconv.Itoa(c.MaxSamples)
	args := []string{"run", spec, "--mbt", "--seed", c.Seed, "--max-samples", n, "--n-traces", n,
		"--out-itf", filepath.Join(c.Dir, "run_{seq}.itf.json"), "--verbosity", "0"}
	if c.Main != "" {
		args = append(args, "--main", c.Main)
	}
	if c.Init != "" {
		args = append(args, "--init", c.Init)
	}
	if c.StepName != "" {
		args = append(args, "--step", c.StepName)
	}
	if c.MaxSteps > 0 {
		args = append(args, "--max-steps", strconv.Itoa(c.MaxSteps))
	}
	files, err := quint(ctx, c, append(args, c.Args...), "run_")
	return files, c, err
}

// generateTest runs the `run` named test of the spec with `quint test` and
// returns its trace files. Such traces carry no mbt:: variables: replay
// them with Config.NondetPath, or validate them instead (package validate).
func generateTest(ctx context.Context, c Config, test string) ([]string, Config, error) {
	c, err := c.resolve(ctx)
	if err != nil {
		return nil, c, err
	}
	if c.compiled != nil {
		return nil, c, fmt.Errorf("connect: %s's run %s is a quint test's, which the Go evaluator does not run (compiled model %s, %s): run it with quint (install quint, or GOTOGETHER_COMPILED=0), or replay the model's random traces with connect.Run",
			c.Spec, test, c.compiled.path, compiledEnv)
	}
	spec, _ := filepath.Abs(c.Spec)
	args := []string{"test", spec, "--seed", c.Seed, "--match", "^" + regexp.QuoteMeta(test) + "$",
		"--max-samples", strconv.Itoa(c.MaxSamples), "--out-itf", filepath.Join(c.Dir, "test_{seq}.itf.json"), "--verbosity", "0"}
	if c.Main != "" {
		args = append(args, "--main", c.Main)
	}
	files, err := quint(ctx, c, append(args, c.Args...), "test_")
	return files, c, err
}

func quint(ctx context.Context, c Config, args []string, prefix string) ([]string, error) {
	// a Dir kept from an earlier run holds its traces under the same
	// names, more of them or of an older spec: they would be replayed too
	stale, _ := filepath.Glob(filepath.Join(c.Dir, prefix+"*.itf.json"))
	for _, f := range stale {
		if err := os.Remove(f); err != nil {
			return nil, fmt.Errorf("removing an earlier run's trace: %w", err)
		}
	}
	if c.Backend != "" {
		args = append(args, "--backend", c.Backend)
	}
	cmd := quintcmd.Command(ctx, c.Quint, filepath.Dir(must(filepath.Abs(c.Spec))), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			err = fmt.Errorf("%w (install quint: npm i -g @informalsystems/quint)", err)
		}
		return nil, &QuintError{Args: args, Output: string(out), Err: err}
	}
	files, _ := filepath.Glob(filepath.Join(c.Dir, prefix+"*.itf.json"))
	sort.Slice(files, func(i, j int) bool { return seqOf(files[i]) < seqOf(files[j]) })
	if prefix == "run_" && len(files) != c.MaxSamples {
		return files, &QuintError{Args: args, Output: string(out), Err: fmt.Errorf("incomplete trace batch: generated %d trace files for %d requested samples; partial files remain in %s", len(files), c.MaxSamples, c.Dir)}
	}
	if len(files) == 0 {
		return nil, &QuintError{Args: args, Output: string(out), Err: errors.New("no traces were generated: check the spec and the configuration")}
	}
	return files, nil
}

var reSeq = regexp.MustCompile(`_(\d+)\.itf\.json$`)

func seqOf(path string) int {
	if m := reSeq.FindStringSubmatch(path); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func must[T any](t T, _ error) T { return t }

// Run generates traces with `quint run --mbt` and replays each on a fresh
// driver from newDriver. Each trace runs in a synchronous trace_<index>
// subtest; newDriver receives that trace's context, cleanup and temporary
// directory lifetime through testing.TB. Its cleanups finish before the next
// trace starts. Ordinary Go test filtering and skips apply to the subtests.
// Run fails t at the first divergence, with the seed that reproduces it.
// A driver Bind made (b.Driver) is checked against the model when the
// first trace's is made, after generation; Binding.Run checks it before
// generating any trace. It is quint-connect's #[quint_run].
func Run(t *testing.T, c Config, newDriver func(testing.TB) Driver) {
	t.Helper()
	runTraces(t, c, newDriver, nil)
}

// runTraces is Run, with bound, if set, checked before generation.
func runTraces(t *testing.T, c Config, newDriver func(testing.TB) Driver, bound modelChecked) {
	t.Helper()
	if c.Dir == "" {
		c.Dir = t.TempDir()
	}
	pre := checkFirst(t, &c, bound)
	files, c, err := GenerateRun(t.Context(), c)
	if err != nil {
		t.Fatalf("connect: generating traces: %v", err)
	}
	replayPrepared(t, c, files, newDriver, "run", pre)
}

// Test replays the traces of the spec's `run` named test (quint test --match)
// on a fresh driver each, with the per-trace subtest lifetimes described in
// Run. It is quint-connect's #[quint_test]. Unless the spec records the
// action itself (Config.NondetPath), use package validate to check such tests
// against Go instead. A driver Bind made is checked as Run says;
// Binding.Test checks it before generating the traces.
func Test(t *testing.T, c Config, test string, newDriver func(testing.TB) Driver) {
	t.Helper()
	testTraces(t, c, test, newDriver, nil)
}

// testTraces is Test, with bound, if set, checked before generation.
func testTraces(t *testing.T, c Config, test string, newDriver func(testing.TB) Driver, bound modelChecked) {
	t.Helper()
	if c.Dir == "" {
		c.Dir = t.TempDir()
	}
	pre := checkFirst(t, &c, bound)
	files, c, err := generateTest(t.Context(), c, test)
	if err != nil {
		t.Fatalf("connect: generating traces of %s: %v", test, err)
	}
	replayPrepared(t, c, files, newDriver, "test "+test, pre)
}

// prepared is what a run compiles before its first trace: Config.Require's
// vals, and the model Step.Eval and a bound driver's check use.
type prepared struct {
	req *required
	ev  *evaluator
}

// prepare compiles c's model for Config.Require, failing t when a name
// is no val of it. Without Require, Step.Eval's model is compiled at the
// first Eval (or a bound driver's check), so a run that never asks pays
// nothing.
func prepare(t *testing.T, c Config) *prepared {
	t.Helper()
	req, err := compileRequired(t.Context(), c)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	ev := &evaluator{ctx: t.Context(), c: c}
	if req != nil {
		ev.m, ev.keep = req.m, c.Require
	}
	return &prepared{req: req, ev: ev}
}

// checkFirst checks bound, Binding.Run's or Binding.Test's binding,
// against the model before a trace is generated (checkBound), and
// returns the compile for the replay; nil when bound is. c is resolved
// here (seed, directory, quint), so the compile is the one the run's
// traces are judged with, and generation's resolve of it changes
// nothing.
func checkFirst(t *testing.T, c *Config, bound modelChecked) *prepared {
	t.Helper()
	if bound == nil {
		return nil
	}
	rc, err := c.resolve(t.Context())
	if err != nil {
		return nil // generating the traces reports it
	}
	*c = rc
	p := prepare(t, rc)
	p.ev.bound = true
	checkBound(t, rc, p.ev, bound)
	return p
}

// replayPrepared replays files, each on a fresh driver from newDriver,
// on what checkFirst compiled before the traces were generated, or,
// given nil, on what it compiles here from c.
func replayPrepared(t *testing.T, c Config, files []string, newDriver func(testing.TB) Driver, what string, pre *prepared) {
	t.Helper()
	if c.compiled != nil {
		t.Logf("connect: %s %s: generated %d traces, seed %s, by the Go evaluator from the compiled model %s (no quint here, or %s=1)", what, c.Spec, len(files), c.Seed, c.compiled.path, compiledEnv)
		for _, n := range c.compiled.notes {
			t.Logf("connect: %s", n)
		}
	} else {
		t.Logf("connect: %s %s: generated %d traces, seed %s", what, c.Spec, len(files), c.Seed)
		c.ignored.log(t)
	}
	if pre == nil {
		pre = prepare(t, c)
	}
	// pre's evaluator was made with the config c is (checkFirst resolved
	// it, and generation's resolve of a resolved config returns it as it
	// is): nothing of it to update
	req, ev := pre.req, pre.ev
	judged := 0 // traces whose states req has seen: all, unless -run skips some
	if req != nil {
		defer func() {
			if t.Failed() {
				return
			}
			lines, missed := req.report(judged)
			for _, l := range lines {
				t.Logf("connect: %s", l)
			}
			if len(missed) == 0 {
				return
			}
			if judged < len(files) {
				t.Logf("connect: %d of %d traces ran: not judging Config.Require", judged, len(files))
				return
			}
			t.Errorf("connect: %s never reached the required %s in %d traces (seed %s), and no witness trace reached it: the run proved nothing of the states they name. Raise MaxSamples or MaxSteps, or steer the model's step toward them; check they are reachable with gotogether simulate", what, strings.Join(missed, ", "), len(files), c.Seed)
		}()
		// the shortest path to each required state, replayed first: a
		// state the samples reach rarely is tested every run. A quint test
		// replays its named run alone, and NondetPath reads the action
		// from the state, which a witness does not record.
		if what == "run" && len(c.NondetPath) == 0 {
			ws, notes := req.witnesses(c)
			for _, n := range notes {
				t.Logf("connect: %s", n)
			}
			for j, w := range ws {
				if !t.Run("witness_"+w.name, func(t *testing.T) {
					req.witnessed(w.name, len(w.trace.States)-1)
					replayOne(t, c, ev, w.trace, len(files)+j, newDriver, fmt.Sprintf("\nthe witness trace of required %s: the shortest path to it the Go evaluator found (%d steps)", w.name, len(w.trace.States)-1))
				}) {
					t.FailNow()
				}
			}
		}
	}
	traces := &traceReader{c: c}
	for i, f := range files {
		if !t.Run(fmt.Sprintf("trace_%d", i), func(t *testing.T) {
			t.Attr("gotogether.trace_index", strconv.Itoa(i))
			if c.Seed != "" {
				t.Attr("gotogether.seed", c.Seed)
			}
			tr, err := traces.read(t.Context(), f)
			if err != nil {
				t.Fatalf("connect: %s: %v", f, err)
			}
			if req != nil {
				if err := req.trace(tr); err != nil {
					t.Fatalf("connect: %s: %v", f, err)
				}
				judged++
			}
			repro := ""
			if c.Seed != "" && what == "run" {
				repro = fmt.Sprintf("\nreproduce with QUINT_SEED=%s (trace file %s)", c.Seed, f)
			}
			replayOne(t, c, ev, tr, i, newDriver, repro)
		}) {
			t.FailNow()
		}
	}
}

// replayOne replays trace number i on a fresh driver, failing t with the
// step and repro at the first divergence.
func replayOne(t *testing.T, c Config, ev *evaluator, tr *itf.Trace, i int, newDriver func(testing.TB) Driver, repro string) {
	t.Helper()
	opt := ReplayOptions{Trace: i, StatePath: c.StatePath, NondetPath: c.NondetPath, eval: ev}
	if c.Verbose > 0 {
		opt.OnStep = func(s *Step) {
			if c.Verbose > 1 {
				t.Logf("[trace %d step %d]\n%s", s.Trace, s.Index, s)
			} else {
				t.Logf("[trace %d step %d] %s", s.Trace, s.Index, s.summary())
			}
		}
	}
	d := newDriver(t)
	if mc, ok := d.(modelChecked); ok && !ev.bound {
		ev.bound = true
		checkBound(t, c, ev, mc)
	}
	if err := Replay(tr, d, opt); err != nil {
		msg := err.Error()
		if se, ok := errors.AsType[*StepError](err); ok && se.Kind != "trace" {
			msg += "\n\n" + indent(se.Step.String(), "    ")
		}
		t.Fatalf("connect: %s\n%s", msg, repro)
	}
}

// checkBound fails t, before the first trace replays, when the driver's
// binding does not fit the run's model (bindcheck.go), with every
// mismatch at once. It reuses the model Step.Eval and Config.Require
// compile, compiling once for Returns' defs when none is at hand. A model
// the check cannot have is logged, not refused: the traces still judge
// the binding.
func checkBound(t *testing.T, c Config, ev *evaluator, mc modelChecked) {
	t.Helper()
	m, err := ev.modelWith(mc.defs())
	if err != nil {
		t.Logf("connect.Bind: the binding is not checked against %s before the traces (%v): the traces still check it", c.Spec, err)
		return
	}
	bad, unbound, notes := mc.checkModel(m, c)
	for _, n := range notes {
		t.Log(n)
	}
	if len(unbound) > 0 {
		one := len(unbound) == 1
		t.Logf("connect.Bind: the model's action%s %s %s bound to nothing: a step of %s fails as unimplemented (bind, Handle or Skip %s)", plural(len(unbound)), strings.Join(unbound, ", "),
			choose(one, "is", "are"), choose(one, "it", "one"), choose(one, "it", "them"))
	}
	if len(bad) > 0 {
		t.Fatalf("connect: the binding does not fit %s (%d mismatch%s, found before any trace ran):\n%s", c.Spec, len(bad), choose(len(bad) == 1, "", "es"), indent(strings.Join(bad, "\n"), "    "))
	}
}

func indent(s, pfx string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = pfx + lines[i]
	}
	return strings.Join(lines, "\n")
}

package validate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/maphash"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/internal/memo"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// An evaluator is a binding's model compiled for the Go evaluator: its
// evaluation module (binding.EvalModule) and the IR quint compiled it to.
// A search checks its candidates with it, and quint only confirms (the
// order a search picks is checked by quint, as every order is).
type evaluator struct {
	em  *binding.EvalModule
	m   *qeval.Model
	err error // why the binding has none
}

// Process-wide: each evaluation module compiled once, by its text and
// its model's source.
var evaluators memo.Map[string, *evaluator]

// evalMode is how searches check candidates: "go" (the evaluator, quint
// for what it refuses; the default), "quint" (quint alone), "shadow" (the
// evaluator, every batch it judges checked by quint too, a disagreement
// the search's error: the nightly's), from GOTOGETHER_EVAL.
// evalMode is o.Eval, else GOTOGETHER_EVAL's, else "go".
func (o Options) evalMode() string {
	m := o.Eval
	if m == "" {
		m = os.Getenv("GOTOGETHER_EVAL")
	}
	if m == "quint" || m == "shadow" {
		return m
	}
	return "go"
}

// shadowCheck fails the search when quint's verdicts of orders are not
// the evaluator's (fs), index for index: under GOTOGETHER_EVAL=shadow.
func shadowCheck(a string, orders [][]qtrace.Step, fs []int, quint func([][]qtrace.Step) ([]int, []string)) {
	qs, _ := quint(orders)
	for i := range orders {
		if fs[i] != qs[i] {
			fail("GOTOGETHER_EVAL=shadow: actor %s: the Go evaluator and quint disagree on a candidate of %d steps (the evaluator: %s, quint: %s): "+
				"a bug of the evaluator (internal/qeval) or of the binding's evaluation module (binding.EvalModule); report it with this run's steps "+
				"(GOTOGETHER_KEEP keeps them), and check with GOTOGETHER_EVAL=quint meanwhile\n  %s",
				a, len(orders[i]), verdictOf(fs[i]), verdictOf(qs[i]), stepsString(orders[i]))
		}
	}
}

// stepsString is an order's steps, one line.
func stepsString(o []qtrace.Step) string {
	parts := make([]string, len(o))
	for i, s := range o {
		parts[i] = s.String()
	}
	return strings.Join(parts, "; ")
}

// evaluatorFor is c's binding's evaluator, compiled the first time.
func evaluatorFor(c *Checker) *evaluator {
	return evaluatorOf(c, func(path string) (*binding.EvalModule, error) { return c.Binding.EvalModule(c.Model, path) })
}

// evaluatorOf is the evaluator of the module make writes for a path
// (binding.EvalModule, or PossibleModule), compiled the first time.
func evaluatorOf(c *Checker, make func(path string) (*binding.EvalModule, error)) *evaluator {
	dir := filepath.Join(os.TempDir(), "gotogether-eval")
	em, err := make(filepath.Join(dir, "x", "eval.qnt"))
	if err != nil {
		return &evaluator{err: err}
	}
	src, err := os.ReadFile(c.Binding.SpecPath())
	if err != nil {
		return &evaluator{err: err}
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s", c.Options.Quint, em.Text, src)
	key := hex.EncodeToString(h.Sum(nil))[:16]
	return evaluators.Once(key, func() *evaluator { return compileEval(c, make, filepath.Join(dir, key)) })
}

// compileEval writes the module make writes in dir and compiles it with
// quint: its stdout to a file, since --out writes the modules
// unflattened, and a pipe loses what quint has not flushed when it exits.
func compileEval(c *Checker, make func(path string) (*binding.EvalModule, error), dir string) *evaluator {
	// the module imports the model relative to where it is written: write
	// it at the same depth
	em, err := make(filepath.Join(dir, "eval.qnt"))
	if err != nil {
		return &evaluator{err: err}
	}
	// quint's IR of the module, kept across processes by its content and
	// the model's (cache.go)
	key, cached := cacheKey(c.Options.Quint, "compile", c.Binding.SpecPath(), "--target", "json", "--main", em.Name, "--init", em.Init, "--step", em.Init, em.Text)
	if cached {
		if data, ok := cacheGet("compile", key); ok {
			if m, err := qeval.Load(data); err == nil {
				return &evaluator{em: em, m: m}
			}
		}
		unlock, data, ok := cacheLock("compile", key)
		defer unlock()
		if ok {
			if m, err := qeval.Load(data); err == nil {
				return &evaluator{em: em, m: m} // another process's, compiled meanwhile
			}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &evaluator{err: err}
	}
	// the module and its IR under names of this process's own: the
	// directory is shared by every process compiling the same module (a
	// test binary, its overlay mutants' subprocesses, the CLI), and one
	// truncating a file another reads gave that one no evaluator
	qf, err := os.CreateTemp(dir, "eval-*.qnt")
	if err != nil {
		return &evaluator{err: err}
	}
	qnt := qf.Name()
	defer os.Remove(qnt)
	_, err = qf.WriteString(em.Text)
	if cerr := qf.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return &evaluator{err: err}
	}
	f, err := os.CreateTemp(dir, "ir-*.json")
	if err != nil {
		return &evaluator{err: err}
	}
	out := f.Name()
	defer os.Remove(out)
	quint := c.Options.Quint
	if quint == "" {
		quint = "quint"
	}
	cmd := exec.Command(quint, "compile", "--target", "json", "--main", em.Name, "--init", em.Init, "--step", em.Init, qnt)
	var stderr strings.Builder
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, f, &stderr
	err = cmd.Run()
	f.Close()
	if err != nil {
		return &evaluator{err: fmt.Errorf("quint compile of the evaluation module: %w: %s", err, strings.TrimSpace(stderr.String()))}
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return &evaluator{err: err}
	}
	m, err := qeval.Load(data)
	if err != nil {
		return &evaluator{err: err}
	}
	if cached {
		cachePut("compile", key, data)
	}
	return &evaluator{em: em, m: m}
}

// Process-wide: how many searches the Go evaluator judged were searched
// again by quint (a test's, to see a proof spare one).
var searchedAgain atomic.Int64

// literal is a rendered Quint literal (an argument, a constant) as a
// value. ParseQuint reads a capitalised name as a constructor: one that is
// no constructor of the model's (a val BIG = 5) is quint's to evaluate.
func (e *evaluator) literal(s string) (qeval.Value, error) {
	v, err := itf.ParseQuint(s)
	if err != nil {
		return nil, &qeval.Unsupported{Op: "literal", Why: fmt.Sprintf("%s is no literal the evaluator reads: %v", s, err)}
	}
	if tag, ok := e.unknownTag(v); ok {
		return nil, &qeval.Unsupported{Op: "literal", Why: fmt.Sprintf("%s in %s is no constructor of the model's", tag, s)}
	}
	return qeval.FromITF(v)
}

// unknownTag is a variant of v whose tag is no constructor of the model.
func (e *evaluator) unknownTag(v itf.Value) (string, bool) {
	if v.Kind == itf.Variant && !e.m.Constructor(v.Tag) {
		return v.Tag, true
	}
	if v.Inner != nil {
		if t, ok := e.unknownTag(*v.Inner); ok {
			return t, true
		}
	}
	for _, x := range v.Elems {
		if t, ok := e.unknownTag(x); ok {
			return t, true
		}
	}
	for _, p := range v.Pairs {
		for _, x := range p {
			if t, ok := e.unknownTag(x); ok {
				return t, true
			}
		}
	}
	for _, x := range v.Fields {
		if t, ok := e.unknownTag(x); ok {
			return t, true
		}
	}
	return "", false
}

// judge checks one candidate order of history's steps, as a quint replay
// of it would: the index of the step it fails at (as failedAt and, with
// invariants, violatedAt read a report), or -1 and its last state. An
// error is the evaluator's refusal (the caller asks quint).
func (e *evaluator) judge(c *Checker, history *qtrace.Trace, order []qtrace.Step, n int, invariants bool) (int, string, error) {
	traces, _ := reconstruct(c.Binding, order)
	if len(traces) != 1 {
		return 0, "", &qeval.Unsupported{Op: "order", Why: fmt.Sprintf("%d actors in one candidate", len(traces))}
	}
	tr := traces[0]
	if hooks.TamperEval.Load() == 3 && len(order)%2 == 1 {
		return 0, "", nil
	}
	if hooks.TamperEval.Load() == 2 {
		return -1, fmt.Sprint(len(order)), nil
	}
	plan, err := c.plan(tr, history)
	if err != nil {
		return 0, "", err
	}
	consts, err := e.constants(plan)
	if err != nil {
		return 0, "", err
	}
	// the deepest prefix of the plan's calls a candidate before this one
	// was accepted with: its state, judged again from there (each was
	// replayed from init, a search's cost quadratic in the history)
	// (a search's candidates share their start, its history's first step:
	// the memo's keys need not say it)
	keys := c.memo.keys(plan.Calls)
	from, s := c.memo.deepest(keys)
	w, err := e.walk(c, consts, plan, from, s, invariants, tr.Settled, nil)
	if err != nil {
		return 0, "", err
	}
	// a position's index in the order: the start's (quint names no step
	// for init's state), else its call's
	startAt := n - 1
	if plan.Start != nil {
		startAt = stepAt([]binding.Call{*plan.Start}, 0, n)
	}
	at := func(pos int) int {
		if pos == 0 {
			return startAt
		}
		return stepAt(plan.Calls, pos-1, n)
	}
	switch {
	case w.startFailed:
		return startAt, "", nil
	case w.rejected >= 0:
		return stepAt(plan.Calls, w.rejected, n), "", nil
	}
	if w.violated < 0 {
		c.memo.put(keys[len(plan.Calls)], w.final) // before the settle's vals: an extension adds steps
	}
	if w.eventual != "" {
		return n - 1, "", nil // the order's, as failedAt says: another may end as the probe saw
	}
	if w.violated >= 0 {
		return at(w.violated), "", nil
	}
	if hooks.TamperEval.Load() == 1 {
		return 0, "", nil
	}
	return -1, e.m.Key(w.final), nil
}

// constants are plan's instance, as the evaluation module's constants.
func (e *evaluator) constants(plan *binding.Plan) (map[string]qeval.Value, error) {
	consts := map[string]qeval.Value{}
	for k, name := range e.em.Consts {
		v, err := e.literal(plan.Constants[k])
		if err != nil {
			return nil, err
		}
		consts[name] = v
	}
	return consts, nil
}

// A walk is what the evaluator's replay of a plan found, as quint's
// fused replay (the conformance run with every invariant, and each
// eventual val's run) checks it. Positions count states: 0 is the
// start's (init's, or the snapshot's), i+1 the state after call i.
type walk struct {
	startFailed bool   // the start is no state: its action disabled, or its expect false
	rejected    int    // the call the model cannot take, or whose expect is false; -1 none
	violated    int    // the position where an invariant first fails; -1 none
	eventual    string // an eventual val false in the last state of a settled trace
	final       qeval.State
}

// walk replays plan's calls from call from in state s (from 0 with s nil:
// from the plan's start), checking each call's expect, with invariants
// every invariant in each state from position from on (a prefix's were
// held already), and, settled, every eventual val in the last state.
// keep, if set, is given each state from the start's on. An error is the
// evaluator's refusal.
func (e *evaluator) walk(c *Checker, consts map[string]qeval.Value, plan *binding.Plan, from int, s qeval.State, invariants, settled bool, keep func(qeval.State)) (walk, error) {
	w := walk{rejected: -1, violated: -1}
	if s == nil {
		from = 0
		states, failed, err := e.start(c, consts, plan)
		if err != nil {
			return w, err
		}
		if failed {
			w.startFailed = true
			return w, nil
		}
		s = states[0]
	}
	check := func(pos int) error {
		if !invariants || w.violated >= 0 {
			return nil
		}
		for _, inv := range c.Binding.Invariants {
			v, err := e.m.Eval(consts, s, e.em.Invariants[inv])
			if err != nil {
				return err
			}
			if v != qeval.Bool(true) {
				w.violated = pos
				return nil
			}
		}
		return nil
	}
	if from == 0 {
		if keep != nil {
			keep(s)
		}
		if err := check(0); err != nil {
			return w, err
		}
	}
	for i, call := range plan.Calls {
		if i < from {
			continue // the prefix's: accepted, every invariant held
		}
		var args []qeval.Value
		for _, p := range c.Model.Actions[call.Action] {
			v, err := e.literal(call.Args[p.Name])
			if err != nil {
				return w, err
			}
			args = append(args, v)
		}
		next, ok, err := e.m.Step(consts, s, e.em.Actions[call.Action], args...)
		if err != nil {
			return w, err
		}
		if !ok {
			w.rejected = i
			return w, nil
		}
		s = next
		if keep != nil {
			keep(s)
		}
		if ok, err := e.holds(consts, s, &call); err != nil {
			return w, err
		} else if !ok {
			w.rejected = i
			return w, nil
		}
		if err := check(i + 1); err != nil {
			return w, err
		}
	}
	w.final = s
	if settled {
		for _, ev := range c.Binding.Eventually {
			v, err := e.m.Eval(consts, s, e.em.Eventually[ev])
			if err != nil {
				return w, err
			}
			if v != qeval.Bool(true) {
				w.eventual = ev
				break
			}
		}
	}
	return w, nil
}

// start is the states plan's replay starts in: init's, or (plan.Start)
// those its snapshot step's action reaches from no state, with its
// expect holding; failed if there are none, the snapshot no state of the
// model. A snapshot with several successors is Unsupported, as a step's
// is: quint's replay takes one.
func (e *evaluator) start(c *Checker, consts map[string]qeval.Value, plan *binding.Plan) (states []qeval.State, failed bool, err error) {
	if plan.Start == nil {
		s, err := e.m.Init(consts, e.em.Init)
		return []qeval.State{s}, false, err
	}
	call := plan.Start
	var args []qeval.Value
	for _, p := range c.Model.Actions[call.Action] {
		v, err := e.literal(call.Args[p.Name])
		if err != nil {
			return nil, false, err
		}
		args = append(args, v)
	}
	succ, err := e.m.Successors(consts, nil, e.em.Actions[call.Action], args...)
	if err != nil {
		return nil, false, err
	}
	if len(succ) > 1 {
		return nil, false, &qeval.Unsupported{Op: call.Action, Why: fmt.Sprintf("the snapshot has %d successors (it chooses): quint takes one", len(succ))}
	}
	for _, s := range succ {
		ok, err := e.holds(consts, s, call)
		if err != nil {
			return nil, false, err
		}
		if ok {
			states = append(states, s)
		}
	}
	return states, len(states) == 0, nil
}

// holds reports whether call's expect holds in s (no expect: it does).
func (e *evaluator) holds(consts map[string]qeval.Value, s qeval.State, call *binding.Call) (bool, error) {
	if call.Expect == "" {
		return true, nil
	}
	if call.ExpectArgs == nil {
		return false, &qeval.Unsupported{Op: "expect", Why: call.Action + "'s expect is not text and fields"}
	}
	var xs []qeval.Value
	for _, a := range call.ExpectArgs {
		v, err := e.literal(a)
		if err != nil {
			return false, err
		}
		xs = append(xs, v)
	}
	v, err := e.m.Eval(consts, s, e.em.Expects[call.Action], xs...)
	if err != nil {
		return false, err
	}
	return v == qeval.Bool(true), nil
}

// stepAt is the order's index of call i: its step's (re-sequenced from
// 1), or, for a call the binding inferred (a restart), the last step's, as
// failedAt reads a report that names no step for it.
func stepAt(calls []binding.Call, i, n int) int {
	if s := calls[i].Step; s != nil && s.Seq >= 1 {
		return int(s.Seq) - 1
	}
	return n - 1
}

// A stateMemo holds a search's states after the candidates the Go
// evaluator accepted, keyed by a hash of their planned calls (128 bits:
// two seeds), for its extensions. The instance (the constants) is the
// search's, fixed. A nil memo holds nothing.
type stateMemo struct {
	mu     sync.Mutex
	seeds  [2]maphash.Seed
	states map[[2]uint64]qeval.State
}

// memoCap bounds a memo: past it, no more states are kept.
const memoCap = 200000

func newStateMemo() *stateMemo {
	return &stateMemo{seeds: [2]maphash.Seed{maphash.MakeSeed(), maphash.MakeSeed()}, states: map[[2]uint64]qeval.State{}}
}

// keys are the rolling keys of calls' prefixes: keys[i] the first i's.
func (m *stateMemo) keys(calls []binding.Call) [][2]uint64 {
	if m == nil {
		return make([][2]uint64, len(calls)+1)
	}
	out := make([][2]uint64, len(calls)+1)
	var h maphash.Hash
	for i, c := range calls {
		for j := range 2 {
			h.SetSeed(m.seeds[j])
			fmt.Fprintf(&h, "%d|%s|%s|%q", out[i][j], c.Quint, c.Expect, c.ExpectArgs)
			out[i+1][j] = h.Sum64()
		}
	}
	return out
}

// deepest is the longest prefix with a state, and its state (0, nil: none).
func (m *stateMemo) deepest(keys [][2]uint64) (int, qeval.State) {
	if m == nil {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(keys) - 1; i > 0; i-- {
		if s, ok := m.states[keys[i]]; ok {
			return i, s
		}
	}
	return 0, nil
}

func (m *stateMemo) put(k [2]uint64, s qeval.State) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.states) < memoCap {
		m.states[k] = s
	}
}

// plan is t's plan within the history whole: by the search's planner when
// it has one for whole.
func (c *Checker) plan(t, whole *qtrace.Trace) (*binding.Plan, error) {
	if c.planner != nil && whole == c.mode.history {
		return c.planner.Plan(t)
	}
	return c.Binding.Planner(c.Model, whole).Plan(t)
}

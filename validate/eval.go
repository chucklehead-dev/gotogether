package validate

import (
	"context"
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

// shadowRuns is, under GOTOGETHER_EVAL=shadow, runs' checks shadowed:
// each actor's steps, on one thread in the order checked, judged by the
// evaluator and, all of them in one batch, by quint; a disagreement is
// its run's error. It covers every binding a run is checked against,
// where the searches' shadow covers only theirs. A search's candidates
// (c.mode.history set) are its shadow's, and an actor the evaluator refuses is
// quint's alone.
func (c *Checker) shadowRuns(runs [][]qtrace.Step) []error {
	errs := make([]error, len(runs))
	if c.Options.evalMode() != "shadow" || c.mode.history != nil || c.mode.shadowed || len(c.Binding.Hidden) > 0 {
		return errs // (a binding with hidden actions is the evaluator's alone: quint has nothing to compare)
	}
	e := evaluatorFor(c)
	if e.err != nil {
		return errs
	}
	inv := len(c.Binding.Invariants) > 0
	type item struct {
		run   int
		actor string
		order []qtrace.Step
		f     int
	}
	var items []item
	var batch [][]qtrace.Step
	for r, steps := range runs {
		traces, _ := reconstruct(c.Binding, steps)
		for _, tr := range traces {
			if len(tr.Steps) == 0 {
				continue
			}
			order := make([]qtrace.Step, len(tr.Steps))
			for i, s := range tr.Steps {
				s.Recorder, s.Thread, s.Seq, s.Order, s.Call = "shadow:"+tr.Actor, "shadow", uint64(i+1), nil, 0
				order[i] = s
			}
			// planned in itself, as a check plans an actor's steps alone
			settled := settledAs(tr, [][]qtrace.Step{order})[0]
			f, _, err := e.judge(inHistory(c, tr), tr, settled, len(order), inv)
			if err != nil {
				continue
			}
			items = append(items, item{run: r, actor: nameOf(tr.Actor), order: order, f: f})
			batch = append(batch, settled)
		}
	}
	if len(batch) == 0 {
		return errs
	}
	dir, err := os.MkdirTemp("", "gotogether-shadow-")
	if err != nil {
		for i := range errs {
			errs[i] = err
		}
		return errs
	}
	defer os.RemoveAll(dir)
	q := *c
	q.mode.shadowed, q.Options.Dir, q.Options.VerdictsOnly, q.Options.NoITF = true, dir, true, true
	reps, qerrs := q.CheckBatch(context.Background(), batch, 0)
	for k, it := range items {
		if errs[it.run] != nil {
			continue
		}
		if qerrs[k] != nil {
			errs[it.run] = qerrs[k]
			continue
		}
		f := failedAt(reps[k], len(it.order))
		if inv && f < 0 {
			f = violatedAt(reps[k], len(it.order))
		}
		if f != it.f {
			errs[it.run] = fmt.Errorf("GOTOGETHER_EVAL=shadow: actor %s: the Go evaluator and quint disagree on its steps as checked (the evaluator: %s, quint: %s): "+
				"a bug of the evaluator (internal/qeval) or of the binding's evaluation module (binding.EvalModule); report it with this run's steps "+
				"(GOTOGETHER_KEEP keeps them), and check with GOTOGETHER_EVAL=quint meanwhile\n  %s",
				it.actor, verdictOf(it.f), verdictOf(f), stepsString(it.order))
		}
	}
	return errs
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
	dir := filepath.Join(os.TempDir(), "gotogether-eval")
	em, err := c.Binding.EvalModule(c.Model, filepath.Join(dir, "x", "eval.qnt"))
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
	return evaluators.Once(key, func() *evaluator { return compileEval(c, em, filepath.Join(dir, key)) })
}

// compileEval writes em in dir and compiles it with quint: its stdout to a
// file, since --out writes the modules unflattened, and a pipe loses what
// quint has not flushed when it exits.
func compileEval(c *Checker, em *binding.EvalModule, dir string) *evaluator {
	// the module imports the model relative to where EvalModule was told it
	// is: write it at the same depth
	again, err := c.Binding.EvalModule(c.Model, filepath.Join(dir, "eval.qnt"))
	if err != nil {
		return &evaluator{err: err}
	}
	em = again
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &evaluator{err: err}
	}
	qnt := filepath.Join(dir, "eval.qnt")
	if err := os.WriteFile(qnt, []byte(em.Text), 0o644); err != nil {
		return &evaluator{err: err}
	}
	out := filepath.Join(dir, "ir.json")
	f, err := os.Create(out)
	if err != nil {
		return &evaluator{err: err}
	}
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
		return &evaluator{err: fmt.Errorf("quint compile of the evaluation module: %v: %s", err, strings.TrimSpace(stderr.String()))}
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return &evaluator{err: err}
	}
	m, err := qeval.Load(data)
	if err != nil {
		return &evaluator{err: err}
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
	consts := map[string]qeval.Value{}
	for k, name := range e.em.Consts {
		v, err := e.literal(plan.Constants[k])
		if err != nil {
			return 0, "", err
		}
		consts[name] = v
	}
	// the deepest prefix of the plan's calls a candidate before this one
	// was accepted with: its state, judged again from there (each was
	// replayed from init, a search's cost quadratic in the history)
	// (a search's candidates share their start, its history's first step:
	// the memo's keys need not say it)
	keys := c.memo.keys(plan.Calls)
	from, s := c.memo.deepest(keys)
	startAt := n - 1 // quint names no step for init's state
	if plan.Start != nil {
		startAt = stepAt([]binding.Call{*plan.Start}, 0, n)
	}
	if s == nil {
		states, failed, err := e.start(c, consts, plan)
		if err != nil {
			return 0, "", err
		}
		if failed {
			return startAt, "", nil
		}
		s = states[0]
	}
	violated := -1
	check := func(at int) error {
		if !invariants || violated >= 0 {
			return nil
		}
		for _, inv := range c.Binding.Invariants {
			v, err := e.m.Eval(consts, s, e.em.Invariants[inv])
			if err != nil {
				return err
			}
			if v != qeval.Bool(true) {
				violated = at
				return nil
			}
		}
		return nil
	}
	if from == 0 {
		if err := check(startAt); err != nil {
			return 0, "", err
		}
	}
	for i, call := range plan.Calls {
		if i < from {
			continue // the prefix's: accepted, every invariant held
		}
		at := stepAt(plan.Calls, i, n)
		var args []qeval.Value
		for _, p := range c.Model.Actions[call.Action] {
			v, err := e.literal(call.Args[p.Name])
			if err != nil {
				return 0, "", err
			}
			args = append(args, v)
		}
		next, ok, err := e.m.Step(consts, s, e.em.Actions[call.Action], args...)
		if err != nil {
			return 0, "", err
		}
		if !ok {
			return at, "", nil
		}
		s = next
		if call.Expect != "" {
			if call.ExpectArgs == nil {
				return 0, "", &qeval.Unsupported{Op: "expect", Why: call.Action + "'s expect is not text and fields"}
			}
			var xs []qeval.Value
			for _, a := range call.ExpectArgs {
				v, err := e.literal(a)
				if err != nil {
					return 0, "", err
				}
				xs = append(xs, v)
			}
			v, err := e.m.Eval(consts, s, e.em.Expects[call.Action], xs...)
			if err != nil {
				return 0, "", err
			}
			if v != qeval.Bool(true) {
				return at, "", nil
			}
		}
		if err := check(at); err != nil {
			return 0, "", err
		}
	}
	if violated < 0 {
		c.memo.put(keys[len(plan.Calls)], s) // before the settle's vals: an extension adds steps
	}
	if tr.Settled {
		for _, ev := range c.Binding.Eventually {
			v, err := e.m.Eval(consts, s, e.em.Eventually[ev])
			if err != nil {
				return 0, "", err
			}
			if v != qeval.Bool(true) {
				return n - 1, "", nil // the order's, as failedAt says: another may end as the probe saw
			}
		}
	}
	if violated >= 0 {
		return violated, "", nil
	}
	if hooks.TamperEval.Load() == 1 {
		return 0, "", nil
	}
	return -1, e.m.Key(s), nil
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

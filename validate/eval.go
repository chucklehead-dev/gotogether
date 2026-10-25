package validate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/hooks"
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
var evaluators sync.Map // key -> *evalOnce

type evalOnce struct {
	once sync.Once
	ev   *evaluator
}

// evalMode is how searches check candidates: "go" (the evaluator, quint
// for what it refuses; the default), "quint" (quint alone), from
// GOTOGETHER_EVAL.
func evalMode() string {
	if m := os.Getenv("GOTOGETHER_EVAL"); m == "quint" {
		return m
	}
	return "go"
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
	v, _ := evaluators.LoadOrStore(key, &evalOnce{})
	o := v.(*evalOnce)
	o.once.Do(func() { o.ev = compileEval(c, em, filepath.Join(dir, key)) })
	return o.ev
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

// judge checks one candidate order of within's steps, as a quint replay
// of it would: the index of the step it fails at (as failedAt and, with
// invariants, violatedAt read a report), or -1 and its last state. An
// error is the evaluator's refusal (the caller asks quint).
func (e *evaluator) judge(c *Checker, within *qtrace.Trace, order []qtrace.Step, n int, invariants bool) (int, string, error) {
	traces, _ := qtrace.Reconstruct(order)
	if len(traces) != 1 {
		return 0, "", &qeval.Unsupported{Op: "order", Why: fmt.Sprintf("%d actors in one candidate", len(traces))}
	}
	tr := traces[0]
	if hooks.TamperEval.Load() == 2 {
		return -1, fmt.Sprint(len(order)), nil
	}
	plan, err := c.Binding.PlanWithin(c.Model, tr, within)
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
	s, err := e.m.Init(consts, e.em.Init)
	if err != nil {
		return 0, "", err
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
	if err := check(n - 1); err != nil { // quint names no step for the initial state
		return 0, "", err
	}
	for i, call := range plan.Calls {
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

// stepAt is the order's index of call i: its step's (re-sequenced from
// 1), or, for a call the binding inferred (a restart), the last step's, as
// failedAt reads a report that names no step for it.
func stepAt(calls []binding.Call, i, n int) int {
	if s := calls[i].Step; s != nil && s.Seq >= 1 {
		return int(s.Seq) - 1
	}
	return n - 1
}

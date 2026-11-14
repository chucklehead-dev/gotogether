package connect

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/itf"
)

// bankRun is one generated run of bank.qnt, read, and the model compiled:
// the tests that judge traces in Go share it, since a quint run and a
// compile each cost seconds of connect's CPU budget.
type bankRun struct {
	c      Config
	traces []*itf.Trace
	m      *qeval.Model
}

var bankRunOnce struct {
	sync.Once
	r   *bankRun
	err error
}

// sharedBankRun is the bankRun, made by the first test that asks (with
// its context: the tests of the package run one at a time).
func sharedBankRun(t *testing.T) *bankRun {
	t.Helper()
	needQuint(t)
	bankRunOnce.Do(func() {
		bankRunOnce.r, bankRunOnce.err = func() (*bankRun, error) {
			dir, err := os.MkdirTemp("", "connect-bank-")
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(dir)
			c := Config{Spec: "testdata/bank.qnt", MaxSamples: 10, MaxSteps: 15, Seed: "0x1", Dir: dir}
			files, c, err := GenerateRun(t.Context(), c)
			if err != nil {
				return nil, err
			}
			traces, err := ReadTraces(t.Context(), c, files)
			if err != nil {
				return nil, err
			}
			m, err := compileModel(t.Context(), c, nil)
			if err != nil {
				return nil, err
			}
			c.Dir = "" // removed on return: an Eval's later compile writes to os.TempDir
			return &bankRun{c: c, traces: traces, m: m}, nil
		}()
	})
	if bankRunOnce.err != nil {
		t.Fatal(bankRunOnce.err)
	}
	return bankRunOnce.r
}

// evalChecker drives the bank and compares each balance with the
// model's balanceOf, not with a copy of it: no StateDriver, so Eval
// alone catches a divergence.
type evalChecker struct{ d *bankDriver }

func (e evalChecker) Step(s *Step) error { return e.d.Step(s) }

func (e evalChecker) CheckStep(s *Step) error {
	for _, a := range []string{"alice", "bob"} {
		if model, impl := MustEval[int](s, "balanceOf", a), e.d.b.balances[a]; model != impl {
			return fmt.Errorf("balanceOf(%s): model %d, implementation %d", a, model, impl)
		}
	}
	return nil
}

// evalStateDriver is evalChecker with the state compared too.
type evalStateDriver struct{ evalChecker }

func (e evalStateDriver) State() (any, error) { return e.d.State() }

func TestEvalCatchesWhatTheProjectionMisses(t *testing.T) {
	r := sharedBankRun(t)
	ev := &evaluator{ctx: t.Context(), c: r.c, m: r.m}
	caught := 0
	for i, tr := range r.traces {
		if err := Replay(tr, evalChecker{&bankDriver{}}, ReplayOptions{Trace: i, eval: ev}); err != nil {
			t.Fatalf("the faithful bank: %v", err)
		}
		err := Replay(tr, evalChecker{&bankDriver{buggy: true}}, ReplayOptions{Trace: i, eval: ev})
		if err == nil {
			continue
		}
		se, ok := errors.AsType[*StepError](err)
		if !ok || se.Kind != "check" || se.Step.Action != "transfer" || !strings.Contains(err.Error(), "balanceOf(") {
			t.Fatalf("the buggy bank: want a check failing at a transfer, got %v", err)
		}
		caught++
	}
	if caught == 0 {
		t.Fatalf("no trace of %d caught the transfer that credits twice", len(r.traces))
	}
}

func TestEvalRefusals(t *testing.T) {
	r := sharedBankRun(t)
	tr := r.traces[0]
	s := &Step{Index: 1, Action: tr.States[1].Action, After: tr.States[1].Vars, eval: &evaluator{ctx: t.Context(), c: r.c, m: r.m}}
	at := fmt.Sprintf("at step 1 (%s)", s.Action)
	if v, err := s.Eval("MAX"); err != nil || v.I.Int64() != 5 {
		t.Errorf("a val: %v, %v", v, err)
	}
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"balanceOff", []any{"alice"}, `connect: Eval("balanceOff") ` + at + `: balanceOff is no definition of testdata/bank.qnt: name a val or a def of the model`},
		{"balances", nil, `connect: Eval("balances") ` + at + `: balances is a variable of testdata/bank.qnt, not a definition: read it with s.Get("balances")`},
		{"deposit", []any{"alice", 1}, `connect: Eval("deposit") ` + at + `: deposit is an action of testdata/bank.qnt, not a value: name a val or a def of the model`},
		{"balanceOf", nil, `connect: Eval("balanceOf") ` + at + `: balanceOf takes 1 argument (a), not 0: pass one Go value per parameter`},
		{"nonNegative", []any{1}, `connect: Eval("nonNegative") ` + at + `: nonNegative takes no arguments, not 1: pass one Go value per parameter`},
		{"balanceOf", []any{func() {}}, `connect: Eval("balanceOf") ` + at + `: argument a (func()): `},
	} {
		_, err := s.Eval(c.name, c.args...)
		if err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("Eval(%s, %v): %v\nwant %q", c.name, c.args, err, c.want)
		}
	}
	if _, err := Eval[string](s, "balanceOf", "alice"); err == nil || !strings.Contains(err.Error(), "decoding") || !strings.HasSuffix(err.Error(), "decode into a type of the definition's value") {
		t.Errorf("an int decoded into a string: %v", err)
	}
	// a model's own runtime error: a key balances lacks
	if _, err := s.Eval("balanceOf", "carol"); err == nil || !strings.Contains(err.Error(), "balanceOf is undefined in this state (") {
		t.Errorf("a missing key: %v", err)
	}

	// Replay alone has no model: the error says where one comes from, and
	// a CheckStep's MustEval fails its step instead of the test binary
	err := Replay(tr, evalChecker{&bankDriver{}}, ReplayOptions{})
	want := `connect: Eval("balanceOf") at step 0 (init): this step has no model to evaluate in: Replay compiles none; replay through connect.Run or connect.Test, which compile the model once for Eval`
	if se, ok := errors.AsType[*StepError](err); !ok || se.Kind != "check" || !strings.Contains(err.Error(), want) {
		t.Errorf("Replay without a model: %v\nwant %q", err, want)
	}
}

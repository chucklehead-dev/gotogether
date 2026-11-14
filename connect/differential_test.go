package connect

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/itf"
)

// inputTraces are traces of bank_inputs.qnt, whose withdrawals may exceed
// the balance: the inputs bank.qnt's guard never lets through.
func inputTraces(t *testing.T) []*itf.Trace {
	t.Helper()
	needQuint(t)
	c := Config{Spec: "testdata/bank_inputs.qnt", MaxSamples: 5, MaxSteps: 10, Seed: "0x1", Dir: t.TempDir()}
	files, c, err := GenerateRun(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	traces, err := ReadTraces(t.Context(), c, files)
	if err != nil {
		t.Fatal(err)
	}
	return traces
}

func pair(a, b *bankDriver) *Differential {
	return &Differential{Impls: map[string]Driver{"faithful": a, "other": b}}
}

// Two banks that differ only where bank.qnt's guard never goes (one lets
// a withdrawal overdraw, one counts a refusal as an operation) each agree
// with the model on its traces, and so with each other. Traces of the
// looser model reach the difference: the overdraft as a step one fails
// and the other does not, the counted refusal as a difference in the
// observed state. Two faithful banks refusing the same withdrawals agree.
func TestDifferentialFindsWhatTheModelNeverReaches(t *testing.T) {
	for i, tr := range sharedBankRun(t).traces {
		for _, other := range []*bankDriver{{overdraft: true}, {countRefusals: true}} {
			if err := Replay(tr, pair(&bankDriver{}, other), ReplayOptions{Trace: i}); err != nil {
				t.Fatalf("on bank.qnt's traces the banks agree with the model and each other: %v", err)
			}
		}
	}
	inputs := inputTraces(t)
	caught := map[string]int{}
	for i, tr := range inputs {
		for name, other := range map[string]*bankDriver{"overdraft": {overdraft: true}, "countRefusals": {countRefusals: true}, "faithful": {}} {
			d := pair(&bankDriver{}, other)
			d.IgnoreModel = true
			err := Replay(tr, d, ReplayOptions{Trace: i})
			if err == nil {
				continue
			}
			se, ok := errors.AsType[*StepError](err)
			if !ok || se.Step.Action != "withdraw" {
				t.Fatalf("%s: want a disagreement at a withdrawal, got %v", name, err)
			}
			switch {
			case name == "overdraft" && se.Kind == "step" && strings.Contains(err.Error(), "the implementations disagree: faithful (insufficient funds) failed, other did not"):
			case name == "countRefusals" && se.Kind == "check" && strings.Contains(err.Error(), ".Ops: faithful ") && strings.Contains(err.Error(), "the implementations disagree (faithful and other)"):
			default:
				t.Fatalf("%s: %v", name, err)
			}
			caught[name]++
		}
	}
	if caught["overdraft"] == 0 || caught["countRefusals"] == 0 || caught["faithful"] != 0 {
		t.Fatalf("disagreements over %d traces: %v; want overdraft and countRefusals caught, faithful never", len(inputs), caught)
	}
}

func TestDifferentialNeedsTwo(t *testing.T) {
	d := &Differential{Impls: map[string]Driver{"one": &bankDriver{}}}
	if err := d.Step(&Step{Action: "init"}); err == nil || !strings.Contains(err.Error(), "at least two implementations, and has 1") {
		t.Fatalf("one implementation: %v", err)
	}
}

// Vary takes the design's own traces past its guard: each withdrawal asks
// for 10 more than the model picked, for every bank alike. The overdraft
// is caught on bank.qnt's traces, where without Vary it never is, two
// faithful banks still agree, and the traces' own picks are unchanged.
func TestDifferentialVaryTakesTheModelsInputsFurther(t *testing.T) {
	traces := sharedBankRun(t).traces
	further := func(s *Step) error {
		if s.Action == "withdraw" {
			s.Picks["amount"] = itf.Value{Kind: itf.Int, I: new(big.Int).Add(s.Picks["amount"].I, big.NewInt(10))}
		}
		return nil
	}
	before := fmt.Sprint(traces[0].States)
	caught := map[string]int{}
	for i, tr := range traces {
		for name, other := range map[string]*bankDriver{"overdraft": {overdraft: true}, "faithful": {}} {
			d := pair(&bankDriver{}, other)
			d.IgnoreModel, d.Vary = true, further
			if err := Replay(tr, d, ReplayOptions{Trace: i}); err != nil {
				if !strings.Contains(err.Error(), "faithful (insufficient funds) failed, other did not") {
					t.Fatalf("%s: %v", name, err)
				}
				caught[name]++
			}
		}
	}
	if caught["overdraft"] == 0 || caught["faithful"] != 0 {
		t.Fatalf("disagreements over %d traces: %v; want the overdraft caught, the faithful pair never", len(traces), caught)
	}
	if after := fmt.Sprint(traces[0].States); after != before {
		t.Fatal("Vary changed the trace's own picks")
	}
}

// modelChecker is a bank whose own CheckStep compares with the model, as
// golem's drivers' do: here it always fails, so it shows when it runs.
type modelChecker struct{ *bankDriver }

func (modelChecker) CheckStep(s *Step) error { return errors.New("compared with the model") }

// Under IgnoreModel a driver's own check is the model's, which a varied
// input leaves behind: it does not run. Without IgnoreModel it does.
func TestDifferentialIgnoreModelSkipsStepCheckers(t *testing.T) {
	tr := sharedBankRun(t).traces[0]
	d := &Differential{Impls: map[string]Driver{"a": modelChecker{&bankDriver{}}, "b": modelChecker{&bankDriver{}}}, IgnoreModel: true}
	if err := Replay(tr, d, ReplayOptions{}); err != nil {
		t.Fatalf("under IgnoreModel: %v", err)
	}
	d = &Differential{Impls: map[string]Driver{"a": modelChecker{&bankDriver{}}, "b": modelChecker{&bankDriver{}}}}
	if err := Replay(tr, d, ReplayOptions{}); err == nil || !strings.Contains(err.Error(), "a: compared with the model") {
		t.Fatalf("with the model: %v", err)
	}
}

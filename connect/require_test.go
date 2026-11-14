package connect

import (
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/itf"
)

// One quint run and one compile (shared with Eval's tests) judge the
// counts and both refusals: a subprocess for each case cost connect's
// budget twice over.
func TestRequireCountsAndRefusals(t *testing.T) {
	r := sharedBankRun(t)
	c, traces := r.c, r.traces
	req, err := newRequired(r.m, c.Spec, []string{"nonNegative", "bobSaves", "MAX"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newRequired(req.m, c.Spec, []string{"bobSpends"}); err == nil || err.Error() != "Config.Require names bobSpends, which is no val of testdata/bank.qnt: name a boolean val of the model" {
		t.Errorf("an unknown name: %v", err)
	}
	// MAX is a val, but no boolean: the first state refuses it
	err = req.trace(traces[0])
	if want := "Config.Require names MAX, which is 5 in state 0, not a boolean: name a boolean val of the model"; err == nil || err.Error() != want {
		t.Errorf("an int val: %v, want %q", err, want)
	}

	req.reached = req.reached[:2]
	req.states = 0
	for i := range req.reached {
		req.reached[i].States, req.reached[i].Traces = 0, 0
	}
	for _, tr := range traces {
		if err := req.trace(tr); err != nil {
			t.Fatal(err)
		}
	}
	lines, missed := req.report(len(traces))
	if want := "required nonNegative: reached in 10 of 10 traces (160 of 160 states)"; lines[0] != want {
		t.Errorf("got %q, want %q", lines[0], want)
	}
	// bobSaves holds in a state exactly when bob's balance there is over 5
	want := 0
	for _, tr := range traces {
		for _, st := range tr.States {
			if bob, ok := st.Vars["balances"].Lookup(itf.Value{Kind: itf.Str, S: "bob"}); ok && bob.I.Int64() > 5 {
				want++
			}
		}
	}
	if req.reached[1].States != want || want == 0 || len(missed) != 0 {
		t.Errorf("bobSaves held in %d states, the traces show %d; missed %v\n%s", req.reached[1].States, want, missed, strings.Join(lines, "\n"))
	}
}

func TestRequireFailsWhenNeverReached(t *testing.T) {
	needQuint(t)
	out := connectFailureOutput(t, "require-missed")
	for _, want := range []string{"required nonNegative: reached in 10 of 10 traces (20 of 20 states)", "required bobSaves: reached in 0 of 10 traces", "required bobSaves: no witness trace (unreachable in one step)", "never reached the required bobSaves in 10 traces (seed 0x1)", "Raise MaxSamples or MaxSteps"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// The witness trace reaches the state a capped deposit is wrong at, which
// the one sampled trace never does: without steering the run passes.
func TestRequireWitnessCatchesWhatSamplesMiss(t *testing.T) {
	needQuint(t)
	out := connectFailureOutput(t, "require-capped")
	for _, want := range []string{"--- FAIL: TestConnectProcessHelper/witness_bobSaves", "states diverge", "the witness trace of required bobSaves: the shortest path to it the Go evaluator found (2 steps)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// Require's vals and Eval's definitions in an instance module, which
// quint's flattening drops when nothing init or step uses reads them
// (golem's publication design): Require keeps its names, Eval compiles
// again for a name the model at hand lacks.
func TestRequireAndEvalInAnInstanceModule(t *testing.T) {
	needQuint(t)
	c := Config{Spec: "testdata/instance.qnt", Main: "design", Dir: t.TempDir()}
	req, err := compileRequired(t.Context(), Config{Spec: c.Spec, Main: c.Main, Dir: c.Dir, Require: []string{"atMax", "half"}})
	if err != nil {
		t.Fatalf("an instance module's vals: %v", err)
	}
	tr := &itf.Trace{States: []itf.State{{Vars: map[string]itf.Value{"n": intValue(2)}}, {Vars: map[string]itf.Value{"n": intValue(4)}}}}
	if err := req.trace(tr); err != nil {
		t.Fatal(err)
	}
	if lines, _ := req.report(1); req.reached[0].States != 1 || req.reached[1].States != 1 {
		t.Errorf("atMax and half each hold in one state:\n%s", strings.Join(lines, "\n"))
	}
	_, err = compileRequired(t.Context(), Config{Spec: c.Spec, Main: c.Main, Dir: c.Dir, Require: []string{"atMax", "atMx"}})
	if want := "Config.Require names atMx, which is no val of testdata/instance.qnt: name a boolean val of the model"; err == nil || err.Error() != want {
		t.Errorf("a misspelled val: %v, want %q", err, want)
	}

	// Eval's first compile keeps nothing: twice is compiled for when asked
	ev := &evaluator{ctx: t.Context(), c: c}
	s := &Step{Index: 1, Action: "inc", After: tr.States[1].Vars, eval: ev}
	if v, err := Eval[int](s, "twice", 3); err != nil || v != 6 {
		t.Errorf("twice(3): %v, %v", v, err)
	}
	if v, err := Eval[bool](s, "atMax"); err != nil || !v {
		t.Errorf("atMax: %v, %v", v, err)
	}
	if !slices.Equal(ev.keep, []string{"twice", "atMax"}) {
		t.Errorf("compiled to keep %v, want twice and atMax", ev.keep)
	}
	_, err = s.Eval("twise", 3)
	if want := `connect: Eval("twise") at step 1 (inc): twise is no definition of testdata/instance.qnt: name a val or a def of the model`; err == nil || err.Error() != want {
		t.Errorf("a misspelled def: %v, want %q", err, want)
	}
}

func intValue(i int64) itf.Value { return itf.Value{Kind: itf.Int, I: big.NewInt(i)} }

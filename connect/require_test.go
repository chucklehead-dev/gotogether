package connect

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/itf"
)

// One quint run and one compile judge the counts and both refusals: a
// subprocess for each case cost connect's budget twice over.
func TestRequireCountsAndRefusals(t *testing.T) {
	needQuint(t)
	c := Config{Spec: "testdata/bank.qnt", MaxSamples: 10, MaxSteps: 15, Seed: "0x1", Dir: t.TempDir(), Require: []string{"nonNegative", "bobSaves", "MAX"}}
	files, c, err := GenerateRun(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	traces, err := ReadTraces(t.Context(), c, files)
	if err != nil {
		t.Fatal(err)
	}
	req, err := compileRequired(t.Context(), c)
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
	for _, want := range []string{"required nonNegative: reached in 10 of 10 traces (20 of 20 states)", "required bobSaves: reached in 0 of 10 traces", "never reached the required bobSaves in 10 traces (seed 0x1)", "Raise MaxSamples or MaxSteps"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestSimulateNativeGraphDocument(t *testing.T) {
	skip.Quint(t)
	for _, tc := range []struct {
		name     string
		args     []string
		complete bool
		cut      string
	}{{"complete", nil, true, ""}, {"state cap", []string{"-states", "1"}, false, "states"}, {"depth cap", []string{"-depth", "1"}, false, "depth"}} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"-json", "-graph", "-binding", "../../validate/testdata/vending.yaml", "-samples", "1"}, tc.args...)
			code, out := capture(t, func() (int, error) { return simulate(t.Context(), args) })
			var doc validate.Document
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("status%d: %v\n%s", code, err, out)
			}
			if doc.Graph == nil || doc.Graph.Scope != "model_state_graph" || doc.Graph.Complete != tc.complete || doc.Graph.Cut != tc.cut {
				t.Fatalf("wrong scope: %s", out)
			}
			coverage := doc.Graph.StepActions
			wantStatus := "unknown"
			if tc.complete {
				wantStatus = "unreachable"
			}
			if coverage == nil || coverage.Status != wantStatus || len(coverage.Actions) == 0 {
				t.Fatalf("lost named step-action reachability: %s", out)
			}
			foundRepair := false
			for _, action := range coverage.Actions {
				if action.Action == "repair" {
					foundRepair = true
					if action.Transitions != 0 || action.States != 0 {
						t.Fatalf("invented repair witness: %s", out)
					}
				}
			}
			if !foundRepair {
				t.Fatalf("omitted dead named action: %s", out)
			}
			if tc.name == "state cap" && doc.Graph.Bound.States != 1 {
				t.Fatalf("lost state bound: %+v", doc.Graph)
			}
			if tc.name == "depth cap" && doc.Graph.Bound.Depth != 1 {
				t.Fatalf("lost depth bound: %+v", doc.Graph)
			}
		})
	}
}

func TestSimulateErrorsAndSamplingHaveNoInventedGraph(t *testing.T) {
	skip.Quint(t)
	for _, args := range [][]string{{"-json", "-binding", "../../validate/testdata/vending.yaml", "-samples", "1", "-steps", "2"}, {"-json", "-graph", "-binding", filepath.Join(t.TempDir(), "missing.yaml")}} {
		_, out := capture(t, func() (int, error) { code, _ := simulate(t.Context(), args); return code, nil })
		var d validate.Document
		if err := json.Unmarshal([]byte(out), &d); err != nil {
			t.Fatal(err)
		}
		if d.Graph != nil {
			t.Fatalf("invented graph: %+v", d.Graph)
		}
	}
}

func TestSimulateUnsupportedScopeAndNativeNoVerdictStatus(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	spec, err := filepath.Abs("../../internal/qeval/testdata/grow.qnt")
	if err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(dir, "grow.yaml")
	if err = os.WriteFile(b, []byte("spec: "+spec+"\nmodule: grow\nactions: {step: {}}\ninvariants: [positive]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	code, out := capture(t, func() (int, error) { return simulate(t.Context(), []string{"-json", "-graph", "-binding", b}) })
	var d validate.Document
	if err = json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	if code != 2 || d.Verdict != "no verdict" || d.Error != "" || d.Graph == nil || d.Graph.Complete || d.Graph.Cut != "unsupported" || d.Graph.Stopped == "" {
		t.Fatalf("unsupported scope misreported: %s", out)
	}
	var buf bytes.Buffer
	writer := machineReports{json: true}
	if _, err = writer.write(&validate.Document{Command: "simulate", Verdict: "no verdict"}, &buf, 2, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"verdict": "no verdict"`)) {
		t.Fatalf("writer overwrote native scope: %s", buf.Bytes())
	}
}

// A graph check's path is data in the report (CW4): a script reads the
// violated invariant's path, the deadlock's and an eventual val's lasso
// from counterexample, never from detail's text, and a check that holds
// carries none.
func TestSimulateCounterexamplesAsData(t *testing.T) {
	skip.Quint(t)
	read := func(binding string) map[string]validate.DocCheck {
		t.Helper()
		_, out := capture(t, func() (int, error) { return simulate(t.Context(), []string{"-json", "-graph", "-binding", binding}) })
		var d validate.Document
		if err := json.Unmarshal([]byte(out), &d); err != nil {
			t.Fatalf("%s: %v\n%s", binding, err, out)
		}
		checks := map[string]validate.DocCheck{}
		for _, c := range d.Checks {
			checks[c.Name] = c
		}
		return checks
	}
	g := read("../../validate/testdata/graph.yaml")
	for _, name := range []string{"small", "deadlock"} {
		cx := g[name].Counterexample
		if cx == nil || len(cx.Steps) != 4 || !strings.HasPrefix(cx.Steps[0].Call, "init") || cx.Steps[3].Call != "bump(by = 2)" || len(cx.Steps[3].State) != 2 || cx.Replay == "" || len(cx.Cycle) != 0 {
			t.Errorf("%s: counterexample %+v, want init, inc, inc, bump(by = 2) with each state and a replay", name, cx)
		}
	}
	if cx := g["small"].Counterexample; cx != nil && (cx.Steps[3].State[0] != validate.NamedValue{Name: "x", Value: "3"}) {
		t.Errorf("small's last state %v, want x = 3 first", cx.Steps[3].State)
	}
	if cx := g["positive"].Counterexample; cx != nil {
		t.Errorf("positive holds, yet carries a counterexample %+v", cx)
	}
	h := read("../../validate/testdata/hot.yaml")
	if cx := h["drained"].Counterexample; cx == nil || len(cx.Steps) != 2 || len(cx.Cycle) != 4 || cx.Stutter || cx.Replay == "" {
		t.Errorf("drained's hot lasso %+v, want a 2-step prefix and a 4-step cycle", cx)
	}
}

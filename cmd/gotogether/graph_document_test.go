package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

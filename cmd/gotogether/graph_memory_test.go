package main

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestGraphMemoryCLIRequiresGraphAndPositiveBound(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-binding", "missing.yaml", "-memory", "2"}, "requires -graph"},
		{[]string{"-binding", "missing.yaml", "-graph", "-memory", "0"}, "positive MiB"},
		{[]string{"-binding", "missing.yaml", "-graph", "-memory", "-1"}, "positive MiB"},
		{[]string{"-binding", "missing.yaml", "-graph", "-memory", "9223372036854775807"}, "positive MiB"},
	} {
		_, err := simulationRequest(tc.args, &machineReports{}, &simLog{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("args=%v err=%v; want %s", tc.args, err, tc.want)
		}
	}
	plan, err := simulationRequest([]string{"-binding", "missing.yaml", "-graph", "-memory", "2"}, &machineReports{}, &simLog{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.options.MemoryBytes != 2<<20 {
		t.Fatalf("MiB budget lost: %d", plan.options.MemoryBytes)
	}
	// The greatest accepted MiB count must convert without signed wraparound.
	if got, err := graphMemoryBytes(math.MaxInt64 / (1 << 20)); err != nil || got <= 0 {
		t.Fatalf("maximum valid count: %d %v", got, err)
	}
}

// -process-memory bounds the whole process (golem's runner, killed by a
// search within its -memory estimate: #144): refused without -graph or
// unless positive, absent by default, and a bound below the process cuts
// the graph as a memory bound, named so, the peak resident set reported.
func TestProcessMemoryCLI(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-binding", "missing.yaml", "-process-memory", "64"}, "-process-memory requires -graph"},
		{[]string{"-binding", "missing.yaml", "-graph", "-process-memory", "0"}, "-process-memory must be a positive MiB count"},
		{[]string{"-binding", "missing.yaml", "-graph", "-process-memory", "9223372036854775807"}, "-process-memory must be a positive MiB count"},
	} {
		_, err := simulationRequest(tc.args, &machineReports{}, &simLog{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("args=%v err=%v; want %s", tc.args, err, tc.want)
		}
	}
	plan, err := simulationRequest([]string{"-binding", "missing.yaml", "-graph"}, &machineReports{}, &simLog{})
	if err != nil || plan.options.ProcessBytes != 0 {
		t.Fatalf("by default: %v, process bound %d, want none", err, plan.options.ProcessBytes)
	}
	plan, err = simulationRequest([]string{"-binding", "missing.yaml", "-graph", "-process-memory", "6000"}, &machineReports{}, &simLog{})
	if err != nil || plan.options.ProcessBytes != 6000<<20 {
		t.Fatalf("6000 MiB: %v, %d", err, plan.options.ProcessBytes)
	}

	skip.Quint(t)
	b := "../../validate/testdata/wide.yaml"
	code, out := capture(t, func() (int, error) {
		return simulate(t.Context(), []string{"-json", "-graph", "-process-memory", "1", "-binding", b})
	})
	var d validate.Document
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	g := d.Graph
	if code != 2 || g == nil || g.Complete || g.Cut != "memory" || !g.ProcessCut || g.ProcessSeen <= 1<<20 || g.Bound.ProcessBytes != 1<<20 || g.PeakResident <= 0 {
		t.Fatalf("a 1 MiB process bound: exit %d, graph %+v", code, g)
	}
	_, text := capture(t, func() (int, error) {
		return simulate(t.Context(), []string{"-graph", "-process-memory", "1", "-binding", b})
	})
	if !strings.Contains(text, "truncated by the process memory bound (1048576 bytes") {
		t.Errorf("the text names no process bound:\n%s", text)
	}
	_, out = capture(t, func() (int, error) { return simulate(t.Context(), []string{"-json", "-graph", "-binding", b}) })
	d = validate.Document{}
	if err := json.Unmarshal([]byte(out), &d); err != nil || d.Graph == nil || !d.Graph.Complete || d.Graph.ProcessCut || d.Graph.PeakResident <= 0 {
		t.Fatalf("no process bound: %v, graph %+v", err, d.Graph)
	}
}

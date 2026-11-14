package main

import (
	"math"
	"strings"
	"testing"
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

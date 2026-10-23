package main

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// The suite's gaps are what no exploration tried: an action each test
// leaves out but another takes is no gap; one no test takes is. A
// declared fault point no test reached, and a kind reached and never
// faulted, are gaps too.
func TestSuiteReport(t *testing.T) {
	m := "model/m.qnt:m"
	es := []*qtest.Evidence{
		{Test: "TestA", Spec: m, Runs: 2, Conform: 2,
			Model:  &qtest.ModelCoverage{Taken: map[string]int{"put": 2}, Never: []string{"get", "del"}},
			Faults: &qenv.Coverage{Points: []*qenv.PointCoverage{{Kind: "rpc.db", Runs: 2, Injected: []int{3, 1}}, {Kind: "disk.node.crash", Sites: []string{"a.go:1"}}}}},
		{Test: "TestB", Spec: m, Runs: 3, Conform: 1, FailingBy: map[string]int{"violates inv": 2},
			Model:  &qtest.ModelCoverage{Taken: map[string]int{"get": 1}, Never: []string{"put", "del"}},
			Faults: &qenv.Coverage{Points: []*qenv.PointCoverage{{Kind: "queue.jobs.ack", Runs: 3, Injected: []int{5}}}}},
	}
	report, gaps := suiteReport(es)
	for _, want := range []string{
		"TestB: 3 runs, 1 conform, 2 violates inv",
		"suite: 2 explorations, 5 runs",
		"model/m.qnt:m: no test took del",
		"declared and reached by no test: disk.node.crash",
		"reached and never faulted by any test: queue.jobs.ack",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("missing %q in:\n%s", want, report)
		}
	}
	if gaps != 3 {
		t.Errorf("%d gaps, want 3 (del, disk.node.crash, queue.jobs.ack):\n%s", gaps, report)
	}
	for name, want := range map[string]bool{"evidence.json": true, "evidence-2.json": false, "other.json": false} {
		if isEvidence(name) != want {
			t.Errorf("isEvidence(%q) = %v", name, !want)
		}
	}
}

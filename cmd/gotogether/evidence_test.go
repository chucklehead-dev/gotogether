package main

import (
	"os"
	"path/filepath"
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
		{Test: "TestA", Role: qtest.RoleFaithful, Spec: m, Runs: 2, Conform: 2,
			Model:  &qtest.ModelCoverage{Taken: map[string]int{"put": 2}, Never: []string{"get", "del"}},
			Faults: &qenv.Coverage{Points: []*qenv.PointCoverage{{Kind: "rpc.db", Runs: 2, Injected: []int{3, 1}}, {Kind: "disk.node.crash", Sites: []string{"a.go:1"}}}}},
		{Test: "TestB", Role: qtest.RoleFaithful, Spec: m, Runs: 3, Conform: 1, FailingBy: map[string]int{"violates inv": 2},
			Model:  &qtest.ModelCoverage{Taken: map[string]int{"get": 1}, Never: []string{"put", "del"}},
			Faults: &qenv.Coverage{Points: []*qenv.PointCoverage{{Kind: "queue.jobs.ack", Runs: 3, Injected: []int{5}}}}},
		// a mutant takes del, which the system never does: not counted
		{Test: "TestB#2", Role: qtest.RoleMutant, Spec: m, Runs: 1,
			Model: &qtest.ModelCoverage{Taken: map[string]int{"del": 1}}},
	}
	report, gaps := suiteReport(es)
	for _, want := range []string{
		"TestB: 3 runs, 1 conform, 2 violates inv",
		"suite: 3 explorations, 6 runs",
		"TestB#2: 1 runs, 0 conform (mutant: not counted below)",
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
}

// The command reads evidence by its format, wherever it is (ITF traces and
// corpus entries beside it are skipped), and refuses an old file.
func TestEvidenceFiles(t *testing.T) {
	dir := t.TempDir()
	e := &qtest.Evidence{Format: qtest.EvidenceFormat, Role: qtest.RoleFaithful, Test: "TestA#1", Runs: 1, Conform: 1}
	if err := e.WriteJSON(filepath.Join(dir, "TestA", "x1.json")); err == nil {
		t.Fatal("WriteJSON made a directory it should not need to")
	}
	_ = os.MkdirAll(filepath.Join(dir, "TestA"), 0o755)
	if err := e.WriteJSON(filepath.Join(dir, "TestA", "x1.json")); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "TestA", "trace.itf.json"), []byte(`{"states": []}`), 0o644)
	if code, err := evidence([]string{dir}); code != 0 || err != nil {
		t.Errorf("evidence: %d %v", code, err)
	}
	_ = os.WriteFile(filepath.Join(dir, "old.json"), []byte(`{"format": "gotogether.evidence/0", "runs": 1}`), 0o644)
	if _, err := evidence([]string{dir}); err == nil || !strings.Contains(err.Error(), "run the tests again") {
		t.Errorf("an old evidence file: %v", err)
	}
}

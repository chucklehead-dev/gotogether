package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ev "github.com/chucklehead-dev/gotogether/internal/evidence"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// views are es as the evidence command reads them: their JSON, as
// WriteJSON writes it, decoded into the command's view.
func views(t *testing.T, es ...*qtest.Evidence) []*suiteEvidence {
	t.Helper()
	var out []*suiteEvidence
	for _, e := range es {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var v suiteEvidence
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		out = append(out, &v)
	}
	return out
}

// The suite's gaps are what no exploration tried: an action each test
// leaves out but another takes is no gap; one no test takes is. A
// declared fault point no test reached, and a kind reached and never
// faulted, are gaps too.
func TestSuiteReport(t *testing.T) {
	m := "model/m.qnt:m"
	es := []*qtest.Evidence{
		{Test: "TestA", Role: qtest.RoleFaithful, Spec: m, Runs: 2, Conform: 2, Exhaust: &qtest.Extent{Delays: 2, Faults: 1, Complete: true},
			Model:  &qtest.ModelCoverage{Taken: map[string]int{"put": 2}, Never: []string{"get", "del"}},
			Faults: &qenv.Coverage{Points: []*qenv.PointCoverage{{Kind: "rpc.db", Runs: 2, Injected: []int{3, 1}}, {Kind: "disk.node.crash", Sites: []string{"a.go:1"}}}}},
		{Test: "TestB", Role: qtest.RoleFaithful, Spec: m, Runs: 3, Conform: 1, FailingBy: map[string]int{"violates inv": 2},
			Sample: &qtest.SampleBound{Runs: 3, Rate: 0.63, OneIn: 1}, Judges: &qtest.Judges{Evaluator: 1},
			Model:  &qtest.ModelCoverage{Taken: map[string]int{"get": 1}, Never: []string{"put", "del"}},
			Faults: &qenv.Coverage{Points: []*qenv.PointCoverage{{Kind: "queue.jobs.ack", Runs: 3, Injected: []int{5}}}}},
		// a mutant takes del, which the system never does: not counted
		{Test: "TestB#2", Role: qtest.RoleMutant, Spec: m, Runs: 1,
			Model: &qtest.ModelCoverage{Taken: map[string]int{"del": 1}}},
	}
	report, gaps := suiteReport(views(t, es...))
	for _, want := range []string{
		"TestA: 2 runs, 2 conform (complete within 2 delays and 1 fault)",
		"TestB: 3 runs, 1 conform, 2 violates inv (a sample: a failure in under 63% of runs may be missed) (1 judged by the Go evaluator alone)",
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
	if !ev.Is(filepath.Join(dir, "TestA", "x1.json")) || ev.Is(filepath.Join(dir, "TestA", "trace.itf.json")) {
		t.Error("evidence.Is: the evidence not seen, or an ITF trace taken for it")
	}
	if code, err := evidence([]string{dir}); code != 0 || err != nil {
		t.Errorf("evidence: %d %v", code, err)
	}
	_ = os.WriteFile(filepath.Join(dir, "old.json"), []byte(`{"format": "gotogether.evidence/0", "runs": 1}`), 0o644)
	if _, err := evidence([]string{dir}); err == nil || !strings.Contains(err.Error(), "run the tests again") {
		t.Errorf("an old evidence file: %v", err)
	}
}

// -expect fails, naming it, on a check that wrote no evidence: a test
// that skipped or was left out passes otherwise unseen. A check names a
// test (its explorations and its subtests'), an exploration, or either
// after its package.
func TestEvidenceExpect(t *testing.T) {
	dir := t.TempDir()
	// as explorations write them: examples/hello is a module of its own,
	// whose root package is "_"
	for _, e := range []*qtest.Evidence{
		{Package: "qtest", Import: "github.com/chucklehead-dev/gotogether/qtest", Test: "TestA#1"},
		{Package: "qtest", Import: "github.com/chucklehead-dev/gotogether/qtest", Test: "TestA#2"},
		{Package: "_", Import: "github.com/chucklehead-dev/gotogether/examples/hello", Test: "TestB/faults#1"},
	} {
		e.Format, e.Role, e.Runs, e.Conform = qtest.EvidenceFormat, qtest.RoleFaithful, 1, 1
		d := filepath.Join(dir, e.Package, strings.ReplaceAll(e.Test, "/", "_"))
		_ = os.MkdirAll(d, 0o755)
		if err := e.WriteJSON(filepath.Join(d, "x1.json")); err != nil {
			t.Fatal(err)
		}
	}
	for _, x := range []struct {
		checks  string
		code    int
		missing []string
	}{
		{"TestA\nTestB # a subtest's explorations count\n\n  # indented\n", 0, nil},
		{"qtest TestA#2\nexamples/hello TestB/faults\nTestB/faults#1", 0, nil},
		{"TestA#3\nTestA\nqtest TestB\nTestC   # skipped", 1, []string{"TestA#3", "qtest TestB", "TestC"}},
		{"TestAB\nTestB/fault", 1, []string{"TestAB", "TestB/fault"}}, // no prefix of a name
		{"github.com/chucklehead-dev/gotogether/examples/hello TestB\n_ TestB\nhello TestB", 0, nil},
		{"les/hello TestB\nexamples/bank TestB", 1, []string{"les/hello TestB", "examples/bank TestB"}}, // whole segments only
	} {
		f := filepath.Join(t.TempDir(), "checks.txt")
		if err := os.WriteFile(f, []byte(x.checks), 0o644); err != nil {
			t.Fatal(err)
		}
		code, out := capture(t, func() (int, error) { return evidence([]string{"-expect", f, dir}) })
		if code != x.code {
			t.Errorf("%q: exit %d, want %d:\n%s", x.checks, code, x.code, out)
		}
		if len(x.missing) > 0 && !strings.Contains(out, "wrote no evidence: "+strings.Join(x.missing, ", ")+" (") {
			t.Errorf("%q: the output does not name %v:\n%s", x.checks, x.missing, out)
		}
	}
	// no evidence at all: every check is missing
	f := filepath.Join(t.TempDir(), "checks.txt")
	_ = os.WriteFile(f, []byte("TestA\n"), 0o644)
	if code, out := capture(t, func() (int, error) { return evidence([]string{"-expect", f, t.TempDir()}) }); code != 1 || !strings.Contains(out, "wrote no evidence: TestA") {
		t.Errorf("no evidence: exit %d:\n%s", code, out)
	}
	// a file that names nothing is an error, not a pass
	_ = os.WriteFile(f, []byte("# nothing\n"), 0o644)
	if _, err := evidence([]string{"-expect", f, dir}); err == nil || !strings.Contains(err.Error(), "no check in it") {
		t.Errorf("an empty file: %v", err)
	}
}

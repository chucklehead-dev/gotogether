package twophase_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
)

// The model's whole state graph for three participants (99 states):
// both invariants held in each, so proved for the instance, and every
// state the step cannot leave is every node decided, the protocol's end
// (simulate.terminal): without it they are deadlocks.
func TestGraphProvesInvariants(t *testing.T) {
	qtest.Model(t, binding).Expect(t, qtest.Proves("consistency"), qtest.Proves("commitIsDecided"), qtest.NoDeadlocks())
}

// The model with the coordinator's decision made early (it commits once
// any participant has prepared, the design error tpc's EarlyCommit makes
// in code): the graph refutes consistency, its shortest counterexample
// four steps (p1 prepares, the coordinator commits, p1 commits, p2
// aborts), which quint test replays.
func TestGraphFindsEarlyDecision(t *testing.T) {
	qtest.RequireQuint(t)
	dir := t.TempDir()
	if err := os.CopyFS(filepath.Join(dir, "model"), os.DirFS("model")); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(dir, "model", "twophase.qnt")
	src, err := os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	const all, early = "ctx.messages.preparedIn() == PARTICIPANTS", "ctx.messages.preparedIn().size() > 0"
	if strings.Count(string(src), all) != 1 {
		t.Fatalf("the model's commit decision is no longer %q", all)
	}
	if err := os.WriteFile(spec, []byte(strings.Replace(string(src), all, early, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(binding)
	if err != nil {
		t.Fatal(err)
	}
	bpath := filepath.Join(dir, binding)
	if err := os.WriteFile(bpath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	qtest.Model(t, bpath).Expect(t, qtest.Refutes("consistency").InAtMost(4))
}

package twophase_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// graphOf is the binding's state graph, explored by the Go evaluator
// (gotogether simulate -graph).
func graphOf(t *testing.T, path string) *validate.GraphResult {
	t.Helper()
	c, err := validate.NewChecker(path, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	r, err := c.Graph(validate.GraphSearch{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The model's whole state graph for three participants: 99 states, both
// invariants held in each, so proved for the instance. Its 9 states with
// no transition are every node decided, the protocol's end
// (simulate.terminal): without it they are deadlocks.
func TestGraphProvesInvariants(t *testing.T) {
	qtest.RequireQuint(t)
	r := graphOf(t, binding)
	if !r.Complete || r.States != 99 {
		t.Fatalf("graph %s", r.Extent())
	}
	for _, v := range r.Invariants {
		if !v.Held() || v.Checked != r.States {
			t.Errorf("%s: %+v", v.Name, v)
		}
	}
	if r.Deadlocks != 0 || r.Terminals != 9 {
		t.Fatalf("deadlocks %d (nearest %+v), terminals %d", r.Deadlocks, r.Deadlock, r.Terminals)
	}
	if never := r.NeverEnabled(); len(never) > 0 {
		t.Errorf("never enabled: %v", never)
	}
}

// The model with the coordinator's decision made early (it commits once
// any participant has prepared, the design error tpc's EarlyCommit makes
// in code): the graph finds consistency violated, its shortest
// counterexample four steps (p1 prepares, the coordinator commits, p1
// commits, p2 aborts), and quint test replays it.
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
	r := graphOf(t, bpath)
	consistency := r.Invariants[0]
	if !r.Complete || consistency.Name != "consistency" || !consistency.Violated {
		t.Fatalf("%s: %+v", r.Extent(), r.Invariants)
	}
	cx := consistency.Counterexample
	want := `init.then(prepare("p1")).then(decideCommit).then(commitAsInstructed("p1")).then(abort("p2")).expect(not(consistency))`
	if cx.Replay != want || len(cx.Sampled) > 0 {
		t.Fatalf("replay %s (sampled %v), want %s exactly", cx.Replay, cx.Sampled, want)
	}
	mod := filepath.Join(dir, "cex.qnt")
	text := "module cex {\n  import twophase(PARTICIPANTS = Set(\"p1\", \"p2\", \"p3\")).* from \"model/twophase\"\n  run early = " + cx.Replay + "\n}\n"
	if err := os.WriteFile(mod, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("quint", "test", "--main", "cex", "--max-samples", "5", mod)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("quint test of the counterexample: %v\n%s", err, out)
	}
}

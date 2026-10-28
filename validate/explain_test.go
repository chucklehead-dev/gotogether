package validate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

func limiterRun(clients ...string) []qtrace.Step {
	sink := &qobs.MemorySink{}
	r := qobs.NewRecorder(sink, qobs.WithActor("limiter"))
	for _, c := range clients {
		op := "admit"
		if name, ok := strings.CutPrefix(c, "-"); ok {
			op, c = "release", name
		}
		r.Record(context.Background(), op, nil, "client", c)
	}
	return sink.Steps()
}

// A rejected step's report names the condition that is false and its
// values (H2): the limiter's third admit fails admitted.size() < LIMIT,
// 2 < 2, with what the step could have done instead; quint's report
// (the step, the state) is as it was. Check and CheckBatch say the same.
func TestRejectionNamesTheConjunct(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/limiter.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	steps := limiterRun("a", "b", "c")
	rep, err := c.Check(context.Background(), steps)
	if err != nil {
		t.Fatal(err)
	}
	reps, errs := c.CheckBatch(context.Background(), [][]qtrace.Step{steps, limiterRun("a", "-a", "a")}, 0)
	if errs[0] != nil || errs[1] != nil {
		t.Fatal(errs)
	}
	if !reps[1].OK() {
		t.Fatalf("a conforming run: %s", reps[1].Summary())
	}
	for _, rep := range []*Report{rep, reps[0]} {
		sum := rep.Summary()
		t.Logf("%s", sum)
		f := rep.Actors[0].Failure
		if rep.OK() || f == nil || f.Kind != "disabled" || f.Index != 2 {
			t.Fatalf("want the third step rejected:\n%s", sum)
		}
		w := f.Why
		if w == nil || w.Unexplained != "" || len(w.Conditions) == 0 {
			t.Fatalf("no explanation: %+v\n%s", w, sum)
		}
		leaf := w.Conditions[len(w.Conditions)-1]
		if leaf.Expr != "admitted.size() < LIMIT" || w.Action != `admit(client = "c")` {
			t.Errorf("the false conjunct: %+v", w)
		}
		for _, want := range []string{
			`why: the model's admit(client = "c") is disabled:`,
			`in admit: admitted.size() < LIMIT is false`,
			`where admitted.size() = 2, admitted = Set("a", "b"), LIMIT = 2`,
			`enabled instead: release(client = "a"), release(client = "b")`,
			"model state before the step:", // quint's, as before
		} {
			if !strings.Contains(sum, want) {
				t.Errorf("the report lacks %q:\n%s", want, sum)
			}
		}
	}
}

// Under GOTOGETHER_EVAL=quint the evaluator explains nothing; the
// verdict is quint's either way.
func TestRejectionUnexplainedUnderQuint(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/limiter.yaml", Options{Dir: t.TempDir(), Eval: "quint"})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := c.Check(context.Background(), limiterRun("a", "b", "c"))
	if err != nil {
		t.Fatal(err)
	}
	if f := rep.Actors[0].Failure; rep.OK() || f == nil || f.Kind != "disabled" || f.Index != 2 || f.Why != nil {
		t.Errorf("quint's verdict alone:\n%s", rep.Summary())
	}
}

// A grist application's rejection reads in its terms: the golden shop's
// add-item committed with an empty read set (a decision that read
// nothing, as an Unconditioned command's or a runtime that dropped its
// reads would) fails covers(reads_, n) for the stock it needed, and the
// reading names the read model that covers it.
func TestRejectionReadsAsGrist(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	// the shop's model, with the grist library its tests vendor beside it
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "model"), 0o755); err != nil {
		t.Fatal(err)
	}
	for to, from := range map[string]string{"shop.binding.yaml": "../grist/testdata/gen/shop.binding.yaml", "model/grist.qnt": "../grist/model/grist.qnt",
		"model/shop.qnt": "../grist/testdata/gen/model/shop.qnt", "model/shop_gen.qnt": "../grist/testdata/gen/model/shop_gen.qnt",
		"model/shop_rules.qnt": "../grist/testdata/gen/model/shop_rules.qnt", "model/shop_steps.qnt": "../grist/testdata/gen/model/shop_steps.qnt"} {
		b, err := os.ReadFile(from)
		if err != nil {
			t.Skipf("the golden shop: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, to), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := NewChecker(filepath.Join(dir, "shop.binding.yaml"), Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	step := qtrace.Step{Action: "addItem", Recorder: "r", Seq: 1, Actor: "t1", Process: "store", Outcome: "ok",
		Args: map[string]any{"item": "pen", "qty": int64(1), "of": "x", "id": "c1", "principal": "p", "readPos": int64(0), "reads": []any{}},
		Obs:  map[string]any{"head": int64(1), "events": `["item-added"]`, "strs": `["pen", "x"]`, "ints": `[1]`, "causes": `[["command", "c1"]]`}}
	rep, err := c.Check(context.Background(), []qtrace.Step{step})
	if err != nil {
		t.Fatal(err)
	}
	sum := rep.Summary()
	t.Logf("%s", sum)
	f := rep.Actors[0].Failure
	if rep.OK() || f == nil || f.Kind != "disabled" || f.Why == nil || f.Why.Unexplained != "" {
		t.Fatalf("want the step rejected, explained:\n%s", sum)
	}
	found := false
	for _, c := range f.Why.Conditions {
		found = found || c.Expr == "covers(reads_, n)"
	}
	if !found {
		t.Errorf("want covers(reads_, n) among the false conditions: %+v", f.Why.Conditions)
	}
	for _, want := range []string{`the decision read nothing of item-added events tagged item="pen" (its read set: nothing)`,
		// both read models cover the need: items folds every item-added
		// event, stock those of one item (its key)
		"read it through the read model items or stock, or the Reads of the event type"} {
		if !strings.Contains(f.Why.Reading, want) {
			t.Errorf("the grist reading %q lacks %q", f.Why.Reading, want)
		}
	}
}

package qtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

// compiledTestdata copies validate's testdata models and the bindings
// named into a directory of their own, each binding's model compiled
// beside it (validate.CompiledFor), and returns the directory. It needs
// quint, to compile.
func compiledTestdata(t *testing.T, bindings ...string) string {
	t.Helper()
	skip.Quint(t)
	dir := t.TempDir()
	for _, f := range append([]string{"graph.qnt", "hot.qnt", "vending.qnt"}, bindings...) {
		data, err := os.ReadFile(filepath.Join("../validate/testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range bindings {
		p := filepath.Join(dir, b)
		cm, err := validate.Compile(p, validate.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := cm.Write(validate.CompiledFor(p)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Every kind of model claim, judged by the binding's compiled model with
// no quint on PATH (GOTOGETHER_COMPILED=1), as TestModelProvesAndRefutes,
// TestModelLive and TestModelPossible judge them with quint: the same
// verdicts, each claim that fails failing as there. What needs quint is
// said not done (the graph's cross-check, the witnesses' replays), and no
// claim says a witness was replayed. Not parallel: it sets PATH and the
// environment.
func TestModelClaimsFromCompiledModel(t *testing.T) {
	dir := compiledTestdata(t, "graph.yaml", "graph_ends.yaml", "hot_stops.yaml", "hot_storm.yaml", "vending_strict.yaml")
	t.Setenv("PATH", t.TempDir())
	t.Setenv(compiledEnv, "1")
	at := func(b string) string { return filepath.Join(dir, b) }
	logs := func(f *failTB) string { return strings.Join(f.logs, "\n") }

	g := at("graph.yaml")
	f := modelExpect(t, g, Proves("positive"), Refutes("small").InAtMost(5))
	if f.failed {
		t.Fatalf("graph: %s", f.errs)
	}
	for _, want := range []string{"analysed by its compiled model", "not done: the cross-check", "not done: the replay of",
		"proving positive: no state violates it in the whole state graph", "refuting small within 5 steps: a state", "not done: quint test would replay it"} {
		if !strings.Contains(logs(f), want) {
			t.Errorf("graph: want %q logged in\n%s", want, logs(f))
		}
	}
	if strings.Contains(logs(f), "replayed by quint test") {
		t.Errorf("graph: a witness said replayed with no quint:\n%s", logs(f))
	}
	wantModel(t, g, []ModelClaim{Proves("small"), Refutes("positive"), NoDeadlocks(), Refutes("small").InAtMost(1), AllStepActionsReachable()},
		"the model fails proving small: a state", "the model fails refuting positive: the model guarantees it",
		"the model fails having no deadlock: 1 reachable states the step cannot leave", "the model fails refuting small within 1 steps",
		"the model fails reaching every step action: the complete graph never executes: never")
	// the terminal predicate is a val of the compiled model's own
	wantModel(t, at("graph_ends.yaml"), []ModelClaim{NoDeadlocks(), Proves("positive")})

	wantModel(t, at("hot_stops.yaml"), []ModelClaim{Live("drained")})
	storm := at("hot_storm.yaml")
	wantModel(t, storm, []ModelClaim{Refutes("drained")})
	wantModel(t, storm, []ModelClaim{Live("drained")}, "the model fails driving drained (live): a fair cycle keeps it false forever")

	// found by the evaluator's search, or by its sampler where the search
	// cannot start (quint's sampling, with quint); impossible by a
	// complete search
	wantModel(t, g, []ModelClaim{Possible(Scenario(Takes("inc"), Takes("reset")))})
	strict := at("vending_strict.yaml")
	sold := Takes("buy").Outcome("ok")
	wantModel(t, strict, []ModelClaim{Possible(Scenario(Takes("insert"), sold))})
	wantModel(t, strict, []ModelClaim{Possible(NamedScenario("three of two", sold, sold, sold))},
		`the model fails able to follow the scenario "three of two"`, "impossible: no trace reaches it")
}

// With GOTOGETHER_COMPILED=1 a binding with no compiled model fails
// Model, naming where it looked, and a stale one is refused with the fix:
// neither asks quint, which would hide it in CI's job without quint. Not
// parallel: it sets the environment.
func TestModelCompiledRefused(t *testing.T) {
	dir := compiledTestdata(t, "graph.yaml")
	b := filepath.Join(dir, "graph.yaml")
	t.Setenv("PATH", t.TempDir())
	t.Setenv(compiledEnv, "1")
	spec := filepath.Join(dir, "graph.qnt")
	data, err := os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec, append(data, "\n// edited\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	f := modelExpect(t, b, Proves("positive"))
	if msg := strings.Join(f.errs, "\n"); !f.failed || !strings.Contains(msg, "is stale") || !strings.Contains(msg, "gotogether compile -binding") {
		t.Errorf("a stale artifact: %s", msg)
	}
	if err := os.Remove(validate.CompiledFor(b)); err != nil {
		t.Fatal(err)
	}
	f = &failTB{TB: t}
	runFail(func() { Model(f, b) })
	if msg := strings.Join(f.errs, "\n"); !f.failed || !strings.Contains(msg, "has none at "+validate.CompiledFor(b)) {
		t.Errorf("no artifact: %s", msg)
	}
}

package qtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modelExpect is Model(t, b).Expect(f, claims...) on a failTB: what it
// failed with, and logged.
func modelExpect(t *testing.T, b string, claims ...ModelClaim) *failTB {
	t.Helper()
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		Model(t, b).Expect(f, claims...)
	}()
	<-done
	return f
}

// wantModel holds the claims of b's model (each logged with its bound),
// or, with fails, wants each failure's text among the errors.
func wantModel(t *testing.T, b string, claims []ModelClaim, fails ...string) {
	t.Helper()
	f := modelExpect(t, b, claims...)
	msg := strings.Join(f.errs, "\n")
	if len(fails) == 0 && f.failed {
		t.Errorf("%s: %v failed:\n%s", b, claims, msg)
	}
	if len(fails) > 0 && !f.failed {
		t.Errorf("%s: %v held, want failures", b, claims)
	}
	for _, w := range fails {
		if !strings.Contains(msg, w) {
			t.Errorf("%s: want %q in\n%s", b, w, msg)
		}
	}
	if !f.failed {
		t.Logf("%s:\n%s", b, strings.Join(f.logs, "\n"))
	}
}

// The model's claims, judged by its state graph: graph.yaml proves
// positive in its whole graph and refutes small (violated in its
// deadlock, three steps from init, the path replayed by quint), and its
// deadlock fails NoDeadlocks with the nearest; graph_ends declares that
// state its end. Each claim fails where the model says otherwise.
func TestModelProvesAndRefutes(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	g := "../validate/testdata/graph.yaml"
	f := modelExpect(t, g, Proves("positive"), Refutes("small").InAtMost(5))
	if f.failed {
		t.Fatalf("graph: %s", f.errs)
	}
	for _, want := range []string{"proving positive: no state violates it in the whole state graph", "refuting small within 5 steps: a state", "(replayed by quint test)"} {
		if !strings.Contains(strings.Join(f.logs, "\n"), want) {
			t.Errorf("graph: want %q logged in %s", want, f.logs)
		}
	}
	wantModel(t, g, []ModelClaim{Proves("small"), Refutes("positive"), NoDeadlocks(), Refutes("small").InAtMost(1)},
		"the model fails proving small: a state", "the model fails refuting positive: the model guarantees it",
		"the model fails having no deadlock: 1 reachable states the step cannot leave", "the model fails refuting small within 1 steps")
	wantModel(t, "../validate/testdata/graph_ends.yaml", []ModelClaim{NoDeadlocks(), Proves("positive")})
}

// A truncated graph proves nothing: with graphStates 3 every claim that
// needs the whole graph has no verdict, naming the bound; a depth bound
// alone is a bounded proof, said so.
func TestModelBounds(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	dir := t.TempDir()
	spec, _ := filepath.Abs("../validate/testdata/graph.qnt")
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("spec: "+spec+"\nmodule: graph\nactions: {inc: {}, bump: {}, reset: {}, never: {}}\ninvariants: [small, positive]\n"+text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cut := write("cut.yaml", "simulate: {graphStates: 3, steps: 10, samples: 50}\n")
	wantModel(t, cut, []ModelClaim{Proves("positive"), NoDeadlocks()},
		"the model has no verdict on proving positive: no state violates it in the first 3 states breadth first, truncated by the state bound",
		"the model has no verdict on having no deadlock")
	deep := write("deep.yaml", "simulate: {graphDepth: 1, steps: 10, samples: 50}\n")
	f := modelExpect(t, deep, Proves("positive"))
	if f.failed || !strings.Contains(strings.Join(f.logs, "\n"), "every state within 1 steps of init") {
		t.Errorf("a depth bound: %s %s", f.errs, f.logs)
	}
}

// Liveness: hot_stops drains its queue (Live, in the whole graph);
// hot_storm's retries keep it full forever, refuting it with a lasso
// replayed by quint.
func TestModelLive(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	wantModel(t, "../validate/testdata/hot_stops.yaml", []ModelClaim{Live("drained")})
	storm := "../validate/testdata/hot_storm.yaml"
	wantModel(t, storm, []ModelClaim{Refutes("drained")})
	wantModel(t, storm, []ModelClaim{Live("drained")}, "the model fails driving drained (live): a fair cycle keeps it false forever")
}

// Follows is a scenario the model can follow, found by the Go
// evaluator (graph's two initial states it cannot start from: quint's
// sampling finds it); one it forbids (vending_strict's three sold of
// two) fails, impossible by a complete search; a name of neither kind
// fails before anything is simulated, as a misspelled claim would hold
// of nothing.
func TestModelPossible(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	g := "../validate/testdata/graph.yaml"
	wantModel(t, g, []ModelClaim{Follows(Scenario(Takes("inc"), Takes("reset")))})
	strict := "../validate/testdata/vending_strict.yaml"
	sold := Takes("buy").Outcome("ok")
	wantModel(t, strict, []ModelClaim{Follows(Scenario(Takes("insert"), sold))})
	wantModel(t, strict, []ModelClaim{Follows(NamedScenario("three of two", sold, sold, sold))},
		`the model fails able to follow the scenario "three of two"`, "impossible: no trace reaches it")
	f := modelExpect(t, g, Proves("smal"))
	if !f.failed || !strings.Contains(strings.Join(f.errs, ""), "smal is no invariant of the binding (invariants: small, positive)") {
		t.Errorf("a misspelled invariant: %s", f.errs)
	}
	f = modelExpect(t, g, Live("small"))
	if !f.failed || !strings.Contains(strings.Join(f.errs, ""), "small is no eventual val") {
		t.Errorf("an invariant claimed live: %s", f.errs)
	}
}

package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

// compileTo compiles bpath's model with quint (validate.Compile) into a
// directory of the test's own, returning the artifact's path.
func compileTo(t *testing.T, bpath string) string {
	t.Helper()
	cm, err := validate.Compile(bpath, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), filepath.Base(validate.CompiledFor(bpath)))
	if err := cm.Write(path); err != nil {
		t.Fatal(err)
	}
	return path
}

// noQuint takes quint (and every other binary) off PATH for the rest of
// the test: a compiled analysis that ran a quint process would fail.
func noQuint(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	t.Setenv(validate.CompiledEnv, "")
}

// verdicts are a report's verdicts, what a compiled analysis must agree
// with quint's on: each check's value and bound's kind, the deadlocks,
// each possible found or impossible, and the exit.
func verdicts(r *Report) string {
	var b strings.Builder
	for _, vs := range [][]Verdict{r.Invariants, r.Eventually} {
		for _, v := range vs {
			fmt.Fprintf(&b, "%s[%s] held=%v violated=%v error=%v unchecked=%v %s; ", v.Name, v.Class, v.Held, v.Violated, v.Error != "", v.Unchecked != "", v.Bound.Kind)
		}
	}
	if d := r.Deadlocks; d != nil {
		fmt.Fprintf(&b, "deadlock held=%v violated=%v; ", d.Held, d.Violated)
	}
	for _, p := range r.Possibles {
		fmt.Fprintf(&b, "possible %s found=%v impossible=%v; ", p.Possible.Text(), p.Found, p.Impossible)
	}
	fmt.Fprintf(&b, "code %d", r.Code)
	return b.String()
}

// withPredicates is testdata's graph model under a binding of its own
// whose simulate section names predicates that are no val of the model:
// a possible reached, one impossible, and a terminal, each a val of the
// compiled model's module (analysisPredicates).
func withPredicates(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src, err := os.ReadFile("../testdata/graph.qnt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "graph.qnt"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(dir, "preds.binding.yaml")
	if err := os.WriteFile(b, []byte(`spec: graph.qnt
module: graph
actions: { inc: {}, bump: {}, reset: {}, never: {} }
invariants: [small, positive]
simulate:
  holds: [positive]
  allows: [small]
  steps: 10
  samples: 200
  terminal: x == 3 and y == 2
  possible:
    - name: two and one
      when: x == 2 and y == 1
    - name: negative
      when: x < 0
`), 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

// A model analysed by its compiled model, with no quint on PATH, gives
// quint's verdicts on the same bindings, sampled or by the graph, for
// each kind of check: invariants held and violated, deadlocks and
// terminals, eventual vals as cycles, possibles of steps and of
// predicates, found and impossible. What needs quint is reported not
// done: the graph's cross-check, the witnesses' replays (quint's were
// done). Not parallel: it takes quint off PATH.
func TestCompiledSimulateAgreesWithQuint(t *testing.T) {
	skip.Quint(t)
	preds := withPredicates(t)
	cases := []struct {
		name, binding string
		opt           SimulateOptions
	}{
		{"graph, terminal", "../testdata/graph_ends.yaml", SimulateOptions{Graph: true, Replay: true}},
		{"lasso", "../testdata/hot.yaml", SimulateOptions{Graph: true, Replay: true}},
		{"live", "../testdata/hot_storm.yaml", SimulateOptions{Graph: true}},
		{"vending sampled", "../testdata/vending.yaml", SimulateOptions{}},
		{"vending graph", "../testdata/vending.yaml", SimulateOptions{Graph: true}},
		{"a literal constant overridden", "../testdata/vending.yaml", SimulateOptions{Graph: true, Constants: []string{"PRICE=3"}}},
		{"predicates sampled", preds, SimulateOptions{}},
		{"predicates graph", preds, SimulateOptions{Graph: true, Replay: true}},
		{"predicates, a cut graph", preds, SimulateOptions{Graph: true, States: 3}},
	}
	quint := make([]*Report, len(cases))
	artifacts := make([]string, len(cases))
	compiled := map[string]string{} // each binding compiled once
	for i, c := range cases {
		r, err := Simulate(t.Context(), c.binding, c.opt)
		if err != nil {
			t.Fatalf("%s: quint's: %v", c.name, err)
		}
		if artifacts[i] = compiled[c.binding]; artifacts[i] == "" {
			artifacts[i] = compileTo(t, c.binding)
			compiled[c.binding] = artifacts[i]
		}
		quint[i] = r
	}
	noQuint(t)
	for i, c := range cases {
		opt := c.opt
		opt.Compiled = artifacts[i]
		r, err := Simulate(t.Context(), c.binding, opt)
		if err != nil {
			t.Errorf("%s: compiled: %v", c.name, err)
			continue
		}
		if got, want := verdicts(r), verdicts(quint[i]); got != want {
			t.Errorf("%s: the compiled model's verdicts\n  %s\nquint's\n  %s\n%s", c.name, got, want, r.Text)
		}
		if r.Compiled != artifacts[i] || !strings.Contains(r.Text, "no quint") {
			t.Errorf("%s: the report does not say it is the compiled model's (%q):\n%s", c.name, r.Compiled, r.Text)
		}
		if g := r.Graph; g != nil && g.Complete && !hasNotDone(r, "the cross-check") {
			t.Errorf("%s: a complete graph's cross-check is not reported not done: %q", c.name, r.NotDone)
		}
		for j, v := range append(append([]Verdict(nil), r.Invariants...), r.Eventually...) {
			if !v.Violated || !opt.Replay {
				continue
			}
			if !strings.HasPrefix(v.Replayed, "not done: ") || !hasNotDone(r, "the replay of") {
				t.Errorf("%s: %s's witness replay %q, want not done (NotDone %q)", c.name, v.Name, v.Replayed, r.NotDone)
			}
			q := append(append([]Verdict(nil), quint[i].Invariants...), quint[i].Eventually...)[j]
			if q.Replayed != "replayed" {
				t.Errorf("%s: quint's replay of %s: %q", c.name, q.Name, q.Replayed)
			}
		}
	}
}

func hasNotDone(r *Report, prefix string) bool {
	for _, n := range r.NotDone {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

// simulate -matrix from a compiled model: each variant's literal
// constants given to the artifact's module as parameters, the same table
// as quint's. Not parallel: it takes quint off PATH.
func TestCompiledMatrixAgreesWithQuint(t *testing.T) {
	skip.Quint(t)
	const b = "../testdata/design.yaml"
	want, err := Matrix(t.Context(), b, SimulateOptions{Graph: true})
	if err != nil {
		t.Fatal(err)
	}
	path := compileTo(t, b)
	noQuint(t)
	got, err := Matrix(t.Context(), b, SimulateOptions{Graph: true, Compiled: path})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text() != want.Text() || got.Code != want.Code {
		t.Errorf("the compiled model's matrix (exit %d):\n%s\nquint's (exit %d):\n%s", got.Code, got.Text(), want.Code, want.Text())
	}
}

// What a compiled model cannot answer is refused with the fix, never
// passed and never asked of quint: an artifact compiled before it carried
// the simulate section's predicates (stale: compile it again); a
// predicate it carries no val of; a constant it inlined given another
// value; a parameter given no literal; quint verify. Not parallel: it
// takes quint off PATH.
func TestCompiledAnalysisRefusals(t *testing.T) {
	skip.Quint(t)
	preds := withPredicates(t)
	predsModel := compileTo(t, preds)
	vendingModel := compileTo(t, "../testdata/vending.yaml")
	// vending, its STOCK an expression: inlined in the module, no parameter
	dir := t.TempDir()
	src, err := os.ReadFile("../testdata/vending.qnt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vending.qnt"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	yaml, err := os.ReadFile("../testdata/vending.yaml")
	if err != nil {
		t.Fatal(err)
	}
	inlined := filepath.Join(dir, "vending.yaml")
	if err := os.WriteFile(inlined, []byte(strings.Replace(string(yaml), "STOCK: '2'", "STOCK: '1 + 1'", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	inlinedModel := compileTo(t, inlined)
	// the artifact as a gotogether before this one wrote it: its module
	// without the section's predicates
	cm, err := validate.LoadCompiled(predsModel)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for l := range strings.SplitSeq(cm.Eval.Text, "\n") {
		if !strings.Contains(l, "__gtg_pos_") {
			kept = append(kept, l)
		}
	}
	cm.Eval.Text = strings.Join(kept, "\n") + "\n"
	old := filepath.Join(t.TempDir(), "old.model.json")
	if err := cm.Write(old); err != nil {
		t.Fatal(err)
	}
	noQuint(t)

	expectErr := func(what string, err error, wants ...string) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: no error", what)
			return
		}
		for _, w := range wants {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %v\nwant it to say %q", what, err, w)
			}
		}
	}
	_, err = Simulate(t.Context(), preds, SimulateOptions{Graph: true, Compiled: old})
	expectErr("an old artifact", err, "is stale", "gotogether compile -binding")

	c, err := validate.NewCompiledChecker(preds, predsModel, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = For(c).Possible(t.Context(), []binding.Possible{{Name: "far", When: "x == 4 and y == 4"}}, PossibleSearch{})
	expectErr("a predicate the artifact lacks", err, "needs quint", "carries no val for x == 4 and y == 4", "simulate.possible", "compile it again")

	_, err = Simulate(t.Context(), inlined, SimulateOptions{Graph: true, Compiled: inlinedModel, Constants: []string{"STOCK=3"}})
	expectErr("an inlined constant overridden", err, "constant STOCK = 3", "STOCK = 1 + 1 in its module", "give the binding a literal")
	if _, err := Simulate(t.Context(), inlined, SimulateOptions{Graph: true, Compiled: inlinedModel}); err != nil {
		t.Errorf("the inlined constant as compiled: %v", err)
	}

	_, err = Simulate(t.Context(), "../testdata/vending.yaml", SimulateOptions{Graph: true, Compiled: vendingModel, Constants: []string{"PRICE=1 + 2"}})
	expectErr("a parameter given no literal", err, "constant PRICE = 1 + 2", "takes PRICE as a literal", "give a literal")

	_, err = Simulate(t.Context(), "../testdata/vending.yaml", SimulateOptions{Verify: 3, Compiled: vendingModel})
	expectErr("quint verify", err, "simulate -verify", "where quint is installed")
}

// GOTOGETHER_COMPILED chooses the artifact beside the binding where no
// compiled model is named: 1 with none fails naming where it looked.
// Not parallel: it sets the environment.
func TestCompiledChoiceInSimulate(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	for _, f := range []string{"graph.qnt", "graph_ends.yaml"} {
		data, err := os.ReadFile(filepath.Join("../testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b := filepath.Join(dir, "graph_ends.yaml")
	t.Setenv(validate.CompiledEnv, "1")
	if _, err := Simulate(t.Context(), b, SimulateOptions{Graph: true}); err == nil || !strings.Contains(err.Error(), "has none at "+validate.CompiledFor(b)) {
		t.Errorf("GOTOGETHER_COMPILED=1 without an artifact: %v", err)
	}
	cm, err := validate.Compile(b, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.Write(validate.CompiledFor(b)); err != nil {
		t.Fatal(err)
	}
	r, err := Simulate(t.Context(), b, SimulateOptions{Graph: true})
	if err != nil || r.Compiled != validate.CompiledFor(b) {
		t.Errorf("GOTOGETHER_COMPILED=1: compiled %q, %v", r.Compiled, err)
	}
	t.Setenv(validate.CompiledEnv, "0")
	if r, err := Simulate(t.Context(), b, SimulateOptions{Graph: true}); err != nil || r.Compiled != "" {
		t.Errorf("GOTOGETHER_COMPILED=0: compiled %q, %v", r.Compiled, err)
	}
	noQuint(t) // unset, and no quint: the artifact beside the binding
	if r, err := Simulate(t.Context(), b, SimulateOptions{Graph: true}); err != nil || r.Compiled != validate.CompiledFor(b) {
		t.Errorf("no quint: compiled %q, %v", r.Compiled, err)
	}
}

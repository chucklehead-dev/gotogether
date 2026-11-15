package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The lease design's two variants side by side (CW2): fencing holds what
// leasing breaks, both let the old leader write before the takeover. The
// table, the JSON document and the document's marked block say so; a
// variant whose classification the model contradicts fails the matrix,
// its cell saying how.
func TestSimulateMatrix(t *testing.T) {
	skip.Quint(t)
	b := "../../validate/testdata/design.yaml"
	code, text := capture(t, func() (int, error) { return simulate(t.Context(), []string{"-matrix", "-graph", "-binding", b}) })
	for _, want := range []string{"variant lease (DESIGN = \"lease\"):", "variant fence (DESIGN = \"fence\"):",
		"| | lease | fence |", "| noStaleWrite | breaks in 2 steps | holds |", "| oldNeverWrites | breaks in 1 step | breaks in 1 step |",
		// lease must only break, and its stale writes grow without bound: it
		// stops once both have broken, where it once ran to the state bound
		"graph   stopped once every invariant it must break broke: 4 states", "graph   complete: 4 states"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if code != 0 {
		t.Errorf("exit %d for a matrix as the design says", code)
	}

	_, out := capture(t, func() (int, error) {
		return simulate(t.Context(), []string{"-json", "-matrix", "-graph", "-binding", b})
	})
	var d validate.MatrixDocument
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if d.Format != "gotogether.matrix/1" || d.Verdict != "pass" || len(d.Variants) != 2 || d.Variants[0].Report == nil || len(d.Rows) != 2 {
		t.Fatalf("matrix document: %+v", d)
	}
	stale := d.Rows[0]
	if stale.Check != "noStaleWrite" || stale.Cells[0] != (validate.MatrixCellDoc{Variant: "lease", Expect: "allows", Verdict: "breaks", Steps: 2, OK: true}) ||
		stale.Cells[1] != (validate.MatrixCellDoc{Variant: "fence", Expect: "holds", Verdict: "holds", OK: true}) {
		t.Errorf("noStaleWrite's row: %+v", stale)
	}

	// the table into a design document, between its markers only
	dir := t.TempDir()
	doc := filepath.Join(dir, "design.md")
	if err := os.WriteFile(doc, []byte("# Lease\n\n<!-- gotogether:matrix -->\nstale\n<!-- /gotogether:matrix -->\n\nprose after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, text := capture(t, func() (int, error) {
		return simulate(t.Context(), []string{"-matrix", "-graph", "-write-matrix", doc, "-binding", b})
	}); code != 0 || !strings.Contains(text, "matrix written to") {
		t.Fatalf("write: %d\n%s", code, text)
	}
	got, _ := os.ReadFile(doc)
	if !bytes.Contains(got, []byte("<!-- gotogether:matrix -->\n| | lease | fence |\n")) || bytes.Contains(got, []byte("stale")) || !bytes.HasSuffix(got, []byte("\nprose after\n")) {
		t.Errorf("the document:\n%s", got)
	}

	// fence said to break noStaleWrite: the model holds it, and the matrix fails
	src, err := os.ReadFile(b)
	if err != nil {
		t.Fatal(err)
	}
	wrong := strings.Replace(string(src), "    - name: fence\n      constants: { DESIGN: '\"fence\"' }\n",
		"    - name: fence\n      constants: { DESIGN: '\"fence\"' }\n      holds: []\n      allows: [noStaleWrite, oldNeverWrites]\n", 1)
	// and lease said to hold it: the model breaks it
	wrong = strings.Replace(wrong, "      holds: []\n      allows: [noStaleWrite, oldNeverWrites]\n    - name: fence", "      holds: [noStaleWrite]\n      allows: [oldNeverWrites]\n    - name: fence", 1)
	spec, _ := filepath.Abs("../../validate/testdata/design.qnt")
	wrong = strings.Replace(wrong, "spec: design.qnt", "spec: "+spec, 1)
	wb := filepath.Join(dir, "wrong.yaml")
	if err := os.WriteFile(wb, []byte(wrong), 0o600); err != nil {
		t.Fatal(err)
	}
	code, text = capture(t, func() (int, error) { return simulate(t.Context(), []string{"-matrix", "-graph", "-binding", wb}) })
	if code != 1 || !strings.Contains(text, "| noStaleWrite | FAIL: breaks in 2 steps (must hold) | FAIL: holds (must break) |") {
		t.Errorf("a variant the model contradicts: exit %d\n%s", code, text)
	}
}

// A variant's own search bound is the one its graph searches (golem's
// leadership designs, each to its own depth, #144: the checker read the
// file's section, so every variant searched the section's bound), and a
// holds its search proved only to a depth says so in the table and the
// JSON, where it once read "holds" as the whole graph does.
func TestSimulateMatrixVariantSearch(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	src, err := os.ReadFile("../../validate/testdata/design.qnt")
	if err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(dir, "design.yaml")
	if err := os.WriteFile(filepath.Join(dir, "design.qnt"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte(`spec: design.qnt
module: design
constants: { DESIGN: '"fence"' }
actions: { takeover: {}, writeOld: {} }
invariants: [noStaleWrite]
simulate:
  holds: [noStaleWrite]
  terminal: 'true'
  variants:
    - name: whole
    - name: shallow
      search: { depth: 1 }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, text := capture(t, func() (int, error) { return simulate(t.Context(), []string{"-matrix", "-graph", "-binding", b}) })
	if code != 0 || !strings.Contains(text, "| noStaleWrite | holds | holds to depth 1 |") {
		t.Fatalf("exit %d, want the shallow variant's bounded proof in its cell:\n%s", code, text)
	}
	_, out := capture(t, func() (int, error) {
		return simulate(t.Context(), []string{"-json", "-matrix", "-graph", "-binding", b})
	})
	var d validate.MatrixDocument
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if c := d.Rows[0].Cells; len(c) != 2 || c[0].Depth != 0 || c[1].Depth != 1 || c[1].Verdict != "holds" {
		t.Errorf("cells %+v: want depth 0 for the whole graph, 1 for the shallow search", c)
	}
}

func TestSimulateMatrixRefusals(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-binding", "a.yaml", "-write-matrix", "d.md"}, "-write-matrix requires -matrix"},
		{[]string{"-matrix", "-binding", "a.yaml", "-binding", "b.yaml"}, "simulate -matrix: one -binding"},
		{[]string{"-variant", "fence", "-binding", "a.yaml"}, "-variant requires -matrix"},
		{[]string{"-matrix", "-variant", "fence", "-write-matrix", "d.md", "-binding", "a.yaml"}, "would write one column over the document's table"},
	} {
		if _, err := simulationRequest(tc.args, &machineReports{}, &simLog{}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: %v, want %q", tc.args, err, tc.want)
		}
	}
	skip.Quint(t)
	code, err := simulate(t.Context(), []string{"-matrix", "-graph", "-binding", "../../validate/testdata/graph.yaml"})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "has no variants") {
		t.Errorf("a binding with no variants: exit %d, %v", code, err)
	}
}

// switchModel has an action a constant turns off: extra is dead unless ON.
const switchModel = `module sw {
  const ON: bool
  var n: int
  action init = n' = 0
  action inc = all { n < 2, n' = n + 1 }
  action extra = all { ON, n < 2, n' = n + 1 }
  action step = any { inc, extra }
  val bounded = n <= 2
  val small = n < 2
}
`

// writeSwitch writes switchModel and a binding of it with the given
// invariants and simulate section, returning the binding's path.
func writeSwitch(t *testing.T, invariants, section string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sw.qnt"), []byte(switchModel), 0o600); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(dir, "sw.yaml")
	body := "spec: sw.qnt\nmodule: sw\nconstants: { ON: 'true' }\nactions: { inc: {}, extra: {} }\ninvariants: " + invariants + "\nsimulate:\n" + section
	if err := os.WriteFile(b, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return b
}

// simulate.dead (golem's graph_holds, #144): the actions a variant's
// switches turn off are listed, a variant's list replaces the section's,
// and the graph fails a listed action it takes, an unlisted one no state
// enables and a name the step does not call. A variant stopped at its
// counterexamples cannot tell, and leaves it unchecked.
func TestSimulateMatrixDead(t *testing.T) {
	skip.Quint(t)
	b := writeSwitch(t, "[bounded]", `  terminal: 'true'
  holds: [bounded]
  dead: []
  variants:
    - name: on
    - name: off
      constants: { ON: 'false' }
      dead: [extra]
    - name: off unlisted
      constants: { ON: 'false' }
    - name: on listed
      dead: [extra]
    - name: misnamed
      constants: { ON: 'false' }
      dead: [extar]
`)
	code, text := capture(t, func() (int, error) { return simulate(t.Context(), []string{"-matrix", "-graph", "-binding", b}) })
	for _, want := range []string{
		"| bounded | holds | holds | holds | holds | holds |",
		"FAIL outside the table: off unlisted: dead; on listed: dead; misnamed: dead",
		"extra: no state enables it, and simulate.dead does not list it",
		"extra: listed dead, but the graph takes it",
		"extar: no action the step calls",
		"dead    as listed: no action is dead",
		"dead    as listed: exactly extra never enabled",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if code != 1 {
		t.Errorf("exit %d: every cell passes, but three variants' dead lists are wrong", code)
	}
	_, out := capture(t, func() (int, error) {
		return simulate(t.Context(), []string{"-json", "-matrix", "-graph", "-binding", b})
	})
	var d validate.MatrixDocument
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(d.Others) != 3 || d.Others[0] != (validate.MatrixOtherDoc{Variant: "off unlisted", Kind: "graph", Name: "dead", Detail: d.Others[0].Detail}) {
		t.Errorf("others: %+v", d.Others)
	}

	// stopped at its counterexample, the graph never took extra, which
	// says nothing: unchecked, the matrix still passing
	broken := writeSwitch(t, "[small]", `  terminal: 'true'
  variants:
    - name: off
      constants: { ON: 'false' }
      allows: [small]
      dead: []
`)
	code, text = capture(t, func() (int, error) { return simulate(t.Context(), []string{"-matrix", "-graph", "-binding", broken}) })
	if code != 0 || !strings.Contains(text, "unchecked dead: not reached: the graph stopped at its counterexamples") || strings.Contains(text, "FAIL outside") {
		t.Errorf("a variant stopped at its counterexample: exit %d\n%s", code, text)
	}
}

// A check no row shows can fail a matrix whose every cell passes (golem's
// model without a terminal, every finished run a deadlock): the table says
// which, where it once showed only passing cells over an exit of 1.
func TestSimulateMatrixShowsOtherFailures(t *testing.T) {
	skip.Quint(t)
	b := writeSwitch(t, "[bounded]", `  holds: [bounded]
  variants:
    - name: on
`)
	code, text := capture(t, func() (int, error) { return simulate(t.Context(), []string{"-matrix", "-graph", "-binding", b}) })
	if code != 1 || !strings.Contains(text, "| bounded | holds |") || !strings.Contains(text, "FAIL outside the table: on: deadlock") {
		t.Errorf("a deadlock outside the table: exit %d\n%s", code, text)
	}
}

// -variant runs one of the matrix's variants, one column, so a script can
// run each in its own process and in parallel (golem's leadership, #144);
// an unknown name is refused with the variants there are.
func TestSimulateMatrixOneVariant(t *testing.T) {
	skip.Quint(t)
	b := "../../validate/testdata/design.yaml"
	code, text := capture(t, func() (int, error) {
		return simulate(t.Context(), []string{"-matrix", "-graph", "-variant", "fence", "-binding", b})
	})
	if code != 0 || !strings.Contains(text, "| | fence |\n") || strings.Contains(text, "variant lease") {
		t.Errorf("one variant: exit %d\n%s", code, text)
	}
	if _, err := simulate(t.Context(), []string{"-matrix", "-graph", "-variant", "fense", "-binding", b}); err == nil || !strings.Contains(err.Error(), "fense") || !strings.Contains(err.Error(), "lease") {
		t.Errorf("an unknown variant: %v", err)
	}
}

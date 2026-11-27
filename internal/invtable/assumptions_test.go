package invtable

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// assumeSrc is a source for typecheck's modules (TestRead's): its
// @assume lines, well and badly formed, in base (which m instantiates
// flattened, so m sees them), and one in a module of another model.
const assumeSrc = `module base {
  var x: int
  // @assume small = bounded: x stays small; the real one is an int64
  // @assume oneWriter: one writer at a time
  //@assume  tight   :   no space needed after the slashes
  /// @assume docComment: quint joins this to the next definition
  val y = 1 // @assume trailing: after code
  // @assume : no name
  // @assume noColon the colon is missing
  // @assume oneWriter: again
  // @assume positive: the name of a val
  // @assume wide = count: count is an int
  // @assume twice = bounded: bounded states small already
  // @assume gone = nowhere: no such val
  // @assumed is another word, no tag
}
module elsewhere {
  // @assume theirs: another model's, left out
}
`

func assumed(t *testing.T, main string) (*Model, []string) {
	t.Helper()
	m := read(t, main)
	return m, m.Assume([]byte(assumeSrc), "s.qnt")
}

// Assume reads each @assume line of a module the main module is or
// sees, in order; a malformed, misplaced, repeated or clashing one is a
// problem at its line with the fix; a val that states an assumption
// leaves the default properties.
func TestAssume(t *testing.T) {
	m, problems := assumed(t, "m")
	var got []string
	for _, a := range m.Assumptions {
		got = append(got, a.Module+"::"+a.Name+"="+a.Predicate+": "+a.Statement)
	}
	want := []string{
		"base::small=bounded: x stays small; the real one is an int64",
		"base::oneWriter=: one writer at a time",
		"base::tight=: no space needed after the slashes",
	}
	if !slices.Equal(got, want) {
		t.Errorf("assumptions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	wantP := []string{
		"s.qnt:6: /// @assume is a doc comment, which quint joins to the next definition's (and refuses at a module's end): write it with // (// @assume name: what the model leaves out)",
		"s.qnt:7: @assume trails code: put it on a line of its own, a // comment (// @assume name: what the model leaves out)",
		"s.qnt:8: @assume declares no assumption: give its name, a colon and what the model leaves out (// @assume unboundedQueue: the queue never fills; the real one caps at 10k), or name the val that states it (// @assume unboundedQueue = queueUnderCap: ...)",
		"s.qnt:9: @assume declares no assumption: give its name, a colon and what the model leaves out (// @assume unboundedQueue: the queue never fills; the real one caps at 10k), or name the val that states it (// @assume unboundedQueue = queueUnderCap: ...)",
		"s.qnt:10: @assume oneWriter is declared again (first at line 4): give each assumption its own name",
		"s.qnt:11: @assume positive has the name of model m's definition base::positive: rename the assumption, so that a directive naming it cannot mean a property (positiveAssumed, say)",
		"s.qnt:12: @assume wide = count: count is no boolean val that module base and model m see: name the val that states the assumption, or drop = count for an assumption in prose alone",
		"s.qnt:13: @assume twice = bounded: bounded states assumption small already (line 3): one val states one assumption",
		"s.qnt:14: @assume gone = nowhere: nowhere is no boolean val that module base and model m see: name the val that states the assumption, or drop = nowhere for an assumption in prose alone",
	}
	if !slices.Equal(problems, wantP) {
		t.Errorf("problems\n%s\nwant\n%s", strings.Join(problems, "\n"), strings.Join(wantP, "\n"))
	}
	// bounded states small: no property of the default table, and a
	// directive naming it says what it is
	if got := m.names(); !slices.Equal(got, []string{"positive", "settles", "own"}) {
		t.Errorf("properties %v, want bounded left out", got)
	}
	if _, why := m.resolve("bounded"); why != "bounded states assumption small of model m, which is no property: target the assumption with //quint:exercises small or //quint:monitors small (or select bounded with -props to table it as a property)" {
		t.Errorf("bounded: %q", why)
	}
	// a selection that names it keeps it
	if err := m.Only("-props", []string{"bounded", "positive"}); err != nil || !slices.Equal(m.names(), []string{"bounded", "positive"}) {
		t.Errorf("-props bounded: %v %v", err, m.names())
	}
	// q instantiates base qualified: it sees none of base's assumptions
	if q, _ := assumed(t, "q"); len(q.Assumptions) != 0 {
		t.Errorf("q's assumptions: %v", q.assumptionNames())
	}
	// no predicate: the properties are untouched
	m2 := read(t, "m")
	if p := m2.Assume([]byte("module base {\n  // @assume prose: only prose\n}\n"), "s.qnt"); len(p) != 0 || len(m2.Properties) != 4 {
		t.Errorf("prose alone: %v, %v", p, m2.names())
	}
}

// An assumption resolves bare, or qualified by its module, the main
// module or one that sees its module; a property's name, another
// model's module and a misspelling say what they are.
func TestResolveAssumption(t *testing.T) {
	m, _ := assumed(t, "m")
	for _, ok := range []string{"small", "m.small", "base.small", "base::oneWriter", "user.tight"} {
		if a, why := m.resolveAssumption(ok); a == nil {
			t.Errorf("%s: %s", ok, why)
		}
	}
	for name, want := range map[string]string{
		"smal":     "model m has no assumption smal",
		"positive": "positive is a property of model m, not an assumption: //quint:enforces and //quint:checks name properties, //quint:exercises and //quint:monitors what the model leaves out (its // @assume lines)",
		"q.small":  "module q does not see module base, which declares assumption small",
		"a-b":      "an assumption is named as its @assume line names it, or module.name",
	} {
		if a, why := m.resolveAssumption(name); a != nil || why != want {
			t.Errorf("%s: %v %q, want %q", name, a, why, want)
		}
	}
	if _, why := m.resolveAssumption("other.small"); why != otherModel {
		t.Errorf("other.small: %q", why)
	}
	// a property directive naming an assumption
	if _, why := m.resolve("oneWriter"); why != "oneWriter is an assumption of model m (what it leaves out), not a property: target it with //quint:exercises oneWriter or //quint:monitors oneWriter" {
		t.Errorf("oneWriter as a property: %q", why)
	}
}

// The table: a row per assumption in the source's order, its statement
// and predicate, the sites that exercise and monitor it; an error per
// name that is none, at its directive's line, with the fix; warnings of
// the kinds asked.
func TestAssumptionsTable(t *testing.T) {
	m, _ := assumed(t, "m")
	anns := []Annotation{
		{Kind: Exercises, Line: 3, Site: Site{Path: "q_test.go", Line: 4, Func: "TestFull"}, Names: []string{"small", "base.small"}},
		{Kind: Monitors, Line: 7, Site: Site{Path: "mon.go", Line: 8, Func: "Watch"}, Names: []string{"small", "oneWriter"}},
		{Kind: Exercises, Line: 9, Site: Site{Path: "q_test.go", Line: 10, Func: "TestX"}, Names: []string{"smal", "positive", "other.thing"}},
		{Kind: Enforces, Line: 11, Site: Site{Path: "e.go", Line: 12, Func: "E"}, Names: []string{"tight", "bounded"}},
	}
	table, errs := m.Table(anns)
	wantErrs := []string{
		"q_test.go:9: //quint:exercises names smal: model m has no assumption smal: did you mean small? name one of model m's assumptions (small, oneWriter, tight), or declare it in the model (// @assume smal: what the model leaves out)",
		"q_test.go:9: //quint:exercises names positive: positive is a property of model m, not an assumption: //quint:enforces and //quint:checks name properties, //quint:exercises and //quint:monitors what the model leaves out (its // @assume lines): name the assumption it goes past (small, oneWriter, tight), or use //quint:enforces or //quint:checks",
		"e.go:11: //quint:enforces names tight: tight is an assumption of model m (what it leaves out), not a property: target it with //quint:exercises tight or //quint:monitors tight",
		"e.go:11: //quint:enforces names bounded: bounded states assumption small of model m, which is no property: target the assumption with //quint:exercises small or //quint:monitors small (or select bounded with -props to table it as a property)",
	}
	if !slices.Equal(errs, wantErrs) {
		t.Errorf("errors\n%s\nwant\n%s", strings.Join(errs, "\n"), strings.Join(wantErrs, "\n"))
	}
	if table.Others["other"] != 1 {
		t.Errorf("another model's assumption: %v", table.Others)
	}
	want := "| assumption | statement | stated by | exercised by | monitored by |\n|---|---|---|---|---|\n" +
		"| `small` | x stays small; the real one is an int64 | `bounded` | `TestFull` [q_test.go:4](q_test.go#L4) | `Watch` [mon.go:8](mon.go#L8) |\n" +
		"| `oneWriter` | one writer at a time | — | — | `Watch` [mon.go:8](mon.go#L8) |\n" +
		"| `tight` | no space needed after the slashes | — | — | — |\n"
	if got := table.MarkdownAssumptions(""); got != want {
		t.Errorf("table\n%s\nwant\n%s", got, want)
	}
	if got := table.Missing("targeted"); !slices.Equal(got, []string{"tight"}) {
		t.Errorf("missing targeted: %v", got)
	}
	if got := table.Missing("exercised"); !slices.Equal(got, []string{"oneWriter", "tight"}) {
		t.Errorf("missing exercised: %v", got)
	}
	// the code qualifies names (base.small, another model's other.thing):
	// so do the fixes
	if w := table.Warnings("targeted"); !slices.Equal(w, []string{"assumption tight: nothing targets it: put //quint:exercises m.tight on a test or double that goes past it, or //quint:monitors m.tight on the production check that watches it"}) {
		t.Errorf("targeted warnings %q", w)
	}
	if w := table.Warnings("exercised", "monitored"); !slices.Equal(w, []string{
		"assumption oneWriter: nothing exercises it: put //quint:exercises m.oneWriter on a test, a double's fault or a generator that goes past it",
		"assumption tight: nothing exercises it: put //quint:exercises m.tight on a test, a double's fault or a generator that goes past it",
		"assumption tight: nothing monitors it: put //quint:monitors m.tight on the production check that watches it",
	}) {
		t.Errorf("exercised, monitored warnings %q", w)
	}
	if bare, _ := m.Table(anns[1:2]); !slices.Equal(bare.Warnings("targeted"), []string{"assumption tight: nothing targets it: put //quint:exercises tight on a test or double that goes past it, or //quint:monitors tight on the production check that watches it"}) {
		t.Errorf("bare targeted warnings %q", bare.Warnings("targeted"))
	}
	// prose alone: no predicate column
	m.Assumptions[0].Predicate = ""
	if got := table.MarkdownAssumptions(""); !strings.HasPrefix(got, "| assumption | statement | exercised by | monitored by |\n|---|---|---|---|\n| `small` | x stays small; the real one is an int64 | `TestFull`") {
		t.Errorf("no predicates:\n%s", got)
	}
}

// The directives are read as the property directives are, anywhere a
// declaration or a statement is: no test needed.
func TestScanAssumptionDirectives(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a/a.go": `package a

// Watch checks a production queue's depth.
//
//quint:monitors unboundedQueue
func Watch() {
	//quint:exercises unboundedQueue, oneKey
	fill()
}

func fill() {}
`,
	})
	anns, problems, err := Scan([]string{dir + "/a"})
	if err != nil || len(problems) != 0 {
		t.Fatal(err, problems)
	}
	var got []string
	for _, a := range anns {
		got = append(got, string(a.Kind)+" "+strings.Join(a.Names, "+")+" "+a.Site.Func)
	}
	if !slices.Equal(got, []string{"monitors unboundedQueue Watch", "exercises unboundedQueue+oneKey Watch"}) {
		t.Errorf("annotations %q", got)
	}
}

// A monitor is shown by what it watches with: its directive's
// label:VALUE (quoted when it has spaces), else the identifier the
// statement it annotates assigns; other directives take no label, and a
// malformed one is a problem with the fix. label::x is a name (module
// label's x), not a label.
func TestMonitorLabels(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a/a.go": `package a

//quint:monitors oneKey label:golem.control.spend.stale
func Watch() {
	//quint:monitors unboundedQueue
	spendStale := gauge()
	//quint:monitors unboundedQueue
	m.behind = gauge()
	//quint:monitors oneKey, unboundedQueue label:"Checkpoint tampered"
	gauge()
	//quint:monitors unboundedQueue
	var lag = gauge()
	//quint:monitors label::x
	_ = gauge()
	//quint:exercises unboundedQueue
	x := gauge()
	//quint:exercises oneKey label:x
	gauge()
	//quint:monitors oneKey label:a label:b
	gauge()
	//quint:monitors oneKey label:"open
	gauge()
	//quint:monitors oneKey label:
	gauge()
}
`,
	})
	anns, problems, err := Scan([]string{dir + "/a"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range anns {
		got = append(got, fmt.Sprintf("%s %s %d %q", a.Kind, strings.Join(a.Names, "+"), a.Site.Line, a.Site.Label))
	}
	want := []string{
		`monitors oneKey 4 "golem.control.spend.stale"`,
		`monitors unboundedQueue 6 "spendStale"`,
		`monitors unboundedQueue 8 "behind"`,
		`monitors oneKey+unboundedQueue 10 "Checkpoint tampered"`,
		`monitors unboundedQueue 12 "lag"`,
		`monitors label::x 14 ""`,
		`exercises unboundedQueue 16 ""`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("annotations\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var gotP []string
	for _, p := range problems {
		gotP = append(gotP, strings.TrimPrefix(filepath.ToSlash(p), filepath.ToSlash(dir)+"/"))
	}
	wantP := []string{
		"a/a.go:17: //quint:exercises gives label:x, and only //quint:monitors takes a label (the metric or alert that watches an assumption): drop it",
		"a/a.go:19: //quint:monitors gives two labels, label:a and label:b: give the one the check is known by (a site that reports two is two directives)",
		`a/a.go:21: //quint:monitors has a quote it does not close: close it (//quint:monitors name label:"the alert's name")`,
		"a/a.go:23: //quint:monitors gives an empty label: name the metric or alert after label: (label:golem.control.spend.stale), or drop it",
	}
	if !slices.Equal(gotP, wantP) {
		t.Errorf("problems\n%s\nwant\n%s", strings.Join(gotP, "\n"), strings.Join(wantP, "\n"))
	}
	// the cell: the label before the function, a pipe escaped
	tb := &Table{}
	if got := tb.siteCell([]Site{{Path: "a.go", Line: 4, Func: "Watch", Label: "spend|stale"}, {Path: "a.go", Line: 8, Func: "Watch"}}, ""); got != "`spend\\|stale` in `Watch` [a.go:4](a.go#L4)<br>`Watch` [a.go:8](a.go#L8)" {
		t.Errorf("cell %q", got)
	}
}

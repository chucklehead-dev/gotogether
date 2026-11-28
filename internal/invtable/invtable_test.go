package invtable

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// typecheck is the shape of quint typecheck's output this package reads
// (declarations by module, the type table by id), written by hand so
// that these tests start no quint: base declares the properties, m is
// its instance, q imports it qualified, lib exports one of base's, and
// user imports lib.
const typecheck = `{
  "modules": [
    {"name": "base", "declarations": [
      {"kind": "var", "name": "x", "id": 4},
      {"kind": "def", "name": "positive", "qualifier": "val", "id": 13},
      {"kind": "def", "name": "count", "qualifier": "val", "id": 17},
      {"kind": "def", "name": "k", "qualifier": "pureval", "id": 18},
      {"kind": "def", "name": "f", "qualifier": "def", "id": 19},
      {"kind": "def", "name": "settles", "qualifier": "temporal", "id": 20},
      {"kind": "def", "name": "bounded", "qualifier": "val", "id": 8, "doc": "x never exceeds N.\nMore lines."}
    ]},
    {"name": "m", "declarations": [
      {"kind": "instance", "protoName": "base", "id": 31},
      {"kind": "def", "name": "own", "qualifier": "val", "id": 40}
    ]},
    {"name": "q", "declarations": [
      {"kind": "instance", "protoName": "base", "qualifiedName": "b", "id": 35}
    ]},
    {"name": "lib", "declarations": [
      {"kind": "import", "protoName": "base", "defName": "count", "id": 50},
      {"kind": "export", "protoName": "base", "defName": "*", "id": 51}
    ]},
    {"name": "user", "declarations": [
      {"kind": "import", "protoName": "lib", "defName": "*", "id": 52}
    ]}
  ],
  "types": {
    "8": {"type": {"kind": "bool"}}, "13": {"type": {"kind": "bool"}}, "17": {"type": {"kind": "int"}},
    "18": {"type": {"kind": "bool"}}, "19": {"type": {"kind": "oper"}}, "20": {"type": {"kind": "bool"}},
    "40": {"type": {"kind": "bool"}}
  }
}`

func read(t *testing.T, main string) *Model {
	t.Helper()
	m, err := Read([]byte(typecheck), main, map[string]string{"base::positive": "x is never below zero.", "base::bounded": "not this: quint's doc wins"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// The properties are the boolean vals and temporals the main module sees
// unqualified (its own, and an instance's or a flattened import's,
// through exports), in the order quint numbered them; their meaning is
// the first line of quint's doc, else the source's comment.
func TestRead(t *testing.T) {
	m := read(t, "m")
	var got []string
	for _, p := range m.Properties {
		got = append(got, p.Module+"::"+p.Name+"="+p.Doc)
	}
	want := []string{"base::bounded=x never exceeds N.", "base::positive=x is never below zero.", "base::settles=", "m::own="}
	if !slices.Equal(got, want) {
		t.Errorf("properties %q, want %q", got, want)
	}
	if q := read(t, "q"); len(q.Properties) != 0 {
		t.Errorf("a qualified instance's vals are no properties of q: %v", q.names())
	}
	if u := read(t, "user"); !slices.Equal(u.names(), []string{"bounded", "positive", "settles"}) {
		t.Errorf("user sees base's through lib's export: %v", u.names())
	}
	if _, err := Read([]byte(typecheck), "nope", nil); err == nil || !strings.Contains(err.Error(), "the spec has no module nope: name its main module with -main (its modules: base, m, q, lib, user)") {
		t.Errorf("unknown main: %v", err)
	}
}

// A name resolves unqualified as the main module sees it, or qualified
// (with . or ::) by any module of the spec that sees it: the declaring
// module, the instance, the main module. Anything else says why.
func TestResolve(t *testing.T) {
	m := read(t, "m")
	for _, ok := range []string{"bounded", "m.bounded", "base.bounded", "base::bounded", "m::own", "lib.positive", "user.bounded"} {
		if p, why := m.resolve(ok); p == nil {
			t.Errorf("%s: %s", ok, why)
		}
	}
	for name, want := range map[string]string{
		"bounde":       "model m has no property bounde",
		"count":        "count is no boolean val of model m",
		"k":            "k is no boolean val of model m",
		"base.nothing": "module base has no definition nothing",
		"lib.count":    "count is no boolean val", // imported, not exported: lib sees it, m's count is not a property
		"a.b.c":        "a property is a val's name, or module.name",
		"limits-ok":    "a property is a val's name, or module.name",
	} {
		if p, why := m.resolve(name); p != nil || !strings.Contains(why, want) {
			t.Errorf("%s: %v %q, want %q", name, p, why, want)
		}
	}
	// a module the spec does not have: another model's, no error
	if p, why := m.resolve("other.x"); p != nil || why != otherModel {
		t.Errorf("other.x: %v %q", p, why)
	}
	// a module m does not see flattened: q's b is qualified
	u := read(t, "user")
	if _, why := u.resolve("m.own"); !strings.Contains(why, "model user does not see m's own") {
		t.Errorf("m.own from user: %q", why)
	}
	// a binding's properties: the others are named as unchecked by it
	if err := m.Restrict([]string{"positive", "bounded"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.names(), []string{"bounded", "positive"}) {
		t.Errorf("restricted: %v", m.names())
	}
	if _, why := m.resolve("own"); !strings.Contains(why, "own is a val of model m that this table leaves out (the binding does not check it, or -props, -invariant or -exclude left it out)") {
		t.Errorf("own after Restrict: %q", why)
	}
	if err := read(t, "m").Restrict([]string{"bounded", "count", "gone"}); err == nil || err.Error() != "the binding checks count, gone, which are no boolean val of module m: fix the binding (its properties: bounded, positive, settles, own)" {
		t.Errorf("Restrict: %v", err)
	}
}

// Comments are the // block directly above a val, by module, its
// lines kept for FirstSentence.
func TestComments(t *testing.T) {
	src := "module a {\n  // first\n  // second\n  val p = true\n\n  // detached\n\n  val q = true\n}\nmodule b {\n  // b's\n  temporal p = true\n}\n"
	got := Comments([]byte(src))
	if len(got) != 2 || got["a::p"] != "first\nsecond" || got["b::p"] != "b's" {
		t.Errorf("comments %q", got)
	}
}

// writeTree writes files (path: source) under a new directory.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Directives annotate a declaration by its doc comment, a statement by
// the line above it or by trailing it; a checks directive names the
// test it is in. Misplaced ones say where to put them.
func TestScan(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a/a.go": `package a

// L is a limiter.
//
//quint:enforces bounded
type L[T any] struct{ n int }

//quint:enforces positive,m.bounded
func (l *L[T]) Admit() {
	x := 1
	//quint:enforces settles
	if x > 0 {
		l.n++ //quint:enforces own
	}
	//quint:enforces bounded
}

//quint:checks bounded
func Helper() {}

//quint:enforces
func Nothing() {}

//quint:enforces bounded

var v = 1

//quint:enforcesX bounded
func NotADirective() {}
`,
		"a/a_test.go": `package a

import "testing"

//quint:checks bounded positive
func TestA(t *testing.T) {
	t.Run("sub", func(t *testing.T) {
		//quint:checks settles
		t.Log()
	})
}

//quint:checks bounded
func Testlower(t *testing.T) {}

//quint:checks own
func FuzzA(f *testing.F) {}
`,
		"a/testdata/t.go":     "package t\n//quint:enforces skipped\nfunc F() {}\n",
		"a/.hidden/h.go":      "package h\n//quint:enforces skipped\nfunc F() {}\n",
		"a/vendor/v.go":       "package v\n//quint:enforces skipped\nfunc F() {}\n",
		"a/_skip/s.go":        "package s\n//quint:enforces skipped\nfunc F() {}\n",
		"a/nested/go.mod":     "module nested\n",
		"a/nested/n.go":       "package n\n//quint:enforces skipped\nfunc F() {}\n",
		"a/sub/s.go":          "package s\n//quint:enforces bounded\nfunc F() {}\n",
		"a/sub/deeper/d.go":   "package d\n",
		"elsewhere/broken.go": "package broken\nfunc {",
	})
	anns, problems, err := Scan([]string{filepath.Join(dir, "a") + "/..."})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range anns {
		rel, _ := filepath.Rel(dir, a.Site.Path)
		got = append(got, string(a.Kind)+" "+strings.Join(a.Names, "+")+" @"+filepath.ToSlash(rel)+":"+strconv.Itoa(a.Site.Line)+" "+a.Site.Func)
	}
	want := []string{
		"enforces bounded @a/a.go:6 L",
		"enforces positive+m.bounded @a/a.go:9 L.Admit",
		"enforces settles @a/a.go:12 L.Admit",
		"enforces own @a/a.go:13 L.Admit",
		"checks bounded+positive @a/a_test.go:6 TestA",
		"checks settles @a/a_test.go:9 TestA/sub",
		"checks own @a/a_test.go:17 FuzzA",
		"enforces bounded @a/sub/s.go:3 F",
	}
	if !slices.Equal(got, want) {
		t.Errorf("annotations\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var gotP []string
	for _, p := range problems {
		gotP = append(gotP, strings.TrimPrefix(filepath.ToSlash(p), filepath.ToSlash(dir)+"/"))
	}
	wantP := []string{
		"a/a.go:15: //quint:enforces in L.Admit annotates no statement: put it on the line above the statement that does it, or on L.Admit's doc comment",
		"a/a.go:18: //quint:checks is in Helper, which is no test: put it on the Test or Fuzz function of a _test.go file that checks the property, or on a statement inside one",
		"a/a.go:21: //quint:enforces names no property: name the properties after it (//quint:enforces limitsHonoured)",
		"a/a.go:24: //quint:enforces annotates nothing: put it on a declaration's doc comment, or on the line above a statement in a function",
		"a/a_test.go:13: //quint:checks is in Testlower, which is no test: put it on the Test or Fuzz function of a _test.go file that checks the property, or on a statement inside one",
	}
	if !slices.Equal(gotP, wantP) {
		t.Errorf("problems\n%s\nwant\n%s", strings.Join(gotP, "\n"), strings.Join(wantP, "\n"))
	}
	// without /..., one directory
	if anns, _, err := Scan([]string{filepath.Join(dir, "a", "sub")}); err != nil || len(anns) != 1 {
		t.Errorf("one directory: %v, %v", anns, err)
	}
	if _, _, err := Scan([]string{filepath.Join(dir, "elsewhere")}); err == nil || !strings.Contains(err.Error(), "broken.go:2") || !strings.Contains(err.Error(), "fix the file, or leave its directory out") {
		t.Errorf("a file that does not parse: %v", err)
	}
	if _, _, err := Scan([]string{"github.com/x/y/..."}); err == nil || err.Error() != "github.com/x/y/... names no directory: give directories, or dir/... for a directory and those below it (import paths are not resolved)" {
		t.Errorf("an import path: %v", err)
	}
}

// The table: a row per property in the model's order, sites sorted and
// deduplicated, "—" where there are none, warnings for those, and an
// error per name that is no property, at its directive's line, with the
// model's properties and the nearest name.
func TestTable(t *testing.T) {
	m := read(t, "m")
	anns := []Annotation{
		{Kind: Enforces, Line: 9, Site: Site{Path: "b.go", Line: 10, Func: "Z"}, Names: []string{"bounded"}},
		{Kind: Enforces, Line: 2, Site: Site{Path: "a.go", Line: 3, Func: "Y"}, Names: []string{"base.bounded", "bounded"}},
		{Kind: Checks, Line: 4, Site: Site{Path: "a_test.go", Line: 5, Func: "TestY"}, Names: []string{"positive", "boundd", "nope"}},
	}
	table, errs := m.Table(anns)
	wantErrs := []string{
		"a_test.go:4: //quint:checks names boundd: model m has no property boundd: did you mean bounded? name one of model m's properties: bounded, positive, settles, own",
		"a_test.go:4: //quint:checks names nope: model m has no property nope: name one of model m's properties: bounded, positive, settles, own",
	}
	if !slices.Equal(errs, wantErrs) {
		t.Errorf("errors\n%s", strings.Join(errs, "\n"))
	}
	want := "| property | meaning | enforced by | checked by |\n|---|---|---|---|\n" +
		"| `bounded` | x never exceeds N. | `Y` [a.go:3](a.go#L3)<br>`Z` [b.go:10](b.go#L10) | — |\n" +
		"| `positive` | x is never below zero. | — | `TestY` [a_test.go:5](a_test.go#L5) |\n" +
		"| `settles` | | — | — |\n" +
		"| `own` | | — | — |\n"
	if got := table.Markdown(""); got != want {
		t.Errorf("table\n%s\nwant\n%s", got, want)
	}
	if got := table.Markdown("docs"); !strings.Contains(got, "[../a.go:3](../a.go#L3)") {
		t.Errorf("relative to docs:\n%s", got)
	}
	if w := table.Warnings("checked"); !slices.Equal(w, []string{
		"property bounded: no test checks it: put //quint:checks bounded on the test that does",
		"property settles: no test checks it: put //quint:checks settles on the test that does",
		"property own: no test checks it: put //quint:checks own on the test that does",
	}) {
		t.Errorf("checked warnings %q", w)
	}
	if w := table.Warnings(); len(w) != 6 {
		t.Errorf("all warnings %q", w)
	}
	// no doc anywhere: no meaning column
	for _, p := range m.Properties {
		p.Doc = ""
	}
	if got := table.Markdown(""); !strings.HasPrefix(got, "| property | enforced by | checked by |\n|---|---|---|\n| `bounded` | `Y`") {
		t.Errorf("no meanings:\n%s", got)
	}
}

// Changes name the rows that differ, and the header.
func TestChanges(t *testing.T) {
	h := "| property | enforced by | checked by |\n|---|---|---|\n| `a` | x | y |\n| `b` | x | y |\n| `c` | x | y |\n"
	if c := Changes(h, h); len(c) != 0 {
		t.Errorf("equal: %v", c)
	}
	w := "| property | meaning | enforced by | checked by |\n|---|---|---|---|\n| `a` | x | y |\n| `b` | x | z |\n| `d` | x | y |\n"
	if c := Changes(h, w); !slices.Equal(c, []string{"the header", "row b changed", "row d added", "row c removed"}) {
		t.Errorf("changes %q", c)
	}
}

// quint 0.34's named vals (qualifiers invariant and observation, as its
// typecheck writes them) are properties like a boolean val.
func TestNamedValsAreProperties(t *testing.T) {
	const tc = `{"modules": [{"name": "n", "declarations": [
	  {"kind": "var", "name": "x", "id": 1},
	  {"kind": "def", "name": "positive", "qualifier": "invariant", "id": 2},
	  {"kind": "def", "name": "reached", "qualifier": "observation", "id": 3},
	  {"kind": "def", "name": "count", "qualifier": "val", "id": 4}
	]}], "types": {"2": {"type": {"kind": "bool"}}, "3": {"type": {"kind": "bool"}}, "4": {"type": {"kind": "int"}}}}`
	m, err := Read([]byte(tc), "n", nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range m.Properties {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"positive", "reached"}) {
		t.Errorf("properties %v, want positive and reached", names)
	}
}

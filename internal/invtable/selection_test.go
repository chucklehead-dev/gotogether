package invtable

import (
	"slices"
	"strings"
	"testing"
)

// conjunctions is a model whose invariant is a conjunction of vals, one
// of which is a conjunction too and another a conjunction with a
// conjunct that is no val (x != 5), in quint typecheck's shape.
const conjunctions = `{
  "modules": [{"name": "c", "declarations": [
    {"kind": "def", "name": "a", "qualifier": "val", "id": 3},
    {"kind": "def", "name": "b", "qualifier": "val", "id": 6},
    {"kind": "def", "name": "c", "qualifier": "val", "id": 9},
    {"kind": "def", "name": "ab", "qualifier": "val", "id": 12,
     "expr": {"kind": "app", "opcode": "and", "args": [{"kind": "name", "name": "a"}, {"kind": "name", "name": "b"}]}},
    {"kind": "def", "name": "cx", "qualifier": "val", "id": 15,
     "expr": {"kind": "app", "opcode": "actionAll", "args": [{"kind": "name", "name": "c"}, {"kind": "app", "opcode": "neq", "args": []}]}},
    {"kind": "def", "name": "safe", "qualifier": "val", "id": 18,
     "expr": {"kind": "app", "opcode": "and", "args": [{"kind": "name", "name": "ab"}, {"kind": "name", "name": "cx"}, {"kind": "name", "name": "a"}]}},
    {"kind": "def", "name": "target", "qualifier": "val", "id": 21}
  ]}],
  "types": {"3": {"type": {"kind": "bool"}}, "6": {"type": {"kind": "bool"}}, "9": {"type": {"kind": "bool"}},
    "12": {"type": {"kind": "bool"}}, "15": {"type": {"kind": "bool"}}, "18": {"type": {"kind": "bool"}}, "21": {"type": {"kind": "bool"}}}
}`

func readConj(t *testing.T) *Model {
	t.Helper()
	m, err := Read([]byte(conjunctions), "c", nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// -invariant expands a val's conjuncts through conjunctions of vals,
// depth first and each once; a conjunction with a conjunct that is no
// val stays a row itself; -props takes names in its order; -exclude
// removes; a name that is no boolean val is refused with the nearest.
func TestSelection(t *testing.T) {
	m := readConj(t)
	got, err := m.Conjuncts([]string{"safe"})
	if err != nil || !slices.Equal(got, []string{"a", "b", "cx", "c"}) {
		t.Errorf("Conjuncts(safe): %v, %v", got, err)
	}
	if got, _ := m.Conjuncts([]string{"target"}); !slices.Equal(got, []string{"target"}) {
		t.Errorf("a val that is no conjunction: %v", got)
	}
	if err := m.Only("-invariant", got); err != nil || !slices.Equal(m.names(), []string{"a", "b", "cx", "c"}) {
		t.Errorf("Only: %v %v", m.names(), err)
	}
	m = readConj(t)
	if err := m.Only("-props", []string{"c", "a", "target"}); err != nil || !slices.Equal(m.names(), []string{"c", "a", "target"}) {
		t.Errorf("-props: %v %v", m.names(), err)
	}
	if err := m.Exclude([]string{"target", "b"}); err != nil || !slices.Equal(m.names(), []string{"c", "a"}) {
		t.Errorf("-exclude: %v %v", m.names(), err)
	}
	// a val the selection left out: named as such by an annotation
	if _, why := m.resolve("target"); !strings.Contains(why, "this table leaves out") {
		t.Errorf("target after -exclude: %q", why)
	}
	for _, c := range []struct {
		err  error
		want string
	}{
		{m.Only("-props", []string{"a", "targt"}), "-props names targt, which is no boolean val of model c: did you mean target? name one of its boolean vals (a, ab, b, c, cx, safe, target)"},
		{m.Exclude([]string{"nope"}), "-exclude names nope, which is no boolean val of model c: name one of"},
		{func() error { _, err := m.Conjuncts([]string{"saf"}); return err }(), "-invariant names saf, which is no boolean val of model c: did you mean safe?"},
	} {
		if c.err == nil || !strings.HasPrefix(c.err.Error(), c.want) {
			t.Errorf("%v, want %q", c.err, c.want)
		}
	}
}

// A meaning is the first sentence of the first paragraph, its lines
// joined.
func TestFirstSentence(t *testing.T) {
	for in, want := range map[string]string{
		"No attempt is lost, whatever\nrestores it. Said once more.": "No attempt is lost, whatever restores it.",
		"One line.":                         "One line.",
		"No period at all":                  "No period at all",
		"Version 1.2 holds. Then":           "Version 1.2 holds.",
		"First paragraph\n\nSecond. Third.": "First paragraph",
		"  \n":                              "",
	} {
		if got := FirstSentence(in); got != want {
			t.Errorf("FirstSentence(%q) = %q, want %q", in, got, want)
		}
	}
}

// Sites are qualified by package when the table spans packages, and
// always by name links, which link the file and list a function of a
// file once; a name qualified by a module the spec lacks is counted, not
// an error.
func TestSiteNames(t *testing.T) {
	m := readConj(t)
	if err := m.Only("-props", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	one := []Annotation{
		{Kind: Enforces, Line: 2, Site: Site{Path: "s/s.go", Line: 3, Func: "F", Pkg: "s"}, Names: []string{"a"}},
		{Kind: Enforces, Line: 8, Site: Site{Path: "s/s.go", Line: 9, Func: "F", Pkg: "s"}, Names: []string{"a"}},
		{Kind: Checks, Line: 4, Site: Site{Path: "s/s_test.go", Line: 5, Func: "TestF", Pkg: "s"}, Names: []string{"a", "other.x", "other::y", "elsewhere.z"}},
	}
	table, errs := m.Table(one)
	if len(errs) != 0 || table.Others["other"] != 2 || table.Others["elsewhere"] != 1 {
		t.Fatalf("errs %v, others %v", errs, table.Others)
	}
	if got := table.Markdown(""); !strings.Contains(got, "| `a` | `F` [s/s.go:3](s/s.go#L3)<br>`F` [s/s.go:9](s/s.go#L9) | `TestF` [s/s_test.go:5]") {
		t.Errorf("one package, bare names:\n%s", got)
	}
	table.ByName = true
	if got := table.Markdown(""); !strings.Contains(got, "| `a` | `s.F` [s/s.go](s/s.go) | `s.TestF` [s/s_test.go](s/s_test.go) |") {
		t.Errorf("by name:\n%s", got)
	}
	two := append(one, Annotation{Kind: Enforces, Line: 1, Site: Site{Path: "u/u.go", Line: 2, Func: "Call.ID", Pkg: "u"}, Names: []string{"b"}})
	table, _ = m.Table(two)
	if got := table.Markdown(""); !strings.Contains(got, "`s.F` [s/s.go:3](s/s.go#L3)") || !strings.Contains(got, "| `b` | `u.Call.ID` [u/u.go:2](u/u.go#L2) |") {
		t.Errorf("two packages, qualified:\n%s", got)
	}
}

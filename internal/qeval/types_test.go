package qeval

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// The IR's types, as connect's binding check reads them: a var's, an
// operator's parameters and result, the nondets a step chooses through
// the actions it applies, in Quint's syntax.
func TestTypes(t *testing.T) {
	m := compile(t, "types.qnt", "types", "init", "step")
	typeOf := func(name string) string {
		t.Helper()
		d, ok := m.Decl(name)
		if !ok {
			t.Fatalf("no %s", name)
		}
		ty, ok := m.TypeOf(d)
		if !ok {
			t.Fatalf("no type for %s", name)
		}
		return ty.String()
	}
	for name, want := range map[string]string{
		"s":       "Set[int]",
		"l":       "List[str]",
		"r":       "{ name: str, limit: int }",
		"o":       "Some(int) | None",
		"c":       "Open(str) | Close | Move({ a: int, b: int })",
		"p":       "(int, str)",
		"g":       "int -> Set[str]",
		"idOf":    "({ name: str, limit: int }) => (int, str)",
		"limits":  "int",
		"limitOf": "({ limit: _t",
	} {
		if got := typeOf(name); !strings.HasPrefix(got, want) {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
	d, _ := m.Decl("limitOf")
	if ty, _ := m.TypeOf(d); !ty.Args[0].Open {
		t.Errorf("limitOf's record is open (a row variable): %+v", ty.Args[0])
	}
	d, _ = m.Decl("o")
	if ty, _ := m.TypeOf(d); func() bool { some, ok := ty.Option(); return !ok || some.Kind != "int" }() {
		t.Errorf("o is an Option of int: %s", ty)
	}
	d, _ = m.Decl("c")
	if ty, _ := m.TypeOf(d); func() bool { _, ok := ty.Option(); return ok }() || !ty.Fields[1].Type.Nullary() || ty.Fields[0].Type.Nullary() {
		t.Errorf("c is no Option, Close nullary and Open not: %s", ty)
	}
	d, _ = m.Decl("act")
	var params []string
	for _, p := range m.Params(d) {
		params = append(params, p.Name+": "+p.Type.String())
	}
	if got, want := strings.Join(params, ", "), "x: { name: str, limit: int }, y: Open(str) | Close | Move({ a: int, b: int }), z: Some(int) | None, w: (int, str), v: List[str]"; got != want {
		t.Errorf("act's params: %s, want %s", got, want)
	}
	nd := m.Nondets("step")
	if got, want := slices.Sorted(maps.Keys(nd)), []string{"n", "v", "w", "x", "y", "z"}; !slices.Equal(got, want) {
		t.Errorf("step's nondets %v, want %v (grow's n among them)", got, want)
	}
	if got := nd["n"]; len(got) != 1 || got[0].String() != "int" {
		t.Errorf("n: %v", got)
	}
	if got := m.Actions(); !slices.Contains(got, "act") || !slices.Contains(got, "grow") || slices.Contains(got, "idOf") {
		t.Errorf("actions %v", got)
	}
}

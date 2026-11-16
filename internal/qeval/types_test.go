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
	if got, want := slices.Sorted(maps.Keys(nd)), []string{"cmd", "d", "gate", "k", "n", "outcome", "v", "w", "x", "y", "z"}; !slices.Equal(got, want) {
		t.Errorf("step's nondets %v, want %v (grow's n among them)", got, want)
	}
	if got := nd["n"]; len(got) != 1 || got[0].String() != "int" {
		t.Errorf("n: %v", got)
	}
	if got := m.Actions(); !slices.Contains(got, "act") || !slices.Contains(got, "grow") || slices.Contains(got, "idOf") {
		t.Errorf("actions %v", got)
	}

	// each action's own picks: its call's arguments (attempt's through
	// a let and a sum: d and k), the guards it is under (failed's gate
	// and k, k shared with attempt), the nondets of its body (grow's n),
	// and the whole model's where a match hides the call (opened,
	// closed, and keepWith, which they apply too)
	own, unsure := m.ActionPicks("step")
	for action, want := range map[string]string{
		"act":     "v w x y z",
		"grow":    "n",
		"attempt": "d k",
		"failed":  "gate k outcome",
	} {
		if got := strings.Join(slices.Sorted(maps.Keys(own[action])), " "); got != want || unsure[action] {
			t.Errorf("%s's picks: %s (unsure %v), want %s", action, got, unsure[action], want)
		}
	}
	if got := own["attempt"]["d"]; len(got) != 1 || got[0].String() != "int" {
		t.Errorf("attempt's d: %v", got)
	}
	for _, action := range []string{"opened", "closed", "keepWith", "init", "step"} {
		if !unsure[action] || len(own[action]) != len(nd) {
			t.Errorf("%s: unsure %v with %d picks, want the whole model's %d", action, unsure[action], len(own[action]), len(nd))
		}
	}
	if own, _ := m.ActionPicks("nothing"); own != nil {
		t.Errorf("a step the model lacks: %v", own)
	}
}

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
		if got := strings.Join(slices.Sorted(maps.Keys(own[action])), " "); got != want || unsure[action] != "" {
			t.Errorf("%s's picks: %s (unsure: %q), want %s", action, got, unsure[action], want)
		}
	}
	if got := own["attempt"]["d"]; len(got) != 1 || got[0].String() != "int" {
		t.Errorf("attempt's d: %v", got)
	}
	for action, why := range map[string]string{
		"opened":   "applied under matchVariant, which the attribution does not follow",
		"closed":   "applied under matchVariant, which the attribution does not follow",
		"keepWith": "applied under matchVariant, which the attribution does not follow",
		"init":     "step does not apply it",
	} {
		if unsure[action] != why || len(own[action]) != len(nd) {
			t.Errorf("%s: unsure %q with %d picks, want %q and the whole model's %d", action, unsure[action], len(own[action]), why, len(nd))
		}
	}
	if own, _ := m.ActionPicks("nothing"); own != nil {
		t.Errorf("a step the model lacks: %v", own)
	}
}

// An instance module's Main (module gateway { import attempts(CALLS =
// ...).* }, the shape the skills recommend for connect) attributes as the
// module itself does: quint aliases step (q::step = its step), which the
// walk applies as an action, and every call once took all of its
// nondets, silently (golem, #144).
func TestActionPicksUnderAnInstance(t *testing.T) {
	m := compile(t, "picks_instance.qnt", "gateway", "init", "step")
	own, unsure := m.ActionPicks("step")
	for action, want := range map[string]string{
		"start":   "c",
		"attempt": "c d",
		"failed":  "c outcome",
	} {
		if got := strings.Join(slices.Sorted(maps.Keys(own[action])), " "); got != want || unsure[action] != "" {
			t.Errorf("%s's picks: %s (unsure: %q), want %s", action, got, unsure[action], want)
		}
	}
}

// An attribution that gives every call all of the nondets is reported
// (suspicious), not passed off: step's own entry (an instance's alias
// applies it) aside, and not when the calls' sets differ or one nondet
// is all there is.
func TestActionPicksSuspicious(t *testing.T) {
	all := nondetSet{1: true, 2: true, 3: true}
	for _, c := range []struct {
		name   string
		own    map[string]nondetSet
		unsure map[string]string
		want   bool
	}{
		{"every call all", map[string]nondetSet{"a": all, "b": all, "step": all}, nil, true},
		{"the calls differ", map[string]nondetSet{"a": {1: true}, "b": all, "step": all}, nil, false},
		{"one call", map[string]nondetSet{"a": all, "step": all}, nil, false},
		{"one nondet", map[string]nondetSet{"a": {1: true}, "b": {1: true}}, nil, false},
		{"an unsure call aside", map[string]nondetSet{"a": {1: true}, "b": all, "c": all}, map[string]string{"a": "x"}, true},
	} {
		if got := suspicious(c.own, c.unsure, "step"); got != c.want {
			t.Errorf("%s: suspicious %v, want %v", c.name, got, c.want)
		}
	}
}

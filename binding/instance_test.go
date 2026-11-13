package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBinding writes a binding over testdata/instance/design.qnt naming
// module, with the extra YAML lines given.
func writeBinding(t *testing.T, module string, extra ...string) string {
	t.Helper()
	spec, err := filepath.Abs("testdata/instance/design.qnt")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "b.yaml")
	text := "spec: " + spec + "\nmodule: " + module + "\n" + strings.Join(extra, "\n") + "\n"
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A binding that names an instance module binds the module it imports,
// with the instance's constants: the binding copies none of them (golem's
// first use had to copy ten, and they would drift from the design).
func TestLoadBindsAnInstanceModulesImport(t *testing.T) {
	for _, tc := range []struct{ module, overshoot string }{{"design", "false"}, {"overshoot", "true"}} {
		b, err := Load(writeBinding(t, tc.module, "invariants: [withinLimit]"))
		if err != nil {
			t.Fatal(err)
		}
		if b.Module != "counter" || len(b.Constants) != 2 || b.Constants["LIMIT"] != "3" || b.Constants["OVERSHOOT"] != tc.overshoot {
			t.Errorf("%s: bound %s with %v; want counter with LIMIT = 3, OVERSHOOT = %s", tc.module, b.Module, b.Constants, tc.overshoot)
		}
	}
}

// A module with anything beside its import is a module of its own (its
// definitions would be lost), and the base module is bound as it is.
func TestLoadLeavesOrdinaryModules(t *testing.T) {
	for _, m := range []string{"extended", "counter"} {
		b, err := Load(writeBinding(t, m, "constants: { LIMIT: '3', OVERSHOOT: 'false' }"))
		if err != nil {
			t.Fatal(err)
		}
		if b.Module != m || len(b.Constants) != 2 {
			t.Errorf("%s: bound %s with %v", m, b.Module, b.Constants)
		}
	}
}

// A constant the design sets and the binding sets too is refused, naming
// both values and the two fixes.
func TestLoadRefusesAConstantSetTwice(t *testing.T) {
	_, err := Load(writeBinding(t, "design", "constants: { LIMIT: '4' }"))
	if err == nil || !strings.Contains(err.Error(), "constant LIMIT is set by instance module design (3) and by the binding (4): remove it from the binding, or name module counter and give every constant") {
		t.Fatalf("got %v", err)
	}
}

func TestInstanceModuleForms(t *testing.T) {
	for _, tc := range []struct {
		src, module, base string
		consts            []string
		ok                bool
	}{
		{"module d { import m.* }", "d", "m", nil, true},
		{"module d {\n  import m(A = Set(1, 2), B = \"x, y\").*\n}", "d", "m", []string{"A = Set(1, 2)", `B = "x, y"`}, true},
		{"module d { import m(A = 1) .* // the design\n}", "d", "m", []string{"A = 1"}, true},
		{"module d { import m(A = 1).* val v = 1 }", "d", "", nil, false},
		{"module d { import m(A = 1) as M }", "d", "", nil, false},
		{"module d { import m.* import n.* }", "d", "", nil, false},
		{"module dd { import m.* }", "d", "", nil, false},
	} {
		base, consts, ok := instanceModule(tc.src, tc.module)
		if ok != tc.ok || base != tc.base || strings.Join(consts, "|") != strings.Join(tc.consts, "|") {
			t.Errorf("%q: got %q %q %v; want %q %q %v", tc.src, base, consts, ok, tc.base, tc.consts, tc.ok)
		}
	}
}

// A constant's expression is kept as written: only the space around it is
// trimmed, never what a string literal holds (a review of #191 found
// LABEL = "two  spaces" loaded as "two spaces").
func TestInstanceConstantsKeepLiterals(t *testing.T) {
	src := "module d {\n  import m(LABEL = \"two  spaces\",\n    SET = Set(1,   2)).*\n}"
	base, consts, ok := instanceModule(src, "d")
	if !ok || base != "m" || len(consts) != 2 || consts[0] != `LABEL = "two  spaces"` || consts[1] != "SET = Set(1,   2)" {
		t.Fatalf("got %q %q %v", base, consts, ok)
	}
	dir := t.TempDir()
	spec := filepath.Join(dir, "m.qnt")
	if err := os.WriteFile(spec, []byte("module m {\n  const LABEL: str\n  var x: int\n  action init = x' = 0\n  action step = x' = x\n}\n"+src+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "b.yaml")
	if err := os.WriteFile(p, []byte("spec: m.qnt\nmodule: d\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if b.Constants["LABEL"] != `"two  spaces"` {
		t.Fatalf("LABEL = %q", b.Constants["LABEL"])
	}
}

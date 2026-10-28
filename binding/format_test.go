package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A binding may name its format; one of a later major is refused with
// the upgrade before its new keys are found unknown, and an unknown key
// in a binding naming none says it may be a later version's.
func TestBindingFormat(t *testing.T) {
	dir := t.TempDir()
	load := func(text string) error {
		p := filepath.Join(dir, "b.yaml")
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		return err
	}
	if err := load("format: " + FormatName + "\nspec: m.qnt\nmodule: m\n"); err != nil {
		t.Errorf("this version's format: %v", err)
	}
	if err := load("format: gotogether.binding/3\nspec: m.qnt\nmodule: m\nsearch: {states: 10}\n"); err == nil || !strings.Contains(err.Error(), "gotogether.binding/3 is a later version than this gotogether reads (gotogether.binding/1 to gotogether.binding/2): upgrade gotogether") {
		t.Errorf("a later major: %v", err)
	}
	if err := load("spec: m.qnt\nmodule: m\nsearch: {states: 10}\n"); err == nil || !strings.Contains(err.Error(), "field search not found") || !strings.Contains(err.Error(), "a key a later version added") {
		t.Errorf("an unknown key: %v", err)
	}
	if err := load("format: gotogether.convert/1\nspec: m.qnt\nmodule: m\n"); err == nil || !strings.Contains(err.Error(), "is no binding YAML") {
		t.Errorf("another format: %v", err)
	}
}

// binding/2 has one search bound, simulate.search; binding/1's three
// (searchStates for the possibles, graphStates and graphDepth for the
// graph) are read into it forever, each refused in the major without it
// with the fix, and the section is read as strictly as the rest.
func TestSimulateSearchByMajor(t *testing.T) {
	dir := t.TempDir()
	load := func(text string) (*Binding, error) {
		p := filepath.Join(dir, "b.yaml")
		if err := os.WriteFile(p, []byte("spec: m.qnt\nmodule: m\n"+text), 0o644); err != nil {
			t.Fatal(err)
		}
		return Load(p)
	}
	b, err := load("format: gotogether.binding/2\nsimulate: {search: {states: 500, depth: 6}, holds: [a]}\n")
	if err != nil || b.Simulate.Search != (SearchBound{States: 500, Depth: 6}) || len(b.Simulate.Holds) != 1 {
		t.Errorf("binding/2's search: %+v, %v", b, err)
	}
	for _, v1 := range []string{"", "format: gotogether.binding/1\n"} {
		b, err = load(v1 + "simulate: {searchStates: 2000, graphStates: 50000, graphDepth: 8}\n")
		if err != nil || b.Simulate.Search != (SearchBound{States: 50000, Depth: 8}) {
			t.Errorf("binding/1's bounds %q: %+v, %v", v1, b, err)
		}
		if _, err := load(v1 + "simulate: {search: {states: 5}}\n"); err == nil || !strings.Contains(err.Error(), "simulate.search is gotogether.binding/2's") || !strings.Contains(err.Error(), "add format: gotogether.binding/2") {
			t.Errorf("search in binding/1 %q: %v", v1, err)
		}
	}
	if _, err := load("format: gotogether.binding/2\nsimulate: {graphDepth: 8}\n"); err == nil || !strings.Contains(err.Error(), "simulate.graphDepth is gotogether.binding/1's") || !strings.Contains(err.Error(), "search: {states: N, depth: D}") {
		t.Errorf("graphDepth in binding/2: %v", err)
	}
	if _, err := load("format: gotogether.binding/2\nsimulate: {stepz: 3}\n"); err == nil || !strings.Contains(err.Error(), "field stepz not found in type binding.Simulation") {
		t.Errorf("an unknown key of the section: %v", err)
	}
}

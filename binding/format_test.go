package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A binding may name its format; one of a later major is refused with
// the upgrade before its new keys are found unknown, and an unknown key
// in a binding naming none says it may be another version's.
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
	if err := load("format: gotogether.binding/3\nspec: m.qnt\nmodule: m\nsearch: {states: 10}\n"); err == nil || !strings.Contains(err.Error(), "gotogether.binding/3 is a later version than this gotogether reads (gotogether.binding/2): upgrade gotogether") {
		t.Errorf("a later major: %v", err)
	}
	if err := load("spec: m.qnt\nmodule: m\nsearch: {states: 10}\n"); err == nil || !strings.Contains(err.Error(), "field search not found") || !strings.Contains(err.Error(), "a key another version of the format has") {
		t.Errorf("an unknown key: %v", err)
	}
	if err := load("format: gotogether.convert/1\nspec: m.qnt\nmodule: m\n"); err == nil || !strings.Contains(err.Error(), "is no binding YAML") {
		t.Errorf("another format: %v", err)
	}
}

// A binding reads its own major only, pre-release: binding/2's one
// search bound reads; binding/1, named, is refused with how to write it
// anew, and its bound keys are unknown, as any key not the current
// format's; the section is read as strictly as the rest.
func TestSimulateSearch(t *testing.T) {
	dir := t.TempDir()
	load := func(text string) (*Binding, error) {
		p := filepath.Join(dir, "b.yaml")
		if err := os.WriteFile(p, []byte("spec: m.qnt\nmodule: m\n"+text), 0o644); err != nil {
			t.Fatal(err)
		}
		return Load(p)
	}
	for _, head := range []string{"", "format: gotogether.binding/2\n"} {
		b, err := load(head + "simulate: {search: {states: 500, depth: 6}, holds: [a]}\n")
		if err != nil || b.Simulate.Search != (SearchBound{States: 500, Depth: 6}) || len(b.Simulate.Holds) != 1 {
			t.Errorf("search %q: %+v, %v", head, b, err)
		}
	}
	if _, err := load("format: gotogether.binding/1\nsimulate: {searchStates: 5}\n"); err == nil || !strings.Contains(err.Error(), "gotogether.binding/1 is an older version than this gotogether reads (gotogether.binding/2") || !strings.Contains(err.Error(), "search: {states: N, depth: D}") {
		t.Errorf("binding/1: %v", err)
	}
	if _, err := load("simulate: {graphDepth: 8}\n"); err == nil || !strings.Contains(err.Error(), "field graphDepth not found in type binding.Simulation") {
		t.Errorf("binding/1's key: %v", err)
	}
	if _, err := load("simulate: {stepz: 3}\n"); err == nil || !strings.Contains(err.Error(), "field stepz not found in type binding.Simulation") {
		t.Errorf("an unknown key of the section: %v", err)
	}
}

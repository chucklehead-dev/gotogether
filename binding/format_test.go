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
	if err := load("format: gotogether.binding/2\nspec: m.qnt\nmodule: m\nsearch: {states: 10}\n"); err == nil || !strings.Contains(err.Error(), "gotogether.binding/2 is a later version than this gotogether reads (gotogether.binding/1): upgrade gotogether") {
		t.Errorf("a later major: %v", err)
	}
	if err := load("spec: m.qnt\nmodule: m\nsearch: {states: 10}\n"); err == nil || !strings.Contains(err.Error(), "field search not found") || !strings.Contains(err.Error(), "a key a later version added") {
		t.Errorf("an unknown key: %v", err)
	}
	if err := load("format: gotogether.convert/1\nspec: m.qnt\nmodule: m\n"); err == nil || !strings.Contains(err.Error(), "is no binding YAML") {
		t.Errorf("another format: %v", err)
	}
}

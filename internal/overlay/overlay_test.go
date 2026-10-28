package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Of builds the overlay of a mutant with a directory, none for a
// generated mutant or no mutant, and Mutant leaves qmut's values out.
func TestOf(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "testdata", "mutants", "m"), 0o755)
	os.WriteFile(filepath.Join(dir, "testdata", "mutants", "m", "a.go"), []byte("package a // m\n"), 0o644)
	p, err := Of(dir, "m", t.TempDir())
	if b, _ := os.ReadFile(p); err != nil || !strings.Contains(string(b), filepath.Join("mutants", "m", "a.go")) {
		t.Errorf("Of(m) = %q (%s), %v", p, b, err)
	}
	for _, name := range []string{"", "drop-rule"} {
		if p, err := Of(dir, name, t.TempDir()); p != "" || err != nil {
			t.Errorf("Of(%q) = %q, %v: want no overlay", name, p, err)
		}
	}
	for v, want := range map[string]string{"m": "m", "12": "", "list": "", "": ""} {
		t.Setenv(Env, v)
		if got := Mutant(); got != want {
			t.Errorf("%s=%q: Mutant() %q, want %q", Env, v, got, want)
		}
	}
}

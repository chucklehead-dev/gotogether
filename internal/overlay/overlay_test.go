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
	os.WriteFile(filepath.Join(dir, "testdata", "mutants", "m", "a.go"), []byte("package a\n\nconst m = 1\n"), 0o644)
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

// A mutant whose files are the package's but for comments and layout
// changes nothing, and is an error: its claims would be judged on the
// faithful program. A changed directive is a change.
func TestWriteRefusesNoChange(t *testing.T) {
	pkg := t.TempDir()
	orig := "package a\n\n// F is f.\nfunc F() int { return 1 }\n"
	os.WriteFile(filepath.Join(pkg, "a.go"), []byte(orig), 0o644)
	os.WriteFile(filepath.Join(pkg, "b.go"), []byte("package a\n"), 0o644)
	for name, c := range map[string]struct {
		files map[string]string
		ok    bool
	}{
		"same":      {map[string]string{"a.go": orig}, false},
		"comments":  {map[string]string{"a.go": "package a\n\n// F, mutated (it is not).\nfunc F() int {\n\treturn 1 // one\n}\n"}, false},
		"twoSame":   {map[string]string{"a.go": orig, "b.go": "package a // b\n"}, false},
		"changed":   {map[string]string{"a.go": "package a\n\n// F is f.\nfunc F() int { return 2 }\n"}, true},
		"oneOfTwo":  {map[string]string{"a.go": orig, "b.go": "package a\n\nvar B = 1\n"}, true},
		"directive": {map[string]string{"b.go": "//go:build ignore\n\npackage a\n"}, true},
		"unparsed":  {map[string]string{"b.go": "package a\n\nfunc {\n"}, true},
	} {
		dir := filepath.Join(t.TempDir(), name)
		os.MkdirAll(dir, 0o755)
		for f, src := range c.files {
			os.WriteFile(filepath.Join(dir, f), []byte(src), 0o644)
		}
		_, err := Write(pkg, dir, t.TempDir())
		if c.ok && err != nil {
			t.Errorf("%s: %v, want an overlay", name, err)
		}
		if !c.ok && (err == nil || !strings.Contains(err.Error(), "mutant "+name+" changes nothing")) {
			t.Errorf("%s: %v, want it to change nothing", name, err)
		}
	}
}

// With PackageEnv set, Of overlays that directory's files (a harness
// package's mutants of another package: payd's, webjobs'), so the
// navigators build a Package harness's mutant as Mutants.Run does.
func TestOfPackage(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "lib"), 0o755)
	os.WriteFile(filepath.Join(dir, "lib", "a.go"), []byte("package lib\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "testdata", "mutants", "m"), 0o755)
	os.WriteFile(filepath.Join(dir, "testdata", "mutants", "m", "a.go"), []byte("package lib\n\nconst m = 1\n"), 0o644)
	t.Setenv(PackageEnv, "")
	if _, err := Of(dir, "m", t.TempDir()); err == nil || !strings.Contains(err.Error(), "replaces no file of the package") {
		t.Errorf("Of(m) without %s: %v, want the harness's own files missing", PackageEnv, err)
	}
	t.Setenv(PackageEnv, "lib")
	p, err := Of(dir, "m", t.TempDir())
	if b, _ := os.ReadFile(p); err != nil || !strings.Contains(string(b), filepath.Join(dir, "lib", "a.go")) {
		t.Errorf("Of(m) with %s=lib: %q (%s), %v", PackageEnv, p, b, err)
	}
}

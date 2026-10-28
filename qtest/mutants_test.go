package qtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Mutant is MutantEnv's value, but for qmut's numbers and list.
func TestMutantIgnoresQmut(t *testing.T) {
	for v, want := range map[string]string{"": "", "noDedupe": "noDedupe", "3": "", "list": "", "drop-limit": "drop-limit"} {
		t.Setenv(MutantEnv, v)
		if got := Mutant(); got != want {
			t.Errorf("%s=%q: Mutant() = %q, want %q", MutantEnv, v, got, want)
		}
	}
}

// writeFiles writes files (path -> content) under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		p = filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Names finds the mutants from Dir's directories, with the generated
// ones; Overlay maps a mutant's files onto Package's, and fails on one
// that replaces none.
func TestMutantsNamesAndOverlay(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"pkg/a.go":                     "package pkg\n",
		"testdata/mutants/b/a.go":      "package pkg\n\nconst b = 1\n",
		"testdata/mutants/a/a.go":      "package pkg\n\nconst a = 1\n",
		"testdata/mutants/same/a.go":   "package pkg // a copy\n",
		"testdata/mutants/stray/z.go":  "package pkg\n",
		"testdata/mutants/notes.txt":   "not a mutant",
		"testdata/mutants/empty/x.txt": "",
	})
	t.Chdir(dir)
	m := Mutants{Package: "pkg", Generated: []string{"drop-x"}}
	if got := m.Names(t); !slices.Equal(got, []string{"a", "b", "empty", "same", "stray", "drop-x"}) {
		t.Errorf("Names = %v", got)
	}
	b, err := os.ReadFile(m.Overlay(t, "a"))
	if err != nil || !strings.Contains(string(b), filepath.Join(dir, "pkg", "a.go")) || !strings.Contains(string(b), filepath.Join(dir, "testdata", "mutants", "a", "a.go")) {
		t.Errorf("overlay %s (%v)", b, err)
	}
	for name, want := range map[string]string{"stray": "replaces no file of the package", "empty": "has no files", "same": "mutant same changes nothing"} {
		f := &failTB{TB: t}
		done := make(chan struct{})
		go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
			defer close(done)
			m.Overlay(f, name)
		}()
		<-done
		if len(f.errs) != 1 || !strings.Contains(f.errs[0], want) {
			t.Errorf("Overlay(%s): %q, want %q", name, f.errs, want)
		}
	}
	for _, c := range []struct {
		m    Mutants
		want string
	}{
		{Mutants{Dir: "none"}, "Mutants.Dir none"},
		{Mutants{Dir: "none", Generated: []string{"drop-x"}}, "Mutants.Dir none"},
		{Mutants{Package: "pkg"}, ""}, // the default Dir, which exists: no failure
		{Mutants{Generated: []string{"drop x"}}, "a name of letters, digits"},
		{Mutants{Generated: []string{"a"}}, "both generated and a directory"},
		{Mutants{Generated: []string{"7"}}, "qmut reads as its own"},
		{Mutants{FullOnly: []string{"c"}}, "FullOnly names c"},
	} {
		f := &failTB{TB: t}
		done := make(chan struct{})
		go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
			defer close(done)
			c.m.Names(f)
		}()
		<-done
		if c.want == "" && len(f.errs) != 0 || c.want != "" && (len(f.errs) != 1 || !strings.Contains(f.errs[0], c.want)) {
			t.Errorf("%+v: %q, want %q", c.m, f.errs, c.want)
		}
	}
}

// Run builds and runs each mutant's binary with its overlay, MutantEnv
// naming it: a module of the standard library alone, whose TestMutant
// fails unless the overlay's constant is the mutant's name.
func TestMutantsRun(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	dir := t.TempDir()
	test := `package m

import (
	"os"
	"testing"
)

func TestMutant(t *testing.T) {
	m := os.Getenv("GOTOGETHER_MUTANT")
	if m == "" {
		t.Skip()
	}
	if planted != m {
		t.Fatalf("mutant %s, planted %q", m, planted)
	}
}
`
	writeFiles(t, dir, map[string]string{
		"go.mod":                    "module m\n\ngo 1.21\n",
		"m.go":                      "package m\n\nconst planted = \"\"\n",
		"m_test.go":                 test,
		"testdata/mutants/one/m.go": "package m\n\nconst planted = \"one\"\n",
		"testdata/mutants/two/m.go": "package m\n\nconst planted = \"two\"\n",
	})
	t.Chdir(dir)
	t.Setenv(MutantEnv, "")
	t.Setenv("GOWORK", "off")
	Mutants{}.Run(t)
}

// A harness package's mutants of another package (Package) replace that
// package's files, and the mutant's binary is told the package
// (GOTOGETHER_MUTANT_PACKAGE), which its navigate line passes to the
// navigator.
func TestMutantsRunPackage(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	dir := t.TempDir()
	test := `package harness

import (
	"os"
	"testing"

	"m/lib"
)

func TestMutant(t *testing.T) {
	m := os.Getenv("GOTOGETHER_MUTANT")
	if m == "" {
		t.Skip()
	}
	if lib.Planted != m || os.Getenv("GOTOGETHER_MUTANT_PACKAGE") != "lib" {
		t.Fatalf("mutant %s, planted %q, package %q", m, lib.Planted, os.Getenv("GOTOGETHER_MUTANT_PACKAGE"))
	}
}
`
	writeFiles(t, dir, map[string]string{
		"go.mod":                      "module m\n\ngo 1.21\n",
		"lib/lib.go":                  "package lib\n\nconst Planted = \"\"\n",
		"harness_test.go":             test,
		"testdata/mutants/one/lib.go": "package lib\n\nconst Planted = \"one\"\n",
	})
	t.Chdir(dir)
	t.Setenv(MutantEnv, "")
	t.Setenv("GOWORK", "off")
	Mutants{Package: "lib"}.Run(t)
}

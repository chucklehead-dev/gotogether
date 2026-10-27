// Package model_test checks grist.qnt, grist's guarantees as a Quint
// module, in grist's own tests: it typechecks, its defs hold on small
// values (testdata/gristlib.qnt's quint tests), and a small machine over
// them keeps its invariants (quint run), so an application's failure is
// its own or its model's, not the library's.
package model_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// requireQuint skips without quint on PATH, or fails under
// GOTOGETHER_STRICT=1, as qtest.RequireQuint does (qtest is not imported:
// it would bring the validator's dependencies into grist's module).
func requireQuint(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("quint"); err != nil {
		if os.Getenv("GOTOGETHER_STRICT") == "1" {
			t.Fatal("quint is not on PATH (GOTOGETHER_STRICT=1 makes this a failure): npm i -g @informalsystems/quint")
		}
		t.Skip("quint is not on PATH: npm i -g @informalsystems/quint")
	}
}

// quint runs quint with args in dir, failing the test with its output.
func quint(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("quint", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("quint %v: %v\n%s", args, err, out)
	}
}

func TestGristModel(t *testing.T) {
	requireQuint(t)
	here, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	quint(t, here, "typecheck", filepath.Join(here, "grist.qnt"))
	lib := filepath.Join(here, "testdata", "gristlib.qnt")
	quint(t, filepath.Dir(lib), "test", "--main=gristlib", lib)
	quint(t, filepath.Dir(lib), "run", "--main=gristrun", "--max-samples=200", "--max-steps=12", "--seed=7",
		"--invariant=unique and effected", lib)
}

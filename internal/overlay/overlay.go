// Package overlay writes a hand-planted mutant's go build overlay: the
// .go files of a directory of testdata/mutants in place of the package's
// of the same names. qtest.Mutants builds each mutant's tests with one,
// and the navigators (gotogether nav, qnav's sessions) build a mutant's
// test binary with one when GOTOGETHER_MUTANT names it, so a failing
// run's navigate line works in the mutant it came from.
package overlay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Env is the environment variable naming the mutant a test binary runs
// as (qtest.MutantEnv). qmut reads it for a woven mutant's number, or
// list.
const Env = "GOTOGETHER_MUTANT"

// TestEnv is the -run pattern that rebuilds and runs the mutant a binary
// runs as from its harness ("^TestMutants$/^noDedupe$"), set by
// qtest.Mutants.Run: what a failing run's reproduce line runs.
const TestEnv = "GOTOGETHER_MUTANT_TEST"

// Dir is where a package's mutants are, relative to it (slash-separated).
const Dir = "testdata/mutants"

// Mutant is the mutant Env names ("" for none), but for qmut's values (a
// number, or list), which name a woven mutant, not an overlay's.
func Mutant() string {
	m := os.Getenv(Env)
	if _, err := strconv.Atoi(m); err == nil || m == "list" {
		return ""
	}
	return m
}

// Write writes into outDir the overlay of the mutant whose files are in
// mutantDir, each replacing pkgDir's file of its name, and returns its
// path. A mutant with no .go files, or a file that replaces none of the
// package's, is an error naming it.
func Write(pkgDir, mutantDir, outDir string) (string, error) {
	pkgDir, err := filepath.Abs(pkgDir)
	if err != nil {
		return "", err
	}
	mutantDir, err = filepath.Abs(mutantDir)
	if err != nil {
		return "", err
	}
	files, err := filepath.Glob(filepath.Join(mutantDir, "*.go"))
	if err != nil || len(files) == 0 {
		return "", fmt.Errorf("mutant %s has no files in %s", filepath.Base(mutantDir), mutantDir)
	}
	replace := map[string]string{}
	for _, f := range files {
		target := filepath.Join(pkgDir, filepath.Base(f))
		if _, err := os.Stat(target); err != nil {
			return "", fmt.Errorf("%s replaces no file of the package: %v", f, err)
		}
		replace[target] = f
	}
	b, _ := json.Marshal(map[string]any{"Replace": replace})
	path := filepath.Join(outDir, "overlay.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Of is the overlay of mutant name of the package in pkgDir (its
// directory of Dir), written into outDir: "" with no error when name is
// "" or the package has no such directory (a generated mutant, which its
// binary makes of itself).
func Of(pkgDir, name, outDir string) (string, error) {
	if name == "" {
		return "", nil
	}
	dir := filepath.Join(pkgDir, filepath.FromSlash(Dir), name)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", nil
	}
	return Write(pkgDir, dir, outDir)
}

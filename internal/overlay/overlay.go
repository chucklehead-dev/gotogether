// Package overlay writes a hand-planted mutant's go build overlay: the
// .go files of a directory of testdata/mutants in place of the package's
// of the same names. qtest.Mutants builds each mutant's tests with one,
// and the navigators (gotogether nav, qnav's sessions) build a mutant's
// test binary with one when GOTOGETHER_MUTANT names it, so a failing
// run's navigate line works in the mutant it came from.
package overlay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Env is the environment variable naming the mutant a test binary runs
// as (qtest.MutantEnv). qmut reads it for a woven mutant's number, or
// list.
const Env = "GOTOGETHER_MUTANT"

// TestEnv is the -run pattern that rebuilds and runs the mutant a binary
// runs as from its harness ("^TestMutants$/^noDedupe$"), set by
// qtest.Mutants.Run: what a failing run's reproduce line runs.
const TestEnv = "GOTOGETHER_MUTANT_TEST"

// PackageEnv is the directory, relative to the harness's package, whose
// files a mutant's replace when it is not the harness's own
// (qtest.Mutants' Package: payd's mutants replace payd/'s files), set by
// qtest.Mutants.Run and named by a failing run's navigate line, so the
// navigators overlay the right package.
const PackageEnv = "GOTOGETHER_MUTANT_PACKAGE"

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
// path. A mutant with no .go files, a file that replaces none of the
// package's, or files that are all the package's but for comments and
// layout (an overlay that changes nothing, whose claims would be judged
// on the faithful program), is an error naming it.
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
	changed := false
	for _, f := range files {
		target := filepath.Join(pkgDir, filepath.Base(f))
		if _, err := os.Stat(target); err != nil {
			return "", fmt.Errorf("%s replaces no file of the package: %v", f, err)
		}
		replace[target] = f
		changed = changed || !sameCode(f, target)
	}
	if !changed {
		return "", fmt.Errorf("mutant %s changes nothing: its files are the package's but for comments and layout (plant its bug in %s)", filepath.Base(mutantDir), mutantDir)
	}
	b, _ := json.Marshal(map[string]any{"Replace": replace})
	path := filepath.Join(outDir, "overlay.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// sameCode reports whether Go files a and b are one program: the same
// tokens, comments left out and a semicolon before a closing bracket
// (which layout decides) too, and the same directives (//go:build,
// //go:embed, //quint:action, //qenv:go: comments that change what is
// built or woven); or byte for byte when either does not scan (the
// compiler then says why).
func sameCode(a, b string) bool {
	ba, errA := os.ReadFile(a)
	bb, errB := os.ReadFile(b)
	if errA != nil || errB != nil {
		return false
	}
	if bytes.Equal(ba, bb) {
		return true
	}
	ta, okA := tokens(ba)
	tb, okB := tokens(bb)
	return okA && okB && slices.Equal(ta, tb) && slices.Equal(directives(ba), directives(bb))
}

// tokens are src's tokens as text, comments left out and every
// semicolon written ";" but those before a ) or }: what layout does not
// change.
func tokens(src []byte) ([]string, bool) {
	fset := token.NewFileSet()
	var s scanner.Scanner
	bad := false
	s.Init(fset.AddFile("", -1, len(src)), src, func(token.Position, string) { bad = true }, 0)
	var out []string
	semi := false
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if semi && tok != token.RPAREN && tok != token.RBRACE {
			out = append(out, ";")
		}
		semi = tok == token.SEMICOLON
		if semi {
			continue
		}
		if lit == "" {
			lit = tok.String()
		}
		out = append(out, lit)
	}
	return out, !bad
}

// directive is a Go directive comment: // then a lower-case name and a
// colon, no space ("//go:embed", "//quint:action"). Read-only.
var directive = regexp.MustCompile(`^//[a-z0-9]+:\S`)

// directives are src's directive lines, trimmed, in order.
func directives(src []byte) []string {
	var out []string
	for _, l := range strings.Split(string(src), "\n") {
		if l = strings.TrimSpace(l); directive.MatchString(l) {
			out = append(out, l)
		}
	}
	return out
}

// Of is the overlay of mutant name of the package in pkgDir (its
// directory of Dir, replacing the files of PackageEnv's directory when
// it is set), written into outDir: "" with no error when name is "" or
// the package has no such directory (a generated mutant, which its
// binary makes of itself).
func Of(pkgDir, name, outDir string) (string, error) {
	if name == "" {
		return "", nil
	}
	dir := filepath.Join(pkgDir, filepath.FromSlash(Dir), name)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", nil
	}
	target := pkgDir
	if p := os.Getenv(PackageEnv); p != "" {
		target = filepath.Join(pkgDir, filepath.FromSlash(p))
	}
	return Write(target, dir, outDir)
}

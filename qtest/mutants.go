package qtest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// MutantEnv is the environment variable naming the mutant a test binary
// runs as: Mutants.Run sets it for each mutant's binary. qmut reads the
// same variable for a woven mutant's number (GOTOGETHER_MUTANT=3, or
// list), which Mutant does not report.
const MutantEnv = "GOTOGETHER_MUTANT"

// Mutant is the mutant this test binary runs as ("" for the system as
// it is): what a TestMutant switches on, and what System's Mutant field
// takes. It is MutantEnv's value, but for qmut's (a number, or list),
// which name a woven mutant, not an overlay's.
func Mutant() string {
	m := os.Getenv(MutantEnv)
	if _, err := strconv.Atoi(m); err == nil || m == "list" {
		return ""
	}
	return m
}

// Mutants are a package's planted bugs, the taught way to plant one by
// hand: each a directory of Dir whose .go files replace the package's
// files of the same names (a go build overlay: the code under test stays
// as it is, each mutant a whole file to diff against it). qweave's
// -mutate (qmut, System.Mutate) is the generated way. A mutant's test
// binary runs Test, which switches on Mutant() and states the claim that
// catches it:
//
//	func TestMutant(t *testing.T) {
//		switch qtest.Mutant() {
//		case "":
//			t.Skip("the system as it is: TestMutants runs this test in each mutant's binary")
//		case "noDedupe":
//			system().Explore(t, qtest.ExploreConfig{}).Expect(t, qtest.Some(qtest.Rejects("apply")))
//		default:
//			t.Fatalf("mutant %s: say what catches it", qtest.Mutant())
//		}
//	}
//
//	func TestMutants(t *testing.T) { qtest.Mutants{}.Run(t) }
type Mutants struct {
	// Dir holds the mutants, one directory each, found there (default
	// testdata/mutants), relative to the test's package.
	Dir string
	// Package is the directory whose files a mutant's replace, relative
	// to the test's package (default "."): a harness package testing
	// another of the module (examples/webjobs' tests replace its webjobs/
	// package's files).
	Package string
	// Test is the test each mutant's binary runs (default TestMutant).
	Test string
	// FullOnly are those built and run in full mode only (not under
	// -short): a mutant whose claim repeats a mechanism a per-push test
	// checks, building its binary being most of its cost.
	FullOnly []string
	// Generated are mutants with no overlay: the binary, built as it is,
	// makes itself the mutant from Mutant() (grist's rules dropped,
	// gristtest.RuleMutants).
	Generated []string
	// Show are substrings of a mutant binary's output lines to log,
	// beside its --- lines and exploration summaries.
	Show []string
}

func (m Mutants) dir() string {
	if m.Dir == "" {
		return filepath.Join("testdata", "mutants")
	}
	return m.Dir
}

func (m Mutants) test() string {
	if m.Test == "" {
		return "TestMutant"
	}
	return m.Test
}

// Names are the mutants, sorted: Dir's directories, then Generated. A
// name qmut would read as its own (a number, list) or a generated
// mutant's that is also a directory fails t, as does no mutant at all.
func (m Mutants) Names(t testing.TB) []string {
	t.Helper()
	entries, err := os.ReadDir(m.dir())
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	for _, g := range m.Generated {
		if slices.Contains(out, g) {
			t.Fatalf("mutant %s is both generated and a directory of %s: rename the directory", g, m.dir())
		}
	}
	out = append(out, m.Generated...)
	for _, n := range out {
		if _, err := strconv.Atoi(n); err == nil || n == "list" {
			t.Fatalf("mutant %s: a name qmut reads as its own (%s=<id>, list): rename it", n, MutantEnv)
		}
	}
	for _, f := range m.FullOnly {
		if !slices.Contains(out, f) {
			t.Fatalf("FullOnly names %s, which is no mutant (the mutants: %s)", f, strings.Join(out, ", "))
		}
	}
	if len(out) == 0 {
		t.Fatalf("no mutants: %s has no directories and none is Generated (plant one: a directory of files replacing the package's of the same names)", m.dir())
	}
	return out
}

// Overlay writes the go build overlay of mutant name (Dir/name's .go
// files in place of Package's of the same names) into a temporary
// directory and returns its path, for `go build -overlay` of a program
// as well as Run's `go test`; a mutant with no files, or a file that
// replaces none of the package's, fails t.
func (m Mutants) Overlay(t testing.TB, name string) string {
	t.Helper()
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(here, m.dir(), name, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("mutant %s has no files in %s", name, filepath.Join(m.dir(), name))
	}
	replace := map[string]string{}
	for _, f := range files {
		target := filepath.Join(here, m.Package, filepath.Base(f))
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("%s replaces no file of the package: %v", filepath.Join(m.dir(), name, filepath.Base(f)), err)
		}
		replace[target] = f
	}
	b, _ := json.Marshal(map[string]any{"Replace": replace})
	path := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Run builds the package's tests once per mutant (Names) with its overlay
// and runs Test in each, MutantEnv naming it (in parallel, -short and the
// deadline passed on), failing with the binary's output when it fails or
// runs no Test, and logging its summaries. In a mutant's own binary it
// skips.
func (m Mutants) Run(t *testing.T) {
	t.Helper()
	if Mutant() != "" {
		t.Skip("already a mutant's binary")
	}
	test := m.test()
	for _, name := range m.Names(t) {
		t.Run(name, func(t *testing.T) {
			if testing.Short() && slices.Contains(m.FullOnly, name) {
				t.Skip("full mode: its claim repeats a per-push test's mechanism")
			}
			t.Parallel()
			args := []string{"test", "-count=1"}
			if !slices.Contains(m.Generated, name) {
				args = append(args, "-overlay", m.Overlay(t, name))
			}
			args = append(args, "-run", "^"+test+"$", "-v")
			if testing.Short() {
				args = append(args, "-short")
			}
			if d, ok := t.Deadline(); ok {
				args = append(args, "-timeout", time.Until(d).Round(time.Second).String())
			}
			cmd := exec.Command("go", append(args, ".")...)
			// a mutant's claims have their stated miss chance at their own
			// seeds: a nightly's GOTOGETHER_SEEDS would only make it slow
			for _, e := range os.Environ() {
				if !strings.HasPrefix(e, "GOTOGETHER_SEEDS=") && !strings.HasPrefix(e, MutantEnv+"=") {
					cmd.Env = append(cmd.Env, e)
				}
			}
			cmd.Env = append(cmd.Env, MutantEnv+"="+name)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("mutant %s: %v\n%s", name, err, out)
			}
			ran := false
			for _, l := range strings.Split(string(out), "\n") {
				l = strings.TrimSpace(l)
				if strings.Contains(l, "runs, ") || strings.HasPrefix(l, "---") || slices.ContainsFunc(m.Show, func(s string) bool { return strings.Contains(l, s) }) {
					t.Log(l)
				}
				ran = ran || strings.HasPrefix(l, "--- PASS: "+test)
			}
			if !ran {
				t.Fatalf("mutant %s: %s did not run (no --- PASS line): its binary ran nothing\n%s", name, test, out)
			}
		})
	}
}

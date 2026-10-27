package gristtest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// MutantEnv is the environment variable naming the mutant a test binary
// runs as: Mutants sets it for each mutant's binary.
const MutantEnv = "GRIST_MUTANT"

// Mutant is the mutant this test binary was built with ("" for the app
// as it is): what a TestMutant switches on, and what qtest.System's
// Mutant field takes.
func Mutant() string { return os.Getenv(MutantEnv) }

// Mutants are an application's mutants, each a directory of Dir whose
// .go files replace the package's files of the same names (a go build
// overlay: the app's own files stay as they are, each mutant a whole
// file to diff against them).
type Mutants struct {
	// Names are the mutants, each a directory of Dir.
	Names []string
	// FullOnly are those built and run in full mode only (not under
	// -short): a mutant whose claim repeats a mechanism a per-push test
	// checks, building its binary being most of its cost.
	FullOnly []string
	// Dir is where they are (default testdata/mutants).
	Dir string
	// Test is the test each mutant's binary runs (default TestMutant),
	// switching on Mutant().
	Test string
}

// Run builds the package's tests once per mutant with its overlay and
// runs Test in each (in parallel, -short and the deadline passed on),
// failing with the binary's output when it fails and logging its
// summaries. In a mutant's own binary it skips, as it does for a mutant
// file that replaces none of the package's.
func (m Mutants) Run(t *testing.T) {
	if Mutant() != "" {
		t.Skip("already a mutant's binary")
	}
	dir, test := m.Dir, m.Test
	if dir == "" {
		dir = filepath.Join("testdata", "mutants")
	}
	if test == "" {
		test = "TestMutant"
	}
	for _, name := range m.Names {
		t.Run(name, func(t *testing.T) {
			if testing.Short() && slices.Contains(m.FullOnly, name) {
				t.Skip("full mode: its claim repeats a per-push test's mechanism")
			}
			t.Parallel()
			args := []string{"test", "-count=1", "-overlay", Overlay(t, dir, name), "-run", "^" + test + "$", "-v"}
			if testing.Short() {
				args = append(args, "-short")
			}
			if d, ok := t.Deadline(); ok {
				args = append(args, "-timeout", time.Until(d).Round(time.Second).String()) //qenv:ok the test binary's own deadline, handed to the mutant's
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
				if strings.Contains(l, "runs, ") || strings.HasPrefix(strings.TrimSpace(l), "---") {
					t.Log(strings.TrimSpace(l))
				}
				ran = ran || strings.HasPrefix(strings.TrimSpace(l), "--- PASS: "+test)
			}
			if !ran {
				t.Fatalf("mutant %s: %s did not run (no --- PASS line): its binary ran nothing\n%s", name, test, out)
			}
		})
	}
}

// Overlay writes the go build overlay of mutant name (dir/name's .go
// files in place of the package's of the same names) into a temporary
// directory and returns its path; a mutant with no files, or a file that
// replaces none of the package's, fails t.
func Overlay(t testing.TB, dir, name string) string {
	t.Helper()
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(here, dir, name, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("mutant %s has no files in %s", name, filepath.Join(dir, name))
	}
	replace := map[string]string{}
	for _, f := range files {
		target := filepath.Join(here, filepath.Base(f))
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("%s replaces no file of the package: %v", filepath.Join(dir, name, filepath.Base(f)), err)
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

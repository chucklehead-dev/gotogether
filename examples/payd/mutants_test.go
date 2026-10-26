package harness

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mutants are the planted bugs, each a directory of testdata/mutants/ whose files
// replace payd's of the same name (a go build overlay): payd's own files
// stay as they are, and each mutant is a whole file to diff against them.
var mutants = []string{"precheck", "noForwardKey", "pendingConflict", "debitFirst", "lostRace", "centsInBody"}

// Overlay writes the go build overlay for mutant m into dir and returns
// its path: the mutant's files in place of payd's, and a test file that
// names the mutant to this package's tests.
func Overlay(t testing.TB, m, dir string) string {
	t.Helper()
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	replace := map[string]string{}
	files, err := filepath.Glob(filepath.Join(here, "testdata", "mutants", m, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("mutant %s has no files in testdata/mutants/%s", m, m)
	}
	for _, f := range files {
		target := filepath.Join(here, "payd", filepath.Base(f))
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("testdata/mutants/%s/%s replaces no file of payd: %v", m, filepath.Base(f), err)
		}
		replace[target] = f
	}
	marker := filepath.Join(dir, "mutant_test.go")
	if err := os.WriteFile(marker, []byte("package harness\n\nfunc init() { mutant = \""+m+"\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	replace[filepath.Join(here, "zz_mutant_test.go")] = marker
	b, _ := json.Marshal(map[string]any{"Replace": replace})
	path := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Each mutant, built with its overlay, is caught as TestMutant says: the
// test binary of each runs TestMutant (and the precheck mutant's real run)
// with this one's flags.
func TestMutants(t *testing.T) {
	t.Parallel() // its explorations record into their own runs
	if mutant != "" {
		t.Skip("already a mutant's binary")
	}
	for _, m := range mutants {
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			args := []string{"test", "-count=1", "-overlay", Overlay(t, m, t.TempDir()), "-run", "^TestMutant$", "-v"}
			if testing.Short() {
				args = append(args, "-short")
			}
			if d, ok := t.Deadline(); ok {
				args = append(args, "-timeout", time.Until(d).Round(time.Second).String())
			}
			cmd := exec.Command("go", append(args, ".")...)
			// a mutant's claims have their stated miss chance at their own
			// seeds: a nightly's GOTOGETHER_SEEDS would only make it slow
			// (fresh seeds, GOTOGETHER_SEED_OFFSET, still apply)
			for _, e := range os.Environ() {
				if !strings.HasPrefix(e, "GOTOGETHER_SEEDS=") {
					cmd.Env = append(cmd.Env, e)
				}
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("mutant %s: %v\n%s", m, err, out)
			}
			for _, l := range strings.Split(string(out), "\n") {
				if strings.Contains(l, "runs, ") || strings.HasPrefix(strings.TrimSpace(l), "---") || strings.Contains(l, "charges for") || strings.Contains(l, "conform") {
					t.Log(strings.TrimSpace(l))
				}
			}
		})
	}
}

// payd is third-party code: it and its SDK import the standard library
// alone, nothing of the toolkit.
func TestPaydImportsNoToolkit(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", "./payd/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, p := range strings.Fields(string(out)) {
		if !strings.HasPrefix(p, "github.com/chucklehead-dev/gotogether/examples/payd/payd") {
			t.Errorf("payd depends on %s: it imports the standard library alone, as code that knows nothing of its tests", p)
		}
	}
	if !strings.Contains(string(out), "github.com/chucklehead-dev/gotogether/examples/payd/payd/client") {
		t.Errorf("go list -deps ./payd/... lists no SDK:\n%s", out)
	}
}

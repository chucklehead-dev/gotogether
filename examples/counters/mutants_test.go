package counters

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// mutants are grain's two bugs, each a directory of testdata/mutants/
// whose files replace the app's of the same name (a go build overlay):
// the app's own files stay as they are, and each mutant is a whole file
// to diff against them.
var mutants = []string{"blind", "dispatchDirect"}

// TestMutant states what catches the mutant this binary was built with;
// TestMutants builds and runs it once per mutant. Rates measured on
// 2026-10-27 (README); seeds give a miss chance under 5%. The faithful
// app passes every one of these configurations (TestFaithful's claims).
func TestMutant(t *testing.T) {
	if mutant == "" {
		t.Skip("the app as it is: TestMutants runs this test built with each mutant's overlay")
	}
	qtest.RequireQuint(t)
	switch mutant {
	case "blind":
		// Claim 1: the race is found in the unsafe variant. Two clients
		// creating counters in one tenant, no faults, names from three
		// letters: a second create of a name commits where the model's
		// precondition is false. At ReadCommitted (PostgreSQL's default) the
		// loser of the race for the head waits for its lock and commits
		// blind: 25 of 40 runs (4 seeds miss with chance 2%). Under
		// Snapshot and Serializable the loser gets ErrConflict, a transient
		// error whose retry decides again on fresh reads: the window is
		// closed (0 of 120 runs in three configurations), so the claim is
		// ReadCommitted's. TestFaithfulAtClaim1 is its contrast
		claim1 := gristsim.Config{Isolation: qenv.ReadCommitted, Tenants: []string{"t1"}, Send: []string{"create-counter"}}
		system(commits, claim1).
			WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(8, 4)}).
			Expect(t, qtest.Some(qtest.Rejects("createCounter")))
		// the same on the SQL tier (sqlstore over qsql at read committed:
		// the loser of the race for the head lock waits for it, then
		// commits blind): 25 of 40 runs, 4 seeds miss with chance 2%
		claim1.SQL = true
		system(commits, claim1).
			WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(8, 4)}).
			Expect(t, qtest.Some(qtest.Rejects("createCounter")))
	case "dispatchDirect":
		// Claim 4: grain's processor duplicate is caught. One tenant, creates
		// and increments: the average's step records no event where the
		// model's action appends one, and the raw append shifts every later
		// head. Without faults in 15 of 30 runs (5 seeds miss with chance
		// 3%), with commit faults and crashes in 22 of 30 (4 seeds: 0.5%)
		cfg := gristsim.Config{Tenants: []string{"t1"}, Send: []string{"create-counter", "increment-counter"}}
		system(commits, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(8, 5)}).
			Expect(t, qtest.Some(qtest.Mismatch("average")))
		cfg.CommitFaults = true
		system(commits, cfg).Explore(t, qtest.ExploreConfig{Seeds: seeds(8, 4)}).
			Expect(t, qtest.Some(qtest.Mismatch("average")))
		// the same on the SQL tier, without faults: 18 of 30 runs (4 seeds
		// miss with chance 3%)
		cfg.CommitFaults, cfg.SQL = false, true
		system(commits, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(8, 4)}).
			Expect(t, qtest.Some(qtest.Mismatch("average")))
	default:
		t.Fatalf("unknown mutant %q", mutant)
	}
}

// Overlay writes the go build overlay for mutant m into dir and returns
// its path: the mutant's files in place of the app's, and a test file
// that names the mutant to this package's tests.
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
		target := filepath.Join(here, filepath.Base(f))
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("testdata/mutants/%s/%s replaces no file of the app: %v", m, filepath.Base(f), err)
		}
		replace[target] = f
	}
	marker := filepath.Join(dir, "mutant_test.go")
	if err := os.WriteFile(marker, []byte("package counters\n\nfunc init() { mutant = \""+m+"\" }\n"), 0o644); err != nil {
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

// Each mutant, built with its overlay, is caught as TestMutant says.
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
				args = append(args, "-timeout", time.Until(d).Round(time.Second).String()) //qenv:ok the test binary's own deadline, handed to the mutant's
			}
			cmd := exec.Command("go", append(args, ".")...)
			// a mutant's claims have their stated miss chance at their own
			// seeds: a nightly's GOTOGETHER_SEEDS would only make it slow
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
				if strings.Contains(l, "runs, ") || strings.HasPrefix(strings.TrimSpace(l), "---") {
					t.Log(strings.TrimSpace(l))
				}
			}
		})
	}
}

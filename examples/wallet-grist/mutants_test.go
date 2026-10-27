package walletgrist

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// mutants are examples/wallet's mutants that grist lets an application
// write, and one of the ledger's, each a directory of testdata/mutants/
// whose files replace the app's of the same name (a go build overlay).
// NoDedupe and UnlockedRecord are not among them: the request record is
// grist's marker, which no application code reads or writes. WriteSkew
// is readThenWrite's skew shape: a conditioned read cannot be left out of
// the condition, so the only skew an application can write is a whole
// command Unconditioned.
var mutants = []string{"noValidation", "readThenWrite", "noOutbox", "ledgerNoDedupe", "replicaRead"}

// TestMutant states what catches the mutant this binary was built with;
// TestMutants builds and runs it once per mutant. The faithful app passes
// every one of these configurations (sim_test.go's claims).
func TestMutant(t *testing.T) {
	if mutant == "" {
		t.Skip("the app as it is: TestMutants runs this test built with each mutant's overlay")
	}
	qtest.RequireQuint(t)
	switch mutant {
	case "noValidation":
		// requests the bank must refuse, applied: an ok where the model's
		// precondition says invalid (TestInvalidRequests is the contrast)
		system(commits, invalid).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(6, 3)}).
			Expect(t, qtest.Some(qtest.Rejects("transfer")))
	case "readThenWrite":
		// at read committed both of a race's transfers spend what one saw:
		// a commit the funds do not cover (a lost update), and a credit
		// line overdrawn by one transfer from each of its accounts (the
		// write skew's effect); TestRaces is the contrast
		system(commits, race).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(20, 8)}).
			Expect(t, qtest.Some(qtest.Rejects("transfer")))
		system(commits, skew).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(20, 8)}).
			Expect(t, qtest.Some(qtest.Rejects("transfer")))
		// Under Serializable the race is narrower but open: a decision's
		// reads come before its append's transaction, so no isolation
		// level protects an Unconditioned one (grist's
		// TestUnconditionedAtEveryLevel). A loser whose append overlaps the
		// winner's gets a transient conflict and decides again; one that
		// read before the winner's commit and began its append after it
		// commits the overdraft (readPos 0, committed at head 4): 7 of 180
		// runs at the race shape, so 80 seeds miss with chance 4%. Full
		// mode: the cost is the seeds
		if testing.Short() {
			return
		}
		ser := race
		ser.Isolation = qenv.Serializable
		system(commits, ser).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 80}).
			Expect(t, qtest.Some(qtest.Rejects("transfer")))
	case "noOutbox":
		// the post at most once: its checkpoint commits before any effect
		// step, which the at-least-once model refuses (atLeastOnceDone)
		system(commits, deposits).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(6, 2)}).
			Expect(t, qtest.Some(qtest.Rejects("post")))
	case "ledgerNoDedupe":
		// a crash after the post and before the checkpoint: the restarted
		// node posts again under the same key, and the ledger posts twice
		system(commits, deposits).Explore(t, qtest.ExploreConfig{Seeds: seeds(6, 2), Chooser: crashAfterPost}).
			Expect(t, qtest.Some(qtest.Rejects("ledgerPost")))
	case "replicaRead":
		// a balance read from the ledger's postings misses a deposit not
		// posted yet
		system(commits, depositsAndReads).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(10, 4)}).
			Expect(t, qtest.Some(qtest.Mismatch("read")))
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
	if err := os.WriteFile(marker, []byte("package walletgrist\n\nfunc init() { mutant = \""+m+"\" }\n"), 0o644); err != nil {
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

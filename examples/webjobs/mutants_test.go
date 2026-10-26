package harness

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtest"
)

// mutant is the mutant this test binary was built with: set by the file a
// mutant's overlay adds (TestMutants), "" for webjobs as it is.
var mutant string

// mutants are the planted bugs, each a directory of testdata/mutants/
// whose files replace webjobs' of the same name (a go build overlay), and
// the invariant that catches each.
var mutants = map[string]string{
	// the page is told ready once the report is written, 250ms before
	// the server commits it
	"readyEarly": "readyIsCommitted",
	// the key is not looked up: the page's double click creates two jobs
	"doubleJob": "oneJobPerKey",
	// a cancel is answered "cancelled" and the job runs on to ready
	"cancelIgnored": "cancelHonoured",
	// a cancel of a running job answers 500 and does nothing (the page
	// ignores the error, and the job runs on to ready)
	"cancelBroken": "noFailedRequest",
	// a poll never says ready: the page shows the job running until a
	// cancel's answer (409, ready) tells it, or for good
	"statusStale": "within 500ms after unseenReady: readySeen",
	// the report is committed whatever a cancel said
	"commitAfterCancel": "noCommitAfterCancel",
}

// Overlay writes the go build overlay for mutant m into dir and returns
// its path: the mutant's files in place of webjobs', and a test file
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
		target := filepath.Join(here, "webjobs", filepath.Base(f))
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("testdata/mutants/%s/%s replaces no file of webjobs: %v", m, filepath.Base(f), err)
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
// test binary of each runs TestMutant with this one's flags. Under -short
// it judges the mutant's recorded run (testdata/runs/<mutant>); out of
// it, a live Bombadil run of the mutant's server.
func TestMutants(t *testing.T) {
	t.Parallel() // each mutant's binary is a process of its own
	if mutant != "" {
		t.Skip("already a mutant's binary")
	}
	for m := range mutants {
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			args := []string{"test", "-count=1", "-overlay", Overlay(t, m, t.TempDir()), "-run", "^TestMutant$", "-v"}
			if testing.Short() {
				args = append(args, "-short")
			}
			if d, ok := t.Deadline(); ok {
				args = append(args, "-timeout", time.Until(d).Round(time.Second).String())
			}
			out, err := exec.Command("go", append(args, ".")...).CombinedOutput()
			if err != nil {
				t.Fatalf("mutant %s: %v\n%s", m, err, out)
			}
			for _, l := range strings.Split(string(out), "\n") {
				if l = strings.TrimSpace(l); strings.HasPrefix(l, "---") || strings.Contains(l, "violates") || strings.Contains(l, "journey:") {
					t.Log(l)
				}
			}
		})
	}
}

// TestMutant states what catches the mutant this binary was built with:
// its invariant (or eventual val) violated by the client's run of the
// mutant's server and by its recorded Bombadil run (under -short), or by
// a live run (Bombadil exploring the mutant's server for 45s).
func TestMutant(t *testing.T) {
	if mutant == "" {
		t.Skip("webjobs as it is: TestMutants runs this test built with each mutant's overlay")
	}
	inv, ok := mutants[mutant]
	if !ok {
		t.Fatalf("unknown mutant %q", mutant)
	}
	qtest.RequireQuint(t)
	if testing.Short() {
		clientRun(t, 1).Expect(t, qtest.Violates(inv))
		recorded(t, mutant).Expect(t, qtest.Violates(inv))
		return
	}
	j := live(t, mutant, Live{Violation: true}) // Bombadil's own properties may catch it too
	j.run.Expect(t, qtest.Violates(inv))
	j.keep(t)
}

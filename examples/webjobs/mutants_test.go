package harness

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
)

// mutants are the planted bugs, each a directory of testdata/mutants/
// whose files replace webjobs' of the same name (a go build overlay,
// found there by qtest.Mutants), and the invariant that catches each.
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
	// a key asked again once its job ended runs the job again in place,
	// back to running under the same id (pageless: see below)
	"rerunFinished": "createdOnce",
}

// pageless are the mutants no page can show: the page never sends a key
// again once its job ended (it makes a new one), so neither Bombadil's
// runs nor a recording reach them; the client's late retry does, in both
// tiers.
var pageless = map[string]bool{"rerunFinished": true}

// planted are webjobs' mutants: testdata/mutants/<name>/ files replacing
// the webjobs package's of the same name.
var planted = qtest.Mutants{Package: "webjobs", Show: []string{"violates", "journey:"}}

// Each mutant, built with its overlay, is caught as its claim says.
// Under -short the overlay builds cmd/webjobs, which the client drives
// from this process; out of it, the test binary of each runs TestMutant,
// a live Bombadil run of the mutant's server.
func TestMutants(t *testing.T) {
	t.Parallel() // each mutant's binary is a process of its own
	if !testing.Short() {
		// the test binary of each runs TestMutant: a live Bombadil run
		planted.Run(t)
		return
	}
	if qtest.Mutant() != "" {
		t.Skip("already a mutant's binary")
	}
	for _, m := range planted.Names(t) {
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			inv, ok := mutants[m]
			if !ok {
				t.Fatalf("mutant %s: no claim in mutants (the invariant that catches it)", m)
			}
			// the mutant's server as a program, driven by the client from
			// here: one small build per mutant, every check in this process
			// (its recorded run is TestRecordedMutants')
			qtest.RequireQuint(t)
			bin := filepath.Join(t.TempDir(), "webjobs")
			if out, err := exec.Command("go", "build", "-overlay", planted.Overlay(t, m), "-o", bin, "./cmd/webjobs").CombinedOutput(); err != nil {
				t.Fatalf("mutant %s: go build: %v\n%s", m, err, out)
			}
			clientRunOf(t, bin, 1).Expect(t, qtest.Violates(inv))
		})
	}
}

// TestMutant states what catches the mutant this binary was built with:
// its invariant (or eventual val) violated by the client's run of the
// mutant's server and by its recorded Bombadil run (under -short), or by
// a live run (Bombadil exploring the mutant's server for 45s).
func TestMutant(t *testing.T) {
	mutant := qtest.Mutant()
	if mutant == "" {
		t.Skip("webjobs as it is: TestMutants runs this test built with each mutant's overlay")
	}
	inv, ok := mutants[mutant]
	if !ok {
		t.Fatalf("mutant %s: no claim in mutants (the invariant that catches it)", mutant)
	}
	qtest.RequireQuint(t)
	if pageless[mutant] {
		clientRun(t, 1).Expect(t, qtest.Violates(inv))
		return
	}
	if testing.Short() {
		// as TestMutants' -short runs it, in this binary
		clientRun(t, 1).Expect(t, qtest.Violates(inv))
		return
	}
	j := live(t, mutant, Live{Violation: true}) // Bombadil's own properties may catch it too
	j.run.Expect(t, qtest.Violates(inv))
	j.keep(t)
}

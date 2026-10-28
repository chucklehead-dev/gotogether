package grist_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// grist's own mutants, each a directory of testdata/mutants whose files
// replace grist's of the same name (qtest.Mutants): snapshots taken at
// the wrong position, reused after their fold changed, and reused after
// an upcaster was fixed. TestMutant
// states the harness that catches each; the faithful grist passes it
// (TestCheckSnapshotsAgree, TestSnapshotOfAChangedFold).
func TestMutant(t *testing.T) {
	ctx := context.Background()
	switch qtest.Mutant() {
	case "":
		t.Skip("grist as it is: TestMutants runs this test built with each mutant's overlay")
	case "snapshotPastItsPosition":
		// a snapshot labelled one past what it folded skips the event at
		// that position: CheckSnapshots' snapshot at the position before
		// an event of the read model's, plus the events after it, misses it
		a, added, _ := items()
		s := newCounting(t)
		interleaved(t, s, added)
		err := a.CheckSnapshots(ctx, s)
		if err == nil || !strings.Contains(err.Error(), "a snapshot taken at 2 plus the events after it gives") {
			t.Errorf("CheckSnapshots: %v, want the snapshot at 2 (before x's event at 3) named", err)
		}
	case "fingerprintByVersion":
		// a fold changed at the same version keeps its fingerprint: the
		// new fold reads the old one's snapshot, and CheckSnapshots finds
		// the stored snapshot disagreeing with a fold from the start
		_, added, oldQ := items()
		newApp, _, newQ := changedFold()
		s := newCounting(t)
		addItems(t, s, added, "x", 20)
		every := grist.SnapshotEvery(ctx, 5)
		oldQ.Run(every, s, grist.Envelope{ID: "q1", Tenant: "t1"}, ItemQuery{Item: "x"})
		if n, _, _ := newQ.Run(every, s, grist.Envelope{ID: "q2", Tenant: "t1"}, ItemQuery{Item: "x"}); n != 20 {
			t.Errorf("the changed fold answered %d: want 20, the old fold's snapshot reused (40 is its own)", n)
		}
		err := newApp.CheckSnapshots(ctx, s)
		if err == nil || !strings.Contains(err.Error(), "its stored snapshot plus the events after it gives 20 at the head (20); a fold from the start gives 40") {
			t.Errorf("CheckSnapshots: %v, want the reused snapshot named", err)
		}
	case "upcasterFixKeepsSnapshots":
		// the fingerprint without the upcasters: a fixed upcaster keeps it,
		// and the snapshot folded through the buggy one answers for the
		// fix (TestFixedUpcasterRefolds is the contrast)
		if _, _, n := buggyThenFixed(t); n != 0 {
			t.Errorf("the fixed upcaster's count: %d, want 0, the buggy snapshot reused", n)
		}
	default:
		t.Fatalf("mutant %s: say what catches it (a case of TestMutant)", qtest.Mutant())
	}
}

// Each of grist's mutants, built with its overlay, is caught as
// TestMutant says.
func TestMutants(t *testing.T) {
	t.Parallel()
	qtest.Mutants{}.Run(t)
}

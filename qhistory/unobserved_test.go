package qhistory_test

import (
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qhistory"
)

// An append that no external read saw still constrains serial explanations.
// Empty reads must precede it; read/append cycles can therefore witness write
// skew, and a later read cannot omit a write known to have committed first.
func TestUnobservedCommittedAppends(t *testing.T) {
	t.Parallel()
	at := func(n int) time.Time { return time.Unix(int64(n+1), 0) }
	timed := func(x qhistory.Txn, from, to int) qhistory.Txn {
		x.Invoked, x.Completed = at(from), at(to)
		return x
	}
	stale := []qhistory.Txn{timed(txn(1, app("x", 1)), 0, 1), timed(txn(2, rd("x")), 2, 3)}
	overlap := []qhistory.Txn{timed(txn(1, app("x", 1)), 0, 3), timed(txn(2, rd("x")), 2, 4)}
	unknown := append([]qhistory.Txn(nil), stale...)
	unknown[0].Outcome = qhistory.Unknown
	aborted := append([]qhistory.Txn(nil), stale...)
	aborted[0].Outcome = qhistory.Aborted
	inferred := txn(1, app("x", 1), app("y", 1))
	inferred.Outcome = qhistory.Unknown
	for _, tc := range []struct {
		name  string
		txns  []qhistory.Txn
		level qhistory.Level
		kind  string
	}{
		{"stale read", stale, qhistory.StrictSerializable, "G-single-realtime"},
		{"serializable can put read first", stale, qhistory.Serializable, ""},
		{"overlap can put read first", overlap, qhistory.StrictSerializable, ""},
		{"unknown timeout is no completion", unknown, qhistory.StrictSerializable, ""},
		{"aborted append imposes no dependency", aborted, qhistory.StrictSerializable, ""},
		{"no reads do not prove loss", []qhistory.Txn{stale[0]}, qhistory.StrictSerializable, ""},
		{"own append and read", []qhistory.Txn{txn(1, app("x", 1), rd("x", 1))}, qhistory.StrictSerializable, ""},
		{"no order invented between unseen writers", []qhistory.Txn{txn(1, app("x", 1)), txn(2, app("x", 2)), txn(3, rd("x"))}, qhistory.Serializable, ""},
		{"unseen write skew", []qhistory.Txn{txn(1, rd("x"), rd("y"), app("x", 1)), txn(2, rd("x"), rd("y"), app("y", 1))}, qhistory.Serializable, "G2"},
		{"snapshot isolation allows unseen write skew", []qhistory.Txn{txn(1, rd("x"), rd("y"), app("x", 1)), txn(2, rd("x"), rd("y"), app("y", 1))}, qhistory.SnapshotIsolation, ""},
		{"unknown inferred committed through another key", []qhistory.Txn{inferred, txn(2, rd("x", 1), rd("y"))}, qhistory.SnapshotIsolation, "G-single"},
		{"multiple unseen appends in one transaction", []qhistory.Txn{timed(txn(1, app("x", 1), app("x", 2)), 0, 1), timed(txn(2, rd("x")), 2, 3)}, qhistory.StrictSerializable, "G-single-realtime"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := qhistory.Check(tc.level, tc.txns)
			if err != nil {
				t.Fatal(err)
			}
			if tc.kind == "" {
				if !rep.OK() {
					t.Fatalf("expected history to pass %s: %s", tc.level, rep)
				}
			} else if rep.OK() || !rep.Has(tc.kind) {
				t.Fatalf("want %s forbidden, got %s", tc.kind, rep)
			}
		})
	}
}

package qenv_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A range scan is a predicate read of its range: under Serializable a
// key committed into the range after the scanner's snapshot aborts the
// scanner's commit (a phantom), and one committed beside the range, under
// the same prefix, does not. With the store's validation broken, the
// history checker (Check) names the phantom as G2, and only for a key in
// the range: it sees the scan as a read of the range, not of the prefix.
func TestRangeScanPhantom(t *testing.T) {
	r := qenv.KeyRange{Prefix: "e/a/", From: "e/a/05", To: "e/a/10"}
	for _, c := range []struct {
		key     string
		phantom bool
	}{
		{"e/a/07", true},
		{"e/a/05", true},
		{"e/a/10", false},
		{"e/a/03", false},
		{"e/b/07", false},
	} {
		// the store keeps its level: the scanner aborts for a phantom only
		err, _ := rangeRace(t, r, c.key, false)
		if c.phantom != errors.Is(err, qenv.ErrConflict) {
			t.Errorf("an insert of %s during a scan of %+v: the scanner's commit returned %v (phantom: %v)", c.key, r, err, c.phantom)
		}
		// the store broken, the history checker sees the same
		_, caught := rangeRace(t, r, c.key, true)
		if c.phantom != strings.Contains(caught, "committed a history its level forbids: G2") {
			t.Errorf("a broken store, an insert of %s during a scan of %+v: the checker said %q (phantom: %v)", c.key, r, caught, c.phantom)
		}
	}
}

// rangeRace runs a scanner (scans r, empty, then writes "cfg" after the
// inserter has committed) and an inserter (reads "cfg", writes key) at
// Serializable under Check, and returns the scanner's commit error and
// the checker's panic, if any.
func rangeRace(t *testing.T, r qenv.KeyRange, key string, broken bool) (scanErr error, caught string) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			caught = fmt.Sprint(p)
		}
	}()
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	s.Check = true
	st := qenv.NewStore(s, "db", qenv.Serializable)
	if broken {
		qenv.BreakValidation(st)
	}
	st.Load("cfg", 1)
	st.Load("e/a/01", 1)
	inserted := false
	s.Spawn("scanner", func(ctx context.Context) {
		tx := st.Begin(ctx)
		if got := qenv.ScanRange(tx, r); len(got) != 0 {
			t.Errorf("the range is empty; the scan returned %v", got)
		}
		qenv.Wait(ctx, func() bool { return inserted })
		tx.Put("cfg", 2)
		scanErr = tx.Commit()
	})
	s.Spawn("inserter", func(ctx context.Context) {
		tx := st.Begin(ctx)
		tx.Get("cfg")
		tx.Put(key, 1)
		if err := tx.Commit(); err != nil {
			t.Errorf("the inserter: %v", err)
		}
		inserted = true
	})
	if err := s.Run(time.Hour); err != nil {
		t.Fatal(err)
	}
	return scanErr, ""
}

// ScanRange of a Txn that is no RangeScanner filters its Scan of the
// prefix: the same keys.
func TestScanRangeFallback(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	st := qenv.NewStore(s, "db", qenv.Serializable)
	for _, k := range []string{"a/1", "a/2", "a/3", "b/2"} {
		st.Load(k, k)
	}
	s.Spawn("reader", func(ctx context.Context) {
		tx := st.Begin(ctx)
		defer tx.Rollback()
		r := qenv.KeyRange{Prefix: "a/", From: "a/2"}
		got, want := qenv.ScanRange(plainTxn{tx}, r), qenv.ScanRange(tx, r)
		if fmt.Sprint(got) != fmt.Sprint(want) || len(got) != 2 {
			t.Errorf("fallback %v, store %v; want a/2 and a/3", got, want)
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
}

// plainTxn hides its Txn's ScanRange.
type plainTxn struct{ qenv.Txn }

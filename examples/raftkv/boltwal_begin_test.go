package raftkv_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/raftkv"
	bolt "go.etcd.io/bbolt"
)

// The DB seam returns refusal at Begin, rather than manufacturing a transaction
// whose first use only later discovers that admission never succeeded.
func TestBoltWALBeginErrors(t *testing.T) {
	w, err := raftkv.OpenBoltWAL(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if tx, err := w.Begin(ctx); tx != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Begin: %v, %v", tx, err)
	}
	tx, err := w.Begin(t.Context())
	if err != nil {
		t.Fatalf("canceled admission retained the writer: %v", err)
	}
	tx.Rollback()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if tx, err := w.Begin(t.Context()); tx != nil || !errors.Is(err, bolt.ErrDatabaseNotOpen) {
		t.Fatalf("closed Begin: %v, %v", tx, err)
	}
}

package memstore_test

import (
	"context"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/grist/storetest"
)

// The memstore keeps the store contract.
func TestContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T, body func(ctx context.Context, s store.Store) error) {
		if err := body(context.Background(), memstore.New("grist")); err != nil {
			t.Error(err)
		}
	})
}

// A restore (Backup, Restore) wakes the memstore's waiters with the log
// below what they wait on (storetest.Rewinds).
func TestRewinds(t *testing.T) {
	s := memstore.New("grist")
	var b *memstore.Backup
	err := storetest.Rewinds(context.Background(), s,
		func(context.Context) error { b = s.Backup(); return nil },
		func(context.Context) error { s.Restore(b); return nil })
	if err != nil {
		t.Error(err)
	}
}

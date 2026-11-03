package simstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// Refused admission is known not committed, and a read must not turn the same
// refusal into empty data. Ending the holder lets ordinary store work recover.
func TestTransactionCapacityIsNotCommitted(t *testing.T) {
	sim := qenv.New(qenv.Nominal{})
	defer sim.Close()
	db := qenv.NewStore(sim, "grist", qenv.Serializable)
	db.MaxTransactions = 1
	s := simstore.New(db)
	sim.Spawn("client", func(ctx context.Context) {
		if err := s.CreateTenant(ctx, "t"); err != nil {
			t.Error(err)
			return
		}
		holder, err := db.Begin(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		defer holder.Rollback()
		before := db.Version()
		if _, err := s.Append(ctx, "t", store.Batch{}); !errors.Is(err, qenv.ErrTransactionCapacity) || store.MayHaveCommitted(err) {
			t.Errorf("capacity refusal must be known not committed: %v", err)
		}
		if _, _, err := s.Read(ctx, "t", store.Selector{}, 0); !errors.Is(err, qenv.ErrTransactionCapacity) {
			t.Errorf("capacity-refused read: %v", err)
		}
		if got := db.Version(); got != before {
			t.Errorf("refused work changed store version: got%d want%d", got, before)
		}
		holder.Rollback()
		if _, err := s.Append(ctx, "t", store.Batch{}); err != nil {
			t.Errorf("append after capacity released: %v", err)
		}
	})
	if err := sim.Run(time.Second); err != nil {
		t.Fatal(err)
	}
}

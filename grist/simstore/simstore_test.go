package simstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/grist/storetest"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// The simstore keeps the store contract at each of qenv.Store's
// isolation levels, with the Store's history checked against its level
// at every commit (as GOTOGETHER_CHECK=1 does).
func TestContract(t *testing.T) {
	for _, iso := range []qenv.Isolation{qenv.Serializable, qenv.SnapshotIsolation, qenv.ReadCommitted} {
		t.Run(iso.String(), func(t *testing.T) {
			storetest.Run(t, func(t *testing.T, body func(ctx context.Context, s store.Store) error) {
				sim := qenv.New(qenv.NewSeeded(1))
				defer sim.Close()
				sim.Check = true
				s := simstore.New(qenv.NewStore(sim, "grist", iso))
				var err error
				sim.Spawn("contract", func(ctx context.Context) { err = body(ctx, s) })
				if rerr := sim.Run(time.Minute); rerr != nil {
					t.Fatal(rerr)
				}
				if err != nil {
					t.Error(err)
				}
			})
		})
	}
}

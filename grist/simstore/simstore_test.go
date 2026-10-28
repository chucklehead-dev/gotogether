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
				// the snapshot faults off: a torn or lost put is grist's to
				// survive (TestSnapshotFaults), not the contract's
				sim := qenv.New(qenv.Without(qenv.NewSeeded(1), "store.grist.snapshot"))
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

// Two simstores over one qenv.Store wake each other's waiters: their
// waits are on the qenv.Store's signal, notified at every commit, not on
// a signal of their own (which slept through the other's appends, and
// lost their steps: a relay waiting on one never saw the other's).
func TestTwoStores(t *testing.T) {
	sim := qenv.New(qenv.NewSeeded(1))
	defer sim.Close()
	db := qenv.NewStore(sim, "grist", qenv.Serializable)
	a, b := simstore.New(db), simstore.New(db)
	var err error
	sim.Spawn("two", func(ctx context.Context) { err = storetest.TwoStores(ctx, a, b) })
	if rerr := sim.Run(time.Minute); rerr != nil {
		t.Fatal(rerr)
	}
	if err != nil {
		t.Error(err)
	}
}

// A restore of the qenv.Store (nemesis.restore.<name>'s, here Backup and
// Restore) wakes the simstore's waiters with the log below what they wait
// on (storetest.Rewinds), the history checker on.
func TestRewinds(t *testing.T) {
	sim := qenv.New(qenv.NewSeeded(1))
	defer sim.Close()
	sim.Check = true
	db := qenv.NewStore(sim, "grist", qenv.Serializable)
	var err error
	sim.Spawn("rewinds", func(ctx context.Context) {
		err = storetest.Rewinds(ctx, simstore.New(db),
			func(context.Context) error { db.Backup(); return nil },
			func(context.Context) error { db.Restore(); return nil })
	})
	if rerr := sim.Run(time.Minute); rerr != nil {
		t.Fatal(rerr)
	}
	if err != nil {
		t.Error(err)
	}
}

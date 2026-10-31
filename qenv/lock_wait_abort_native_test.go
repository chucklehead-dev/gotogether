package qenv

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Native waits use a condition variable rather than the virtual scheduler's
// ready scan. End a definitely-blocked transaction, then require its owner to
// return before releasing the holder. No sleep guesses when the wait began.
func TestNativeStoreEndedLockWaits(t *testing.T) {
	for _, commit := range []bool{false, true} {
		name := "lock"
		if commit {
			name = "commit"
		}
		t.Run(name, func(t *testing.T) {
			s := NewNative(Nominal{}, nil)
			st := NewStore(s, "db", ReadCommitted)
			st.Load("k", 7)
			held, returned, release := false, false, false
			var waiter *tx
			var got error
			s.Spawn("holder", func(ctx context.Context) {
				holder := st.Begin(ctx)
				if _, _, err := holder.GetForUpdate("k"); err != nil {
					t.Error(err)
					return
				}
				held = true
				Wait(ctx, func() bool { return release })
				holder.Rollback()
			})
			s.Spawn("waiter", func(ctx context.Context) {
				Wait(ctx, func() bool { return held })
				waiter = st.Begin(ctx).(*tx)
				if commit {
					waiter.Put("k", 50)
					got = waiter.Commit()
				} else {
					_, _, got = waiter.GetForUpdate("k")
				}
				returned = true
				waiter.Rollback()
			})
			s.Spawn("abort", func(ctx context.Context) {
				Wait(ctx, func() bool { return waiter != nil && len(waiter.waitKeys) > 0 })
				waiter.Rollback()
				Wait(ctx, func() bool { return returned })
				release = true
			})
			err := s.Run(100 * time.Millisecond)
			s.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !returned || !release || !errors.Is(got, ErrConflict) {
				t.Fatalf("native aborted waiter did not return before holder release: returned=%v release=%v error=%v", returned, release, got)
			}
			if v, _ := st.Read("k"); v != 7 {
				t.Fatalf("aborted native waiter wrote k=%v", v)
			}
		})
	}
}

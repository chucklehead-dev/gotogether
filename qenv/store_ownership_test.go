package qenv

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The roster owns unfinished handles, locks belong to unfinished admitted
// handles, and the admission count agrees with those owners. Reset handles are
// deliberately unfinished but no longer own capacity or locks: their last reads
// remain available until a locking or commit operation reports the reset.
func checkStoreOwnership(t *testing.T, st *Store, handles ...*tx) {
	t.Helper()
	admitted := 0
	for handle := range st.open {
		if handle.done {
			t.Errorf("terminal transaction %d retained by Store roster", handle.id)
		}
		if handle.admitted {
			admitted++
		}
	}
	if st.active != admitted {
		t.Errorf("Store admission count %d disagrees with %d roster owners", st.active, admitted)
	}
	for key, owner := range st.locks {
		if owner.done || owner.reset || !owner.admitted || !st.open[owner] {
			t.Errorf("lock %q retained by non-owning transaction %d", key, owner.id)
		}
	}
	for _, handle := range handles {
		if handle.done {
			if st.open[handle] || handle.admitted {
				t.Errorf("terminal transaction %d retained roster/admission ownership", handle.id)
			}
		} else if !st.open[handle] {
			t.Errorf("unfinished transaction %d absent from Store roster", handle.id)
		}
	}
}

func ownershipBegin(t *testing.T, st *Store, ctx context.Context, key string) *tx {
	t.Helper()
	handle, err := st.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := handle.GetForUpdate(key); err != nil {
		t.Fatal(err)
	}
	handle.Put(key, "staged")
	return handle.(*tx)
}

func TestStoreOwnershipCommitAndRollback(t *testing.T) {
	s := New(Nominal{})
	defer s.Close()
	st := NewStore(s, "ownership", Serializable)
	st.MaxTransactions = 2
	finished := false
	s.Spawn("client", func(ctx context.Context) {
		first := ownershipBegin(t, st, ctx, "first")
		second := ownershipBegin(t, st, ctx, "second")
		checkStoreOwnership(t, st, first, second)
		if err := first.Commit(); err != nil {
			t.Fatal(err)
		}
		if !first.done || second.done {
			t.Error("Commit changed the wrong transaction's lifetime")
		}
		checkStoreOwnership(t, st, first, second)
		// A newly admitted handle can acquire the ended handle's lock.
		fresh := ownershipBegin(t, st, ctx, "first")
		first.Rollback()
		checkStoreOwnership(t, st, first, second, fresh)
		second.Rollback()
		fresh.Rollback()
		checkStoreOwnership(t, st, first, second, fresh)
		if !second.done || !fresh.done || len(st.open) != 0 || len(st.locks) != 0 {
			t.Error("completed workload retained transaction owners or locks")
		}
		finished = true
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if !finished {
		t.Fatal("ownership scenario did not finish within the simulation horizon")
	}
}

func TestStoreOwnershipResetRetainsOnlyReadableHandle(t *testing.T) {
	for _, reset := range []string{"failover", "restore"} {
		for _, refuse := range []string{"lock", "commit"} {
			t.Run(reset+"/"+refuse, func(t *testing.T) {
				s := New(Nominal{})
				defer s.Close()
				st := NewStore(s, "ownership", Serializable)
				st.MaxTransactions = 2
				st.Load("key", "original")
				st.Backup()
				finished := false
				s.Spawn("client", func(ctx context.Context) {
					stale := ownershipBegin(t, st, ctx, "key")
					other := ownershipBegin(t, st, ctx, "other")
					if reset == "restore" {
						if !st.Restore() {
							t.Fatal("backup unavailable")
						}
					} else {
						st.FailOver(0, 0)
					}
					if stale.done || !stale.reset || stale.admitted || other.done || !other.reset || other.admitted || len(st.locks) != 0 {
						t.Error("reset did not preserve an unadmitted, unlocked, readable handle")
					}
					if value, ok := stale.Get("key"); !ok || value != "staged" {
						t.Errorf("reset handle lost staged last answer: %v, %v", value, ok)
					}
					checkStoreOwnership(t, st, stale, other)
					fresh := ownershipBegin(t, st, ctx, "key")
					var err error
					if refuse == "lock" {
						_, _, err = stale.GetForUpdate("key")
					} else {
						err = stale.Commit()
					}
					if !errors.Is(err, ErrUnavailable) || !stale.done {
						t.Errorf("reset refusal did not terminate stale handle: %v", err)
					}
					checkStoreOwnership(t, st, stale, other, fresh)
					stale.Rollback()
					if value, ok := other.Get("other"); !ok || value != "staged" {
						t.Errorf("reset refusal ended another readable handle: %v, %v", value, ok)
					}
					other.Rollback()
					if st.locks["key"] != fresh || !fresh.admitted || st.active != 1 {
						t.Error("stale cleanup disturbed fresh ownership")
					}
					if err := fresh.Commit(); err != nil {
						t.Fatal(err)
					}
					checkStoreOwnership(t, st, stale, other, fresh)
					finished = true
				})
				if err := s.Run(time.Second); err != nil {
					t.Fatal(err)
				}
				if !finished {
					t.Fatal("reset scenario did not finish within the simulation horizon")
				}
			})
		}
	}
}

func TestStoreOwnershipCrashReleasesOnlyCrashedProcess(t *testing.T) {
	s := New(Nominal{})
	defer s.Close()
	st := NewStore(s, "ownership", Serializable)
	st.MaxTransactions = 3
	finished := false
	var first, sameProcess, second *tx
	a := s.Process("a", false, func(ctx context.Context) {
		first = ownershipBegin(t, st, ctx, "a")
		sameProcess = ownershipBegin(t, st, ctx, "a-extra")
		Wait(ctx, func() bool { return false })
	})
	b := s.Process("b", false, func(ctx context.Context) {
		second = ownershipBegin(t, st, ctx, "b")
		Wait(ctx, func() bool { return false })
	})
	s.Spawn("supervisor", func(ctx context.Context) {
		Wait(ctx, func() bool { return first != nil && sameProcess != nil && second != nil })
		a.Crash()
		checkStoreOwnership(t, st, first, sameProcess, second)
		if !first.done || !sameProcess.done || second.done || st.locks["b"] != second {
			t.Error("crash did not isolate transaction ownership by process")
		}
		fresh := ownershipBegin(t, st, ctx, "a")
		if err := fresh.Commit(); err != nil {
			t.Fatal(err)
		}
		b.Crash()
		checkStoreOwnership(t, st, first, sameProcess, second, fresh)
		if !second.done || len(st.open) != 0 || len(st.locks) != 0 {
			t.Error("crashed processes retained unfinished transaction resources")
		}
		if _, ok := st.Read("b"); ok {
			t.Error("crashed transaction applied its staged data")
		}
		finished = true
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if !finished {
		t.Fatal("ownership scenario did not finish within the simulation horizon")
	}
}

package qenv

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func capacityBegin(t *testing.T, st *Store, ctx context.Context) Txn {
	t.Helper()
	tx, err := st.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func capacityScenario(t *testing.T, native bool, scenario func(context.Context, *Store)) {
	t.Helper()
	var s *Sim
	if native {
		s = NewNative(Nominal{}, nil)
	} else {
		s = New(Nominal{})
	}
	defer s.Close()
	st := NewStore(s, "db", Serializable)
	s.Spawn("client", func(ctx context.Context) { scenario(ctx, st) })
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestStoreTransactionCapacity(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "deterministic"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			capacityScenario(t, native, func(ctx context.Context, st *Store) {
				st.MaxTransactions = 1
				held := capacityBegin(t, st, ctx)
				hooks := 0
				held.Put("pending", 1)
				held.OnCommit(func() { hooks++ })
				refused, err := st.Begin(ctx)
				var snapshot *TransactionCapacityError
				if refused != nil || !errors.Is(err, ErrTransactionCapacity) || !errors.As(err, &snapshot) {
					t.Fatalf("full Store: tx=%v error=%v", refused, err)
				}
				if snapshot.Store != "db" || snapshot.Limit != 1 || snapshot.Active != 1 || errors.Is(err, ErrUnknownOutcome) {
					t.Fatalf("capacity evidence: %+v", snapshot)
				}
				if _, ok := st.Read("pending"); ok || hooks != 0 || st.Version() != 0 || st.txs != 1 {
					t.Fatal("refused admission changed store effects or transaction identity")
				}
				held.Rollback()
				st.MaxTransactions = 2
				if snapshot.Active != 1 || snapshot.Limit != 1 || st.active != 0 {
					t.Fatal("returned capacity evidence changed with store state")
				}
				st.MaxTransactions = 1
				fresh := capacityBegin(t, st, ctx)
				// Cleanup of an already-ended holder cannot free the new lease.
				held.Rollback()
				if tx, err := st.Begin(ctx); tx != nil || !errors.Is(err, ErrTransactionCapacity) {
					t.Fatalf("double rollback freed fresh lease: %v, %v", tx, err)
				}
				if snapshot.Active != 1 || snapshot.Limit != 1 {
					t.Fatal("returned capacity evidence mutated after recovery")
				}
				fresh.Put("committed", 2)
				fresh.OnCommit(func() { hooks++ })
				if err := fresh.Commit(); err != nil {
					t.Fatal(err)
				}
				fresh.Rollback()
				if v, ok := st.Read("committed"); !ok || v != 2 || hooks != 1 || st.active != 0 {
					t.Fatalf("commit/release: value=%v exists=%v hooks=%d active=%d", v, ok, hooks, st.active)
				}
				capacityBegin(t, st, ctx).Rollback()
			})
		})
	}
}

func TestStoreCapacityConfigurationAndCancellation(t *testing.T) {
	capacityScenario(t, false, func(ctx context.Context, st *Store) {
		var unlimited []Txn
		for range 3 {
			unlimited = append(unlimited, capacityBegin(t, st, ctx))
		}
		st.MaxTransactions = 1
		if tx, err := st.Begin(ctx); tx != nil || !errors.Is(err, ErrTransactionCapacity) {
			t.Fatalf("unlimited leases disappeared on limit change: %v, %v", tx, err)
		}
		for _, tx := range unlimited {
			tx.Rollback()
		}
		st.MaxTransactions = -1
		if tx, err := st.Begin(ctx); tx != nil || err == nil || errors.Is(err, ErrTransactionCapacity) || !strings.Contains(err.Error(), "MaxTransactions") {
			t.Fatalf("negative configuration: %v, %v", tx, err)
		}
		st.MaxTransactions = 1
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		before := st.txs
		if tx, err := st.Begin(canceled); tx != nil || !errors.Is(err, context.Canceled) || st.active != 0 || st.txs != before {
			t.Fatalf("canceled admission allocated a lease: %v, %v", tx, err)
		}
		owned, cancelOwned := context.WithCancel(ctx)
		tx := capacityBegin(t, st, owned)
		cancelOwned()
		if next, err := st.Begin(ctx); next != nil || !errors.Is(err, ErrTransactionCapacity) {
			t.Fatalf("caller-owned transaction implicitly released on cancellation: %v, %v", next, err)
		}
		tx.Rollback()
		//lint:ignore SA1012 nil is an explicit supported goroutine-fallback contract
		nilContext, err := st.Begin(nil)
		if err != nil {
			t.Fatal(err)
		}
		nilContext.Rollback()
	})
}

func TestStoreCapacityResetDoesNotReleaseAnotherLease(t *testing.T) {
	capacityScenario(t, false, func(ctx context.Context, st *Store) {
		st.MaxTransactions = 1
		stale := capacityBegin(t, st, ctx)
		stale.Put("lost", 1)
		st.FailOver(0, 0)
		fresh := capacityBegin(t, st, ctx)
		if err := stale.Commit(); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("reset transaction did not retain unavailable outcome: %v", err)
		}
		stale.Rollback()
		if tx, err := st.Begin(ctx); tx != nil || !errors.Is(err, ErrTransactionCapacity) {
			t.Fatalf("stale reset cleanup freed fresh lease: %v, %v", tx, err)
		}
		if _, ok := st.Read("lost"); ok {
			t.Fatal("reset transaction applied its write")
		}
		fresh.Rollback()
		capacityBegin(t, st, ctx).Rollback()
	})
}

func TestStoreCapacityCommitOutcomesRelease(t *testing.T) {
	t.Run("conflict", func(t *testing.T) {
		capacityScenario(t, false, func(ctx context.Context, st *Store) {
			st.MaxTransactions = 2
			old := capacityBegin(t, st, ctx)
			fresh := capacityBegin(t, st, ctx)
			old.Put("k", 1)
			fresh.Put("k", 2)
			if err := fresh.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := old.Commit(); !errors.Is(err, ErrConflict) {
				t.Fatalf("conflict: %v", err)
			}
			if st.active != 0 {
				t.Fatalf("conflict leaked capacity: %d", st.active)
			}
			capacityBegin(t, st, ctx).Rollback()
		})
	})
	t.Run("unknown applied commit", func(t *testing.T) {
		s := New(&Script{Faults: []Scripted{{Kind: "store.db.commit", Name: "unknownOutcome"}}})
		defer s.Close()
		st := NewStore(s, "db", Serializable)
		st.MaxTransactions, st.CommitFaults = 1, true
		s.Spawn("writer", func(ctx context.Context) {
			tx := capacityBegin(t, st, ctx)
			tx.Put("k", 1)
			if err := tx.Commit(); !errors.Is(err, ErrUnknownOutcome) || errors.Is(err, ErrTransactionCapacity) {
				t.Fatalf("unknown applied outcome: %v", err)
			}
			if v, ok := st.Read("k"); !ok || v != 1 {
				t.Fatal("forced unknown commit was not applied")
			}
			capacityBegin(t, st, ctx).Rollback()
		})
		if err := s.Run(time.Second); err != nil {
			t.Fatal(err)
		}
	})
}

func TestStoreCapacityChoicesReplay(t *testing.T) {
	run := func(c Chooser) []string {
		s := New(c)
		defer s.Close()
		st := NewStore(s, "db", Serializable)
		st.MaxTransactions = 1
		var outcomes []string
		s.Spawn("client", func(ctx context.Context) {
			held := capacityBegin(t, st, ctx)
			_, err := st.Begin(ctx)
			if !errors.Is(err, ErrTransactionCapacity) {
				t.Fatalf("forced capacity refusal missing: %v", err)
			}
			outcomes = append(outcomes, err.Error())
			held.Rollback()
			capacityBegin(t, st, ctx).Rollback()
		})
		if err := s.Run(time.Second); err != nil {
			t.Fatal(err)
		}
		return outcomes
	}
	log := &ChoiceLog{C: NewSeeded(7)}
	want := run(log)
	replay := Replay(log.Choices)
	got := run(replay)
	if replay.Err() != nil || replay.Left() != 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("replay: outcomes=%v want=%v error=%v left=%d", got, want, replay.Err(), replay.Left())
	}
}

func TestStoreCapacityCrashReleasesOnlyItsLease(t *testing.T) {
	s := New(Nominal{})
	defer s.Close()
	st := NewStore(s, "db", Serializable)
	st.MaxTransactions = 1
	var stale Txn
	ready := false
	p := s.Process("holder", false, func(ctx context.Context) {
		stale = capacityBegin(t, st, ctx)
		stale.Put("uncommitted", 1)
		ready = true
		Wait(ctx, func() bool { return false })
	})
	s.Spawn("supervisor", func(ctx context.Context) {
		Wait(ctx, func() bool { return ready })
		p.Crash()
		fresh := capacityBegin(t, st, ctx)
		stale.Rollback()
		if tx, err := st.Begin(ctx); tx != nil || !errors.Is(err, ErrTransactionCapacity) {
			t.Fatalf("crashed transaction cleanup freed fresh lease: %v, %v", tx, err)
		}
		if _, ok := st.Read("uncommitted"); ok {
			t.Fatal("crashed transaction applied writes")
		}
		fresh.Rollback()
		capacityBegin(t, st, ctx).Rollback()
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
}

type capacityCancelOnSchedule struct {
	cancel context.CancelFunc
	armed  bool
}

func (c *capacityCancelOnSchedule) Choose(kind string, _ int) int {
	if c.armed && kind == "sched.task" {
		c.cancel()
	}
	return 0
}

func TestStoreCapacityCancellationDuringAdmissionSeam(t *testing.T) {
	c := &capacityCancelOnSchedule{}
	s := New(c)
	defer s.Close()
	st := NewStore(s, "db", Serializable)
	s.Spawn("client", func(ctx context.Context) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		c.cancel, c.armed = cancel, true
		tx, err := st.Begin(ctx)
		if tx != nil || !errors.Is(err, context.Canceled) || st.active != 0 || st.txs != 0 {
			t.Fatalf("admission ignored cancellation at its seam: tx=%v error=%v active=%d ids=%d", tx, err, st.active, st.txs)
		}
	})
	// A second runnable task makes the scheduler consult the chooser at Yield.
	s.Spawn("other", func(context.Context) {})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
}

type capacityRefusingDB struct{ calls int }

func (db *capacityRefusingDB) Begin(context.Context) (Txn, error) {
	db.calls++
	return nil, &TransactionCapacityError{Store: "full", Limit: 1, Active: 1}
}

func TestStoreCapacityRetryDoesNotRunOrRetryCallback(t *testing.T) {
	db := &capacityRefusingDB{}
	called := false
	err := Retry(t.Context(), db, 5, func(Txn) error { called = true; return nil })
	if !errors.Is(err, ErrTransactionCapacity) || called || db.calls != 1 {
		t.Fatalf("refused Retry: error=%v callback=%v admissions=%d", err, called, db.calls)
	}
}

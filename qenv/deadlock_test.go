package qenv_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Two transfers lock the same rows in opposite orders. Without detection
// both waited forever and the run recorded nothing; now the transaction
// that closes the cycle aborts with ErrDeadlock (an ErrConflict), and Retry
// lets both commit.
func TestStoreDetectsDeadlock(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	st := qenv.NewStore(s, "db", qenv.Serializable)
	st.Load("a", 10)
	st.Load("b", 10)
	var deadlocks int
	committed := 0
	transfer := func(first, second string) func(context.Context) {
		return func(ctx context.Context) {
			err := qenv.Retry(ctx, st, 5, func(tx qenv.Txn) error {
				if _, _, err := tx.GetForUpdate(first); err != nil {
					return err
				}
				qenv.Sleep(ctx, 10*time.Millisecond) // both hold their first lock
				_, _, err := tx.GetForUpdate(second)
				if errors.Is(err, qenv.ErrDeadlock) {
					deadlocks++
				}
				return err
			})
			if err != nil {
				t.Errorf("%s then %s: %v", first, second, err)
				return
			}
			committed++
		}
	}
	s.Spawn("ab", transfer("a", "b"))
	s.Spawn("ba", transfer("b", "a"))
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if deadlocks != 1 || committed != 2 {
		t.Fatalf("%d deadlocks, %d committed; want 1 and 2", deadlocks, committed)
	}
}

// A commit's wait for a lock is a lock wait the deadlock check sees: T2
// commits a write of a, locked by T1, while holding b; T1 then locks b.
// The check once followed only GetForUpdate's waits, and both waited
// forever. On the native runtime too, where a waiter re-reads its locks
// only when woken: the check follows its keys, not a key it saw.
func TestStoreDetectsDeadlockThroughCommit(t *testing.T) {
	t.Run("scheduler", func(t *testing.T) { deadlockThroughCommit(t, qenv.New(qenv.Nominal{}), time.Minute) })
	// the native runtime's Run lasts its whole time, in real time
	t.Run("native", func(t *testing.T) { deadlockThroughCommit(t, qenv.NewNative(qenv.Nominal{}, nil), time.Second) })
}

func deadlockThroughCommit(t *testing.T, s *qenv.Sim, d time.Duration) {
	st := qenv.NewStore(s, "db", qenv.Serializable)
	st.Load("a", 10)
	st.Load("b", 10)
	var t1, t2 error
	s.Spawn("t1", func(ctx context.Context) {
		tx := st.Begin(ctx)
		if _, _, err := tx.GetForUpdate("a"); err != nil {
			t1 = err
			return
		}
		qenv.Sleep(ctx, 20*time.Millisecond) // t2 is waiting in its commit
		if _, _, err := tx.GetForUpdate("b"); err != nil {
			t1 = err
			return
		}
		t1 = tx.Commit()
	})
	s.Spawn("t2", func(ctx context.Context) {
		tx := st.Begin(ctx)
		if _, _, err := tx.GetForUpdate("b"); err != nil {
			t2 = err
			return
		}
		qenv.Sleep(ctx, 10*time.Millisecond) // t1 holds a
		tx.Put("a", 1)
		t2 = tx.Commit()
	})
	if err := s.Run(d); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if !errors.Is(t1, qenv.ErrDeadlock) || t2 != nil {
		t.Fatalf("t1: %v, t2: %v; want t1 a deadlock and t2 committed", t1, t2)
	}
}

// A commit writes every key at once, when none is locked: waiting for each
// in turn let another transaction lock a key already waited for, and the
// commit wrote under its lock, a lost update at ReadCommitted.
func TestCommitWaitsForAllLocks(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	st := qenv.NewStore(s, "db", qenv.ReadCommitted)
	st.Load("a", 10)
	st.Load("b", 10)
	s.Spawn("holder", func(ctx context.Context) { // holds b until 20ms
		tx := st.Begin(ctx)
		if _, _, err := tx.GetForUpdate("b"); err != nil {
			t.Error(err)
			return
		}
		qenv.Sleep(ctx, 20*time.Millisecond)
		if err := tx.Commit(); err != nil {
			t.Error(err)
		}
	})
	s.Spawn("writer", func(ctx context.Context) { // writes a and b, waits for b
		qenv.Sleep(ctx, 5*time.Millisecond)
		tx := st.Begin(ctx)
		tx.Put("a", 100)
		tx.Put("b", 1)
		if err := tx.Commit(); err != nil {
			t.Error(err)
		}
	})
	s.Spawn("incrementer", func(ctx context.Context) { // locks a at 10ms until 40ms
		qenv.Sleep(ctx, 10*time.Millisecond)
		tx := st.Begin(ctx)
		v, _, err := tx.GetForUpdate("a")
		if err != nil {
			t.Error(err)
			return
		}
		qenv.Sleep(ctx, 30*time.Millisecond)
		tx.Put("a", v.(int)+1)
		if err := tx.Commit(); err != nil {
			t.Error(err)
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if a, _ := st.Read("a"); a != 100 {
		t.Fatalf("a = %v, want 100: the writer's commit came first and was lost", a)
	}
}

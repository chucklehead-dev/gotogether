package qenv_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// put commits key=val in its own transaction.
func put(ctx context.Context, st *qenv.Store, key string, val any) error {
	tx := st.Begin(ctx)
	defer tx.Rollback()
	tx.Put(key, val)
	return tx.Commit()
}

// A failover with loseTail, scripted at the fifth commit: that commit is
// applied and answered unknown, then it and the two acknowledged before it
// are gone (asynchronous replication's tail); a transaction open at the
// failover is reset (ErrUnavailable at its commit, nothing applied); the
// promoted replica refuses writes for its read-only window and takes them
// after. Option failover alone loses nothing. Option 0 (no Failovers)
// fails nothing over.
func TestFailoverLosesTail(t *testing.T) {
	type result struct {
		errs        []error
		keys        []string
		openErr     error
		readOnlyErr error
		afterErr    error
		lost        int
	}
	run := func(option string, failovers bool) result {
		var r result
		commits := 0
		sc := &qenv.Script{Faults: []qenv.Scripted{{Kind: "store.db.failover", Name: option, After: 4}}, Progress: func() int { return commits }}
		if !failovers {
			sc = &qenv.Script{}
		}
		s := qenv.New(sc)
		defer s.Close()
		s.Check = true // the history checker starts afresh after the lost tail
		st := qenv.NewStore(s, "db", qenv.Serializable)
		if failovers {
			st.Failovers(qenv.Failover{LoseTail: 2, ReadOnly: 5 * time.Second})
		}
		s.Spawn("client", func(ctx context.Context) {
			open := st.Begin(ctx)
			open.Get("z")
			open.Put("open", true)
			for i := 1; i <= 5; i++ {
				if i == 5 {
					commits = 4 // the script's fault is due at the fifth commit
				}
				r.errs = append(r.errs, put(ctx, st, fmt.Sprintf("k%d", i), i))
			}
			r.openErr = open.Commit()
			r.readOnlyErr = put(ctx, st, "during", true)
			qenv.Sleep(ctx, 6*time.Second)
			r.afterErr = put(ctx, st, "after", true)
			r.keys = st.Keys("")
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		r.lost = st.Lost()
		return r
	}
	r := run("loseTail", true)
	for i, err := range r.errs[:4] {
		if err != nil {
			t.Errorf("loseTail: commit %d: %v, want it acknowledged", i+1, err)
		}
	}
	if !errors.Is(r.errs[4], qenv.ErrUnknownOutcome) {
		t.Errorf("loseTail: the commit in flight answered %v, want ErrUnknownOutcome", r.errs[4])
	}
	if got := fmt.Sprint(r.keys); got != "[after k1 k2]" || r.lost != 3 {
		t.Errorf("loseTail: keys %s, %d lost; want [after k1 k2] (k3 and k4, acknowledged, and k5, in flight, lost), 3 lost", got, r.lost)
	}
	if !errors.Is(r.openErr, qenv.ErrUnavailable) || !errors.Is(r.readOnlyErr, qenv.ErrUnavailable) || r.afterErr != nil {
		t.Errorf("loseTail: the open transaction's commit %v, a write in the read-only window %v, after it %v; want ErrUnavailable, ErrUnavailable, nil", r.openErr, r.readOnlyErr, r.afterErr)
	}
	r = run("failover", true)
	if got := fmt.Sprint(r.keys); !errors.Is(r.errs[4], qenv.ErrUnknownOutcome) || got != "[after k1 k2 k3 k4 k5]" || r.lost != 0 {
		t.Errorf("failover: the fifth commit %v, keys %s, %d lost; want ErrUnknownOutcome, every key, none lost", r.errs[4], got, r.lost)
	}
	r = run("", false)
	if got := fmt.Sprint(r.keys); got != "[after during k1 k2 k3 k4 k5 open]" || r.openErr != nil {
		t.Errorf("no failovers: keys %s, the open transaction's commit %v; want every key, nil", got, r.openErr)
	}
}

// A transaction waiting for a lock held by one a failover resets is woken
// (the lock released) and is reset itself: it locks nothing, and the
// store takes a new transaction's lock at once.
func TestFailoverReleasesLocks(t *testing.T) {
	s := qenv.New(qenv.NewSeeded(1))
	defer s.Close()
	st := qenv.NewStore(s, "db", qenv.ReadCommitted)
	var holderErr, waiterErr, freshErr error
	s.Spawn("holder", func(ctx context.Context) {
		tx := st.Begin(ctx)
		if _, _, err := tx.GetForUpdate("k"); err != nil {
			t.Error(err)
		}
		qenv.Sleep(ctx, time.Second)
		holderErr = tx.Commit()
	})
	s.Spawn("waiter", func(ctx context.Context) {
		qenv.Sleep(ctx, 100*time.Millisecond)
		tx := st.Begin(ctx)
		_, _, waiterErr = tx.GetForUpdate("k")
		tx.Rollback()
	})
	s.Spawn("operator", func(ctx context.Context) {
		qenv.Sleep(ctx, 200*time.Millisecond)
		st.FailOver(0, 0)
		tx := st.Begin(ctx)
		_, _, freshErr = tx.GetForUpdate("k")
		tx.Rollback()
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(holderErr, qenv.ErrUnavailable) || !errors.Is(waiterErr, qenv.ErrUnavailable) || freshErr != nil {
		t.Errorf("the holder's commit %v, the waiter's lock %v, a new transaction's %v; want ErrUnavailable, ErrUnavailable, nil", holderErr, waiterErr, freshErr)
	}
}

// The restore nemesis puts the store back to its latest backup (taken at
// the first commit of each period), once a run, every later commit gone;
// the history checker (GOTOGETHER_CHECK) starts afresh from the restored
// state, so commits after it check clean. Without Restores, no nemesis.
func TestRestoreNemesis(t *testing.T) {
	run := func(restores bool) (keys []string, restored int, err error) {
		steps := 0
		sc := &qenv.Script{Faults: []qenv.Scripted{{Kind: "nemesis.restore.db", After: 3}}, Progress: func() int { return steps }}
		s := qenv.New(sc)
		defer s.Close()
		s.Check = true
		st := qenv.NewStore(s, "db", qenv.SnapshotIsolation)
		if restores {
			st.Restores(10*time.Second, 1)
		}
		s.Spawn("client", func(ctx context.Context) {
			for i := 1; i <= 6; i++ {
				// a read-modify-write of one counter: after the restore a
				// version the old history saw overwritten is current again
				tx := st.Begin(ctx)
				v, _, _ := tx.GetForUpdate("n")
				n, _ := v.(int)
				tx.Put("n", n+1)
				tx.Put(fmt.Sprintf("k%d", i), i)
				if e := tx.Commit(); e != nil && err == nil {
					err = e
				}
				steps++
				qenv.Sleep(ctx, 4*time.Second) // backups at the commits of 0s and 12s
			}
			keys = st.Keys("k")
		})
		if e := s.Run(time.Minute); e != nil {
			t.Fatal(e)
		}
		return keys, st.Restored(), err
	}
	keys, restored, err := run(true)
	if err != nil || restored != 1 || len(keys) >= 6 || len(keys) < 3 {
		t.Errorf("with Restores: keys %v, %d restores, error %v; want one restore losing the commits after a backup (fewer than 6 keys, at least the 3 after it)", keys, restored, err)
	}
	keys, restored, err = run(false)
	if err != nil || restored != 0 || len(keys) != 6 {
		t.Errorf("without Restores: keys %v, %d restores, error %v; want every key, none", keys, restored, err)
	}
}

// Restore without a backup does nothing; Backup and Restore put the store
// back exactly, and a transaction open across it is reset.
func TestBackupRestore(t *testing.T) {
	s := qenv.New(qenv.NewSeeded(1))
	defer s.Close()
	st := qenv.NewStore(s, "db", qenv.Serializable)
	s.Spawn("client", func(ctx context.Context) {
		if st.Restore() {
			t.Error("Restore with no backup restored")
		}
		put(ctx, st, "a", 1)
		st.Backup()
		put(ctx, st, "b", 2)
		put(ctx, st, "a", 3)
		open := st.Begin(ctx)
		open.Put("c", 4)
		if !st.Restore() {
			t.Error("Restore after Backup did not restore")
		}
		if err := open.Commit(); !errors.Is(err, qenv.ErrUnavailable) {
			t.Errorf("a transaction open across the restore committed: %v", err)
		}
		v, _ := st.Read("a")
		if got := fmt.Sprint(st.Keys(""), v); got != "[a] 1" {
			t.Errorf("after the restore: %s, want [a] 1", got)
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
}

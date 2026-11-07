package qenv_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

func TestFailoverConfigurationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*qenv.Store)
		panics    bool
	}{
		{"zero failover", func(st *qenv.Store) { st.Failovers(qenv.Failover{}) }, false},
		{"zero tail", func(st *qenv.Store) { st.Failovers(qenv.Failover{ReadOnly: time.Second}) }, false},
		{"zero read-only", func(st *qenv.Store) { st.Failovers(qenv.Failover{LoseTail: 1}) }, false},
		{"negative tail", func(st *qenv.Store) { st.Failovers(qenv.Failover{LoseTail: -1}) }, true},
		{"negative read-only", func(st *qenv.Store) { st.Failovers(qenv.Failover{ReadOnly: -time.Second}) }, true},
		{"zero backup period", func(st *qenv.Store) { st.Restores(0, 1) }, false},
		{"negative backup period", func(st *qenv.Store) { st.Restores(-time.Second, 1) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			defer s.Close()
			st := qenv.NewStore(s, "db", qenv.Serializable)
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				tc.configure(st)
			}()
			if panicked != tc.panics {
				t.Fatalf("configuration panicked=%v, want %v", panicked, tc.panics)
			}
		})
	}
}

func TestRestoreIncidentLimit(t *testing.T) {
	for _, max := range []int{-1, 0, 2} {
		t.Run(fmt.Sprint(max), func(t *testing.T) {
			commits := 0
			script := &qenv.Script{Progress: func() int { return commits }, Faults: []qenv.Scripted{
				{Kind: "nemesis.restore.db", After: 1},
				{Kind: "nemesis.restore.db", After: 3},
				{Kind: "nemesis.restore.db", After: 5},
			}}
			s := qenv.New(script)
			defer s.Close()
			st := qenv.NewStore(s, "db", qenv.Serializable)
			st.Restores(time.Hour, max)
			s.Spawn("writer", func(ctx context.Context) {
				for range 8 {
					if err := put(ctx, st, "key", commits); err != nil {
						t.Error(err)
					}
					commits++
					qenv.Sleep(ctx, time.Millisecond)
				}
			})
			if err := s.Run(time.Second); err != nil {
				t.Fatal(err)
			}
			want := max
			if want <= 0 {
				want = 1
			}
			if st.Restored() != want || len(script.Remaining()) != 3-want {
				t.Fatalf("restored=%d remaining=%v; want %d restores and %d unperformed incidents", st.Restored(), script.Remaining(), want, 3-want)
			}
		})
	}
}

func TestRestoredHistoryReadsUntouchedKey(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	s.Check = true
	st := qenv.NewStore(s, "db", qenv.Serializable)
	s.Spawn("reader", func(ctx context.Context) {
		if err := put(ctx, st, "retained", 1); err != nil {
			t.Error(err)
		}
		st.Backup()
		if err := put(ctx, st, "retained", 2); err != nil {
			t.Error(err)
		}
		if !st.Restore() {
			t.Error("backup was not restored")
		}
		tx := mustBegin(t, st, ctx)
		defer tx.Rollback()
		v, ok := tx.Get("retained")
		if !ok || v != 1 {
			t.Errorf("restored read = %v, %v; want 1,true", v, ok)
		}
		if err := tx.Commit(); err != nil {
			t.Errorf("restored read-only transaction: %v", err)
		}
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRollbackEventsWakeChangedWaiter(t *testing.T) {
	for _, event := range []string{"failover", "restore"} {
		t.Run(event, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			defer s.Close()
			st := qenv.NewStore(s, "db", qenv.Serializable)
			ready, woke := false, false
			s.Spawn("consumer", func(ctx context.Context) {
				if err := put(ctx, st, "key", 1); err != nil {
					t.Error(err)
				}
				st.Backup()
				if err := put(ctx, st, "key", 2); err != nil {
					t.Error(err)
				}
				ready = true
				woke = st.Changed().Wait(ctx, time.Second)
				v, _ := st.Read("key")
				if v != 1 {
					t.Errorf("rollback event left key=%v; want 1", v)
				}
			})
			s.Spawn("operator", func(ctx context.Context) {
				qenv.Wait(ctx, func() bool { return ready })
				qenv.Sleep(ctx, time.Millisecond)
				if event == "restore" {
					if !st.Restore() {
						t.Error("restore had no backup")
					}
				} else if lost := st.FailOver(1, 0); lost != 1 {
					t.Errorf("lost=%d; want 1", lost)
				}
			})
			if err := s.Run(2 * time.Second); err != nil {
				t.Fatal(err)
			}
			if !woke {
				t.Fatal("waiting consumer was not notified of changed data")
			}
		})
	}
}

func TestReadOnlyRejectionReleasesTransactionCapacity(t *testing.T) {
	for _, operation := range []string{"commit", "lock"} {
		t.Run(operation, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			defer s.Close()
			st := qenv.NewStore(s, "db", qenv.Serializable)
			st.MaxTransactions = 1
			s.Spawn("client", func(ctx context.Context) {
				st.FailOver(0, time.Second)
				tx := mustBegin(t, st, ctx)
				var err error
				if operation == "lock" {
					_, _, err = tx.GetForUpdate("key")
				} else {
					tx.Put("key", 1)
					err = tx.Commit()
				}
				if !errors.Is(err, qenv.ErrUnavailable) {
					t.Errorf("read-only %s returned %v", operation, err)
				}
				// Rejection ends the transaction. Its caller need not roll back
				// before retrying or admitting an unrelated transaction.
				fresh, err := st.Begin(ctx)
				if err != nil {
					t.Errorf("rejected transaction retained capacity: %v", err)
					return
				}
				fresh.Rollback()
				if _, ok := st.Read("key"); ok {
					t.Error("rejected write was applied")
				}
			})
			if err := s.Run(2 * time.Second); err != nil {
				t.Fatal(err)
			}
		})
	}
}

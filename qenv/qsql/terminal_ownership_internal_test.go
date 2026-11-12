package qsql

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

func terminalOwnershipDB(t *testing.T) (*qenv.Sim, *qenv.Store, *sql.DB, *connector) {
	t.Helper()
	s := qenv.New(qenv.Nominal{})
	s.Check = true
	t.Cleanup(s.Close)
	st := qenv.NewStore(s, "owned", qenv.ReadCommitted)
	db, c := open(s, st, Options{})
	t.Cleanup(func() {
		s.Close()
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	mustExec(t, db, `CREATE TABLE accounts (id TEXT PRIMARY KEY, value BIGINT)`)
	mustExec(t, db, `INSERT INTO accounts VALUES ('one', 1)`)
	return s, st, db, c
}

func assertNoOwnedSQLTransactions(t *testing.T, c *connector) {
	t.Helper()
	for tx := range c.live {
		t.Errorf("terminal transaction retained in cancellation roster: id=%d ended=%v context=%v", tx.id, tx.ended, tx.ctx.Err())
	}
}

func TestSQLTerminalTransactionsLeaveCancellationRoster(t *testing.T) {
	for _, outcome := range []string{"commit", "failed statement"} {
		t.Run(outcome, func(t *testing.T) {
			s, _, db, c := terminalOwnershipDB(t)
			completed := false
			s.Spawn("client", func(ctx context.Context) {
				ctx, cancel := context.WithCancel(ctx)
				defer cancel()
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer tx.Rollback()
				if outcome == "commit" {
					if _, err := tx.ExecContext(ctx, `UPDATE accounts SET value=2 WHERE id='one'`); err != nil {
						t.Error(err)
						return
					}
					if err := tx.Commit(); err != nil {
						t.Error(err)
						return
					}
				} else {
					_, err := tx.ExecContext(ctx, `INSERT INTO accounts VALUES ('one', 3)`)
					if state, ok := errors.AsType[*Error](err); !ok || state.Code != "23505" {
						t.Errorf("duplicate statement: %v; want 23505", err)
						return
					}
				}
				// Inspect ownership before deferred cancellation can make a
				// stale terminal entry look like work for the reaper.
				assertNoOwnedSQLTransactions(t, c)
				completed = true
			})
			if err := s.Run(time.Second); err != nil {
				t.Fatal(err)
			}
			if !completed {
				t.Fatal("client did not reach its terminal SQL outcome")
			}
			assertNoOwnedSQLTransactions(t, c)
		})
	}
}

func TestSQLCancelledTransactionStaysOwnedUntilReaped(t *testing.T) {
	s, _, db, c := terminalOwnershipDB(t)
	cancelled := false
	s.Spawn("client", func(ctx context.Context) {
		txctx, cancel := context.WithCancel(ctx)
		defer cancel()
		tx, err := db.BeginTx(txctx, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback()
		cancel()
		// Cancellation itself does not run a simulated Store rollback on
		// database/sql's background goroutine. Its task still owns cleanup.
		if len(c.live) != 1 {
			t.Errorf("unreaped cancellation has %d owners, want 1", len(c.live))
		}
		for pending := range c.live {
			if pending.ended {
				t.Error("cancellation marked the Store transaction ended before reaping")
			}
		}
		cancelled = true
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if !cancelled {
		t.Fatal("client did not cancel its transaction")
	}
	assertNoOwnedSQLTransactions(t, c)
}

func TestSQLUnwoundTransactionLeavesCancellationRoster(t *testing.T) {
	s, st, db, c := terminalOwnershipDB(t)
	holder := Open(s, st)
	t.Cleanup(func() {
		s.Close()
		if err := holder.Close(); err != nil {
			t.Error(err)
		}
	})
	held, entered := false, false
	s.Spawn("holder", func(ctx context.Context) {
		tx, err := holder.BeginTx(ctx, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET value=2 WHERE id='one'`); err != nil {
			t.Error(err)
			return
		}
		held = true
		qenv.Sleep(ctx, time.Hour)
	})
	var got error
	returned := false
	p := s.Process("client", true, func(ctx context.Context) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback()
		qenv.Wait(ctx, func() bool { return held })
		entered = true
		_, got = tx.ExecContext(ctx, `UPDATE accounts SET value=3 WHERE id='one'`)
		returned = true
	})
	s.Spawn("crash", func(ctx context.Context) {
		qenv.Wait(ctx, func() bool { return entered })
		qenv.Sleep(ctx, time.Second)
		p.Crash()
	})
	if err := s.Run(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if !p.Down() {
		t.Fatal("client did not crash")
	}
	s.Close() // joins all parked tasks; no particular traversal order is needed
	if !returned {
		t.Fatal("crashed statement did not return during unwind")
	}
	if state, ok := errors.AsType[*Error](got); !ok || state.Code != "08006" {
		t.Errorf("unwound statement: %v; want 08006", got)
	}
	assertNoOwnedSQLTransactions(t, c)
}

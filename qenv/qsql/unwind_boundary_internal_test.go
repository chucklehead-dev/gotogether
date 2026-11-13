package qsql

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// One task may own multiple transactions on separate pool connections. If it
// dies inside one driver call, its later cleanup of another handle must still
// release that handle's cancellation ownership, without touching the Store
// from the already-dead task.
func TestSQLUnwindingTaskEndsEachOwnedTransaction(t *testing.T) {
	for _, cleanup := range []string{"commit", "rollback"} {
		t.Run(cleanup, func(t *testing.T) {
			s, st, db, c := terminalOwnershipDB(t)
			holder := Open(s, st)
			t.Cleanup(func() {
				s.Close()
				if err := holder.Close(); err != nil {
					t.Error(err)
				}
			})
			held, entered, returned, cleaned := false, false, false, false
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
			p := s.Process("client", true, func(ctx context.Context) {
				ctx, cancel := context.WithCancel(ctx)
				defer cancel()
				other, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer func() {
					if cleanup == "commit" {
						err := other.Commit()
						if state, ok := errors.AsType[*Error](err); !ok || state.Code != "08006" {
							t.Errorf("dead-task commit: %v; want 08006", err)
						}
					} else if err := other.Rollback(); err != nil {
						t.Errorf("dead-task rollback: %v", err)
					}
					assertNoOwnedSQLTransactions(t, c)
					cleaned = true
				}()
				blocked, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer blocked.Rollback()
				qenv.Wait(ctx, func() bool { return held })
				entered = true
				_, err = blocked.ExecContext(ctx, `UPDATE accounts SET value=3 WHERE id='one'`)
				if state, ok := errors.AsType[*Error](err); !ok || state.Code != "08006" {
					t.Errorf("unwound statement: %v; want 08006", err)
				}
				if len(c.live) != 1 {
					t.Errorf("terminal statement left %d roster owners, want the one unfinished sibling", len(c.live))
				}
				for pending := range c.live {
					if pending.ended {
						t.Error("terminal driver handle retained before deferred cleanup")
					}
				}
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
			s.Close()
			if !returned || !cleaned {
				t.Fatalf("terminal boundaries not reached: statement=%t sibling cleanup=%t", returned, cleaned)
			}
			assertNoOwnedSQLTransactions(t, c)
		})
	}
}

// A transaction handle can outlive the task that was interrupted inside the
// driver. A later caller must learn the abort's cause rather than mistake it
// for a transaction that simply completed.
func TestSQLInterruptedTransactionRetainsAbortCause(t *testing.T) {
	s, st, db, _ := terminalOwnershipDB(t)
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
	var abandoned *sql.Tx
	var interrupted error
	p := s.Process("client", true, func(ctx context.Context) {
		var err error
		abandoned, err = db.BeginTx(ctx, nil)
		if err != nil {
			t.Error(err)
			return
		}
		qenv.Wait(ctx, func() bool { return held })
		entered = true
		_, interrupted = abandoned.ExecContext(ctx, `UPDATE accounts SET value=3 WHERE id='one'`)
	})
	s.Spawn("crash", func(ctx context.Context) {
		qenv.Wait(ctx, func() bool { return entered })
		qenv.Sleep(ctx, time.Second)
		p.Crash()
	})
	if err := s.Run(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if !p.Down() || abandoned == nil {
		t.Fatal("interrupted transaction not established")
	}
	if state, ok := errors.AsType[*Error](interrupted); !ok || state.Code != "08006" {
		t.Fatalf("interrupted statement: %v; want 08006", interrupted)
	}
	_, err := abandoned.ExecContext(context.Background(), `SELECT value FROM accounts WHERE id='one'`)
	state, ok := errors.AsType[*Error](err)
	if !ok || state.Code != "25P02" {
		t.Fatalf("reuse of interrupted transaction: %v; want 25P02", err)
	}
	cause, ok := errors.AsType[*Error](errors.Unwrap(state))
	if !ok || cause.Code != "08006" {
		t.Fatalf("abort cause: %v; want 08006", errors.Unwrap(state))
	}
	if err := abandoned.Rollback(); err != nil {
		t.Fatal(err)
	}
}

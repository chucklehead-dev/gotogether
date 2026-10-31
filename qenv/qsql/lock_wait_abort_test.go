package qsql_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

// Cancelling the transaction whose statement is asleep must wake that statement,
// not merely release locks the cancelled transaction held for someone else.
// The driver's post-yield error remains ErrTxDone, even though the Store's
// interrupted lock wait returns a conflict. No changes or hooks may commit.
func TestCancelledSQLLockWaitReturnsPromptly(t *testing.T) {
	for _, mode := range []string{"update", "for update", "commit"} {
		t.Run(mode, func(t *testing.T) {
			for seed := uint64(0); seed < 10; seed++ {
				run := func(c qenv.Chooser) (time.Duration, error, int64, int) {
					commits := 0
					w := newWorld(t, c, qenv.ReadCommitted, qsql.Options{OnCommit: func(context.Context, []qsql.Change) { commits++ }})
					if mode == "commit" {
						w.exec(t, `CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT UNIQUE)`)
						w.exec(t, `INSERT INTO users VALUES ('alice','old')`)
					}
					commits = 0
					held, started, prepared := false, false, false
					var cancel context.CancelFunc
					var ended time.Duration
					var got error
					var sqltx *sql.Tx
					w.s.Spawn("holder", func(ctx context.Context) {
						if mode == "commit" {
							qenv.Wait(ctx, func() bool { return prepared })
							tx := w.st.Begin(ctx)
							keys := w.st.Keys("users!email/")
							if len(keys) != 1 {
								t.Errorf("old index keys: %v", keys)
								return
							}
							if _, _, err := tx.GetForUpdate(keys[0]); err != nil {
								t.Error(err)
								return
							}
							held = true
							qenv.Sleep(ctx, 10*time.Second)
							tx.Rollback()
							return
						}
						tx, err := w.db.BeginTx(ctx, nil)
						if err != nil {
							t.Error(err)
							return
						}
						if _, err = tx.ExecContext(ctx, `UPDATE accounts SET balance=1 WHERE id='alice'`); err != nil {
							t.Error(err)
							return
						}
						held = true
						qenv.Sleep(ctx, 10*time.Second)
						tx.Rollback()
					})
					w.s.Spawn("waiter", func(ctx context.Context) {
						if mode == "commit" {
							// A changed UNIQUE value deletes its old index entry. A
							// Store client can lock it after this statement, leaving
							// the SQL commit waiting for that key too.
							var err error
							var txctx context.Context
							txctx, cancel = context.WithCancel(ctx)
							sqltx, err = w.db.BeginTx(txctx, nil)
							if err != nil {
								t.Error(err)
								return
							}
							if _, err = sqltx.ExecContext(txctx, `UPDATE users SET email='new' WHERE id='alice'`); err != nil {
								t.Error(err)
								return
							}
							prepared = true
						}
						qenv.Wait(ctx, func() bool { return held })
						if mode == "commit" {
							started = true
							got = sqltx.Commit()
						} else {
							txctx, stop := context.WithCancel(ctx)
							cancel = stop
							tx, err := w.db.BeginTx(txctx, nil)
							if err != nil {
								t.Error(err)
								return
							}
							started = true
							if mode == "update" {
								_, got = tx.ExecContext(txctx, `UPDATE accounts SET balance=2 WHERE id='alice'`)
							} else {
								var balance int64
								got = tx.QueryRowContext(txctx, `SELECT balance FROM accounts WHERE id='alice' FOR UPDATE`).Scan(&balance)
							}
							_ = tx.Rollback()
						}
						ended = w.s.Elapsed()
					})
					w.s.Spawn("cancel", func(ctx context.Context) {
						qenv.Wait(ctx, func() bool { return started })
						qenv.Sleep(ctx, time.Second)
						cancel()
					})
					w.run(t)
					if mode == "commit" {
						rows := qsql.Table(w.st, "users")
						if len(rows) != 1 || rows[0]["email"] != "old" {
							t.Errorf("aborted unique update changed rows: %v", rows)
						}
						if len(w.st.Keys("users!email/")) != 1 {
							t.Error("aborted unique update published its index")
						}
					}
					balance := w.balance(t, "alice")
					w.s.Close()
					return ended, got, balance, commits
				}
				log := &qenv.ChoiceLog{C: qenv.NewSeeded(seed)}
				ended, got, balance, commits := run(log)
				if ended != time.Second || !errors.Is(got, sql.ErrTxDone) || balance != 100 || commits != 0 {
					t.Fatalf("seed%d: ended%v error%v balance%d commits%d; want1s ErrTxDone 100 0", seed, ended, got, balance, commits)
				}
				replay := qenv.Replay(log.Choices)
				replayed := &qenv.ChoiceLog{C: replay}
				end2, err2, balance2, commits2 := run(replayed)
				if replay.Err() != nil || replay.Left() != 0 || !reflect.DeepEqual(replayed.Choices, log.Choices) || end2 != ended || !errors.Is(err2, sql.ErrTxDone) || balance2 != balance || commits2 != commits {
					t.Fatalf("seed%d replay: %v %v balance%d commits%d choices%v", seed, end2, err2, balance2, commits2, replay.Err())
				}
			}
		})
	}
}

// Reset has its existing unavailable (25006) meaning, not a serialization
// conflict. Fresh work after the reset cannot keep an old connection asleep.
func TestResetSQLLockWaitReturnsUnavailable(t *testing.T) {
	for _, statement := range []string{`UPDATE accounts SET balance=2 WHERE id='alice'`, `SELECT balance FROM accounts WHERE id='alice' FOR UPDATE`} {
		t.Run(fmt.Sprint(strings.HasPrefix(statement, "SELECT")), func(t *testing.T) {
			w := newWorld(t, lastSQLLockWaitTask{}, qenv.ReadCommitted)
			held, started, freshHeld := false, false, false
			var ended time.Duration
			var got error
			w.s.Spawn("holder", func(ctx context.Context) {
				tx, err := w.db.BeginTx(ctx, nil)
				if err != nil {
					t.Error(err)
					return
				}
				if _, err = tx.ExecContext(ctx, `UPDATE accounts SET balance=1 WHERE id='alice'`); err != nil {
					t.Error(err)
					return
				}
				held = true
				qenv.Sleep(ctx, 10*time.Second)
				_ = tx.Rollback()
			})
			w.s.Spawn("waiter", func(ctx context.Context) {
				qenv.Wait(ctx, func() bool { return held })
				tx, err := w.db.BeginTx(ctx, nil)
				if err != nil {
					t.Error(err)
					return
				}
				started = true
				if strings.HasPrefix(statement, "SELECT") {
					var n int64
					got = tx.QueryRowContext(ctx, statement).Scan(&n)
				} else {
					_, got = tx.ExecContext(ctx, statement)
				}
				ended = w.s.Elapsed()
				_ = tx.Rollback()
			})
			w.s.Spawn("reset", func(ctx context.Context) {
				qenv.Wait(ctx, func() bool { return started })
				qenv.Sleep(ctx, time.Second)
				w.st.FailOver(0, 0)
				fresh := w.st.Begin(ctx)
				var key string
				for _, k := range w.st.Keys("accounts/") {
					v, _ := w.st.Read(k)
					if v.(map[string]any)["id"] == "alice" {
						key = k
					}
				}
				if key == "" {
					t.Error("alice row key missing")
					return
				}
				if _, _, err := fresh.GetForUpdate(key); err != nil {
					t.Error(err)
					return
				}
				freshHeld = true
				qenv.Sleep(ctx, 10*time.Second)
				fresh.Rollback()
			})
			w.run(t)
			if !freshHeld || ended != time.Second || code(got) != "25006" || w.balance(t, "alice") != 100 {
				t.Fatalf("reset waiter: %v %v balance%d", ended, got, w.balance(t, "alice"))
			}
		})
	}
}

// Prefer the newest task so the reset operator reacquires the row before the
// old SQL waiter resumes. Fault choices stay nominal.
type lastSQLLockWaitTask struct{}

func (lastSQLLockWaitTask) Choose(string, int) int { return 0 }
func (lastSQLLockWaitTask) ChooseTask(ids []int) int {
	best := 0
	for i, id := range ids {
		if id > ids[best] {
			best = i
		}
	}
	return best
}

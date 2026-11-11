package qsql_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A statement's cancellation aborts its transaction even when the transaction's
// own context is still live. Releasing a competing lock must not revive it.
func TestCancelledStatementAbortsLiveTransaction(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.ReadCommitted)
	held, started := false, false
	var cancel context.CancelFunc
	var statementErr, laterErr, commitErr error
	w.s.Spawn("holder", func(ctx context.Context) {
		tx, err := w.db.BeginTx(ctx, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance=1 WHERE id='alice'`); err != nil {
			t.Error(err)
			return
		}
		held = true
		qenv.Sleep(ctx, 2*time.Second)
	})
	w.s.Spawn("waiter", func(ctx context.Context) {
		qenv.Wait(ctx, func() bool { return held })
		tx, err := w.db.BeginTx(ctx, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance=90 WHERE id='bob'`); err != nil {
			t.Error(err)
			return
		}
		var statementCtx context.Context
		statementCtx, cancel = context.WithCancel(ctx)
		defer cancel()
		started = true
		_, statementErr = tx.ExecContext(statementCtx, `UPDATE accounts SET balance=2 WHERE id='alice'`)
		_, laterErr = tx.ExecContext(ctx, `UPDATE accounts SET balance=80 WHERE id='bob'`)
		commitErr = tx.Commit()
	})
	w.s.Spawn("cancel statement", func(ctx context.Context) {
		qenv.Wait(ctx, func() bool { return started })
		qenv.Sleep(ctx, time.Second)
		cancel()
	})
	w.run(t)
	if code(statementErr) != "57014" || !errors.Is(statementErr, context.Canceled) {
		t.Errorf("cancelled statement error=%v; want 57014 wrapping context.Canceled", statementErr)
	}
	if code(laterErr) != "25P02" || code(commitErr) != "25P02" {
		t.Errorf("after statement cancellation: next statement=%v, commit=%v; want 25P02 for both", laterErr, commitErr)
	}
	if alice, bob := w.balance(t, "alice"), w.balance(t, "bob"); alice != 100 || bob != 100 {
		t.Errorf("aborted transaction changed balances: alice=%d bob=%d; want 100 each", alice, bob)
	}
}

func TestClosedDriverConnectionIsNotReusable(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.Serializable)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		connection := raw.(driver.Conn)
		validator := raw.(driver.Validator)
		if !validator.IsValid() {
			t.Error("new driver connection is invalid")
		}
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
		if validator.IsValid() {
			t.Error("closed driver connection is still reusable")
		}
		return driver.ErrBadConn // database/sql must discard the manually closed connection
	})
	if !errors.Is(err, driver.ErrBadConn) {
		t.Errorf("Raw returned %v, want ErrBadConn", err)
	}
	if err := w.db.PingContext(t.Context()); err != nil {
		t.Errorf("replacement connection: %v", err)
	}
}

func TestCompositeKeyReferencesCannotRepeatColumns(t *testing.T) {
	w := newWorld(t, qenv.Nominal{}, qenv.Serializable)
	w.exec(t, `CREATE TABLE triples (a TEXT, b TEXT, c TEXT, PRIMARY KEY (a,b,c))`)
	w.exec(t, `INSERT INTO triples VALUES ('a','b','c') ON CONFLICT (c,a,b) DO NOTHING`)
	for _, tc := range []struct{ query, state string }{
		{`INSERT INTO triples VALUES ('x','y','z') ON CONFLICT (a,b,a) DO NOTHING`, "42P10"},
		{`SELECT * FROM triples ORDER BY a,a`, "42601"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			defer func() {
				if panicValue := recover(); panicValue != nil {
					t.Errorf("invalid SQL panicked: %v; want SQLSTATE %s", panicValue, tc.state)
				}
			}()
			rows, err := w.db.Query(tc.query)
			if rows != nil {
				rows.Close()
			}
			if code(err) != tc.state {
				t.Errorf("%s: error=%v; want SQLSTATE %s", tc.query, err, tc.state)
			}
		})
	}
}

// A conflict with a row observed absent is a serialization failure. An unrelated
// duplicate, not covered by the transaction's reads, remains a uniqueness error.
func TestSerializableScanClassifiesLaterInsertConflict(t *testing.T) {
	for _, tc := range []struct {
		name, query, otherID, insertID, email, want string
	}{
		{"inside key range", `SELECT id FROM users WHERE id > 'm' AND id < 'z'`, "nancy", "nancy", "new", "40001"},
		{"outside key range", `SELECT id FROM users WHERE id > 'm' AND id < 'z'`, "alice", "alice", "new", "23505"},
		{"whole table includes unique values", `SELECT id FROM users`, "nancy", "other", "taken", "40001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, qenv.Nominal{}, qenv.Serializable)
			w.exec(t, `CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT UNIQUE)`)
			read, inserted := false, false
			var insertErr, conflictErr error
			w.s.Spawn("scanner", func(ctx context.Context) {
				tx, err := w.db.BeginTx(ctx, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer tx.Rollback()
				rows, err := tx.QueryContext(ctx, tc.query)
				if err != nil {
					t.Error(err)
					return
				}
				if rows.Next() {
					t.Error("initial scan unexpectedly found a user")
				}
				if err := rows.Err(); err != nil {
					t.Error(err)
				}
				rows.Close()
				read = true
				qenv.Wait(ctx, func() bool { return inserted })
				_, conflictErr = tx.ExecContext(ctx, `INSERT INTO users VALUES (?, ?)`, tc.insertID, tc.email)
			})
			w.s.Spawn("inserter", func(ctx context.Context) {
				qenv.Wait(ctx, func() bool { return read })
				_, insertErr = w.db.ExecContext(ctx, `INSERT INTO users VALUES (?, 'taken')`, tc.otherID)
				inserted = true
			})
			w.run(t)
			if insertErr != nil || code(conflictErr) != tc.want {
				t.Errorf("concurrent insertion=%v, conflicting insertion=%v; want successful concurrent insert and SQLSTATE %s", insertErr, conflictErr, tc.want)
			}
		})
	}
}

// Finishing a driver transaction restores autocommit on that same connection,
// before database/sql returns it to the pool and calls ResetSession.
func TestDriverTransactionRestoresAutocommit(t *testing.T) {
	for _, finish := range []string{"commit", "rollback"} {
		t.Run(finish, func(t *testing.T) {
			w := newWorld(t, qenv.Nominal{}, qenv.Serializable)
			conn, err := w.db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			err = conn.Raw(func(raw any) error {
				tx, err := raw.(driver.ConnBeginTx).BeginTx(t.Context(), driver.TxOptions{})
				if err != nil {
					return err
				}
				exec := raw.(driver.ExecerContext)
				if _, err := exec.ExecContext(t.Context(), `UPDATE accounts SET balance=90 WHERE id='alice'`, nil); err != nil {
					_ = tx.Rollback()
					return err
				}
				if finish == "commit" {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					return err
				}
				_, err = exec.ExecContext(t.Context(), `UPDATE accounts SET balance=80 WHERE id='bob'`, nil)
				return err
			})
			if err != nil {
				t.Errorf("autocommit after %s: %v", finish, err)
			}
			wantAlice := int64(100)
			if finish == "commit" {
				wantAlice = 90
			}
			if alice, bob := w.balance(t, "alice"), w.balance(t, "bob"); alice != wantAlice || bob != 80 {
				t.Errorf("after %s and autocommit: alice=%d bob=%d; want %d, 80", finish, alice, bob, wantAlice)
			}
		})
	}
}

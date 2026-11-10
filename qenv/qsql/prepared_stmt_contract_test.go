package qsql_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A prepared DML call must return the same usable result or SQL error as a
// direct call. database/sql's non-nil Result wrapper alone does not establish
// that the driver's result supports RowsAffected.
func TestPreparedStmtPublicDMLOutcomes(t *testing.T) {
	t.Run("affected rows and committed values", func(t *testing.T) {
		w := newWorld(t, qenv.Nominal{}, qenv.Serializable)
		w.s.Spawn("client", func(ctx context.Context) {
			stmt, err := w.db.PrepareContext(ctx, `UPDATE accounts SET balance=balance+? WHERE id=?`)
			if err != nil {
				t.Errorf("prepare UPDATE: %v", err)
				return
			}
			defer stmt.Close()
			for _, tc := range []struct {
				id   string
				want int64
			}{{"alice", 1}, {"missing", 0}} {
				result, err := observePreparedExec(ctx, stmt, int64(7), tc.id)
				if err != nil {
					t.Errorf("prepared UPDATE %s: %v", tc.id, err)
					continue
				}
				rows, err := observePreparedRowsAffected(result)
				if err != nil || rows != tc.want {
					t.Errorf("prepared UPDATE %s affected rows=%d, error=%v; want %d, nil", tc.id, rows, err, tc.want)
				}
			}
			// These are separate autocommit reads, not the prepared statement's
			// buffered values: the successful update must already be committed.
			for _, tc := range []struct {
				id   string
				want int64
			}{{"alice", 107}, {"bob", 100}} {
				var balance int64
				err := w.db.QueryRowContext(ctx, `SELECT balance FROM accounts WHERE id=?`, tc.id).Scan(&balance)
				if err != nil || balance != tc.want {
					t.Errorf("committed %s balance=%d, error=%v; want %d, nil", tc.id, balance, err, tc.want)
				}
			}
		})
		w.run(t)
	})

	t.Run("unique error preserves SQLSTATE and data", func(t *testing.T) {
		w := newWorld(t, qenv.Nominal{}, qenv.Serializable)
		w.exec(t, `CREATE TABLE contacts (id TEXT PRIMARY KEY, email TEXT UNIQUE)`)
		w.exec(t, `INSERT INTO contacts VALUES ('first','one@example.test')`)
		w.s.Spawn("client", func(ctx context.Context) {
			stmt, err := w.db.PrepareContext(ctx, `INSERT INTO contacts VALUES (?,?)`)
			if err != nil {
				t.Errorf("prepare INSERT: %v", err)
				return
			}
			defer stmt.Close()
			result, err := observePreparedExec(ctx, stmt, "second", "one@example.test")
			if code(err) != "23505" {
				t.Errorf("prepared duplicate INSERT error=%v, SQLSTATE=%q; want 23505", err, code(err))
			}
			if result != nil {
				t.Errorf("prepared failed INSERT returned a result: %T", result)
			}
			var count int64
			if err := w.db.QueryRowContext(ctx, `SELECT count(*) FROM contacts`).Scan(&count); err != nil || count != 1 {
				t.Errorf("rows committed after prepared duplicate=%d, error=%v; want 1, nil", count, err)
			}
			var email string
			if err := w.db.QueryRowContext(ctx, `SELECT email FROM contacts WHERE id=?`, "first").Scan(&email); err != nil || email != "one@example.test" {
				t.Errorf("retained committed email=%q, error=%v", email, err)
			}
		})
		w.run(t)
	})
}

// Recover only the public invocation under observation, so an unusable API
// result becomes an explicit contract assertion without hiding test/setup bugs.
func observePreparedExec(ctx context.Context, stmt *sql.Stmt, args ...any) (result sql.Result, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("Stmt.ExecContext did not return a result/error: %v", value)
		}
	}()
	return stmt.ExecContext(ctx, args...)
}

func observePreparedRowsAffected(result sql.Result) (rows int64, err error) {
	if result == nil {
		return 0, fmt.Errorf("successful prepared execution returned no result")
	}
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("prepared result has unusable RowsAffected: %v", value)
		}
	}()
	return result.RowsAffected()
}

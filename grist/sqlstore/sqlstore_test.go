package sqlstore_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // the PostgreSQL tier's driver, a test's only
	_ "modernc.org/sqlite"             // the SQLite tier's driver, a test's only

	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/grist/storetest"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
)

// The sqlstore on qsql keeps the store contract at each of qenv.Store's
// isolation levels, with the Store's history checked against its level
// at every commit (as GOTOGETHER_CHECK=1 does): the statements the real
// tier runs, interleaved statement by statement. Under Snapshot and
// Serializable a lost race for the head lock is a 40001, transient.
func TestContractOnQSQL(t *testing.T) {
	for _, iso := range []qenv.Isolation{qenv.Serializable, qenv.SnapshotIsolation, qenv.ReadCommitted} {
		t.Run(iso.String(), func(t *testing.T) {
			t.Parallel()
			storetest.Run(t, func(t *testing.T, body func(ctx context.Context, s store.Store) error) {
				sim := qenv.New(qenv.NewSeeded(1))
				defer sim.Close()
				sim.Check = true
				db := qsql.Open(sim, qenv.NewStore(sim, "grist", iso))
				var err error
				sim.Spawn("contract", func(ctx context.Context) {
					var s *sqlstore.Store
					if s, err = sqlstore.Open(ctx, db, sqlstore.QSQL, sqlstore.Options{}); err == nil {
						err = body(ctx, s)
					}
				})
				if rerr := sim.Run(time.Minute); rerr != nil {
					t.Fatal(rerr)
				}
				if err != nil {
					t.Error(err)
				}
			})
		})
	}
}

// openSQLite opens a fresh SQLite file in its single-writer mode.
func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqlstore.SQLiteDSN(filepath.Join(t.TempDir(), "grist.db"), 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// The sqlstore on SQLite (a temp file, modernc.org/sqlite) keeps the
// store contract, its concurrent check with goroutines appending at once
// (BEGIN IMMEDIATE, a writer waiting busy for the lock).
func TestContractOnSQLite(t *testing.T) {
	t.Parallel()
	storetest.Run(t, func(t *testing.T, body func(ctx context.Context, s store.Store) error) {
		s, err := sqlstore.Open(context.Background(), openSQLite(t), sqlstore.SQLite, sqlstore.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := body(context.Background(), s); err != nil {
			t.Error(err)
		}
	})
}

// SQLite without its single-writer mode (the default rollback journal,
// no busy_timeout) is refused, naming the fix.
func TestSQLiteModeChecked(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "plain.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := sqlstore.Open(context.Background(), db, sqlstore.SQLite, sqlstore.Options{}); err == nil || !strings.Contains(err.Error(), "SQLiteDSN") {
		t.Errorf("a plain SQLite file: %v, want the mode's error naming sqlstore.SQLiteDSN", err)
	}
}

// postgresDSN is GRIST_POSTGRES, a PostgreSQL to test against (pgx's
// DSN): none in CI; a scratch server is two commands (initdb, then
// postgres -k on a directory of its own).
func postgresDSN(t *testing.T) string {
	dsn := os.Getenv("GRIST_POSTGRES")
	if dsn == "" {
		t.Skip("no PostgreSQL: GRIST_POSTGRES=<dsn> runs the contract against one")
	}
	return dsn
}

// The sqlstore on PostgreSQL keeps the store contract, at read committed
// (stated, with lock_timeout and idle_in_transaction_session_timeout)
// and at repeatable read and serializable, where a lost race for the
// head lock is a 40001, transient. Each check runs on fresh tables.
func TestContractOnPostgres(t *testing.T) {
	dsn := postgresDSN(t)
	for _, iso := range []sql.IsolationLevel{sql.LevelDefault, sql.LevelRepeatableRead, sql.LevelSerializable} {
		t.Run(iso.String(), func(t *testing.T) {
			storetest.Run(t, func(t *testing.T, body func(ctx context.Context, s store.Store) error) {
				ctx := context.Background()
				db, err := sql.Open("pgx", dsn)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				schema := fmt.Sprintf("grist_test_%d", time.Now().UnixNano()) //qenv:ok a scratch schema's unique name, outside any simulation
				if _, err := db.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
					t.Fatal(err)
				}
				defer db.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
				db.Close()
				if db, err = sql.Open("pgx", dsn+" search_path="+schema); err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				s, err := sqlstore.Open(ctx, db, sqlstore.Postgres, sqlstore.Options{Isolation: iso})
				if err != nil {
					t.Fatal(err)
				}
				if err := body(ctx, s); err != nil {
					t.Error(err)
				}
			})
		})
	}
}

// pgError and liteError are drivers' errors as the store reads them:
// through their methods.
type pgError string

func (e pgError) Error() string    { return "pg " + string(e) }
func (e pgError) SQLState() string { return string(e) }

type liteError int

func (e liteError) Error() string { return fmt.Sprint("sqlite ", int(e)) }
func (e liteError) Code() int     { return int(e) }

// A statement no retry fixes is permanent; a lost race, a deadlock, a
// lock wait that timed out, a lost connection and SQLITE_BUSY are
// transient, whatever the driver (read through SQLState and Code).
func TestErrorsClassified(t *testing.T) {
	db, err := sql.Open("sqlite", sqlstore.SQLiteDSN(filepath.Join(t.TempDir(), "e.db"), time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := sqlstore.Open(context.Background(), db, sqlstore.SQLite, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE grist_events`); err != nil {
		t.Fatal(err)
	}
	s.CreateTenant(context.Background(), "t1")
	_, err = s.Append(context.Background(), "t1", store.Batch{Events: []store.Event{{Type: "a", Version: 1, Body: []byte(`{}`)}}})
	var pe *store.PermanentError
	if !errors.As(err, &pe) || store.Transient(err) {
		t.Errorf("an append to a missing table: %v, want a *store.PermanentError", err)
	}
	for _, c := range []struct {
		err       error
		permanent bool
	}{
		{pgError("40001"), false}, {pgError("40P01"), false}, {pgError("55P03"), false}, {pgError("08006"), false},
		{pgError("42P01"), true}, {pgError("42601"), true}, {pgError("0A000"), true}, {pgError("22P02"), true},
		{liteError(5), false}, {liteError(517), false}, {liteError(6), false}, {liteError(1), true}, {liteError(21), true},
		{&qsql.Error{Code: "40001"}, false}, {&qsql.Error{Code: "42601"}, true},
	} {
		got := sqlstore.Classify(c.err)
		if errors.As(got, &pe) != c.permanent || store.Transient(got) == c.permanent {
			t.Errorf("%v: classified %v (transient %v), want permanent %v", c.err, got, store.Transient(got), c.permanent)
		}
	}
}

// On PostgreSQL a holder of a tenant's lock that never lets go (a stuck
// node, a transaction left open) makes an append an error after
// lock_timeout (55P03, transient), not a hang.
func TestPostgresLockTimeout(t *testing.T) {
	dsn := postgresDSN(t)
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("grist_test_%d", time.Now().UnixNano()) //qenv:ok a scratch schema's unique name
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	sdb, err := sql.Open("pgx", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	s, err := sqlstore.Open(ctx, sdb, sqlstore.Postgres, sqlstore.Options{LockTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	holder, err := sdb.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if _, err := holder.ExecContext(ctx, `UPDATE grist_heads SET head = head WHERE tenant = 't1'`); err != nil {
		t.Fatal(err)
	}
	start := time.Now() //qenv:ok the wait's wall time, against a real server
	_, err = s.Append(ctx, "t1", store.Batch{Events: []store.Event{{Type: "a", Version: 1, Body: []byte(`{}`)}}})
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "55P03" || !store.Transient(err) || time.Since(start) > 3*time.Second { //qenv:ok the wait's wall time
		t.Errorf("an append behind a stuck holder: %v after %v, want lock_timeout's 55P03 (transient) after about 200 ms", err, time.Since(start)) //qenv:ok
	}
}

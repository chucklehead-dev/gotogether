package sqlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/store"
)

// A Dialect is what a database needs said differently: its default
// isolation level, the settings each transaction states, and how its
// errors read. Every statement the store runs is in one SQL that
// PostgreSQL, SQLite (3.35 or later: RETURNING) and qsql all run, so a
// dialect changes nothing else: the simulated tier runs the real tier's
// statements.
type Dialect struct {
	name string
	// iso is the level BeginTx states when Options.Isolation is the
	// default: PostgreSQL's read committed, stated rather than the
	// server's (after the head lock, each statement sees every commit
	// before it, which the condition check needs); qsql's, the same (its
	// Store's level runs, read committed or stronger); SQLite's default
	// (one writer: every transaction serializable).
	iso sql.IsolationLevel
	// timeouts: each transaction sets lock_timeout and
	// idle_in_transaction_session_timeout (PostgreSQL's set_config, local
	// to the transaction).
	timeouts bool
	// pragmas: Open checks SQLite's WAL and busy_timeout.
	pragmas bool
}

// The dialects.
var (
	// Postgres is PostgreSQL through any database/sql driver (pgx's
	// stdlib): read committed stated, and each transaction's lock_timeout
	// (a stuck holder of the tenant's lock is an error, not a hang) and
	// idle_in_transaction_session_timeout (a crashed node's open
	// transaction releases the lock). 40001 and 40P01 are transient,
	// never a failed condition.
	Postgres = Dialect{name: "postgres", iso: sql.LevelReadCommitted, timeouts: true} // read-only
	// SQLite is SQLite through any database/sql driver (modernc.org/sqlite:
	// no cgo), in WAL with a busy_timeout: open it with SQLiteDSN (or the
	// driver's own spelling of journal_mode WAL and a busy_timeout), which
	// Open checks. Tenants do not append in parallel: SQLite has one
	// writer for the whole file.
	SQLite = Dialect{name: "sqlite", iso: sql.LevelDefault, pragmas: true} // read-only
	// QSQL is qsql (gotogether/qenv/qsql) over a qenv.Store, for
	// simulations: the real tier's statements, interleaved at the Store's
	// isolation level, with its anomalies, SQLSTATEs and commit faults.
	// It runs no SET (qsql parses none).
	QSQL = Dialect{name: "qsql", iso: sql.LevelReadCommitted} // read-only
)

// String is the dialect's name.
func (d Dialect) String() string { return d.name }

// SQLiteDSN is the DSN of the SQLite file at path in the mode grist
// needs, as modernc.org/sqlite spells it: WAL (readers do not block the
// writer, nor it them) and a busy_timeout (a writer waits busy for the
// write lock instead of failing at once). Transactions begin deferred,
// SQLite's default: an append's first statement is a write (the head
// lock's UPDATE), so it takes the write lock before it reads anything and
// never upgrades a read snapshot that another writer made stale (the
// SQLITE_BUSY no busy_timeout waits through). _txlock=immediate (BEGIN
// IMMEDIATE), which this DSN once set for that reason, buys an append
// nothing, and a driver that applied it to read-only transactions too
// would make every read wait for the writer (modernc applies it to
// read-write ones only). The concurrent contract check runs appends
// from goroutines at once on it (TestContractOnSQLite). Another driver
// spells these its own way.
func SQLiteDSN(path string, busy time.Duration) string {
	return "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(" + fmt.Sprint(busy.Milliseconds()) + ")"
}

// checkSQLite is Open's check of SQLite's mode on one connection: WAL
// and a busy_timeout.
func checkSQLite(ctx context.Context, db *sql.DB) error {
	var mode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		return err
	}
	var busy int64
	if err := db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busy); err != nil {
		return err
	}
	if !strings.EqualFold(mode, "wal") || busy <= 0 {
		return fmt.Errorf("sqlstore: SQLite runs journal_mode %s with busy_timeout %d ms; grist needs WAL (readers beside the writer) and a busy_timeout (a writer waits for the lock): open it with sqlstore.SQLiteDSN(path, 5*time.Second), or your driver's spelling of journal_mode=WAL and busy_timeout", mode, busy)
	}
	return nil
}

// SQLite's primary result codes that no retry fixes: SQLITE_ERROR (a
// statement it cannot run: no such table, a syntax error), READONLY,
// CORRUPT, MISMATCH, MISUSE, RANGE and NOTADB. BUSY and LOCKED (a
// busy_timeout ran out) and the rest stay transient. Read-only.
var sqlitePermanent = map[int]bool{1: true, 8: true, 11: true, 20: true, 21: true, 25: true, 26: true}

// PostgreSQL's SQLSTATE classes (and codes) that no retry fixes: a
// statement the server cannot run (42: syntax, an undefined table or
// column, privileges; 0A: a feature it lacks), bad data (22), a missing
// database or schema (3D, 3F), authentication (28). 40001 (serialization),
// 40P01 (deadlock), 55P03 (lock_timeout), the connection's class 08 and
// the rest stay transient. Read-only.
var pgPermanent = map[string]bool{"42": true, "0A": true, "22": true, "3D": true, "3F": true, "28": true}

// classify is err as the store contract reads it: a *store.PermanentError
// for what no retry fixes, err itself (transient) for the rest. Every
// driver's errors are read through their methods (SQLState, Code), so
// the store imports none.
func classify(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, driver.ErrBadConn) {
		return err
	}
	var pg interface{ SQLState() string }
	if errors.As(err, &pg) {
		if s := pg.SQLState(); len(s) == 5 && pgPermanent[s[:2]] {
			return store.Permanent(err)
		}
		return err
	}
	var lite interface{ Code() int }
	if errors.As(err, &lite) && sqlitePermanent[lite.Code()&0xff] {
		return store.Permanent(err)
	}
	return err
}

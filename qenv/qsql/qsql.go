// Package qsql is a database/sql driver over a qenv.Store: an unmodified
// database/sql service runs in a simulation, its transactions interleaved
// statement by statement, at the Store's isolation level, with the
// Store's anomalies and nothing else.
//
//	db := qsql.Open(s, qenv.NewStore(s, "paydb", qenv.ReadCommitted))
//	db.ExecContext(ctx, `CREATE TABLE accounts (id TEXT PRIMARY KEY, balance BIGINT NOT NULL)`)
//	db.ExecContext(ctx, `UPDATE accounts SET balance = balance - ? WHERE id = ?`, 10, "alice")
//
// The SQL is a subset (one table at a time, equality only; the error for
// anything else names the subset). Rows are keyed in the Store by primary
// key, so the Store's history checker (GOTOGETHER_CHECK=1) judges every
// commit, and its fault kinds apply: "store.<name>.commit" (a commit
// applied, the connection lost: SQLSTATE 08006) with Store.CommitFaults,
// and "sql.<name>.conn" ("badConn": driver.ErrBadConn before a
// transaction or statement ran, which database/sql retries on another
// connection) with Options.BadConn.
//
// Errors are *Error, with PostgreSQL's SQLSTATE (the SQLState method that
// retry loops look for with errors.As): 40001 for a serialization failure
// (qenv.ErrConflict), 40P01 for a deadlock, 23505 for a duplicate key
// (a key another transaction committed, at every level, as PostgreSQL's
// unique check), 25P02 for a statement after a failed one in a
// transaction. After the transaction's context is cancelled, statements
// fail with sql.ErrTxDone and Commit with context.Canceled or
// sql.ErrTxDone (database/sql's own answers, by its goroutine's timing).
//
// database/sql's own goroutines stay out of the simulation's way: its
// connection opener runs only when the pool is limited (SetMaxOpenConns)
// and full, which is a hang in a simulation (Sim.Hang names it); a
// cancelled context's rollback, which database/sql runs from a goroutine
// of its own, is left to the transaction's reaper, a task of the
// simulation, so the Store is only ever touched by tasks.
package qsql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Options configure a database opened with Open.
type Options struct {
	// Name is the database's, for its fault kind "sql.<name>.conn"
	// (default: the Store's name). Two databases of one name share it,
	// as two pools of one server would.
	Name string
	// BadConn turns on the fault kind "sql.<name>.conn": a connection
	// found broken (driver.ErrBadConn) when a transaction or an
	// autocommit statement starts, before anything ran. database/sql
	// retries it on another connection, twice, then on a new one; the
	// caller sees driver.ErrBadConn only if all three fail. The kind has
	// no process (the Store is none's): under Sim.SettleCore it is drawn
	// for a task outside the core and nominal for one inside, as any fault
	// a task makes.
	BadConn bool
	// OnCommit, if set, runs at each commit that changed rows, with the
	// rows' changes in order, when the Store applies it: before Commit
	// returns, and even if the caller is then told the outcome is unknown
	// (08006), as Txn.OnCommit does. The place to record a model step whose
	// linearization point is the commit (change data capture, in
	// production: an outbox written in the same transaction).
	OnCommit func(ctx context.Context, changes []Change)
}

// Change is a row a committed transaction inserted, updated or deleted.
type Change struct {
	Table string
	Op    string         // "insert", "update" or "delete"
	Old   map[string]any // the row before: nil for an insert
	New   map[string]any // the row after: nil for a delete
}

// Error is an error of the database, with its SQLSTATE.
type Error struct {
	Code    string // PostgreSQL's SQLSTATE: "40001", "23505", ...
	Message string
	// Err is the cause: qenv.ErrConflict for 40001 and 40P01,
	// qenv.ErrUnknownOutcome for 08006, the failed statement's error for
	// 25P02.
	Err error
}

func (e *Error) Error() string { return "qsql: " + e.Message + " (SQLSTATE " + e.Code + ")" }

// SQLState is the error's SQLSTATE: what retry loops written for
// PostgreSQL and CockroachDB drivers check (errors.As to an interface with
// this method), so they retry 40001 here as they do there.
func (e *Error) SQLState() string { return e.Code }

// Unwrap is Err.
func (e *Error) Unwrap() error { return e.Err }

// Open returns a database over st. It is closed when s closes (its reaper
// task unwinds), which ends database/sql's goroutine for it.
func Open(s *qenv.Sim, st *qenv.Store, o ...Options) *sql.DB {
	var opt Options
	if len(o) > 0 {
		opt = o[0]
	}
	db, _ := open(s, st, opt)
	return db
}

func open(s *qenv.Sim, st *qenv.Store, opt Options) (*sql.DB, *connector) {
	if opt.Name == "" {
		opt.Name = st.Name
	}
	c := &connector{sim: s, st: st, opt: opt, stmts: map[string]statement{}, tables: map[string]*table{}, live: map[*tx]bool{}, killed: map[int64]*killedTask{},
		badConn: qenv.DeclareFaults("sql."+opt.Name+".conn", "badConn")} // a broken connection, found before anything ran
	db := sql.OpenDB(c)
	s.Spawn("qsql."+opt.Name+".reaper", func(ctx context.Context) {
		defer db.Close() // at Sim.Close, as the task unwinds
		qenv.Daemon(ctx)
		for {
			qenv.Wait(ctx, c.cancelled)
			c.reap()
		}
	})
	return db, c
}

// Table returns the committed rows of a table, in primary-key order, each
// a map of its columns (nil for NULL): for tests and final-state checks.
// A table that does not exist panics, naming those that do.
func Table(st *qenv.Store, name string) []map[string]any {
	if _, ok := st.Read(schemaPrefix + name); !ok {
		var names []string
		for _, k := range st.Keys(schemaPrefix) {
			names = append(names, strings.TrimPrefix(k, schemaPrefix))
		}
		panic(fmt.Sprintf("qsql: no table %q in store %s (its tables: %s)", name, st.Name, strings.Join(names, ", ")))
	}
	var out []map[string]any
	for _, k := range st.Keys(name + "/") {
		v, _ := st.Read(k)
		out = append(out, copyRow(asRow(v)))
	}
	return out
}

// ---- connector --------------------------------------------------------------

type connector struct {
	sim     *qenv.Sim
	st      *qenv.Store
	opt     Options
	badConn qenv.FaultKind

	mu     sync.Mutex            // stmts and tables: database/sql may prepare from any goroutine
	stmts  map[string]statement  // parsed statements, by text
	tables map[string]*table     // committed schemas (they never change)
	txs    int64                 // transactions begun: their ids, the reaper's order
	killed map[int64]*killedTask // the goroutines of tasks the driver saw unwinding (unwound)

	anyKilled atomic.Bool // killed is not empty: dead looks the goroutine up only then

	// live are the open transactions whose context can be cancelled, for
	// the reaper. Only tasks touch it, and the scheduler's predicate
	// (cancelled) between their steps.
	live     map[*tx]bool
	scanOnly bool // tests: every WHERE by scan, for the plan's brute-force check
}

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	// no fault here: database/sql connects when its pool is short of one,
	// which depends on when its goroutines returned connections; a fault
	// drawn here would make the run's choices depend on them too
	return &conn{c: c}, nil
}

func (c *connector) Driver() driver.Driver { return drv{} }

// drv is the connector's driver.Driver, which database/sql requires.
type drv struct{}

func (drv) Open(string) (driver.Conn, error) {
	return nil, errors.New("qsql: a database over a qenv.Store is opened with qsql.Open(sim, store), not sql.Open")
}

func (c *connector) parse(q string) (statement, error) {
	c.mu.Lock()
	st, ok := c.stmts[q]
	c.mu.Unlock()
	if ok {
		return st, nil
	}
	st, err := parse(q)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.stmts[q] = st
	c.mu.Unlock()
	return st, nil
}

func (c *connector) knownTable(name string) *table {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tables[name]
}

func (c *connector) learnTable(tb *table) {
	c.mu.Lock()
	c.tables[tb.name] = tb
	c.mu.Unlock()
}

// cancelled reports whether a live transaction's context is done: the
// reaper's predicate. It reads the contexts, never a flag database/sql's
// goroutine sets: a context is cancelled by a task's own call (or a
// deadline in wall time, which no simulation should rely on), so the step
// it holds at is the run's.
func (c *connector) cancelled() bool {
	for t := range c.live {
		if t.ctx.Err() != nil {
			return true
		}
	}
	return false
}

// reap rolls back the live transactions whose context is done, in the
// order they began: database/sql rolls them back from a goroutine of its
// own (Tx.awaitDone), which must not touch the Store, and the caller may
// never reach the driver again to do it, while their locks hold up others.
func (c *connector) reap() {
	var done []*tx
	for t := range c.live {
		if t.ctx.Err() != nil {
			done = append(done, t)
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].id < done[j].id })
	for _, t := range done {
		t.fail(&Error{Code: "57014", Message: "canceling statement due to user request: the transaction's context is done, and it was rolled back", Err: t.ctx.Err()})
	}
}

// onTask reports whether ctx (or the calling goroutine) is a task of the
// connector's simulation: where faults may be drawn.
func (c *connector) onTask(ctx context.Context) bool { return qenv.From(ctx) == c.sim }

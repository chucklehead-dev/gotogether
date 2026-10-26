package qsql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// conn is a connection: database/sql serializes the calls on one, except
// IsValid and Close, which its goroutines may call after a cancelled
// context's rollback: those touch only atomics.
type conn struct {
	c      *connector
	cur    *tx         // the transaction in progress, if any
	bad    atomic.Bool // lost (08006): database/sql discards it
	closed atomic.Bool
}

var (
	_ driver.Conn               = (*conn)(nil)
	_ driver.ConnBeginTx        = (*conn)(nil)
	_ driver.ConnPrepareContext = (*conn)(nil)
	_ driver.ExecerContext      = (*conn)(nil)
	_ driver.QueryerContext     = (*conn)(nil)
	_ driver.NamedValueChecker  = (*conn)(nil)
	_ driver.SessionResetter    = (*conn)(nil)
	_ driver.Validator          = (*conn)(nil)
)

func (cn *conn) Prepare(q string) (driver.Stmt, error) {
	return cn.PrepareContext(context.Background(), q)
}

// PrepareContext parses q (once per database: the parse is cached).
func (cn *conn) PrepareContext(_ context.Context, q string) (driver.Stmt, error) {
	st, err := cn.c.parse(q)
	if err != nil {
		return nil, err
	}
	return &stmt{cn: cn, st: st}, nil
}

func (cn *conn) Close() error {
	cn.closed.Store(true)
	return nil
}

// IsValid is false for a connection lost during a commit: database/sql
// discards it rather than reuse it.
func (cn *conn) IsValid() bool { return !cn.bad.Load() && !cn.closed.Load() }

// ResetSession runs when database/sql reuses the connection. A transaction
// left on it was cancelled (database/sql rolled it back from its own
// goroutine): the reaper rolls it back in the Store, at a step of the
// run's, whichever connection database/sql hands out.
func (cn *conn) ResetSession(context.Context) error {
	if cn.bad.Load() {
		return driver.ErrBadConn
	}
	cn.cur = nil
	return nil
}

func (cn *conn) Begin() (driver.Tx, error) {
	return cn.BeginTx(context.Background(), driver.TxOptions{})
}

// BeginTx starts a transaction of the Store. The Store's isolation level
// is store-wide: a transaction may ask for it or for any weaker level, and
// a stronger one fails with the fix.
func (cn *conn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	if cn.bad.Load() {
		return nil, driver.ErrBadConn
	}
	if err := ctx.Err(); err != nil {
		return nil, canceled(err)
	}
	if err := cn.c.level(sql.IsolationLevel(o.Isolation)); err != nil {
		return nil, err
	}
	if err := cn.c.fault(ctx); err != nil {
		return nil, err
	}
	t := cn.c.begin(ctx, o.ReadOnly)
	t.cn = cn
	if ctx.Done() != nil {
		cn.c.live[t] = true
	}
	cn.cur = t
	return t, nil
}

// level checks a transaction's isolation level against the Store's.
func (c *connector) level(l sql.IsolationLevel) error {
	var need qenv.Isolation
	switch l {
	case sql.LevelDefault:
		return nil
	case sql.LevelReadUncommitted, sql.LevelReadCommitted, sql.LevelWriteCommitted:
		need = qenv.ReadCommitted
	case sql.LevelRepeatableRead, sql.LevelSnapshot:
		need = qenv.SnapshotIsolation
	case sql.LevelSerializable:
		need = qenv.Serializable
	default:
		return &Error{Code: "0A000", Message: fmt.Sprintf("isolation level %s is not one a Store offers (serializable, snapshot isolation, read committed)", l)}
	}
	if c.st.Iso <= need { // Serializable < SnapshotIsolation < ReadCommitted: stronger first
		return nil
	}
	return &Error{Code: "0A000", Message: fmt.Sprintf("store %s runs %s; sql.%s needs NewStore(..., qenv.%s)", c.st.Name, c.st.Iso, levelName[l], isoName[need])}
}

// levelName is a level's constant in database/sql. Read-only.
var levelName = map[sql.IsolationLevel]string{
	sql.LevelReadUncommitted: "LevelReadUncommitted", sql.LevelReadCommitted: "LevelReadCommitted", sql.LevelWriteCommitted: "LevelWriteCommitted",
	sql.LevelRepeatableRead: "LevelRepeatableRead", sql.LevelSnapshot: "LevelSnapshot", sql.LevelSerializable: "LevelSerializable",
}

// isoName is an isolation level's constant in qenv. Read-only.
var isoName = map[qenv.Isolation]string{qenv.Serializable: "Serializable", qenv.SnapshotIsolation: "SnapshotIsolation", qenv.ReadCommitted: "ReadCommitted"}

// fault draws the connection fault, on a task only (never on one of
// database/sql's goroutines, never on the test's own before Run).
func (c *connector) fault(ctx context.Context) error {
	if c.opt.BadConn && c.onTask(ctx) && c.sim.FaultOf(c.badConn) == "badConn" {
		return driver.ErrBadConn
	}
	return nil
}

func canceled(err error) *Error {
	return &Error{Code: "57014", Message: "canceling statement due to user request (its context is done)", Err: err}
}

// ExecContext runs a statement: in the connection's transaction, or in one
// of its own (autocommit).
func (cn *conn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	st, err := cn.c.parse(q)
	if err != nil {
		return nil, err
	}
	r, err := cn.run(ctx, st, args)
	if err != nil {
		return nil, err
	}
	return res(r.affected), nil
}

// QueryContext runs a statement and returns its rows, read in full.
func (cn *conn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	st, err := cn.c.parse(q)
	if err != nil {
		return nil, err
	}
	r, err := cn.run(ctx, st, args)
	if err != nil {
		return nil, err
	}
	return &rows{cols: r.cols, data: r.rows}, nil
}

// CheckNamedValue takes the values columns store as they are and leaves
// the rest (other integers, driver.Valuer) to database/sql's conversion.
func (cn *conn) CheckNamedValue(nv *driver.NamedValue) error {
	if nv.Name != "" {
		return &Error{Code: "42601", Message: fmt.Sprintf("named parameter @%s: qsql takes positional ones (? or $n)", nv.Name)}
	}
	switch nv.Value.(type) {
	case nil, int64, string, bool, []byte, time.Time:
		return nil
	}
	return driver.ErrSkip
}

func (cn *conn) run(ctx context.Context, st statement, nargs []driver.NamedValue) (*result, error) {
	args := make([]any, len(nargs))
	for i, a := range nargs {
		args[i] = a.Value
	}
	if len(args) != st.params() {
		return nil, &Error{Code: "08P01", Message: fmt.Sprintf("bind message supplies %d parameters, but the statement requires %d", len(args), st.params())}
	}
	if t := cn.cur; t != nil {
		return t.statement(ctx, st, args)
	}
	// autocommit: a transaction of its own, begun, run and committed
	// within the call
	if cn.bad.Load() {
		return nil, driver.ErrBadConn
	}
	if err := ctx.Err(); err != nil {
		return nil, canceled(err)
	}
	if err := cn.c.fault(ctx); err != nil {
		return nil, err
	}
	t := cn.c.begin(ctx, false)
	t.cn = cn
	r, err := t.exec(st, args)
	if err != nil {
		t.txn.Rollback()
		return nil, sqlError(cn.c.st.Name, err)
	}
	if err := t.commit(); err != nil {
		return nil, err
	}
	return r, nil
}

// ---- transactions -----------------------------------------------------------

// tx is a transaction of the Store, explicit (a driver.Tx) or an
// autocommit statement's.
type tx struct {
	c        *connector
	cn       *conn
	id       int64
	ctx      context.Context
	txn      qenv.Txn
	proc     *qenv.Process // the process it runs in, and its incarnation: a crash ends it in the Store
	inc      int
	readOnly bool
	created  map[string]*table // tables it created, not yet committed
	read     map[string]bool   // the keys it looked up, and the prefixes it scanned (claim)
	scanned  []string
	changes  []Change // for Options.OnCommit
	failed   error    // the error that aborted it: what 25P02 names
	ended    bool     // committed or rolled back in the Store
}

func (c *connector) begin(ctx context.Context, readOnly bool) *tx {
	c.mu.Lock()
	c.txs++
	id := c.txs
	c.mu.Unlock()
	t := &tx{c: c, id: id, ctx: ctx, txn: c.st.Begin(ctx), readOnly: readOnly, created: map[string]*table{}, read: map[string]bool{}}
	if p := qenv.ProcessOf(ctx); p != nil {
		t.proc, t.inc = p, p.Incarnation
	}
	t.txn.OnCommit(func() {
		for _, tb := range t.created {
			c.learnTable(tb)
		}
		if len(t.changes) > 0 && c.opt.OnCommit != nil {
			c.opt.OnCommit(t.ctx, t.changes)
		}
	})
	return t
}

// crashed reports whether its process crashed since it began: the Store
// ended it, and any use but Rollback would panic.
func (t *tx) crashed() bool {
	return t.proc != nil && (t.proc.Down() || t.proc.Incarnation != t.inc)
}

// fail aborts the transaction: rolled back in the Store, every later
// statement refused (25P02), as PostgreSQL does after any error in a
// transaction block.
func (t *tx) fail(err error) {
	if t.failed == nil {
		t.failed = err
	}
	t.end()
}

func (t *tx) end() {
	if !t.ended {
		t.ended = true
		t.txn.Rollback() // after a commit, or after the Store ended it, a no-op
	}
	delete(t.c.live, t)
}

// usable is the error a statement or Commit gets on a transaction that
// cannot go on, or nil.
func (t *tx) usable(ctx context.Context) error {
	if t.ctx.Err() != nil {
		// cancelled: the reaper rolls it back at its step. database/sql
		// answers sql.ErrTxDone itself once its goroutine has rolled the
		// Tx back, and that goroutine races the task: the same answer
		// here makes a statement's error the same either way
		return sql.ErrTxDone
	}
	if t.failed != nil {
		return &Error{Code: "25P02", Message: fmt.Sprintf("current transaction is aborted, commands ignored until end of transaction block (it failed with: %v)", t.failed), Err: t.failed}
	}
	if t.ended {
		return &Error{Code: "25P01", Message: "there is no transaction in progress"}
	}
	if err := ctx.Err(); err != nil {
		t.fail(canceled(err)) // the statement's own context, a task's: PostgreSQL aborts the transaction
		return t.failed
	}
	if t.crashed() {
		t.cn.bad.Store(true)
		t.fail(&Error{Code: "08006", Message: "connection lost: the process crashed and the transaction was aborted"})
		return t.failed
	}
	return nil
}

func (t *tx) statement(ctx context.Context, st statement, args []any) (*result, error) {
	if err := t.usable(ctx); err != nil {
		return nil, err
	}
	r, err := t.exec(st, args)
	if err != nil {
		err = sqlError(t.c.st.Name, err)
		t.fail(err)
		return nil, err
	}
	return r, nil
}

// Commit commits it in the Store. After a failed statement it rolls back
// and returns 25P02 (PostgreSQL answers ROLLBACK): the caller learns that
// nothing committed.
func (t *tx) Commit() error {
	if t.cn != nil && t.cn.cur == t {
		t.cn.cur = nil
	}
	if err := t.usable(context.Background()); err != nil {
		if t.ctx.Err() == nil { // cancelled: the reaper's
			t.end()
		}
		return err
	}
	return t.commit()
}

func (t *tx) commit() error {
	err := t.txn.Commit()
	t.ended = true
	delete(t.c.live, t)
	if errors.Is(err, qenv.ErrUnknownOutcome) {
		t.cn.bad.Store(true) // the connection is gone: database/sql must not reuse it, nor retry on ErrBadConn
	}
	return sqlError(t.c.st.Name, err)
}

// Rollback rolls it back in the Store, unless its context is done: then
// database/sql may be calling from a goroutine of its own, and the reaper
// does it at a step of the run's, a task.
func (t *tx) Rollback() error {
	if t.ctx.Err() != nil {
		return nil
	}
	if t.cn != nil && t.cn.cur == t {
		t.cn.cur = nil
	}
	t.end()
	return nil
}

// ---- statements and rows ----------------------------------------------------

type stmt struct {
	cn *conn
	st statement
}

var (
	_ driver.StmtExecContext  = (*stmt)(nil)
	_ driver.StmtQueryContext = (*stmt)(nil)
)

func (s *stmt) Close() error  { return nil }
func (s *stmt) NumInput() int { return s.st.params() }

func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), named(args))
}

func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), named(args))
}

func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	r, err := s.cn.run(ctx, s.st, args)
	if err != nil {
		return nil, err
	}
	return res(r.affected), nil
}

func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	r, err := s.cn.run(ctx, s.st, args)
	if err != nil {
		return nil, err
	}
	return &rows{cols: r.cols, data: r.rows}, nil
}

func named(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, a := range args {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: a}
	}
	return out
}

// res is a statement's result: rows affected.
type res int64

// LastInsertId is not supported: rows are keyed by the primary key the
// caller gives.
func (r res) LastInsertId() (int64, error) {
	return 0, &Error{Code: "0A000", Message: "LastInsertId is not supported: qsql keys rows by the primary key the INSERT gives"}
}

func (r res) RowsAffected() (int64, error) { return int64(r), nil }

// rows are a query's rows, read in full when it ran: Close (which
// database/sql may call from a goroutine of its own) touches nothing else.
type rows struct {
	cols []string
	data [][]any
	i    int
}

func (r *rows) Columns() []string { return r.cols }
func (r *rows) Close() error      { return nil }

func (r *rows) Next(dest []driver.Value) error {
	if r.i >= len(r.data) {
		return io.EOF
	}
	for i, v := range r.data[r.i] {
		dest[i] = v
	}
	r.i++
	return nil
}

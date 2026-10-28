# database/sql in a simulation: `qenv/qsql`

Read this before simulating a service that uses `database/sql` (a
payments service, anything written for PostgreSQL or CockroachDB): its
queries, transactions and retry loops stay as they are, over a
`qenv.Store`. [examples/payd](../../../examples/payd) is a whole service
done this way, together with qhttp (services.md): its harness (`sim.go`)
opens the database in the process's start function, records charges and
debits from `OnCommit`, and probes the balances after the settle.

```go
st := qenv.NewStore(sim, "paydb", qenv.ReadCommitted) // the level is the Store's, store-wide
st.CommitFaults = true                                // 08006: applied, the connection lost
db := qsql.Open(sim, st, qsql.Options{
	BadConn:  true, // "sql.paydb.conn": driver.ErrBadConn before anything ran; database/sql retries it
	OnCommit: func(ctx context.Context, ch []qsql.Change) { /* record the step: the commit is its linearization point */ },
})
sim.Process("payd", true, func(ctx context.Context) { /* qsql.Open, payd.Migrate(ctx, db), srv.Handle(ctx, payd.New(db, ...)) */ })
rows := qsql.Table(st, "accounts") // committed rows, for final-state checks
```

- **The SQL is a subset**, one table at a time: `CREATE TABLE [IF NOT
  EXISTS]` (TEXT, INTEGER/BIGINT, BOOLEAN, BLOB/BYTEA, TIMESTAMP; a
  column's PRIMARY KEY or the table constraint `PRIMARY KEY (a, b, ...)`,
  UNIQUE on one column, NOT NULL), `INSERT ... [ON CONFLICT [(cols)] DO
  NOTHING | ON CONFLICT (cols) DO UPDATE SET c = ? | c = excluded.c | c =
  t.c ± ? [WHERE t.c ...]] [RETURNING cols|*]`, `SELECT ... [WHERE a = ? AND
  ... AND k > ? AND k <= ? | k BETWEEN ? AND ?] [ORDER BY key cols
  [DESC]] [LIMIT n | LIMIT NULL] [FOR UPDATE]`, `UPDATE ... SET c = ? | c = c + ? | c
  = c - ? [WHERE ...] [RETURNING ...]`, `DELETE ... [RETURNING ...]`; `?`
  or `$n`. A join looks a second table's rows up by its whole key:
  `SELECT t.c, u.d FROM t [INNER] JOIN u ON u.k1 = t.c1 AND u.k2 = t.c2
  [AND u.d = t.e] [WHERE t....] [ORDER BY t....] [LIMIT n]`, every column
  qualified, ON fixing all of u's `PRIMARY KEY`, WHERE and ORDER BY on
  t's columns (an inner join; no aliases, no FOR UPDATE). Each joined
  row is a read of its own, so at read committed a commit may fall
  between t's rows and u's, where PostgreSQL reads the statement in one
  snapshot: join rows that never change once committed. A range (`<`, `<=`, `>`, `>=`, `BETWEEN`) is on one key
  column, every key column before it fixed by equality (`WHERE tenant =
  ? AND pos > ?` over `PRIMARY KEY (tenant, pos)`), and is a predicate
  read of that range alone: serializable aborts a transaction whose
  range another commit inserted into (a phantom), not one whose range it
  wrote beside. ORDER BY names the key's columns in key order (those
  fixed by equality may be skipped), one direction; a column fixed by
  equality orders nothing and may be named anywhere, in either
  direction. Anything else fails
  with 42601 naming what it found and the subset: rewrite the query, or
  keep that code out of the simulation. qsql is checked against SQLite
  on these shapes (examples/payd's `TestQsqlAgainstSQLite`). In a DO
  UPDATE's values and WHERE a column is `t.c` (the existing row's) or
  `excluded.c` (the proposed row's): a bare `c` fails with 42702, as in
  PostgreSQL (SQLite takes the existing row's). A table's name has no `/`
  or `!` and no `_qsql` prefix (they are its keys' separators and qsql's
  schemas).
- **Anomalies are the Store's level's** and every commit is judged by its
  history checker under `GOTOGETHER_CHECK=1`. Read committed loses an
  update written as read-then-write (`SELECT balance`, then `SET balance =
  ?`), not one written `SET balance = balance - ?` or after `SELECT ... FOR
  UPDATE`; an UPDATE that waited for a lock re-checks its WHERE on the
  row as left (a compare-and-set applies once). Snapshot isolation allows
  write skew; serializable aborts one of the two (40001).
- **Errors carry PostgreSQL's SQLSTATE** (`*qsql.Error`, `SQLState()`,
  which crdb-style retry loops find with `errors.As`): 40001
  (`qenv.ErrConflict`), 40P01 (a deadlock), 23505 (a duplicate key),
  23502, 25P02 (a statement after a failed one: the transaction is
  aborted, and its Commit commits nothing), 25006 (a write in a
  `ReadOnly` transaction), 08006 (`qenv.ErrUnknownOutcome`: the commit
  may have applied; never ErrBadConn, which database/sql would retry).
  A key another transaction inserted and committed is 23505 at every
  level, as PostgreSQL's unique check reads the latest rows; 40001
  instead at serializable if the transaction read the key absent first,
  and for `ON CONFLICT DO NOTHING` when its snapshot cannot see the
  other's row.
- **TxOptions**: `LevelDefault` or any level the Store meets
  (RepeatableRead and Snapshot are snapshot isolation); a stronger one
  fails with the `NewStore` to write.
- **database/sql's goroutines.** A cancelled context's rollback, which
  database/sql runs from a goroutine of its own, is done by the
  database's reaper task at the run's next step, so the Store's state does
  not depend on that goroutine. The errors the caller sees are database/sql's
  own: a statement in the transaction fails with `sql.ErrTxDone`, and
  `Commit` with `context.Canceled` or `sql.ErrTxDone`, which of the two
  depending on that goroutine's timing; treat both as rolled back. A context with a wall-clock deadline is wall
  time: do not let one expire inside a simulation. `SetMaxOpenConns`
  with a full pool blocks a task outside the simulation: the watchdog
  (`Sim.Hang`) panics naming it. The database closes with the
  simulation. A task killed inside a statement (a crash, or a wait left
  at Close) gets 08006 there, so its deferred `tx.Rollback` runs.
- **SettleCore.** `sql.<name>.conn` and `store.<name>.commit` belong to no
  process (the Store is none's): a core settle draws them for a task
  outside the core and leaves them nominal for one inside it, as for any
  fault a task makes.
- Not simulated: joins, aggregates, OR, ranges on a non-key column,
  ORDER BY a non-key column not fixed by equality, expressions in RETURNING, DEFAULT,
  savepoints, `LastInsertId`, schema changes after CREATE TABLE.

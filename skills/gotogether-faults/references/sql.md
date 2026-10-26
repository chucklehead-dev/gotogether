# database/sql in a simulation: `qenv/qsql`

Read this before simulating a service that uses `database/sql` (a
payments service, anything written for PostgreSQL or CockroachDB): its
queries, transactions and retry loops stay as they are, over a
`qenv.Store`.

```go
st := qenv.NewStore(sim, "paydb", qenv.ReadCommitted) // the level is the Store's, store-wide
st.CommitFaults = true                                // 08006: applied, the connection lost
db := qsql.Open(sim, st, qsql.Options{
	BadConn:  true, // "sql.paydb.conn": driver.ErrBadConn before anything ran; database/sql retries it
	OnCommit: func(ctx context.Context, ch []qsql.Change) { /* record the step: the commit is its linearization point */ },
})
sim.Process("payd", true, func(ctx context.Context) { /* payd.Serve(ctx, db) */ })
rows := qsql.Table(st, "accounts") // committed rows, for final-state checks
```

- **The SQL is a subset**, one table at a time, equality only: `CREATE
  TABLE [IF NOT EXISTS]` (TEXT, INTEGER/BIGINT, BOOLEAN, BLOB/BYTEA,
  TIMESTAMP; one-column PRIMARY KEY, UNIQUE, NOT NULL), `INSERT ...
  [ON CONFLICT [(col)] DO NOTHING]`, `SELECT ... [WHERE a = ? AND ...]
  [ORDER BY pk [DESC]] [LIMIT n] [FOR UPDATE]`, `UPDATE ... SET c = ? |
  c = c + ? | c = c - ?`, `DELETE`; `?` or `$n`. Anything else fails
  with 42601 naming what it found and the subset: rewrite the query, or
  keep that code out of the simulation.
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
  may have applied; never ErrBadConn, which database/sql would retry),
  57014 (the transaction's context is done).
- **TxOptions**: `LevelDefault` or any level the Store meets
  (RepeatableRead and Snapshot are snapshot isolation); a stronger one
  fails with the `NewStore` to write.
- **database/sql's goroutines.** A cancelled context's rollback, which
  database/sql runs from a goroutine of its own, is done by the
  database's reaper task at the run's next step, so the run does not
  depend on that goroutine. A context with a wall-clock deadline is wall
  time: do not let one expire inside a simulation. `SetMaxOpenConns`
  with a full pool blocks a task outside the simulation: the watchdog
  (`Sim.Hang`) panics naming it. The database closes with the
  simulation.
- Not simulated: joins, aggregates, ranges and OR, RETURNING, DEFAULT,
  upserts (`DO UPDATE`), savepoints, `LastInsertId`, schema changes
  after CREATE TABLE.

# Writing the app against seams, and recording where effects happen

From the gotogether-faults skill (its SKILL.md says when to read this).

## Fault injection with qenv

A fault is a choice the environment's contract allows:
- a response lost after the request took effect;
- a message redelivered;
- a read from a snapshot;
- a crash between two statements.

`qenv` runs the system deterministically and makes every such choice
through one `Chooser`. Option 0 of every choice is nominal, so Hegel's
shrinking removes faults.

## 1. Write the app against seams

- **Data:** `qenv.DB` and `qenv.Txn` (Get, GetForUpdate, Put, Delete,
  Scan, Commit, Rollback, OnCommit). In production, an adapter over
  `database/sql` implements them. `qenv.Retry(ctx, db, n, fn)` begins,
  runs fn and commits, again on a conflict: fn returns nil to commit and
  never calls Commit itself.
- **GetForUpdate or Get:** a row a transaction reads and then writes
  from what it read (a balance debited, a stock decremented, a "done"
  record checked before acting) is read with `GetForUpdate`, as SELECT
  ... FOR UPDATE: under `SnapshotIsolation` two such transactions that
  each `Get` the row both read the old value and the second's commit
  aborts only if they write the same key (two rows checked together, a
  joint balance, still skew), and under `ReadCommitted` the last writer
  wins, a lost update. `Get` is for rows only read (a probe, a lookup
  that decides no write). `Serializable` aborts what `Get` lets through,
  but the code should not rely on the level it is tested at:
  examples/wallet's `ReadThenWrite` and `WriteSkew` mutants are the two
  mistakes, each caught.
- **Typed records:** declare an app's own records once as a
  `qenv.Table[T]` and read them without assertions:

  ```go
  var accounts = qenv.NewTable[[]Entry]("acct/") // keys "acct/<account>", values []Entry

  es, _, err := accounts.GetForUpdate(tx, a) // a is the key after the prefix
  accounts.Put(tx, a, append(slices.Clip(es), e))
  for _, r := range accounts.Scan(tx) { /* r.Key is "<account>", r.Val a []Entry */ }
  ```

  Each method is the one Txn call on `prefix+k`, so isolation, commit
  faults, the history check and any Txn wrapper see what they would of
  the raw call. Values are shared and immutable: clip a slice before
  appending (`slices.Clip`), copy a map before changing it. A key
  holding another type (written raw) panics naming the key and both
  types. Use a Table for an app's own records (examples/wallet's
  accounts, examples/checkout's orders and stock); raw `Txn.Get`/`Put`
  for an untyped flag or marker row, in code that implements or wraps
  `Txn` (a recording DB, an adapter), and in tests through
  `Store.Read`/`Store.Load` (whole store keys: `Table.Key(k)`); `qenv/qsql`
  for relational data ([`references/sql.md`](sql.md)).
- **Other services:** small interfaces of your own (a broker, a payment
  client), implemented in the deployment over `qenv.Queue` and
  `qenv.Service`.
- **Concurrency and time:** `qenv.Go`, `qenv.Now` and `qenv.Sleep`, not
  `go`, `time.Now` or `time.Sleep`. Outside a simulation they fall through
  to the real thing.
- **Iteration:** iterate maps in sorted order wherever the order affects
  behaviour.
- **Kubernetes controllers:** take readers and writers per cluster
  through an interface (`Clusters`). In simulation, give them
  controller-runtime fake clients behind `interceptor.Funcs`; that's the
  seam where generation, conflicts, lag and lost responses happen
  (`qenv/kubesim`, a module: `kubesim.NewCluster`, `Cluster.OnWrite` to
  record a write where it lands, `kubesim.Fleet`; examples/fleetrollout).

## 2. Record steps where effects happen

- **A commit:** `tx.OnCommit(func() { qobs.Record(ctx, "place", nil, "order", id, "qty", n) })`.
  The hook runs when the commit is applied, even if the caller is told
  the outcome is unknown.
- **A remote service** records its own operations (a payment provider's
  charges and refunds).
- **A fake API server** records patches as it applies them. With real
  clusters the controller records them when the update succeeds.
- **Evidence:** record every read the code decides on, with its version
  (`resourceVersion`), and let the model order evidence by it.
- **On return, from the caller** (`//quint:action`): the step is what
  the caller saw, and a crash before the return means no step. A crashed
  task's deferred recording at `Close` goes to `Recorder.Interrupted`,
  reported with a failing run but not checked. If the model must see an
  effect whether or not its caller survives (a write that landed, then a
  crash), record it at the effect's site instead.
- **The actor:** a step's actor is its recorder's (`qobs.WithActor`),
  and in a simulation every process records into the run's one recorder,
  so the whole system is one actor, `default`: what a cluster's model
  needs. Give actors only to independent histories (one account each).
- **From a hook:** a crash or network hook runs outside any task; record
  with the simulation's context, `s.Context()`, which carries the run's
  recorder (`OnCrash`'s `stop`, `OnHeal`'s `heal`).
- **Crash tests in a real environment** need a durable step log:
  `qobs.FileSink(path, qobs.PerStep)` is (one write per step).
  `qobs.Buffered` loses the last steps of a killed process, undetectably.

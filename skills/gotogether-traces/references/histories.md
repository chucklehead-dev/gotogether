# A database's isolation level from its clients' history (qhistory)

A model of your application assumes the database's isolation level. To
check the database itself (a real one, a new driver, a proxy in
between), run Elle's list-append workload against it and check the
history with `qhistory`:

```go
txns := qhistory.Workload{Keys: 3, Txns: 500, Seed: seed}.Generate()
/* run them from several clients at once: for each Op, an Append appends
   Op.Value to Op.Key's list (atomically, in the database: UPDATE ...
   SET v = v || ...), a Read reads the whole list into Op.List; set
   Outcome to Committed, Aborted (a rollback, a failed commit) or
   Unknown (a timeout: the client never learned) */
rep, err := qhistory.Check(qhistory.SnapshotIsolation, txns)
if err != nil || !rep.OK() {
	/* rep lists each anomaly, its transactions and the cycle's edges */
}
```

- Values are unique per key, so a read's list names who wrote what, in
  which order; the dependencies (ww, wr, rw) follow, and their cycles are
  Adya's anomalies: G0, G1c, G-single (read skew, a lost update), G2
  (write skew). From the reads alone: G1a (an aborted value read), G1b
  (an intermediate value read), an incompatible order, garbage, internal
  (a transaction's read missing its own appends).
- Read committed forbids all but G-single and G2, snapshot isolation all
  but G2, serializability all. `rep.OK()` is false on a forbidden one;
  `rep.Has("G2")` says one was seen, allowed or not (write skew is
  expected under snapshot isolation: assert it happens in some run, as
  qhistory's own tests do of qenv's store).
- A transaction of unknown outcome committed if some committed read saw
  its value; its reads then count. Never mark a timed-out commit
  Aborted: a G1a would be the checker's error, not the database's.
- Few keys and short transactions make conflicts, and anomalies show.
  The checker is linear in the history plus a cycle search per
  strongly connected component: thousands of transactions are cheap.
- qenv's `Store` checks its own commits against its level under
  `GOTOGETHER_CHECK=1` (it knows its versions); qhistory is for
  databases that do not tell.
- An application whose rows are append-only lists (examples/wallet's
  accounts) records its own history through any `qenv.DB` (a Store, an
  adapter over a real database) with `qhistory.Record`, instead of a
  hand-written Txn wrapper:

  ```go
  h := &qhistory.History{}
  db := qhistory.Record(store, h, func(key string, val any) (string, []int, bool) {
  	/* the key's list name and its elements; ok false for other keys */
  	return "", nil, false
  })
  /* run the app on db, then */
  rep, err := qhistory.Check(qhistory.Serializable, h.WithOps())
  ```

  Every attempt (each of `qenv.Retry`'s) is a transaction: a read the
  list it returned, a Put the append of its last element, a commit's
  `ErrUnknownOutcome` left Unknown (a Rollback after the commit records
  nothing), any other failure Aborted.

# Fitting gotogether to a service

Four questions a team asks before its first test: whether a service that
already emits spans can start without changing its code, why a service
that only records steps still needs Go 1.27.3, whether `qenv.Store`
behaves like the database it stands in for (Aurora DSQL here), and when
`testing/synctest` is enough. The README's [one path](../README.md#one-path-then-additions)
is where the tests themselves start.

## A service that already emits OpenTelemetry spans

A service whose spans already say what it did can be checked against a
model without importing gotogether, whatever Go version it builds with:

1. Write a rule file that maps spans to model steps: which spans are
   which action, their attributes as the action's arguments (`args`) and
   what the service observed (`obs`), and what orders concurrent spans
   (`order`). [examples/grpcledger/ledger.convert.yaml](../examples/grpcledger/ledger.convert.yaml)
   converts a gRPC server's otelgrpc spans; `gotogether.convert-1.json`
   (in `schemas/`) is the file's schema, and `gotogether lint` checks one.
2. Export the spans as OTLP/JSON (the collector's file exporter writes
   it), from a test, staging or production.
3. Check them, from CI, where the toolkit's Go is:

   ```sh
   gotogether validate -binding b.yaml -convert rules.yaml spans.jsonl
   gotogether convert -rules rules.yaml -out steps.jsonl spans.jsonl   # or keep the steps
   ```

   A span no rule matches is reported with the fix (name it, or `drop:
   true` for operations the model does not name).

What the model checks is what the spans carry. A library's spans name
the operation, rarely its effect: the ledger's server stamps each
request's span with the action and the balances at its commit
(`ledger.action`, `ledger.src_balance`), and a model whose actions
`expect` an observation needs that attribute. Spans the exporter lost
cannot be seen missing (README, Limits): a converted run conforms or not
as far as its spans go.

From there a service can move to recording steps where they take effect
(`qobs.Record`, `qobs.SlogHandler` for `log/slog` records, or
`//quint:action` with Orchestrion), and to the simulation, both of which
import the toolkit.

## Why the Go floor holds for a recording-only service

A service that imports gotogether only to record steps still needs Go
1.27.3. The project supports one current Go family
([go-toolchain.md](go-toolchain.md)), and a recorder on an older one
would be a second toolkit to keep:

- `qobs` writes the step log that `validate`, `qtrace` and Linearize read.
  A recorder built with an older Go would need its encoding proven equal
  to the reader's on every release; the port to 1.27.3 found one such
  difference, on invalid UTF-8.
- `qobs` also runs inside simulations that gosim translates, whose
  runtime target is 1.27.3.

The paths that need no import, and so no floor:

- spans and `convert` rules, as above;
- writing the step log directly, once its format is a documented,
  versioned contract (ROADMAP K9).

## `qenv.Store` and Aurora DSQL

`qenv.Store` at `qenv.SnapshotIsolation` is close to Aurora DSQL, which
runs every transaction at PostgreSQL's REPEATABLE READ with optimistic
concurrency:

| | `qenv.Store`, `SnapshotIsolation` | Aurora DSQL |
|---|---|---|
| reads | the snapshot at Begin | the snapshot at the transaction's start |
| two concurrent writers of a key | the second to commit aborts (`qenv.ErrConflict`; through `qenv/qsql`, SQLSTATE 40001) | the second to commit aborts (SQLSTATE 40001) |
| write skew | allowed | allowed, unless the reads are `FOR UPDATE` |
| `SELECT ... FOR UPDATE` (`GetForUpdate`) | locks the row: a second locker or writer **waits** until the transaction ends; a cycle of waits aborts one (`qenv.ErrDeadlock`, 40P01); a row changed since the snapshot aborts at once | takes no lock and never waits: the row is checked for a concurrent write at commit, and the transaction aborts then |
| per-transaction limits (rows modified, size, duration) | none | enforced (AWS's DSQL quotas) |

So a DSQL service checked on the Store finds its retry-on-40001 paths,
its lost updates and its write skew. What the Store does not show:

- a code path that relies on `FOR UPDATE` blocking (it never blocks on
  DSQL: the conflict comes at commit instead, after the work is done);
- deadlocks: DSQL has none, and a 40P01 the Store returns is not one a
  DSQL service will see;
- a transaction DSQL refuses for its size.

A DSQL profile for the Store (optimistic only, those limits; ROADMAP K5)
would close the gaps, checked against the history classifier as the
other levels are.

## `testing/synctest` or qenv

Both run concurrent Go code on a fake clock, for different questions:

| | `testing/synctest` | qenv (`qenv.New`, `qtest.System`) |
|---|---|---|
| goroutines, schedule | real goroutines, Go's scheduler | qenv's tasks (`qenv.Go`), one at a time; every interleaving a choice |
| time | the bubble's clock | virtual time (`qenv.Now`, `qenv.Sleep`) |
| faults | none of its own | the doubles' and the nemeses', each a choice |
| a failure replays | no: the schedule is Go's | yes, from its seed or Hegel's choices, and shrinks (`qhegel.Shrink`) |
| judged by | the test's assertions | the model, every run |
| code changes | none | the seams (or weaving: `qenv/weave`) |

Use `testing/synctest` for code whose question is timing: a timeout
fires, a ticker drains, a retry backs off, with no faults to inject and
nothing to replay. The toolkit's own pure in-memory concurrency tests do.

Use qenv when the question is what any interleaving and any fault can
do: lost responses, crashes, redeliveries, an isolation level's
anomalies, each run checked by the model and a failing one replayed.

The two meet in qenv's native runtime: `qenv.NewNative(c, nil)` runs real
goroutines with Go's scheduler, and inside a synctest bubble its sleeps
pass on the bubble's clock, with the faults still the chooser's and the
run judged by the model
([examples/checkout/synctest_test.go](../examples/checkout/synctest_test.go):
80 s of timeouts, retries and redeliveries in milliseconds). Such a run's
schedule is Go's, so it is not reproducible from its seed (DESIGN §34).
Under gosim (`qgosim`), synctest is not supported: gosim simulates the
runtime itself (DESIGN §14).

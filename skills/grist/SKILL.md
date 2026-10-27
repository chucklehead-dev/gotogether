---
name: grist
description: Build an event-sourced Go application on grist (gotogether's framework, slice 1 and slice 2's SQL store and HTTP API) so it is checked against a Quint model without annotations - events, plain and keyed read models, commands conditioned on their pinned reads (Unconditioned only on purpose), idempotency by command id (a key's first answer final, refusals included), queries, processors (Emit, Skip, Effect at least or at most once), options, deny-by-default authorization (Authorize, Internal), the HTTP API (NewHandler, Idempotency-Key, status codes) and its recording client (grist/client), grist.Main for an app's binary, the store contract (grist/store) with memstore, simstore and sqlstore (PostgreSQL, SQLite, qsql), commits recorded from the step log (grist.Relay), gristsim.Run as a qtest.System on simstore, the SQL tier or the HTTP tier, a real-tier run over HTTP on SQLite judged by the same binding, a hand-written model over one tenant's log and its bindings (store side and client side, actors:). Use when writing or changing a grist application, its model or binding, when a gristsim exploration fails, or when a store must keep grist's contract (grist/storetest).
---

# grist: an application checked by its model

grist (module `github.com/chucklehead-dev/gotogether/grist`, design in
docs/grist.md, slice 1 built, slice 2's SQL store and HTTP API built;
§0.2 to §0.5 say what changed) records every block's step itself: each commit and
committed refusal from the row it wrote in its own transaction (the
store's step log, read back by `grist.Relay`), each query on return,
each effect. The application writes
its decisions and folds; its model is one tenant's log.
examples/counters is the walk-through: read its README's table first.

## The loop

1. **Elicit** the blocks: events (structs, tags `grist:"tag"` keyed by
   the field's JSON name, integers in a stated unit, no floats or
   times), read models (`NewReadModel`, `NewKeyedReadModel` with one key,
   which must be a tag of every handler's type), commands (a decision:
   `*Reads` and arguments in, events or a `grist.Rejection` out), queries,
   processors (a command over several entities: one event per entity
   touched, a keyed read model per entity kind; read
   [`references/entities.md`](references/entities.md) when a command
   touches more than one). Reads: `X.Get(r)`, `X.For(r, key)`, `r.Exists(Event,
   grist.Tag(k, v))`, `grist.Events(r, Event, tags...)` (typed),
   `r.Select(grist.Selector{...})` (raw), and `r.Actor()` (the
   envelope's actor, recorded in every command's and query's step as
   `principal`: a rule over who asks, such as only a counter's creator
   retiring it, is the decision's `Rejection` and the model's, by
   `principal`; a `Policy` sees only the actor and the tenant). A read of a type the App does
   not register, or of a tag key the type has none of, is the decision's
   error, never an empty answer. Options (one `grist.Option` type):
   `Unconditioned(reason)`, `ConditionTries(n)`, `TransientTries(n)` on
   commands (and queries), `EffectTries(n)`, `BatchSize(n)` on
   processors, and on every command and query `Authorize(policy)`
   (`grist.Authenticated`, `grist.Actors(...)`, or a `func(actor, tenant
   string) bool`) or `Internal()`: blocks are denied by default, and the
   HTTP adapter refuses an App with a block that has neither. `App.Verify()` names every problem and its fix;
   `App.Catalog()` is the registry as data.
2. **Model first**: `model/<app>.qnt`, one tenant's log, imports and
   `export`s a vendored `grist.qnt` (copy `grist/model/grist.qnt`);
   state `log`, `checkpoints`, `appended`, `effects`, `answered`; read
   models are defs over the log. Each command's action takes its
   fields, `id`, `readPos`, `reads`, `principal` and `result`, and
   requires `freshKey`, `covers` and `conditionHolds` per outcome; each
   processor's actions follow its declared delivery (at least once: an
   effect step and a checkpoint; at most once: checkpoint, effect,
   outcome). **Read [`references/model.md`](references/model.md) before
   writing or changing a model**: every rule, the Quint names a Go
   field must avoid (`to`, `of`, `type`, `size`, ...), exactly-once
   claims, and how a double records its own effect. Check it alone:
   `gotogether simulate -binding <app>.binding.yaml`.
3. **Bind**: every parameter the same-named step field, but `reads:
   'readsOf({{toList .reads}})'` (slice 1 records triples); `expect:` for
   commits and refusals is `log.length() == {{.obs.head}} and
   batchIs(appended, {{toList .obs.events}}, {{toList .obs.strs}},
   {{toList .obs.ints}}, {{toList .obs.causes}})`, for a query the read
   models' defs against `.obs.*`. Outcomes include `conflict` and
   `idempotency-key-reused`. No `restart`: every step's process is the
   store. `qtest.Validate` logs "no model-level ITF ... readsOf is not a
   literal" for such a binding: harmless, the verdict is quint's, which
   evaluates `readsOf`; only the ITF file written beside the step log
   for inspection (`Run.ITF`) is skipped.
4. **Explore**: `qtest.System{Binding: ..., Rates: ..., Run: func(c
   qenv.Chooser) error { return gristsim.Run(c, App, cfg) }}`;
   `gristsim.Config` sets the isolation level (Serializable and
   ReadCommitted: Snapshot's runs are Serializable's, every append
   writing the head), `SQL` (the app on grist/sqlstore over qsql: the
   real tier's statements, qsql's SQLSTATEs and commit faults; the
   nemesis acts per scheduling step and qsql has more of them, so raise
   `store.<app>.commit`'s rate to reach commit faults in few seeds), `Tenants` and `Alphabet` (one tenant and two
   letters for contention), `CommitFaults`, `Check`, `Send`, `Setup` (the
   app's doubles, reached through a processor's ctx; a double records
   its own effect with `actor` the tenant and `process` the App's name:
   [`references/model.md`](references/model.md)). Every run records
   both sides: the store's steps (actor the tenant: commits relayed from
   the step log) and the clients' calls (`gristsim.ClientsActor`,
   `<tenant>/clients`: no tenant name holds a slash, `grist.CheckTenant`);
   the commits binding says `actors: ['!*/clients']`,
   a client-side one `actors: ['*/clients']`, judged with
   `sys.WithBinding(calls, qtest.Linearize())` over a client-side model
   module.
   Duplicated and late requests: `rpc.api.request` and `rpc.api.delay`
   rates. `HTTP: true` serves the node's HTTP API on qhttp and its
   clients call it with `grist/client` (the API's codes and the
   network's faults, `rpc.api.response` among them, under the same
   kinds).
5. **Prove each check can fail** with a mutant per claim (overlay files,
   as examples/counters' `blind` and `dispatchDirect`), and the faithful
   app at the mutant's configuration with the opposite claim
   (`None(Rejects("createCounter"))`): an `Unconditioned` create is
   `Rejects` when the race hits, at every isolation level (its reads
   come before its append's transaction, so no level protects it; under
   Serializable a loser whose append overlaps the winner's retries and
   decides again, which narrows the window: claim it at ReadCommitted,
   where it is wide, or with many seeds), a raw append
   from a processor is `Mismatch`, an at-least-once effect's duplicate
   is admitted and a test-only invariant over `effects` judges it
   (`qenv.Script` of `grist.<processor>.effect`'s `crashAfter`); an
   at-most-once effect's loss (`crashBefore`, after its checkpoint) is
   admitted and a test-only exactly-once eventual val rejects it; an
   effect declared at most once where the model says at least once is
   `Rejects(<processor>)` (counters' `checkpointFirst`). A declared
   guarantee contradicting the model is rejected at its first step,
   before what it would cause (wallet-grist's `noOutbox` is
   `Rejects("post")`, never a lost post): a mutant of the declaration
   shows the mismatch, not its consequence.

## Triage

- An exploration's summary says "model actions taken N of M (never:
  ...)": M counts the model's actions but `init`, `step` and frame
  helpers, so helper actions other actions call (`appends`, `refuses`)
  are in it and always listed as never taken; read the list for the
  commands and processors no run reached.

- `Rejects(command)`: a commit the model's precondition forbids: the
  decision's rule, its condition, a read missing from the read set
  (`covers`), or a key answered twice (`freshKey`: a refusal's late
  duplicate committing).
- `Mismatch(command or processor)`: the recorded head, batch or causes
  differ from the model's: an event appended outside grist (a raw append
  shows at its tenant's next step), or a fold that disagrees with the
  def.
- `grist.ErrOutcomeUnknown`: the transient budget ran out after an
  append whose outcome is unknown, or the context ended while one
  committed; retry with the same id (gristsim's clients do).
  `grist.ErrUnavailable`: nothing committed.
- `Relay.Err()` (gristsim.Run returns it): a step row the relay cannot
  read (`"v"` another grist's: upgrade the node) stopped its tenant, or
  a read keeps failing.
- A run that does not settle names its tasks: a poller is a daemon; a
  client waiting means the node is down (gristsim restarts it from an
  `OnCrash` hook).
- A store of your own implements `store.Store`, `store.StepLog` (package
  grist/store: each commit's step written in its transaction and timed
  under the tenant's lock, `StepRow.Time` from `store.StepTime`; `Steps`,
  `WaitSteps`, `StepCount`; a node records commits from it alone) and
  `store.TenantFeed` (`WaitTenants`: a node starts tenants created after
  it): run `storetest.Run(t, harness)`; its checks compare conditions and
  reads (selectors of one or two tags) and concurrent appends with brute
  force over every event, wake a `Wait`, a `WaitSteps` and a
  `WaitTenants`, and mutate readers' copies; two Stores over one
  database, `storetest.TwoStores(ctx, a, b)`. Errors no retry fixes are
  `store.Permanent`; transient errors before the COMMIT are
  `store.NotCommitted` (else grist answers `ErrOutcomeUnknown`).
- Tenants, ids and tag values are valid UTF-8 without NUL (PostgreSQL's
  TEXT), a tenant without a slash: grist refuses others with the fix.
- **The real tier**: `sqlstore.Open(ctx, db, sqlstore.Postgres |
  sqlstore.SQLite, sqlstore.Options{Name: app.Name})` on the
  application's own driver (SQLite: `sql.Open("sqlite",
  sqlstore.SQLiteDSN(path, 5*time.Second))`: WAL and busy_timeout,
  checked; `Open` checks the options, the isolation level by beginning
  a transaction at it);
  `App.Start(ctx, st, grist.WithRelay(relay))` with a recorder writing
  `qobs.FileSink(path, qobs.PerStep)`; clients record their calls with
  `grist/client`'s `Client.Do` over HTTP, or `gristsim.Do` in process (as a run's clients do); before recording a settle, wait
  for the processors' checkpoints and `Relay.Behind(ctx) == 0`, failing
  on `Relay.Err()` (`grist.Main serve` does so itself on shutdown and
  records the settle); validate the file with both bindings
  (`qtest.Validate(t, binding, steps)`, the clients' with
  `qtest.Linearize()`), and check each client's answer has its store-side
  step (a lost refusal step passes both bindings). examples/counters'
  `TestRealHTTP` is the walk-through (a node killed mid-run, a second
  one recording what it never did). A second node's relay records the
  log again from its first row; Reconstruct drops the copies (info
  issues "seen twice"); a durable relay cursor is designed, not built
  (docs/grist.md §0.4), so validate a node's step files together.
- **The HTTP API**: `grist.NewHandler(app, st, opts...)` serves `POST
  /t/{tenant}/commands/{name}` (header `Idempotency-Key`: the command id)
  and `GET /t/{tenant}/queries/{name}?field=value`; 200 with the receipt
  (the same on every retry of the key), 422 `{"rejected": name}` (a
  rejection, `idempotency-key-reused`), 409 conflict, 503 with
  `Retry-After` (`{"retry": "outcome-unknown"|"unavailable"}`: send it
  again under the same key), 400 (`grist.ErrBadArgs`: not UTF-8, a
  field missing or null, a NUL byte, an integer past int64; every field
  is required but a pointer's), 403 (`denied`), 404 (an Internal block
  too), 413 (past `grist.MaxBody`), 500; a replay of a key's answer
  carries `Idempotent-Replayed: true`. The actor is
  `grist.WithActor`'s, set by `grist.Middleware` (the app's
  authentication, which wins) or `grist.ActorFromHeader` (tests, behind
  a proxy: only for a request no middleware authenticated);
  `grist.Dependencies` attaches the app's doubles. `grist/client`'s
  `Client.Do` calls it and records the call as a client's (retrying
  under the key with jittered exponential backoff within a retry
  `client.Budget`; a 5xx that is not grist's is an unknown outcome,
  retried); the handler records nothing of its own. A field added to a
  command's arguments with a zero default keeps a key's answer (the
  marker hashes the arguments' canonical value).
  examples/counters' `TestRealHTTP` is the walk-through of a real tier
  over HTTP.
- **An app's binary**: `func main() { grist.Main(app, opts...) }` with
  the driver imported: `catalog` (the registry as JSON, types as JSON
  Schemas), `verify`, `serve -db sqlite:PATH|postgres:DSN -tenant t1
  -steps steps.jsonl [-actor-header X-Actor]`; validate the steps file
  with the app's binding (`gotogether validate -binding ...`).
  examples/counters' `cmd/counters` and `SERVE=1 ./run-demo.sh`.
- **PostgreSQL**: `sqlstore.Postgres` states read committed and sets
  `lock_timeout` and `idle_in_transaction_session_timeout` per
  transaction; grist/sqlstore's contract test runs against one with
  `GRIST_POSTGRES=<pgx dsn>` (CI's grist job has a postgres:16 service;
  locally a scratch server: `initdb -D dir`, then `postgres -D dir -k
  sockdir`).

## References

- [`references/model.md`](references/model.md): read before writing or
  changing a model: the state, every command's and processor's rules
  (at least once, at most once, exactly once by test-only vals), the
  Quint names fields must avoid, a declaration the model contradicts,
  and a double's record of its own effect.
- [`references/entities.md`](references/entities.md): read when a
  command reads or changes more than one entity (a transfer, an order
  of several items): events per entity, keyed read models per entity
  kind, the read set and `needs`.

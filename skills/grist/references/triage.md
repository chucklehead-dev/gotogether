# Triage: what each grist failure means and what to do

From the grist skill (its SKILL.md says when to read this). The user
guide's [troubleshooting table](https://github.com/chucklehead-dev/gotogether/blob/main/docs/grist-guide.md#10-troubleshooting)
and [running in production](https://github.com/chucklehead-dev/gotogether/blob/main/docs/grist-guide.md#9-running-in-production)
are the person-facing account (docs/grist-guide.md); this page adds what
an agent reads off a failing run.

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
  `grist/client`'s `Client.Do` over HTTP, or `gristsim.Do` in process (as a run's clients do). `gristtest.NewTier(t, App, gristtest.TierConfig{Tenants, Serve, Local, Deps})`
  is all of it as a library: `Start(id)` a node (its `Kill`: its sink
  dead and nothing committed after, a second `Start` taking the traffic;
  `Current()` the node taking it), `Clients()` and `Do` for the clients,
  every fifth command's answer lost (`LoseEvery`); `Serve` runs a
  dependency's handlers behind `grist.ServeCalls` on a loopback server
  of the tier's (its own step file, read with the nodes' by `Steps`,
  `DependencySteps(dep)` alone), the nodes calling it with
  `grist.OverHTTP`, every fifth answer lost after the call was applied
  (`DependencyLoseEvery`, a 502; `OnLostAnswer` runs then: kill a node
  there for the lost-answer-and-crash shape), `Local` serves handlers in
  process, one `grist.Local` for the tier, `Deps` attaches the rest;
  `Settle(probe)`
  waiting for every processor and the relay before it records the
  settle, `Check(commits, calls)` validating both bindings and that every
  client's answer has its store-side step (`gristtest.Answered`; a
  real run's concurrent calls may need `qtest.LinearizeBudget(n)`, passed
  to `Check`). By hand: before recording a settle, wait
  for the processors' checkpoints and `Relay.Behind(ctx) == 0`, failing
  on `Relay.Err()` (`grist.Main serve` does so itself on shutdown and
  records the settle); validate the file with both bindings
  (`qtest.Validate(t, binding, steps)`, the clients' with
  `qtest.Linearize()`), and check each client's answer has its store-side
  step (a lost refusal step passes both bindings). examples/counters'
  `TestRealHTTP` is the walk-through (a node killed mid-run, a second
  one recording what it never did; retirements paid by a bank behind
  `ServeCalls` that loses answers; a third party's mailer over
  `OverHTTP`, so it is judged by `counters.real.binding.yaml`), and
  `TestRealLostThenKilled` the lost answer with the node killed (the
  bank's file the payment's only record). A second node's relay records the
  log again from its first row; Reconstruct drops the copies (info
  issues "seen twice"); a durable relay cursor is designed, not built
  (docs/HISTORY.md (grist §0.4)), so validate a node's step files together.
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
  carries `Idempotent-Replayed: true`. The caller is
  `grist.WithCaller`'s, set by `grist.Middleware` (the app's
  authentication, which wins) or `grist.CallerFromHeader` (tests, behind
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
  -steps steps.jsonl [-caller-header X-Caller] [-dependency NAME=URL]
  [-health-addr :8081] [-grace 45s]`;
  `grist.ServeDependency(dep, handlers)` serves a dependency the team owns
  beside the node over HTTP; validate the steps file
  with the app's binding (`gotogether validate -binding ...`). The node
  records as `<app>/<node>/<incarnation>` (`grist.Identity`: `GRIST_NODE`,
  else `POD_NAME`, else the host name, and a fresh nonce per start;
  `WithIdentity` in tests), so several pods' or starts' files merge (give
  `-steps` a directory for a file per start; in a pod always a directory:
  a restart truncates a `-steps FILE`, losing the earlier start's steps,
  and serve warns); never name a node by its
  pid (1 in every container: Reconstruct's duplicate error, no verdict).
  On SIGTERM the drain (`grist.Drain`, `WithDrain`): `/readyz` fails, the
  server serves on for `Delay` (5 s in a pod), then shutdown, settle and
  flush, 10 s each: give the pod a 45 s grace, or pass `-grace` to budget
  the phases into a shorter one (the startup line says what each gets).
  `-health-addr` (on by default in a pod) serves `/livez` (a poller stuck on
  one trigger past 5 minutes; never the store), `/readyz` (started, not draining;
  never a dependency or processor lag: point no probe at the database)
  and `/metrics` (processor lag, 0 when no trigger waits, and oldest trigger, relay backlog,
  effects and requests in flight). In a pod `-addr` on loopback is
  refused (give `-addr :8080`); `GOMEMLIMIT` unset is 90% of the
  cgroup's `memory.max`.
  examples/counters' `cmd/counters` and `SERVE=1 ./run-demo.sh`.
- **A cluster, simulated**: `gristsim.Config{Cluster: &gristsim.Cluster{
  Grace: 45 * time.Second, EndpointLag: 3 * time.Second, Disruptions: 1,
  Drain: app's drain, Rolling: true}}` runs the nodes (3 by default) as an
  orchestrator does: every node serves the API behind a router whose view
  lags (`route.<app>.lag`, option `stale`; each node's API
  `rpc.<app>.<node>.*`), each node may be sent SIGTERM
  (`nemesis.terminate.<node>`; `Rolling` restarts each in turn under
  load) and drains as `Main` does (the same `grist.Drain` schedule: serve
  on for the delay, shut down, quiet, stop, flush, exit), then restarts.
  A client's try no server took (an endpoint gone, a listener closed) is
  retried and counted on its call's step (`refused`); judge the clients'
  binding with `qtest.All(gristtest.NoClientErrors())` (and
  `qtest.Some(qtest.Not(gristtest.NoClientErrors()))` for a mutant that
  causes them). Give the drain's delay more than the
  router's lag, and never cancel an effect's context at the signal
  (`grist.Terminating` is for work that may stop at once): an
  at-most-once call cancelled in flight is `grist.unknown`. Not on the
  HTTP tier yet. examples/counters' `TestRollingRestart` and, on real
  pods behind a router polling `/readyz`, `TestRealRollingRestart`
  (`ROLLING=1 ./run-demo.sh`).
- **PostgreSQL**: `sqlstore.Postgres` states read committed and sets
  `lock_timeout` and `idle_in_transaction_session_timeout` per
  transaction; grist/sqlstore's contract test runs against one with
  `GRIST_POSTGRES=<pgx dsn>` (CI's grist job has a postgres:16 service;
  locally a scratch server: `initdb -D dir`, then `postgres -D dir -k
  sockdir`).

## References

- [`references/model.md`](model.md): read before writing or
  changing a model: the state, every command's and processor's rules
  (at least once, at most once, exactly once by test-only vals), the
  Quint names fields must avoid, a declaration the model contradicts,
  and a double's record of its own effect.
- [`references/entities.md`](entities.md): read when a
  command reads or changes more than one entity (a transfer, an order
  of several items): events per entity, keyed read models per entity
  kind, the read set and `needs`.

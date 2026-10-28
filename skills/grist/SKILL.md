---
name: grist
description: Build an event-sourced Go application on grist (gotogether's framework, slice 1 and slice 2's SQL store and HTTP API) so it is checked against a Quint model without annotations - events, plain and keyed read models, commands conditioned on their pinned reads (Unconditioned only on purpose), idempotency by command id (a key's first answer final, refusals included), queries, processors (Emit, Skip, Effect at least or at most once), options, deny-by-default authorization (Authorize, Internal), the HTTP API (NewHandler, Idempotency-Key, status codes) and its recording client (grist/client), grist.Main for an app's binary, the store contract (grist/store) with memstore, simstore and sqlstore (PostgreSQL, SQLite, qsql), commits recorded from the step log (grist.Relay), gristsim.Run as a qtest.System on simstore, the SQL tier or the HTTP tier, a real-tier run over HTTP on SQLite judged by the same binding, the model's skeleton and both bindings generated from the App's catalog (grist model: the app writes only its rules and invariants), grist new for a new app, and grist/gristtest's harnesses (Generated, Catalog for qtest's catalog claims, RuleMutants for qtest.Mutants, Tier). Use when starting, writing or changing a grist application, its model or binding, when a gristsim exploration fails, or when a store must keep grist's contract (grist/storetest).
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

## The first hour

`go run github.com/chucklehead-dev/gotogether/grist/cmd/grist new
<name>` (`-module`, `-dir`; `-replace <checkout>` inside this repository)
writes a module green on its first run: an App (two events, a keyed
read model, a command with declared refusals, a query, a processor), its
binary, `model/<name>_rules.qnt` with its rules filled and
`model/<name>.qnt` with its invariants, the generated skeleton
and bindings, a gristsim test with `Expect` claims, a mutant, a
real-tier test, `run-demo.sh` and a README. Then change it one block at
a time: the block in Go, `go run ./cmd/<name> model` (it names each new
hole with a stub), the hole in `model/<name>_rules.qnt`, a claim and a mutant
for it. No binding is written by hand.

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
   `r.Select(grist.Selector{...})` (raw), and `r.Caller()` (the
   envelope's caller, the authenticated principal, recorded in every
   command's and query's step as `caller`: a rule over who asks, such as
   only a counter's creator retiring it, is the decision's `Rejection`
   and the model's, by `caller`; a `Policy` sees only the caller and the
   tenant). A read of a type the App does
   not register, or of a tag key the type has none of, is the decision's
   error, never an empty answer. Options (one `grist.Option` type):
   `Unconditioned(reason)`, `ConditionTries(n)`, `TransientTries(n)` on
   commands (and queries), `EffectTries(n)`, `BatchSize(n)` on
   processors, and on every command and query `Authorize(policy)`
   (`grist.Authenticated`, `grist.Callers(...)`, or a `func(caller, tenant
   string) bool`) or `Internal()`: blocks are denied by default, and the
   HTTP adapter refuses an App with a block that has neither. `App.Verify()` names every problem and its fix;
   `App.Catalog()` is the registry as data.
2. **Declare the shapes, generate, fill the holes**: each command's
   refusals (`grist.Refuses(ErrA, ErrB)`; another rejection is the
   decision's error); a requirement on every append of an event, whichever
   command makes it, as a rule (`grist.NewRule(App, "loan-limit",
   Borrowed, func(r *grist.Reads, e BookBorrowed) error {...},
   grist.Refuses(ErrLimit))`: it runs after an ok decision on the
   command's own reads, which join the condition, and its rejection is
   the command's refusal, added to its catalog's refusals; the commands
   appending the event declare it, `grist.Appends(Borrowed)`; Verify
   refuses a rule on an event no block appends; a processor's Emit a rule
   refuses fails its trigger, naming the rule), each processor's result (`grist.Emits(Type)` or
   `grist.Delivers(grist.AtLeastOnce|grist.AtMostOnce, SuccessType)`;
   another result fails the trigger), a remote party an effect calls as
   a declared dependency and its typed calls (`var Bank =
   grist.NewDependency(App, "bank")`, `var Pay = grist.NewCall[PayRequest,
   PayReceipt](Bank, "pay", grist.Keyed(), grist.Refuses(ErrDeclined))`,
   requests and responses structs of scalars, the processor declaring
   `grist.Uses(Pay)` beside its `Delivers`; in the effect `_, err :=
   Pay.Do(ctx, req)`, whose error is already a refusal's `NotApplied` or
   an unknown outcome): grist records the caller's view (`bankPay`, an
   invocation and its completion) and the bank's application
   (`bankPayServed`, by the handler's wrapper, ordered after the commit
   holding the trigger), and gristsim serves each dependency as a
   `qenv.Service` named after it (`rpc.bank.*`), by default with its
   **default double** (`grist.DefaultHandlers(dep)`: a `Keyed` call
   applied once per key and acknowledged, a later try of the key replayed
   from that first answer; an unkeyed call applied every time), for a
   dependency whose calls answer no fields (an acknowledgement, `struct{}`):
   most apps write no double for those. A call whose response has fields
   (a receipt's reference) has no default: what the dependency answers is
   its own, so the run is an error naming the fix until you give one.
   Give a double to answer (a receipt, a decline) or as a mutant (a double
   that applies every try), in the run's `Config.Doubles` (`func() gristsim.Doubles`:
   `gristsim.Doubles{Bank: grist.Handlers{Pay.Handler(f)}}`, per run; a
   deduplicating double answering a retry from its first answer calls
   `grist.Replayed(ctx)`, recorded and judged as `bankPayReplayed`, and
   keeps each key's first answer, a refusal included; `grist.CallKey(ctx)`
   the key). Outside a simulation attach a transport where the node
   starts: over HTTP `Bank.With(ctx, grist.OverHTTP(baseURL, nil))`
   (`POST base/<call>`, the key as `Idempotency-Key`, the cause as
   `Grist-Cause`; 2xx the response, 422 `{"rejected": name}` a refusal,
   anything else an unknown outcome, another 4xx and a redirect, which
   it does not follow, included: only a declared refusal says nothing
   happened; one attempt, never retried), in process
   `Bank.With(ctx, grist.Local(handlers))` (its site records going to the
   node's recorder), in `grist.Dependencies`. A dependency the team owns
   serves its handlers with `grist.ServeCalls(dep, handlers)` (nil: the
   default double, which refuses a call answering fields), wrapped in
   `qobshttp.Handler(h, rec)` so its site records go to its own recorder
   (its own step file, validated with the caller's), into the caller's
   tenant's trace, each judged against the processor's rule: serve one
   per dependency, whichever node calls. It authenticates nobody (the
   `Grist-Cause` is the caller's word, refused only when it names
   another App, a processor that does not use the call, or a key that
   is not the effect's): serve it where only the App's nodes reach it,
   or behind the service's own authentication. `grist.Main` serves one beside
   the node with `grist.ServeDependency(dep, handlers)` (a loopback port,
   its records in the steps file under a recorder of their own), and
   `serve -dependency NAME=URL` calls another over HTTP. A third party's
   dependency (a real bank, a mail provider) records no site: judge such
   a real tier by a test-only variant, `<app>.real.binding.yaml`, whose
   vals are marked `// grist: ... real` and leave out the site claims
   (an eventual val over `<c>Records`), stated in simulations instead
   (docs/grist-instrumentation.md §5, decision 4). Do outside an effect, or of a call
   the processor does not list in `Uses`, is the effect's error. What is
   no call stays a typed `grist.NewDep[T](name, fallback)` (T best an
   interface, its fallback a stand-in refusing every call with the fix;
   `Dep.Of(ctx)` in an effect, `Dep.With(ctx, v)` where the node starts:
   gristsim's `Setup`, `grist.Dependencies`). A double grist does not
   serve may record its effects with a `grist.NewSite[T](App, name)`
   (`Site.Record(ctx, tenant, v)`): an escape hatch no example needs
   (its records are ordered with no commit; a call's are). Then
   `go run ./cmd/<app> model` (`grist.Main`; `-init` the first time)
   writes `model/<app>_gen.qnt` (events, reads), `model/<app>_steps.qnt`
   (an action per block with its outcome branches and frame conditions,
   `step`), the vendored `grist.qnt` and the bindings; you write the rules
   in `model/<app>_rules.qnt`
   (per command its outcome, events and needs; per query its answer;
   per processor its events, and a Delivers one which triggers it skips;
   per processor and call it uses the request it makes, `<p><Call>`; per
   site its guard; the read models' folds)
   and its invariants in `model/<app>.qnt`, each marked `// grist:
   invariant|eventually|violable [bindings]` (a binding name other than `default` is a test-only
   variant, `<app>.<name>.binding.yaml`). Every trigger handled
   (`<processor>EventuallyHandled`) and every answered call of a `Keyed`
   dependency served (`<call>EventuallyServed`) are generated
   obligations, judged at the settle: write only eventual vals that say
   more. **Read
   [`references/model.md`](references/model.md) before writing or
   changing a model**: the holes, the marks, the names Quint refuses,
   exactly-once claims. Check it alone: `gotogether simulate -binding
   <app>.binding.yaml`: its invariants, and the generated `possible:`
   entries, one per command outcome (ok, each refusal, its rules' too)
   and per query, processor step, call and site, each reached by the
   model on its own or FAIL (rules too strict for any conforming run to
   show it: a model that can never refuse `limit` fails here).
3. **Witness**: `func TestGenerated(t *testing.T) { gristtest.Generated(t,
   App) }` fails when the checked-in skeleton or bindings are not what
   the catalog generates now (regenerate; `GRIST_UPDATE=1` writes them)
   or a hole is missing, or a binding's module does not import the
   events and the rules itself (the validator's evaluator needs it). The bindings are never written by hand:
   every parameter the step's field (the read set's triples by the
   default template), the outcome as `result`, each commit's head and batch and
   each query's answer compared with the model's, `actors:` per side,
   no `restart` (every step's process is the store).
4. **Explore**: `qtest.System{Binding: ..., Rates: ..., Run: func(c
   qenv.Chooser) error { return gristsim.Run(c, App, cfg) }}`;
   `gristsim.Config` sets the isolation level (Serializable and
   ReadCommitted: Snapshot's runs are Serializable's, every append
   writing the head), `SQL` (the app on grist/sqlstore over qsql: the
   real tier's statements, qsql's SQLSTATEs and commit faults; the
   nemesis acts per scheduling step and qsql has more of them, so raise
   `store.<app>.commit`'s rate to reach commit faults in few seeds), `Tenants` and `Alphabet` (one tenant and two
   letters for contention), `CommitFaults`, `Check`, `Send` (each op drawn
   uniformly from the list: a name listed twice is sent about twice as
   often), `Prologue` (`[]gristsim.Call{{Name: "join", Args: Join{...}}}`:
   calls made in order, each answered, before any client's first: the
   state a block that needs others first depends on, a member joined
   before borrows), `Doubles` (a declared dependency's handlers, else its
   default double), `Setup` (what is no call: a `grist.NewDep`'s value,
   reached through a processor's ctx; a `NewSite` double records its own
   effect with `actor` the tenant and `process` the App's name:
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
   **Claim the runs reached the app**: a faithful test adds
   `gristtest.Catalog(App).Exercised(except...)` (qtest's
   `Catalog.Exercised` over the App's catalog; `Catalog.Take(name)` one
   block, `.InAtLeast(share)` in a share of the runs), every block of the catalog
   taken in some run (each command by `ok` and each declared refusal,
   its rules' included, every query, a processor's handling, effect and checkpoint, every
   site's record), naming each missed with how to reach it; except
   names a block (`"receipts"`), a command's outcome (`"borrow.limit"`)
   or a command whole, each with its reason. A run that took none of the
   app's blocks passes every other claim. It is not applied by default:
   a contrast test's workload is narrow by design, and claims the one
   outcome it needs, `qtest.Takes("borrow").Outcome("limit")`.
   **Acceptance scenarios** in the catalog's values:
   `gristtest.Scenario("at the limit", JoinCmd.With(Join{Member: "a"}),
   ..., BorrowCmd.With(Borrow{Member: "a", Book: "b"}).Refused(ErrLimit))`
   (`Command.With(args)`, `Invocation.Refused(err)`: a declared refusal),
   checked possible in the model first (`qtest.Possible(t, commits,
   scenario)`, arguments in the model's STRS and INTS) and claimed of
   runs like any expectation (`All(scenario)`, `Some`): a miss names, per
   run, how far it got and where it stopped. The
   summary's "never" list names the binding's actions (the app's
   blocks) apart from grist's helpers (`appends`, `readsThrough`).
5. **Prove each check can fail** with a mutant per claim (files in
   `testdata/mutants/<name>/` replacing the app's of the same names, run
   found and run by the core's `qtest.Mutants{FullOnly: ...}.Run(t)` from
   a `TestMutants`, each binary's `TestMutant` switching on
   `qtest.Mutant()`, as examples/counters' `blind` and
   `dispatchDirect`; a mutant carries the declarations its code needs;
   `qtest.Mutants{Generated: gristtest.RuleMutants(App)}` adds a generated `drop-<rule>` per rule, the
   app with the rule dropped, which `TestMutant` handles before its
   switch: `if _, caught, ok := gristtest.DroppedRule(App); ok {
   system(...).Explore(...).Expect(t, caught); return }`, at a workload
   where the rule refuses, as examples/library's `drop-loan-limit`),
   and the faithful
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
  lags (`route.api.lag`, option `stale`), each node may be sent SIGTERM
  (`nemesis.terminate.<node>`; `Rolling` restarts each in turn under
  load) and drains as `Main` does (the same `grist.Drain` schedule: serve
  on for the delay, shut down, quiet, stop, flush, exit), then restarts.
  A client's try no server took (an endpoint gone, a listener closed) is
  retried and counted on its call's step (`refused`); judge the clients'
  binding with `gristtest.NoClientErrors()` (and `ClientErrors()` for a
  mutant that causes them). Give the drain's delay more than the
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

- [`references/model.md`](references/model.md): read before writing or
  changing a model: the state, every command's and processor's rules
  (at least once, at most once, exactly once by test-only vals), the
  Quint names fields must avoid, a declaration the model contradicts,
  and a double's record of its own effect.
- [`references/entities.md`](references/entities.md): read when a
  command reads or changes more than one entity (a transfer, an order
  of several items): events per entity, keyed read models per entity
  kind, the read set and `needs`.

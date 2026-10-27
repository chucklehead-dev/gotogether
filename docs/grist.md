# grist: an event-sourced application framework checked by its model

Status: slice 1 built (grist/, examples/counters; §0.1 says what
building it changed here, §0.2 what its review changed); slice 2's SQL
store built (grist/sqlstore, the relay, gristsim's SQL tier, a real tier
on SQLite: §0.3; its review: §0.4), its HTTP API, `grist/client` and
`grist.Main` built (§0.5), `AtMostOnce` built (§0.6), their review's
fixes (§0.7), a newcomer's trial's fixes (§0.8), the generator, `grist
new` and the test harnesses built (§0.9); gristflow is design, as is
slice 3. Decisions taken 2026-10-27 (a module
inside this repository, event sourcing first, Quint and Choreo as the
specification languages, an API layer and Datastar, the G track goes on
alongside); revised the same day after a review, every point of which
was checked against the code (§0). This note says what to build and in
what order (§8: three slices); the Go below is **proposed API** of
packages that do not exist yet (`grist`, `grist/simstore`, ...), written
so that it parses. Every name it takes from an existing package (`qenv`,
`qobs`, `qtest`, ...) exists today. "grist" is a codename (§10.5).

grist takes the shape of ObneyAI/grain (Clojure): an application is a
fixed set of blocks (commands, events, read models, queries,
processors, periodic tasks, screens) wired by a declared topology, on
an event store that serializes appends per tenant. grain verifies by
linking prose specs to names and by generated example tests. grist's
reason to exist is the other half: because every block is the
framework's, the framework records each block's step, declares each
seam's faults and knows the topology, so a grist application is
checked against a Quint model, explored in a simulation, shrunk and
mutated without one annotation of its own. What the application writes
is its decisions and folds; what it gets is gotogether.

## 0. What the review changed, and where the code disagreed

Adopted: tenants as actors with one fixed process and a domain clock
(§3.1); reads pinned to one position (§2.3); processors reading as of
their trigger (§2.6); checkpoints in a table (§2.6, §10.2); verdicts as
Rejects and Mismatch where the model's precondition or expectation is
what fails (§3.2); qsql's real prerequisite as its own item (§8.2); the
SQL tier's isolation, retries and timeouts (§2.10); idempotency in full
(§2.4); the binding's typed values and recorded read set (§3.2);
client-side steps (§3.1); three slices instead of one spike (§8).

Where the code said something other than the review, or than the first
version of this note:

- **The ordering rule is DESIGN §3 and §4, not §6.** `qtrace.build`
  orders by a domain clock only within one (actor, process), equal
  values concurrent, every step of a value before every step of a
  larger one; hence the fixed process (§3.1). Thread (program order)
  edges apply too, so the encoding must agree with each task's order:
  the review's 2·P+1 holds for queries and rejections, but a
  processor's checkpoint-only step at 2·T+1 (T its trigger) would make
  a cycle (emit at 7 for trigger 5, then checkpoint trigger 6: 14
  before 13 on one thread). §3.1 uses the head its transaction read.
- **A settle is voided by a later step on any recorder**, not only its
  own (beyond `RealTimeSlack`): probes on a separate recorder are sound
  only while they take no virtual time, so §3.6 settles again after them.
- **qsql already has `ORDER BY pk [ASC|DESC]` and `LIMIT`.** It lacked
  keys of several columns (42P16, "qsql keys rows by one column": the
  first note's tables were keyed by two and three), ranges, `RETURNING`
  and `DO UPDATE`, which slice 2's first core item added (§8.2); its
  isolation is store-wide.
- **`qenv.FaultPoint` has one crash option**, `crash`: the effect's two
  crash points are a `DeclareFaults` kind and `Sim.FaultOf` (§3.5).
- **qobs encodes structs with `fmt.Sprint`**, not JSON, and binding's
  `jsonElem` renders objects as quoted strings and floats truncated:
  grist encodes events itself, and binding needs a typed function (§3.2;
  `toQuint`, added for slice 2, §8.2).
- **One System has one binding, and qtest had no actor filter**: a
  run records commit steps or client steps, not both (§3.1). Slice 2's
  third core item adds the filter (a binding's `actors:`, §8.2).
- **`Scan` walks every row of the store** (a Serializable commit again
  per scanned prefix), with no range: simstore's cost grows with the
  store, not the tenant (§2.10).
- **`GetForUpdate` under Snapshot and Serializable aborts with
  ErrConflict** if the key changed since the snapshot: a lost race for
  the head is transient, not a failed condition (§2.4).
- **qlint has no map-range rule**: gristpure needs one written (§4.1).
- **`GOTOGETHER_STRICT=1` turns toolkit skips into failures**
  (`qtest.Optional` included), nothing more: stubs fail under it only if
  reported through `qtest.Optional` (§3.2).

Decided differently from the review: `leakTenant` is dropped, tenant
isolation being a contract-suite test with a mutant store (§3.3). (The
first version also decided that rejections are not remembered under
their idempotency key; slice 1's review showed that wrong, §0.2.)

## 0.1 What building slice 1 changed

Implementing slice 1 (grist/, examples/counters) showed this note wrong
or incomplete in these places; each is fixed where it stands below.

- **Claim 1 runs at ReadCommitted.** Under Snapshot and Serializable the
  loser of a race for the head gets `ErrConflict`, a transient error
  whose retry starts again at the marker and decides again on fresh
  reads: that closes most of the blind create's window (2 of 40 runs
  race). At ReadCommitted the loser waits for the head's lock and
  commits blind (25 of 40). After slice 1's review counters' shape
  showed none under Serializable (0 of 120 runs) and claim 1 is
  ReadCommitted's (30 of 40, §8.1). **The window is narrowed, not
  closed** (§0.8): a decision's reads are taken before its append's
  transaction begins, so no isolation level protects an `Unconditioned`
  decision; examples/wallet-grist's `readThenWrite` commits an overdraft
  at Serializable, and counters' `blind` a second create of a name at
  four clients.
- **The read set's step fields are `readPos` and `reads`**, not
  `read.pos` and `read.queries`: a model parameter is a Quint identifier,
  and a binding gives templates only where a parameter is not the
  same-named step field. (The recorded read set was the *condition*,
  `[]` for an `Unconditioned` command; since slice 1's review it is what
  the decision read, whatever the condition, §0.2.)
- **The effect step is `<processor>Effect`** (`welcomeEffect`), not
  `<processor>.effect`: a step's action is its model action's name. Its
  arguments are `trigger`, `key` and `ok`. The fault kind keeps the dots
  (`grist.welcome.effect`).
- **A processor's steps are on one thread per poller** (`qobs.Thread`):
  an effect step and a checkpoint-only commit can both sit at 2·H+1 and
  on the same `order.p.<processor>` value, and only program order puts
  the effect first.
- **A refusal records the head and an empty batch** (`obs.head`,
  `obs.events: []`), so one `expect:` serves a command's every step.
  Every commit records its batch as `obs.events` (types), `obs.strs` and
  `obs.ints` (the events' string and integer fields in struct order):
  scalars and lists, all slice 1's binding has. Processor steps add
  `obs.checkpoint`.
- **The model keeps two ghost variables** beside the log and the
  checkpoints: `appended` (the last step's batch, what its observations
  are compared with) and `effects` (the effect steps, what at-least-once
  and `welcomedOnce` count). The model module `export`s grist.qnt's and
  the library's defs, or the binding's expects cannot name them.
- **`grist.failed` carries the processor and the trigger, not the
  error's text**: the model could not compute it. A handler's error is
  `grist.Fail(err)`, tried `EffectTries` times like an effect's.
- **The Store interface has `Name`, `Marker` and `Checkpoint`**, and
  `Query` an `After` (a poller reads after its checkpoint); `Batch`
  carries `Marker`, `Checkpoint` (a compare-and-set), `Condition`, `Time`,
  `Cause` and the commit hook. A failed checkpoint is a
  `*CheckpointMoved`; a marker that exists a `*ConditionFailed` naming it.
  The node (pollers, the effect inline in its poller) is `App.Start`, not
  a `runtime/` package. (The types lived in package grist; slice 1's
  review moved them to `grist/store`, §0.2.)
- **A command's result is a `grist.Receipt`** (`Result` is a processor
  handler's).
- **Keyed read models take one key in slice 1**, and a query one tag: the
  read set is (type, tag, value) triples. `Keys("account", "currency")`
  waits for slice 2's typed binding function.
- **A poller is one task per (processor, tenant)**, waiting on the
  store's signal (`Store.Wait`) rather than polling every 250 ms in a
  simulation, so a quiet system is quiet for `Sim.Settle`; tenants are
  the store's when the node starts.
- **gristsim restarts the node from an `OnCrash` hook**: an effect's crash
  point crashes the node from its own task, which a nemesis's
  crash-and-restart does not cover. The node serves commands and queries
  over a `qenv.Service` ("api"), so requests and answers are lost and
  late; its configuration has `Send` (which commands and queries the
  workload sends) and `Setup` (the application's doubles, attached to the
  node's contexts).

## 0.2 What the review of slice 1 changed

A review of slice 1 probed it (a reviewer's tests against grist and the
example) and found these; each is fixed, with a test that fails without
the fix, and stated where it stands below.

- **A key's first answer is final, a refusal's too.** A rejection wrote
  no marker, so a late or duplicated request decided again and could
  commit after its client was told 422 (4 of 40 runs at the clients with
  the API delaying and duplicating 30% of requests). A refusal now
  commits its marker alone (`Marker.Outcome`, the rejection's name),
  conditioned on its reads like a commit; a conflict commits a
  `conflict` marker, unconditioned. The marker is checked under the head
  lock (the store's), so a refused key cannot commit later; the cost is
  that every refusal takes the tenant's lock (§2.4). The model's
  `freshCause(log, id)` (no event of the id) is `freshKey(answered, id)`
  over a ghost set of answered ids, which a committed refusal joins.
- **The marker's hash is the command's**: its name, version and
  canonical arguments. It was the arguments' only, so another command
  under the same id with JSON-equal arguments got the first's receipt.
- **Reads are checked against the App.** A tag key no type has (the Go
  field's name where the JSON name is the key), a keyed read model keyed
  by a field that is no tag, a misspelled type or another App's event
  matched nothing and conditioned nothing: grain's race with no
  `Unconditioned` in sight. Every read is now the decision's error when
  its type is not the App's or a tag key not the type's;
  `NewKeyedReadModel` refuses a key that is no tag of every handler's
  type (Verify); `Exists` takes an `EventName` whose method is
  unexported, so a read model or a command does not pass for one.
- **Under at least once, the committed run decides the outcome.** An ok
  send, a crash before the checkpoint, then three failed sends on the
  restarted node checkpoint `grist.failed`; the model insisted on
  `notified` (any ok effect wins) and rejected the faithful app. The
  effect's checkpoint step records `ok` (which outcome it commits), and
  grist.qnt's `atLeastOnceDone` admits notified after an ok effect step
  and `grist.failed` after `EFFECT_TRIES` failed ones, whichever the run
  committed: a library def every at-least-once application uses.
- **An unknown outcome at the end of the transient budget** returned an
  error though the command had committed. The marker is read once more
  before giving up, and a command that gives up after an append whose
  outcome is unknown returns `ErrOutcomeUnknown` (retry with the same
  id), one that never appended `ErrUnavailable`; gristsim's clients
  retry both under the same id, as for a lost answer.
- **The model checks the read set, not only the condition.** With
  `readPos` and `reads` the runtime's own report, `conditionHolds` was
  circular: a runtime that dropped a read from the read set dropped it
  from the condition too, and the model agreed. grist.qnt's `covers`
  states what a decision's rule needs (create-counter: whether a counter
  has the name), and the model requires it of the recorded read set at
  every committed step, so losing coverage is caught at every commit.
  The read set is recorded whatever the condition (an `Unconditioned`
  command records what it read), so the blind mutant is caught by the
  race, as claim 1 says, not by its missing condition. `conditionHolds`
  also requires `0 ≤ readPos ≤ head`; a conflict requires the condition
  *not* to have held.
- **The claims test the mechanism.** Claim 1 gains its contrast (the
  faithful app at claim 1's configuration: `None(Rejects("createCounter"))`,
  which fails in 13 of 20 runs with the runtime's condition taken out);
  a claim of duplicated and late requests (the API duplicating and
  delaying three in ten) catches the marker taken out; claim 2 runs one
  tenant and two letters, and drops Snapshot, whose runs are
  Serializable's byte for byte (every append writes the head).
- **The bindings compare more**: every event's cause (`obs.causes`, a
  (kind, name) pair, in `batchIs`), the effect's key (`keyStable`: the
  same key on every effect step of a trigger, no other trigger's), and
  a refusal's head and empty batch (it commits now).
- **A commit's step is written in its transaction.** `Batch.OnCommit`
  running even when the outcome is unknown is qenv's property, not
  database/sql's: a node that dies between COMMIT and the hook loses the
  step. `Batch.Step` carries the step (its fields but those the head
  gives), which a `store.StepLog` writes in the batch's transaction;
  memstore and simstore keep one, storetest checks it, and slice 2's
  sqlstore records from its rows (§2.10, §3.8).
- **Zero values are invalid**: the zero `Result`, an `Effect` without a
  function or of a delivery slice 1 lacks, the zero `Option`; each is
  the handler's error (`grist.failed`) or a Verify problem.
- **grist.qnt is checked in grist's own tests**: typechecked, its defs
  run on small values (`quint test`), and a small machine over them run
  (`quint run`), grist/model/model_test.go.
- **storetest checks concurrency**: tasks appending at once with
  conditions, checkpoint compare-and-sets and markers, the committed log
  replayed through `CheckCondition` (a mutant store checking conditions
  outside its lock fails it); `Wait` woken by an append (a mutant whose
  `Wait` returns at once fails it); and no slice shared with a reader or
  a writer (a fold mutating its events in place corrupts nothing).
- **API, before slice 2**: the store's contract is package `grist/store`
  (`Store`, `Batch`, `Condition`, `Marker`, `Selector`, the errors,
  `CheckCondition`, `Select`, `NewReceipt`), with later methods as
  optional interfaces (`StepLog`); grist keeps aliases of the types an
  application meets (`grist.Event`, `grist.Stored`, `grist.Position`,
  ...). The raw filter `grist.Query` is `Selector`, read with
  `Reads.Select`, beside the typed `grist.Events[E](r, t, tags...)`.
  `PosKey`, `StepFields` and `Quiet` are internal, as are gristsim's
  request and response. The tries and the batch size are options
  (`ConditionTries`, `TransientTries`, `EffectTries`, `BatchSize`) of
  one `grist.Option` type for every block (queries take `Authorize`
  later), defaults 5, 10, 3 and 100. `storetest.Checks` is a function.
  `App.Catalog()` is the registry as data, for slice 2's generator. The
  slice-2 read-set encoding is chosen: grist.qnt's `Read` is `{ typ,
  tags: Set[(str, str)] }`, which slice 1's binding builds from the
  recorded triples (`readsOf`) and slice 2's typed template function
  will record directly.

## 0.3 What building slice 2's SQL store changed

Slice 2's first part (grist/sqlstore, the relay, gristsim's SQL tier,
examples/counters' SQL and real tiers) decided these; each is stated
where it stands below.

- **Every tier records commits from the step log; there is no commit
  hook.** `grist.Relay` reads each tenant's committed step rows in order
  (`store.StepLog.Steps`, waking on `WaitSteps`: a refusal commits its
  marker and its step without moving the head, so `Wait` would sleep
  through it) and records them; `App.Start` runs one relay daemon per
  tenant and refuses a store without a step log. memstore and simstore
  switched too: one code path, so the simulation exercises the recording
  the real tier uses, and `Batch.OnCommit` (which only qenv could run
  even when the caller was told the outcome is unknown) is gone. A row
  holds the batch's steps (one per trigger of an `Emit` batch) with every
  field but those the head gives (`obs.head`, `order.pos`), which the
  relay adds from the row's head and each step's event count.
- **A relayed step is the same record however often it is relayed.** It
  is recorded under a recorder of its own, `grist/<store>/<tenant>`
  (`grist.RecorderID`), numbered by its place in the tenant's step log
  and timed by its commit (the row's time, taken under the tenant's
  lock since §0.4: the simulation's true time, or the wall clock's), on
  one thread (commit order). A relay that starts afresh (a
  restarted process, a second node) records the log from its first row,
  and `qtrace.Reconstruct` keeps one copy of each (an info issue, "seen
  twice"), so no relay progress has to be durable. gristsim holds one
  relay across the node's incarnations (`grist.WithRelay`, a
  `StartOption` since §0.4), which resumes
  where the crashed one stopped. `Relay.Behind` says how many rows are
  unrecorded: a real tier's harness waits for 0 before its settle.
  Recorded through `qobs.Record` instead, a second relay's copies are new
  steps (`TestRelayRecordsCommitsOnce` fails so).
- **A processor's order key separates its effect and its handling**:
  `order.p.<processor>` is 2·T on T's effect steps and 2·T+1 on its
  handling step (slice 1: T on both, the poller's thread putting the
  effect first; the handling step is now the relay's, on another thread).
- **Every gristsim run records both sides**: the clients' calls under
  `<tenant>/clients` (`gristsim.ClientsActor`; `<tenant>.clients` until
  §0.4), the store's steps under
  the tenant; a binding's `actors:` picks its side, and
  `System.WithBinding(calls, qtest.Linearize())` judges the same runs at
  the clients. `Config.Record` (`AtCommits`, `AtClients`) and the quiet
  context are gone. The relay's tasks change the schedule, so slice 1's
  measured rates moved (examples/counters' README has today's).
- **sqlstore runs one SQL on every dialect** (§2.10): the head lock and
  the new head and step number in the first statement, then the marker,
  checkpoint and condition under it; markers keep their receipt as its
  last position and event count; an event's tags are a JSON column
  beside its body (both TEXT, which SQLite does not coerce). `Dialect`
  (`Postgres`, `SQLite`, `QSQL`) says only the default isolation
  (PostgreSQL's and qsql's read committed, stated; SQLite's default,
  serializable with one writer), PostgreSQL's per-transaction
  `set_config` of `lock_timeout` and
  `idle_in_transaction_session_timeout` (one statement, local to the
  transaction, so no connection hook), and SQLite's mode check. grist
  imports no driver; its tests import modernc.org/sqlite and pgx.
- **sqlstore retries nothing itself.** A transient error (40001 from the
  head lock under snapshot or serializable, 40P01, 55P03, SQLITE_BUSY
  after `busy_timeout`, a lost connection, a commit of unknown outcome)
  goes back to grist, whose retry starts at the key's marker and decides
  again, as with simstore: an internal retry of the same batch would keep
  a stale decision (harmless under its condition, but the claims'
  mechanism would differ between tiers). What no retry fixes is new in
  the contract: `store.Permanent` (`*store.PermanentError`), which
  `store.Transient` excludes, so a missing table is an error at once, not
  ten tries and "outcome unknown"; a processor pauses before reading
  again.
- **Waiting**: `Wait` and `WaitSteps` wake on every COMMIT this Store
  tried (an unknown outcome may have committed; a waiter that slept
  through it would sleep through its step), re-read if a COMMIT happened
  while they read, and poll every 250 ms outside a simulation for other
  processes' appends; inside one they poll never, so a settled run is
  quiet (§0.4: on qsql they wait on the database's own signal, so a
  second Store's commits wake them too, and return once satisfied).
- **The contract on every tier**: storetest passes over sqlstore on qsql
  at Serializable, Snapshot and ReadCommitted with the Store's history
  checker on, on SQLite (a temp file, goroutines appending at once), and
  on PostgreSQL 16 (a scratch server on /dev/shm, `GRIST_POSTGRES`, at
  read committed, repeatable read and serializable; in CI since §0.4, a
  service container of the grist job), with `TestPostgresLockTimeout` (a stuck holder of the head
  lock makes an append 55P03 after `lock_timeout`). The head lock taken
  after the condition instead fails `ConcurrentAgainstBruteForce` at read
  committed on qsql and on PostgreSQL (SQLite's single writer passes:
  its transactions serialize whatever the statements' order). The suite
  now requires a step log and checks `WaitSteps` against a mutant whose
  wait is on the head.
- **The SQL tier commits less per run**: every statement is a scheduling
  point, so the nemesis (per scheduling step) crashes the node about
  three times as often; examples/counters' SQL claims raise the commit
  fault's rate to reach it in few seeds.

## 0.4 What the review of slice 2's first part changed

A review of the relay, grist/sqlstore and examples/counters' SQL and
real tiers found these; each is fixed with a test that fails without the
fix (ablated before committing), and stated where it stands below.

- **A node handles tenants created after it started.** The relay and the
  pollers covered the tenants listed at `App.Start`: a later tenant's
  commits were never recorded (`Relay.Behind` stayed at their count) and
  its triggers never handled. The node now runs a watcher task that
  waits on the store's new optional interface `store.TenantFeed`
  (`WaitTenants(ctx, n)`: until the store has more than n tenants) and
  starts the relay and pollers of each new tenant; every tier implements
  it, `App.Start` requires it, and storetest checks it (`TenantFeed`,
  against a mutant whose wait returns at once). A wait, not a poll: a
  timer would keep a settled simulation busy.
- **Two stores over one database wake each other.** A simstore or a
  sqlstore on qsql woke its waiters only at its own appends and, in a
  simulation, polled never: a second Store's commits were slept through
  and, with a relay waiting on the first, their steps silently lost.
  The core's `qenv.Store` now notifies a signal at every commit that
  writes (`Store.Changed`, with `Store.Version` to tell a commit that
  fell between a read and its wait), `qsql.StoreOf` names the Store a
  qsql database runs on, and both tiers wait on it; on SQLite and
  PostgreSQL another Store's commits are found by `Options.Poll`.
  `storetest.TwoStores(ctx, a, b)` checks a pair (on simstore, qsql and
  SQLite), failing for a Store's own signal. Refusing a second Store
  was the alternative; two nodes over one database are the real tier's
  normal shape, so they are made to work.
- **A step row has a version, `"v": 1`.** A row of another version (a
  newer grist's, or one no grist wrote) is a `rowError`: its tenant's
  relay stops, and `Relay.Err()` names the row and the fix (upgrade the
  node) instead of retrying forever; transient read errors are retried
  with backoff and reported by `Err` until a read succeeds (`gristsim.Run`
  returns it, examples/counters' harness fails on it).
- **A relayed step's time is its commit's.** The row was encoded with
  the decision's time, before the append, so a command that decided first
  and committed second carried the earlier time at the later seq. A row
  is now timed by the store under the tenant's lock (`store.StepRow.Time`,
  `store.StepTime`: the simulation's true time, else the wall clock's;
  sqlstore's `grist_steps.at_ns`), so seq order is time order; storetest's
  `StepLog` checks it.
- **An error before COMMIT says nothing committed.** Every transient
  error set "may have committed", so a lost race for the head lock
  (40001, 55P03 on its UPDATE) ran out as `ErrOutcomeUnknown`. A tier
  now marks an error before its COMMIT `store.NotCommitted`
  (`*NotCommittedError`; sqlstore every statement's before COMMIT,
  simstore a lost lock and a refused validation), `store.MayHaveCommitted`
  tells the two, and `Execute` answers `ErrUnavailable` when every
  transient error came before a commit. A context cancelled while an
  append committed is `ErrOutcomeUnknown` (wrapping `context.Canceled`),
  not a bare cancel that reads as "nothing happened".
- **Text no store holds alike is grist's error.** PostgreSQL refuses a
  NUL byte and invalid UTF-8 (22021) where SQLite and memstore take them,
  and JSON rewrites invalid UTF-8: tenants, ids, emitted events' tags,
  read tags and block names are checked in grist, on every tier, with
  the fix (encode binary data). A tenant name holds no slash
  (`grist.CheckTenant`), so a run's client-side actor is
  `<tenant>/clients` (was `<tenant>.clients`, which a tenant could be
  named); the bindings say `actors: ['!*/clients']` and `['*/clients']`.
  `RecorderID` path-escapes the store's and the tenant's names.
- **sqlstore's waits return once satisfied** (a commit during a read
  that already found what it waited for once made it read again), and
  `Open` validates its options: a negative duration, a name with NUL, a
  stated isolation level on SQLite (whose driver takes any and runs its
  one), and one transaction at the level, so pgx's "unsupported
  isolation" (no SQLSTATE: transient) is `Open`'s error with the fix.
- **SQLite's DSN drops `_txlock=immediate`**: its stated reason (a
  deferred transaction's upgrade from read to write fails with
  SQLITE_BUSY) does not apply, since an append's first statement is the
  head lock's write; deferred transactions take the write lock at it.
- **A relay with no recorder records as `qobs.Record` does**: dropped,
  or in a strict run an escape that fails it (it advanced in silence).
- **The relay's API**: `App.Start(ctx, s, grist.WithRelay(r))` (a
  `StartOption`; was a context value, and a relay of another store was
  ignored: now Start's error); `Relay.Flush` may run beside a node of the
  same Relay (a per-tenant lock held only between a row's progress and
  its record, where no seam is reached, so a crashed node's frozen task
  never holds it; each row recorded once, in order); `Relay.Behind` is
  the store's count of rows less the relay's progress
  (`store.StepLog.StepCount`), not a scan of the rows.
- **storetest draws selectors of two tags** (one or two types) in
  `ReadsAgainstBruteForce` and `ConditionsAgainstBruteForce`, on every
  tier, PostgreSQL included; a store honouring a selector's first tag
  alone fails both.
- **TestRealSQLite proves what it waits for.** The clients record their
  calls (`gristsim.Do`: `<tenant>/clients`), judged by
  `counters.calls.binding.yaml` too; every answer a client got is checked
  against the store's side (a lost refusal step passes both bindings:
  it commits no event); the first node is killed mid-phase (its sink dies
  at once, its relay behind, the clients still committing), the second
  phase creates new counters, and a mailer taking 20 ms makes a settle
  recorded without waiting (`quiet`) fail the eventual val. Each of the
  two ablations (a refusal's step dropped, the wait removed) fails it.
- **Restarts still re-record from row 1: deferred.** A relay that starts
  afresh records the tenant's log from its first row (Reconstruct drops
  the copies), which costs a restart O(log) and rewrites, in a new file,
  history an older file holds. The design, not built: a durable cursor
  per (tenant, sink) in the store (`grist_relay(tenant, sink, seq)`,
  written after the sink wrote the rows: at least once, a crash between
  the two re-recording one batch), and a relay declared to start after
  row N that records a lost-prefix record first, so a file whose steps
  begin at seq N+1 is judged as a suffix rather than reported as a gap
  (a qtrace change: an instance's state at the prefix is unknown, so the
  binding needs a way to start the model there, or the prefix's file is
  validated with it). Until then, keep a node's step files together and
  validate them as one run.
- **Cost**: examples/counters' SQL-tier claims that repeat a mechanism
  another per-push test covers run in full mode only
  (`TestSQLDuplicateRequests`, `TestSQLFaithfulAtClaim1`, `blind`'s and
  `dispatchDirect`'s SQL-tier halves, `TestSQLTier`'s Serializable half,
  `TestFaithfulAtClients`); its -short CPU went from 303 to 206 s here.
  The grist job runs PostgreSQL 16 as a service container, so the
  PostgreSQL contract runs in CI.

## 0.5 What building slice 2's API changed

Slice 2's second part (the HTTP API, `grist/client`, gristsim's HTTP
tier, `grist.Main`, examples/counters over HTTP) decided these; each is
stated where it stands below.

- **The adapter is package grist's, not `grist/api`.** `grist.Main`
  serves it, and the adapter reads what a block declares (its policy,
  `Internal`), which a separate package could only read through exported
  hooks: `grist.NewHandler(app, store, opts...)` is a plain
  `http.Handler` (net/http only), served by `qhttp.NewServer` in a
  simulation and by `http.Server` on the real tier. grist imports
  `grist/sqlstore` for Main (no driver: the binary imports one).
- **Routes and codes** (§5): `POST /t/{tenant}/commands/{name}`, the
  `Idempotency-Key` header the command id (required: 400 without it);
  `GET /t/{tenant}/queries/{name}`, the arguments as URL parameters by
  JSON name (a string as it is, anything else as JSON: `QueryArgs`, and
  `client.Params` its inverse). A command's receipt is **200** on its
  first answer and on every retry: the key's answer is the same
  response, which a 201 for the first and a 200 for a replay would not
  be (grist cannot tell them apart: a retry after a lost answer finds
  the marker). A rejection is **422** with its name; a reused key is
  **422** `idempotency-key-reused` too (the IETF Idempotency-Key draft's
  code for a key sent with another payload; the key's own answer
  stands; §0.7: the hash is of the arguments' canonical value); a
  conflict after the tries **409**; `ErrOutcomeUnknown` and
  `ErrUnavailable` **503** with `Retry-After: 1` and `{"retry":
  "outcome-unknown" | "unavailable"}`, to be sent again under the same
  key; arguments that do not decode (a misspelled field included: the
  body is decoded strictly; §0.7: a required field missing, invalid
  UTF-8, a NUL byte, an integer past int64 too) 400; a body past
  `MaxBody` 413 (§0.7); an unknown block or tenant 404 (an Internal
  block too, §0.7); the decision's own error 500.
- **Deny by default, at the adapter** (§4.3): one `grist.Option`,
  `Authorize(Policy)` (a `func(actor, tenant string) bool`; policies
  `Authenticated`, `Actors(...)`) or `Internal()`, on commands and
  queries; both on one block is a Verify problem. `NewHandler` refuses
  an App with a block that has neither, naming each (the adapter's
  boot): `App.Verify` does not, since gristsim's in-process clients and
  tests call blocks directly. A refused request is 403 (`ErrDenied`).
  The actor is the request context's (`WithActor`), set by the
  application's authentication (`Middleware`) or, behind an
  authenticating proxy and in tests, `ActorFromHeader` (§0.7: only for
  a request the middleware left without an actor).
- **A denial records nothing at the store.** The note had it a refused
  step (`outcome: denied`); but nothing ran, nothing was read and
  nothing committed, and the server records only what its blocks do
  (commits from the step log, refusals at their commit, queries and
  reused keys on return): the handler records nothing of its own. The
  client records its 403 as its call's outcome, `denied` (§0.7: a
  client-side model's outcome; a refusal that depends on the state, a
  counter's creator, is the decision's, `Reads.Actor`).
- **The client records its side**: `grist/client`'s `Client.Do` records
  each call as gristsim's clients do (`<tenant>/clients`, invoked before
  the first request, done with the answer, a lost answer or a 503 sent
  again under the same key after `RetryDelay` or the `Retry-After`; at
  most `MaxAttempts`, then pending), one recording with gristsim's
  (`grist/internal/calls`). A request that got no answer is
  `ErrOutcomeUnknown` for a command (it may have committed),
  `ErrUnavailable` for a query (§0.7: retries back off, jittered,
  within a retry budget, and a 5xx that is not grist's is an unknown
  outcome too).
- **gristsim's HTTP tier** (`Config.HTTP`): the node serves
  `NewHandler` on a `qhttp.Server` named `api` from its start function
  (every incarnation listens again), the clients are `client.Client`s
  over qhttp's Transport (2 s of virtual time per request), so the
  network's faults are qhttp's under the same kinds as the Service
  tier's (`rpc.api.request`, `rpc.api.delay`, `rpc.api.response`, the
  node's crashes). examples/counters' `TestHTTPTier` is claim 2 there, judged
  at the clients per push (a retry under a new key is caught there alone:
  9 of 20 runs, 12 of 20 with answers lost at 0.15 since §0.7; the store admits each key as a new command) and at the
  store in full mode.
- **The real tier is HTTP**: examples/counters' `TestRealHTTP` (was
  `TestRealSQLite`, whose clients called `App.Execute` in process) serves
  `NewHandler` on a real `http.Server` (a loopback port) over a SQLite
  file, four clients calling it with `grist/client`; when the first node
  is killed the second takes the server's traffic at once, and both
  bindings judge the file.
- **`grist.Main(app, opts...)`**: `catalog` (the registry as JSON, each
  type a JSON Schema derived by reflection: `grist.Schema`, the
  catalog's `MarshalJSON`), `verify`, and `serve -db sqlite:PATH |
  postgres:DSN -addr -tenant -steps -actor-header -poll`: the API and the
  node on sqlstore, every step to a `PerStep` file (a new one each
  start), the application's dependencies attached by `Dependencies`;
  SIGINT or SIGTERM shut it down, the relay flushed into the file first
  (`Relay.Flush`: a row the relay had not reached would otherwise be
  only the next node's, in its own file). `MainArgs` is the testable
  form. examples/counters' `cmd/counters` is one, and `SERVE=1
  ./run-demo.sh` serves it, drives it with curl and validates its file.
- **Ids starting with `grist:` are grist's** (an at-most-once outcome's
  marker, §2.6): a command under one is refused (400 at the API).

## 0.6 What building `AtMostOnce` changed

- **The semantics** (§0.7: an error's outcome is `grist.unknown` unless
  f says `NotApplied`): the checkpoint commits first, alone (its step
  records `ok: false`: no outcome is known when it commits); then f runs
  **once** (`EffectTries` does not apply: a try that failed may have
  taken effect, and trying again would be at least once); its effect
  step is recorded; then the outcome event, `OnSuccess`'s after an ok
  effect or `grist.failed` after a failed one, commits **in a commit of
  its own**, under a marker keyed by the effect key (`grist:<key>`, so a
  retry of an outcome whose answer was lost appends it once; ids
  starting with `grist:` are refused to commands). A crash between the
  checkpoint and f loses the effect, one after f its outcome: no restart
  runs either again. A checkpoint commit whose answer was lost counts as
  another poller's (its effect is not run): at most once, and before
  leases (§3.4) the safe reading.
- **Its steps**: the handling step `<processor>` (trigger, `ok: false`,
  the checkpoint), the effect step `<processor>Effect`, and the outcome
  step **`<processor>Outcome`** (trigger, key, `ok`; the events), relayed
  from its row. `App` registration claims `<processor>Outcome` as a
  Quint name too. Order: `order.p.<processor>` is 2·T for an
  at-most-once checkpoint and 2·T+1 for its effect step (at least once
  keeps 2·T for effects and 2·T+1 for its checkpoint); the outcome step
  has `order.pos` only (2·P, after the effect's 2·H+1 since it appends).
- **The crash points are the same kind** (`grist.<processor>.effect`,
  `crashBefore` and `crashAfter`, just before and just after f, whatever
  the delivery): at least once both precede the checkpoint (crashAfter
  duplicates), at most once both follow it (crashBefore loses the
  effect, crashAfter its outcome). One kind, one option list.
- **grist.qnt** gains `triesOf`, `holds`, `atMostOnceFirst` (the
  checkpoint before any effect step of its trigger, `ok` false),
  `atMostOnceEffect` (one effect step, after the checkpoint),
  `atMostOnceDone` (the outcome agrees with the one effect step and comes
  once: the success's events, which must name the trigger, and the
  trigger's `grist.failed` not in the log yet) and `atMostOnceOutcome`;
  checked in grist/model's tests on small values (§0.7 adds `unknown`,
  `holdsUnknown` and `atMostOnceOutcomesLogged`).
- **examples/counters' at-most-once processor is `payout`**, on a new
  command `retire-counter` (`counter-retired{counter, total}`, refused
  for an unknown counter): it pays the counter's total out through a
  simulated `bank` (`qenv.Service`), a duplicate payment being worse
  than a lost one, `OnSuccess(paid-out{counter, retired})`. Its claims
  (`TestPayoutAtMostOnce`): a scripted `crashBefore` loses the payment
  and the model admits it (`counters.retiring.binding.yaml`:
  `Conforms`, `All(Settles)`, the eventual val `everyRetirementHandled`
  needing only the checkpoint); a test-only binding
  (`counters.exactly.binding.yaml`, the eventual val
  `everyRetirementPaidOnce`) rejects those runs, `All(Violates(...))`;
  `crashAfter` and no faults conform under it (no payment twice, none
  lost). With grist's `AtMostOnce` run as at least once, every run
  rejects `payoutEffect`. Runs that retire are judged by their own
  binding; the other claims send no `retire-counter` and keep their
  bindings (a binding's eventual vals each cost a replay).
- **`checkpointFirst`** (the note's mutant): welcome declared
  `AtMostOnce` while the model says at least once:
  `Some(Rejects("welcome"))`, 10 of 10 runs (its checkpoint step before
  any effect step, which `atLeastOnceDone` refuses). The model gained
  `welcomeOutcome`, never enabled (an at-least-once outcome commits with
  its checkpoint), so the mutant's outcome steps are a model's action.
  The note's second claim (with a crash between, `Violates` of
  `everyCounterWelcomedOrFailed` in a model variant whose welcome is at
  most once) is `payout`'s: a real at-most-once processor, admitted by
  its model and rejected by a test-only exactly-once val. The mutant
  runs in full mode (its claim repeats `TestPayoutAtMostOnce`'s and
  grist's mechanism; building its binary is most of its cost).

## 0.7 What the review of slice 2's second part changed

A review of the HTTP API, grist/client, gristsim's HTTP tier,
`grist.Main` and `AtMostOnce` found these; each is fixed with a test
that fails without the fix (ablated before committing), and stated where
it stands below (§0.5 and §0.6 point here where it replaces them).

- **The application's authentication wins over `ActorFromHeader`.** The
  header's actor replaced the context's, so a request a middleware
  authenticated as a viewer that named itself an editor in the header
  was an editor's. The header now names the actor only of a request the
  context carries none for (`ActorOf` empty): a proxy that
  authenticates sets it, and a middleware's actor is never overridden
  (`TestActorHeaderDoesNotOverride`). Refusing both options together was
  the alternative; a middleware is also logging and limits, so the
  precedence is stated instead.
- **Main drains before the node stops.** The node's context was the
  signal's, so at SIGTERM the processors and the relay stopped while
  the server still answered: an at-most-once effect in flight had its
  context cancelled and its outcome never appended (a payment taken,
  recorded nowhere). The node now runs on `context.WithoutCancel` of the
  signal's context and is cancelled after `Shutdown` returns
  (`ShutdownTimeout`, 10 s), and the relay's flush has its own
  `FlushTimeout` (it shared the shutdown's 5 s, which slow requests
  could use up). `TestMainShutdownDrains`: an effect in flight at the
  signal while a client trickles a body across it runs to its end with
  its context live, its outcome is in the steps file, the trickled
  request is answered. An effect longer than the drain is still
  cancelled when the node stops.
- **Main's server bounds every phase** (`ReadHeaderTimeout` 10 s,
  `ReadTimeout` 1 m, `WriteTimeout` 2 m, `IdleTimeout` 2 m,
  `MaxHeaderBytes` 64 KiB; it set only the first), and a command's body
  bound is a `ServeOption`, `MaxBody(n)` (`DefaultMaxBody`, 1 MiB; was
  the constant `MaxBody`): a larger body is **413**, was 400.
- **Arguments are decoded exactly, and refused as the client's error.**
  A step's fields were decoded through `float64`: an integer past 2^53
  was recorded rounded, `MaxInt64` as `MinInt64`, while the events
  decoded exactly. `rec.StepFields` now decodes into the block's type
  (integers exact; a field the body names in another case, which
  encoding/json takes, is recorded by its JSON name: the client's steps
  lost it, recording from the raw body) and refuses an unsigned value
  past int64 with the fix. Every adapter path decodes through
  `decodeArgs`: valid UTF-8 (encoding/json rewrote invalid UTF-8 into
  U+FFFD silently), strictly, **every field required but a pointer's**
  (a missing field and a `null` body decided on the zero value while the
  catalog's schema said required: the schema is now enforced, and says
  a pointer's field optional and a pointer, slice or map nullable), and
  every string text a store holds (a NUL byte in an argument a decision
  read as a tag was the decision's error, a 500). Its errors are
  `ErrBadArgs`, a **400**; typed `Execute` and `QueryBlock.Run` refuse
  the same before anything runs (`TestAPIRefusesBadArgs`,
  `TestStepFieldsExact`, internal/rec's `TestStepFields`).
- **A retry across a deploy is the same command.** The marker's hash
  covered the re-encoded typed arguments, so a field added to the
  command's struct between a try and its retry (its zero in the retry's
  encoding) made the retry `idempotency-key-reused`. The hash now covers
  the arguments' value, canonical: members sorted, every zero-valued
  member left out at every depth (`canonicalArgs`), so a field added
  with a zero default or fields reordered keep the key's answer, while a
  retry that sets the new field, or renames one, is other arguments (a
  reused key). Hashing the body as received was the alternative; it
  would make the in-process path (typed arguments, every field encoded)
  and the API's disagree, and a client that adds `"note": ""` another
  command. A change of meaning under the same fields is the command's
  version's to state (`TestRetryAcrossDeploy`). Markers written before
  this change hash otherwise: a retry in flight across the upgrade is a
  reused key (pre-release, no store to migrate).
- **Query parameters**: `QueryArgs` takes a pointer to a string as a
  string (`?name=a` for a `*string` was "no JSON value", 400), leaves an
  absent pointer nil, and refuses a parameter that is not UTF-8;
  `client.Params` leaves a null (nil pointer) out, and the two are each
  other's inverse (`TestQueryArgsParams`).
- **An Internal block is 404, as an unknown one**, and a 404 lists only
  the blocks the API serves: a 403 for an internal block, and the
  unknown block's list of every command, told a client the internal
  names.
- **Ids are bounded**: `MaxIDBytes` (255) in `checkID`, so an
  `Idempotency-Key` past it is a 400; a longer one reached PostgreSQL's
  btree limit (54000), which sqlstore classed transient (retried for
  ever): class 54 is now permanent.
- **`Idempotent-Replayed: true`** (`ReplayedHeader`) marks a command's
  answer taken from its key's marker before deciding (an earlier request
  was answered; `TestAPIReplayedHeader`), receipts and refusals alike.
- **grist/client backs off, within a budget, and reads a foreign 5xx as
  an unknown outcome.** Its retries waited a fixed `RetryDelay` (or the
  503's `Retry-After`) up to 50 times per call, so a server that stopped
  answering met every client's every call, retried at once, for ever: a
  retry storm (examples/retrystorm: retries that multiply an overloaded
  server's load keep it from recovering after the overload is gone, and
  qmeta's chain and the load simulation agree that backoff alone keeps
  the storm while a retry budget ends it). A retry now waits an
  exponential backoff (`RetryDelay` doubling to `MaxRetryDelay`, 5 s;
  "equal jitter", half of each wait drawn from a hash of the call and
  the retry, so a simulation stays a function of its choices), at least
  a 503's `Retry-After`, and only while the Client's `Budget` (a token
  bucket every call fills by `Ratio`, 0.2, holding at most `Max`, 10:
  Finagle's defaults without its time-based reserve) holds a retry; a
  call the budget refuses ends with its last error, a command's pending.
  A 5xx without grist's body (a proxy's 502 or 504, a load balancer's
  bare 503) was a final `StatusError`, so a command whose answer a proxy
  lost was told failed: it is now an unknown outcome, sent again under
  its key (a query's: unavailable). `TestClientBackoffAndBudget`.
  gristsim's HTTP tier gives its clients a budget every call refills
  (`MaxAttempts` still bounds each): its calls are judged by a
  client-side model, and a call the default budget left pending after
  its effect committed is one that model cannot place (a full-mode run
  of `TestHTTPTier` found one).
- **The real tier exercises the same-key retry.** `TestRealHTTP` lost no
  answer, so its clients never retried a command under its key. Its
  server now loses every fifth command's answer after serving it (the
  connection closed without a response); the clients retry under the
  key, the marker answers (`Idempotent-Replayed`, counted: the test
  fails if none was replayed), and both bindings and the answers' check
  judge the run. With the client retrying under another key, the
  answers' check fails (the store recorded no step of the client's id).

- **An at-most-once outcome that never comes is caught.** The model's
  outcome action is optional (a crash after the effect loses it, by
  right), so a runtime that returned before appending any outcome, or an
  app whose payout declared no success event, conformed to every action
  and to `everyRetirementPaidOnce` (which counted effect steps).
  grist.qnt gains `atMostOnceOutcomesLogged(log, effects, processor,
  success)`, a library def for an eventual val: every effect step of the
  processor has its outcome in the log. examples/counters states it as
  `everyPayoutOutcomeLogged` in the test-only `counters.exactly.binding.yaml`:
  it holds without faults, under `crashBefore` (no payment, no outcome
  owed) and with the bank's answer lost, and every run violates it under
  `crashAfter`; with grist returning before an ok effect's outcome every
  run violates it (an ablation), and so does the app mutant
  `silentPayout` (no `OnSuccess`: 6 of 6 runs, full mode), whose runs
  the retiring binding admits.
- **At most once with two pollers is checked.** `gristsim.Config.Nodes`
  runs n node processes on the one store (node, node2, ...: each starts
  the App, its pollers racing the others' for every checkpoint; the
  first serves the API; one Relay, so each row is recorded once; each
  restarted when it crashes). examples/counters' `TestPayoutTwoNodes`:
  two nodes without faults conform to the retiring binding (per push, 4
  seeds) and pay every retirement exactly once (full mode); grist's
  branch for a checkpoint append that failed after which the checkpoint
  had moved (counted as another poller's, its effect left alone) is
  reached, counted from the run's log (`Sim.Log`, which grist now
  writes at that branch: `qtest.Reaches` names fault kinds, and the
  branch is none). With that branch taking the moved checkpoint for its
  own (the review's mutant in `commitAt`), 10 of 10 runs reject
  `payoutEffect` (`atMostOnceEffect` refuses a second payment step);
  every claim with one node passed it. grist's
  `TestEffectAtMostOnceLostCheckpoint` is the unit test: an append
  answered with an error after another poller moved the checkpoint runs
  no effect.
- **A poller re-reads its checkpoint after reading its triggers.** Two
  nodes through crashes found it (2 of 10 runs of `TestPayoutTwoNodes`'
  faulty half, a reconstruction cycle at `welcomeEffect`): the
  checkpoint and the triggers are two reads, and another poller's commit
  between them left this poller handling a trigger already checkpointed,
  with the newer head, so its at-least-once effect step came after the
  other's checkpoint on the tenant's clock (`order.pos`) and before it
  among the processor's (`order.p`). A checkpoint unmoved at a second read
  was the same when the head was read; a moved one is read again
  (`TestPollerCheckpointRaced`; without the re-read it fails).
- **The payout under faults, per push**: `TestPayoutTwoNodes` then runs
  two nodes through commit faults and crashes (3 seeds per push, 10 in
  full mode; `Conforms`, `All(Settles)`, commit faults reached): before,
  nothing model-judged ran the payout per push under any fault but the
  scripted `crashBefore`.
- **An unknown outcome is not a failure.** At most once, f's error
  committed `grist.failed`, read as "not paid", for a payment whose
  answer was lost (paid, and recorded failed). An error is now unknown
  unless f wraps it in `grist.NotApplied` (the dependency said it did
  not happen): an unknown outcome appends **`grist.unknown`**
  (`{processor, error, trigger}`, the error's text carried, for
  reconciliation; registered in every App beside `grist.failed`), a
  known one `grist.failed`. The outcome step records `known` and
  `error`; grist.qnt's `atMostOnceOutcome(processor, trigger, ok, known,
  error, success)` and `unknown(...)`, and `atMostOnceDone` refuses a
  second outcome of either kind. A node stopping after f ran still tries
  to append its outcome (its context's cancellation does not stop the
  append: `defaultTransientTries` tries). At least once is unchanged
  (every error retried).
- **The simulated bank records its payments** (`bankPays`: actor the
  tenant, the key, the counter and the amount), where it makes them:
  an effect's site records the effect. `everyRetirementPaidOnce` now
  counts the bank's payments under each retirement's effect key, so a
  payment made twice inside f, or made while its answer was lost, is
  seen whatever the effect step says (with the answer lost: paid once,
  both vals hold). `Bank.Pay` takes the tenant.
- **Decisions see the actor, and steps record it.** A decision or query
  could not tell who asked (`Envelope.Actor` reached the policy only),
  and no step recorded it, so no model could judge a rule over the
  actor. `Reads.Actor()` is the envelope's actor ("" for a processor's
  handler), and every command's and query's step records it as
  **`principal`** (a reserved argument name), the store's side and the
  client's alike (`TestActorInDecisionsAndSteps`). examples/counters'
  `retire-counter` is its creator's only: `counter-created` records its
  `creator` (`Reads.Actor` at creation), and another actor's retire is
  refused `not-creator`, a decision's refusal (422, committed as the
  key's answer and recorded at the store; a `Policy` sees the actor and
  the tenant, not the counter). The model's `retireCounter(counter, id,
  principal, ...)` requires `createdBy(log, counter, principal)` for ok
  and its negation for `not-creator`; `TestRetireByCreatorOnly`: two
  clients creating and retiring conform, and some retire is refused so;
  without the check (the `anyoneRetires` mutant, full mode) 6 of 6 runs
  reject `retireCounter`. A policy's denial (403) still records nothing
  at the store (nothing ran); `denied` is now an outcome the calls
  binding knows (`countersCalls`' `denied` action: nothing appended, the
  key unanswered), so a client-side run with a denial is judged rather
  than failing on an unknown outcome.
- **`checkpointFirst`'s claim says what it shows.** `Some(Rejects("welcome"))`
  is the at-least-once model refusing a checkpoint step before any
  effect step of its trigger (`atLeastOnceDone`): the mutant's every
  welcome comes so, which no other bug of the counters' runs makes
  without a crash (the faithful app's runs at that configuration reject
  nothing). It shows a declaration contradicting the model, not a lost
  mail; the lost effect at most once is `payout`'s claim.

## 0.8 What wallet-grist's trial changed

A newcomer agent built examples/wallet's bank on grist
(examples/wallet-grist, its TRIAL.md) from the skill and the docs alone.
What it found, each fixed where it stands:

- **`Unconditioned` is unprotected at every isolation level.** The
  skill and counters' README said the race's loser retries a transient
  conflict under Serializable and decides again, so the window closes;
  `readThenWrite` (wallet's `transfer` `Unconditioned`) committed an
  overdraft at Serializable in 2 of 20 runs (3 of 60 re-measured), a
  transfer decided at position 0 committing after the other's. A
  decision's reads are store reads of their own, pinned before the
  append, whose transaction begins after them (simstore takes its
  snapshot at `Begin`): a loser retries only when the two appends
  overlap. counters' 0 of 120 was its shape's luck, not a property:
  `blind` at Serializable with four clients creating three counters each
  commits a second create of a name in 2 of 60 runs.
  `TestUnconditionedAtEveryLevel` (grist) pins it: a create of a name
  another create took between the read and the append commits blind on
  simstore at Serializable, Snapshot and ReadCommitted, and is refused
  conditioned. wallet-grist's `readThenWrite` claim at Serializable is
  `Some(Rejects("transfer"))`. Reads inside the append's transaction were
  not built: the condition already protects at every level and on every
  store (§2.4).
- **`grist.Main serve` records a settle** on shutdown, once the server
  drained, every poller of the node waits with nothing after its
  checkpoint (an effect in flight is work: an at-most-once one's
  outcome is waited for) and the relay caught up (`SettleTimeout`; none,
  said on stderr, if the node does not quiet): a served run's eventual
  vals are judged (`TestMainServeSettles`; counters' `SERVE=1` demo).
- **An exploration's summary names the untaken actions**: "model
  actions taken 5 of 11 (never: ...)", of the model's actions but `init`,
  `step` and frame helpers. An action only other actions call (a grist
  model's `appends`, `refuses`) is one of them and shows as never taken.
- **The real tier's "no model-level ITF" log says the verdict is
  unaffected**: the ITF file (`Run.ITF`) is written in-process from
  literal arguments only, and `readsOf(...)` is a call; quint, which
  judges, evaluates it.
- **The skill** links reference pages (the model's rules, multi-entity
  commands), says that a declared guarantee contradicting the model is
  rejected at its first step (the lost effect it would cause is never
  reached), documents a double's site record (`actor` the tenant,
  `process` the App's name), and names Quint's reserved and built-in
  names (`to`, `of`, `type`, `size`, `map`, ...), which a Go field's JSON
  name must avoid. `Catalog`'s doc comment no longer reads as if the
  generator (§3.2) were built.

## 0.9 What building the generator changed (GR2)

GR2 (docs/ROADMAP.md): onboarding with minimal work, done when a trial
agent writes no binding by hand. The trial (§0.8, wallet-grist's
TRIAL.md) wrote 200 of 314 model lines, three bindings, an 80-line
mutant harness and a 120-line real-tier harness by hand. Built, each
with a test that fails without it:

- **The generator is the app's binary's** (`<app> model`, `grist.Main`;
  `grist.Generate`, `grist.Starter`), not a `cmd/grist` that would have
  to load an application's catalog it cannot import: only the app's own
  process has its `App`. It needs no binding or validate (text from the
  `Catalog`), so a production binary links nothing more.
- **Shapes the generator needs are declared, and enforced.** The
  catalog lacked each command's refusals and each processor's result:
  `grist.Refuses(rejections...)` on a command (a decision returning an
  undeclared rejection is its error, not remembered), `grist.Emits(types...)`
  or `grist.Delivers(d, success...)` on a processor (a result that
  contradicts it fails the trigger: tried, then `grist.failed`; Skip and
  Fail allowed under any, though a Delivers processor's have no step in
  its generated model: §0.10). A declaration the code could contradict
  silently would make the generated model wrong silently
  (`TestRefusesDeclared`, `TestShapeDeclared`; each fails with its
  enforcement taken out). A processor declaring neither is the
  generator's error.
- **What it writes, and what the app writes.** `model/<app>_gen.qnt`
  holds three modules: `<app>Events` (an `Event` constructor per type,
  accessors, reads, each read model's selection of the log and the reads
  a read of it records, a record type per block's arguments with
  `principal`, per query's result, per site's record), `<app>Gen` (the
  store's side: every variable, an action per command with its reused
  key, conflict, ok and declared refusals over `freshKey`, `covers` and
  `conditionHolds`, per query, per processor by its declared shape over
  grist.qnt's at-least-once and at-most-once defs, per site; every frame
  condition; `step`) and `<app>CallsGen` (the clients'). Beside it the
  vendored `grist.qnt`, `<app>.binding.yaml`, `<app>.calls.binding.yaml`
  and a binding per test-only variant. The app writes
  `model/<app>.qnt`: module `<app>Rules` with the holes (per command
  `<c>Outcome`, `<c>Events`, `<c>Needs`; per query `<q>Answer`; per
  processor `<p>Emits` or `<p>Success`; per site `<s>Admits`; `STRS`
  and `INTS` for `step`) and the read models' folds; module `<app>` and
  `<app>Calls`, importing the skeleton, with the invariants. **Folds
  are holes**, not generated: a fold is a Go closure the generator
  cannot read, and an analyzer that translated one (gristpure's facts,
  §4.1) is slice 3's; the generated selection (`totalsEvents(log, c)`)
  leaves a fold one line. **A rule is a function** (the outcome its
  decision reaches on the log), not a predicate per outcome: the Go
  decision is one, it makes `step` able to drive every command, and a
  generated action checks `result == <c>Outcome(log, c)`.
- **Regeneration preserves the holes by construction**: the skeleton is
  a module of its own file, which the app's model imports and the
  generator overwrites whole; the app's file is never written but by
  `model -init` when it does not exist. Markers inside one file were the
  alternative; they make a stale region a merge, where a module makes it
  a quint error naming the hole. A hole takes its block's arguments as a
  record (`c.counter`), so a field added to a command changes no hole's
  signature; a new block's holes are reported missing, each with a stub.
- **Invariants are marked, not listed**: `// grist: invariant|eventually|violable
  [bindings]` on the line before a val (`default` the store's binding,
  another name a test-only variant `<app>.<name>.binding.yaml`; in
  `<app>Calls`, the clients' binding). counters' `once`, `exactly` and
  `retiring` are three such variants. A variant keeps its own eventual
  vals so a per-push binding pays for few replays; one that judges only
  its own vals compares no observation (the default binding's claims
  do), one that judges every default invariant is a default binding with
  more vals and compares them, as counters' hand-written variants did.
- **The binding module exports the skeleton**, so binding's `LoadModel`
  follows whole-module exports transitively (a core change:
  `TestLoadModelFollowsExports`). And the app's modules import grist,
  the events and the rules themselves beside the skeleton: quint's
  flattener (`quint compile`, which the validator's Go evaluator runs
  on a module importing the binding's from another directory) loses
  names re-exported twice, and without the evaluator every
  linearization's candidates were checked by quint processes:
  wallet-grist's linearized claims cost twice the CPU, with nothing
  failing. `gristtest.Generated` fails a module without those imports
  (ablated: it does), without running quint (every validation
  typechecks the model).
- **The read set is a parameter of triples** (`reads: Set[(str, str,
  str)]`, `readsOf` in the action), which the binding's default template
  renders as literals: a generated binding needs no template for it,
  and the "no model-level ITF" log (§0.8) is gone. A set, not a list
  (§0.10): a read set has no order, and the generated `step`'s
  `triplesOf` folding a set into a list was outside the Go evaluator's
  fragment.
- **Names**: a parameter Quint reserves (`of`, `item`, `size`: quint's
  built-ins, read from its own list) is renamed (`of_`, with the binding's
  template); an integer tag, a pointer argument and a JSON name that is
  no identifier are the generator's errors with the fix.
- **`grist new <name>`** (`grist/cmd/grist`) writes a module (an App of
  two events, a keyed read model, a command with two refusals, a query, a
  processor that emits; its binary, its rules, a gristsim test with
  `Expect` claims, a mutant, a real-tier test, `run-demo.sh`, a README),
  runs `go mod tidy` and the app's `model` command; `TestNewIsGreen` runs
  `go test -short ./...` in it (full mode, about a CPU minute).
- **Harnesses** (grist/gristtest): `Generated(t, App)` (the CI diff: the
  checked-in skeleton, grist.qnt and bindings are the catalog's, every
  hole defined, both modules importing the events and rules;
  `GRIST_UPDATE=1` writes them);
  `Mutants{Names, FullOnly}.Run(t)` and `Mutant()` (each mutant's binary
  built with a go `-overlay` of `testdata/mutants/<m>/*.go`, told its
  name by `GRIST_MUTANT`: no marker file); `NewTier(t, App, TierConfig)`
  (the real tier: SQLite, real HTTP, nodes started and killed, lost
  answers, `Settle` waiting for every processor of the catalog and the
  relay, `Check` validating both bindings and `Answered`, the
  every-client-answer-has-its-store-step cross-check). In grist:
  `grist.NewDep[T]` (a typed dependency: `With`, `Of`, a fallback) for
  each app's context key, and `grist.NewSite[T]` (`Record(ctx, tenant,
  v)`: a double's record of its effect at its site, actor the tenant,
  process the App's) for the hand-written `qobs.Record` of `actor` and
  `process`.
- **What remains hand-written**: examples/counters' model 472 lines (329
  of code) → 143 (75), its five bindings (203 lines) → none, its mutant
  and real-tier harnesses 563 → 265 lines; wallet-grist's model 314
  (211) → 124 (64), its three bindings → none, its harnesses 388 → 145.
  Their claims are unchanged and pass in full mode.

## 0.10 What the review of GR2 changed

Two reviewers' reading of GR2 (an architect's and a newcomer's, who ran
`grist new` and grew the app it wrote), each fix with a test that fails
without it:

- **The generated models judge what the hand-written ones did.**
  counters' and wallet-grist's skeletons plus their holes were diffed
  against the hand-written models they replaced: on the store's side
  the actions are equivalent (the generated at-least-once checkpoint
  takes the processor's own EffectTries, where the hand-written one
  wrote EFFECT_TRIES), and every mutant is caught by its claim as
  before. The clients' side is weaker in one place: a conflict answers
  any call of a conditioned command there, where wallet's hand-written
  model required a valid transfer (an invalid one is refused before it
  reads, so it cannot conflict). The store's side still refuses it (a
  conflict needs a recorded read the condition failed on, and the
  invalid decision recorded none), and both sides judge every run, so
  no mutant escapes; a generic rule would need the clients to know the
  reads.
- **Regeneration never writes the app's file**, but an app named
  `grist` had its model at `model/grist.qnt`, the vendored library's,
  which every `model` overwrote (with its module named as the
  library's): Generate, Starter and `grist new` refuse the name
  (`TestGenerateRefusesGristsName`, `TestNewRefuses`).
- **A refusal named as one of grist's own outcomes** (`ok`, `conflict`,
  `denied`, `error`) would be judged as that outcome: a "conflict"
  refusal by the failed-condition branch (and committed without its
  condition), a "denied" one at the clients as a 403 that answers
  nothing. `Refuses` refuses them (`TestRefusesDeclared`).
- **Answered** keyed commands by id alone: two tenants' clients using
  one id failed it ("two steps on one side"), and so did a call the API
  denied (no store step, by right), a key answered twice from its
  marker, and a key reused with other arguments (its refusal a step per
  try). It now keys by tenant and id, skips denied calls, and requires
  of the store one outcome per key (key-reuse refusals aside) and each
  outcome a client was told (`TestAnswered`).
- **The generated `step` was outside the Go evaluator's fragment**
  (internal/qeval's `TestAgreesWithQuint`, red since wallet-grist
  landed): grist.qnt's `triplesOf` folded a command's needed reads, a
  set, into a list, an order-dependent fold, and the step drew every
  block's arguments in one product of nondets (wallet's 98304
  successors, past the evaluator's 65536). Read sets are sets end to
  end (`readsOf`, `triplesOf`, `covers`, `conditionHolds`; the action's
  `reads: Set[(str, str, str)]`, rendered by the default template), and
  each branch of `step` draws its own arguments. Nothing is weaker: a
  read set's order never meant anything (`covers` asks for some read,
  `conditionHolds` for every one), and the step's successors are the
  same.
- **The starter's stub of a query whose result has no scalar field** was
  `{  }`, which does not parse; it is `{ unit: true }`, the record type
  and the binding's answer (`TestStarterEmptyResult`).
- **Onboarding**: `<app> model` with no `model/<app>.qnt` listed every
  hole as undefined; it now says the file is missing and that `model
  -init` writes one, and names the files it writes
  (`TestMainModel`, the model command's first test). `gristtest.Overlay`
  is unexported: it is `Mutants`' mechanics.
- **The library trial** (examples/library, TRIAL.md), from `grist new`
  and the docs alone: a query result's `[]string` field was dropped by
  the generator without a word (`MemberLoansResult = { unit: bool }`),
  so the answer compared nothing of it. A field of an event, arguments,
  result or site record that no step records (a list, a struct) is now
  the generator's error with the fix, unless tagged `grist:"-"` (the
  model leaves it out, said where it is declared; library's `Books`).
  `grist new -replace` checks the checkout before writing anything and
  writes the replace relative to the module (`../..` in an example), not
  an absolute path. A claim `Violates("x")` of an eventual val x says to
  write `Violates(validate.EventuallyName("x"))`. §2.4's `Within` and
  `Also` are marked designed, not built. Documented (skills/grist): a
  site's records are ordered with no commit of the tenant's log, so a
  relation between them and the log is an eventual val (`// grist:
  eventually`); gristsim's `Send` draws uniformly from its list, so a
  name listed twice is sent about twice as often, and there is no
  ordered prologue; `r.Exists(T, Tag(k, v))` records the read `<t>By<K>(v)`
  (`Exists(T)`: `<t>Read`); a `NewDep`'s T is best an interface, whose
  fallback refuses every call.
- **Left**: a `Delivers` processor's Skip or handler Fail (the runtime
  allows both) commits a checkpoint with no effect step and no `ok`,
  which its generated actions cannot take: the binding fails on the
  missing parameter, or the at-least-once checkpoint is rejected. A
  sound model needs the rule for which triggers skip (a hole
  `<p>Skips(prefix, trigger)`) and an action for the skip; the docs say
  so meanwhile (Delivers, skills/grist's model reference). And a
  command that declares no `Refuses` but refuses at run time fails in
  the binding ("outcome has no mapping"), an error that names the
  binding, not `grist.Refuses`. From the trial: a site record carrying
  the position of the commit that triggered its effect (an order edge,
  so a relation to the log is an invariant), an ordered workload in
  gristsim, a claim that every block of the catalog was taken (qtest's
  `Exercised` counts grist's helper actions too, and the summary's
  "never" list mixes them with the app's and truncates), and an outcome
  filter on `Takes`: each a design change of its own, in the ROADMAP.
- **Cost**: counters' `-short` measured 359.5 CPU s here (load 10-25 on
  4 cores; ci/test-times.txt says 354): 90 `quint test` processes, 4
  typechecks and one compile. Each `quint test` of a counters replay
  costs about 3.3 CPU s, of which a typecheck of the replay is 3.1 and of
  `grist.qnt` alone 1.5 (a trivial module's whole test is 0.45): the
  library's typecheck is most of every replay's fixed cost, spread over
  its defs (no one def is slow). Moving the clients' modules out of the
  store binding's files saved 3% per process, under the noise; sharing
  one typecheck between a spec's two bound modules would save one
  quint process per test binary. Neither is the cheap waste the 5% was
  suspected to be: the generated skeleton is larger than the
  hand-written model, and quint typechecks all of it per process.

## 0.11 What GR2b changed (the library trial's left items)

The left items of §0.10 and the library trial's ranked list (its
TRIAL.md, items 2-4 and 8), each with a test that fails with it taken
out:

- **A run that reached none of the app's blocks fails.** library's first
  `TestFaithful` never committed a borrow and passed every claim.
  `gristtest.Exercised(App, except...)` claims every block of the
  catalog taken in some run: each command by `ok` and by each declared
  refusal, each query, a processor's handling (Emits) or its effect and
  checkpoint (Delivers; an at-most-once outcome when it declares a
  success event), each site's record. It names every one missed with
  how to reach it (`Config.Prologue`, `Gen`, `Send`, `Setup`).
  A conflict, a reused key and a skip are not wanted (faults and
  workloads decide them). It is qtest's `TakesEach` of the catalog's
  `Takes` (core: `Take.Outcome`, `Take.Hint`, `TakesEach`), so its
  verdict is any Takes' (`TestExercised`, qtest's
  `TestTakesOutcomeAndEach`). **Not applied by default**: gristsim's
  `Run` is a `qtest.System`'s run, which knows no claims, and a default
  in qtest would fail every contrast and mutant test, whose workloads
  are narrow by design (library's `TestScarce` never returns a book),
  so each would carry an exception list. That noise is what makes
  exceptions a habit. The faithful test claims it: the starter's
  `TestFaithful` does (`TestNewIsGreen`), as do library's.
  Applied there it found two real gaps. `TestGreedy`, the contrast of the
  `staleLimit` mutant, had never reached the limit refusal it stands for
  (its join and additions were random sends). `TestFaithful`'s runs had
  never refused an invalid addition. Both are now reached through the
  prologue below.
- **The summary's "never" list** names the actions the binding names
  (a grist binding names every block) apart from the model's others
  (grist's `appends`, `readsThrough`, ...), and never cuts the first
  short (`TestSummarySeparatesTheBindingsActions`).
- **`Takes(action).Outcome(o)`**: a run counts only if it took the
  action by a step recorded with outcome o. A contrast test claims the
  refusal its mutant removes (library's `TestGreedy` claims `limit`,
  `TestScarce` `unavailable`).
- **An ordered workload**: `gristsim.Config.Prologue []gristsim.Call`,
  calls made in order, each answered (or pending after MaxAttempts),
  before any client sends its first. They go to each tenant or the one
  named, recorded as clients' calls (actor `prologue`) through the same
  faults (`TestPrologue`, which fails with the clients not waiting).
  library's faithful, greedy and lending shapes open with it, where they
  weighted `Send`.
- **A Delivers processor's Skip has a step**: a Skip or a handler's Fail
  of a Delivers processor commits its checkpoint as the processor's skip
  step (`<p>Skip`, Fail's with its `grist.failed`). The generator writes a
  hole `<p>Skips(prefix, trigger): bool` and an action `<p>Skip(trigger)`
  that requires it, no effect step of the trigger, and nothing appended.
  Its effect actions (and an at-most-once checkpoint) require it false.
  A Fail is then a Mismatch of the skip (its `grist.failed` against the
  skip's empty batch), as an Emits processor's Fail is a Mismatch of its
  emit. `TestDeliversSkip`: the faithful rule conforms and takes both
  the skip and the effect; a rule that skips nothing is
  `Rejects("mailSkip")`, one that skips everything
  `Rejects("mailEffect")`. Before, the skip's step bound to the
  checkpoint action and failed on its missing `ok`. counters',
  wallet-grist's and library's rules say `false` (no handler skips).
- **A command that declares no refusals and refuses** is still refused
  (its key answered: changing that would make every hand-written
  command's refusal an error), but its step carries an error naming the
  command and saying to declare `grist.Refuses`. The binding's "outcome
  has no mapping" prints the step's error, so it now names the fix
  (`TestUndeclaredRefusalNamesRefuses`; the step row gains an optional
  `error`, which an older grist ignores).
- **Left to GR3: a site's record ordered after its trigger's commit**
  (docs/grist-instrumentation.md §3.3, its slices 1-2). The order GR3
  specifies (`order.pos` at the cause, a thread per key, the cause
  carried with the call) needs the call declared and its cause carried
  to wherever it is served, which is GR3's `NewCall`. A `Site.Record`
  that read the cause from its context would order an in-process double
  only, and GR3's served record replaces it. One finding went into that
  design (its 1b7309e): an order of 2·T+1 would place a record of a
  trigger inside a batch of several events (wallet-grist's transfer
  appends two moves) before the commit holding the trigger, whose step
  is at 2·P, P the batch's end. The record is placed at the poller's
  head, with the rule given the prefix through T. So library's
  `everyReceiptForABorrow` stays an eventual val, and `<s>Admits` still
  sees only the records.

## 0.12 What building GR3's slice 1 changed (declared calls, simulated)

docs/grist-instrumentation.md §4's slice 1: a dependency an effect
calls is a declaration, recorded at both ends and modelled by the
generator, on the simulated tier. Built, each with a test that fails
with it taken out:

- **Declared calls** (grist/calls.go): `grist.NewDependency(App, name)`,
  `grist.NewCall[Req, Resp](dep, name, grist.Keyed(), grist.Refuses(...))`
  (request and response structs of scalars; a field of one name on both
  sides, or named `processor`, `trigger`, `key` or `result`, a refusal
  named `unknown`, a dependency named `api` are `Verify`'s problems), and
  `grist.Uses(calls...)` on a processor, enforced as `Emits` is: `Do`
  from an effect of a processor that does not list the call, or outside
  any effect, is the effect's error naming the fix and records nothing
  (`TestCallDeclared`); `Uses` on a processor that declares no
  `Delivers`, or of another App's call, is a problem
  (`TestCallDeclarations`). The catalog gains `Dependencies` (each call's
  types, `Keyed`, refusals and users) and each processor's `Uses`.
- **Both ends recorded by grist** (`TestCallSteps`). `Call.Do` records
  the caller's view, the step `<dep><Call>` (`bankPay`), as
  `qobs.Invoke` before the request and `Call.Done` with the answer: the
  request's fields, the processor, the trigger, the effect's key, the
  response's fields (zeros without one), the outcome `ok`, a declared
  refusal's name (returned as `NotApplied` of its `*Rejected`: at most
  once, `grist.failed`) or `unknown` (any other error: `grist.unknown`);
  ordered as the effect step it precedes (`order.pos` 2·H+1, the
  effect's `order.p.<processor>`, the poller's thread). The handler's
  wrapper (`Call.Handler`, served by `grist.Local`) records the
  application, `<dep><Call>Served`, at its cause, `grist.CallInfo`
  (App, tenant, processor, trigger, the head the poller read, the key):
  `order.pos` 2·H+1, on a thread of the key, so it follows the commit
  holding the trigger even inside a two-event batch
  (`TestSiteAfterItsCommit`, which fails with 2·T+1: a batch's commit is
  at 2·P). A handler answering a key from its first answer says
  `grist.Replayed(ctx)` and records nothing (`TestReplayedRecordsNothing`);
  one returning an error that is no declared refusal is recorded with
  the fix. `grist.CallKey(ctx)` is the key in an effect and in a handler.
- **gristsim serves each dependency** as a `qenv.Service` named after it
  (faults `rpc.<dependency>.request`, `.delay`, `.response`), on no
  process, from `Config.Doubles` (`func() gristsim.Doubles`, per run);
  a dependency without a double, or a call without a handler, is the
  run's error (`TestDoublesServed`). The default double is slice 2's.
- **The generator** writes per call its request, response and record
  types (`BankPayRequest`, `BankPayResponse`, `BankPayRecord`), the
  caller's steps and the site's (`bankPayCalls`, `bankPayRecords`), the
  caller's action (a user's trigger, before its effect step, under the
  trigger's key, `callKeyStable`; at most once, no earlier call of the
  key; the request the rule's), the site's (a user's trigger in the log,
  `<c>Admits` given `log.slice(0, T)`), a rule hole per (processor,
  call), `<p><Call>` (`payoutBankPay`), and `<c>Admits` itself unless
  the rules define one (a `Keyed` call: no earlier record of the key;
  else any), with `step` branches drawing each (in the Go evaluator's
  fragment: `TestAgreesWithQuint`). grist.qnt gains `CallKey`,
  `callKeyStable` and `keyCalled`, checked in grist/model's tests. The
  binding names both actions with no template but the outcome as
  `result` (and a reserved name's), and the calls' outcomes.
  `gristtest.Exercised` takes each call, answered ok and applied.
- **counters' bank** is a declared call (`Bank`, `Pay`: `Keyed`,
  refusing `declined`), its double a handler attached per run in its
  tests (deduplicating by key, `Replayed`); deps.go's `Bank` interface, `BankDep`,
  `noBank`, `payRequest`, `SimBank`, `BankPays` and `Payment` are gone,
  its rules gain `payoutBankPay` and lose `bankPaysAdmits`, and
  `everyRetirementPaidOnce` reads the served records by trigger. The new
  mutant `wrongAmount` (the payout pays one more) is
  `Some(Rejects("bankPay"))` in 4 of 4 runs, where no record the store
  makes differs; `TestPayoutAmount` is its contrast (`None(Rejects("bankPay"))`,
  and conforming through the bank's own lost, duplicated and late
  requests and lost answers). The other payout mutants and claims are
  unchanged and caught as before.

Where the design changed (docs/grist-instrumentation.md, corrected in
place): the site's action does not judge the request, the caller's
does. The site's record is recorded inside the call, before the caller's
completion, at the same 2·H+1: the two are concurrent and reconstructed
in recording order, the site first, so a request judged at the site
would reject `bankPayServed` for the caller's error and the caller's
step would never be judged; grist records the caller wherever it
records the site. The site's records are `<c>Records`, not `<c>Served`
(the site action's name). A response's fields are step arguments, not
observations, so the actions take them as same-named parameters.
`grist.Handler` is a fold's handler, so a call's is `grist.CallHandler`
(`Handlers` as designed). `Transport` and `Local` came with slice 1
(gristsim serves through `Local`), and `Replayed` is new: without it a
deduplicating double's replay is a second application. The call's
timeout is `gristsim.DependencyTimeout` (1 s), not yet an option.

**The slice's review** (2026-10-27), each probed by a counterexample:

- **A replay was unjudged.** `grist.Replayed` recorded nothing, so the
  model could not tell a replay of the first answer from one of another
  answer, or of no application at all (the faithful default double of
  slice 2 could get it wrong unseen). The wrapper now records the step
  `<c>Replayed` with the handler's answer, and the generated action
  admits it only as an earlier served record of the key, request,
  outcome and response alike (`<c>Records.indices().exists(i =>
  <c>Records[i] == rec_)`): counters' bank answering a duplicate with a
  new receipt rejects `bankPayReplayed` in every run (TestPayoutAmount,
  `paying` with its first request duplicated; the faithful bank there
  conforms and replays), where it conformed before; grist's
  `TestReplayedRecorded`. `Replayed` stays the handler's word: a double
  that applies a call again and says `Replayed` with the first answer
  hides it, which no record can show. counters' double now keeps each
  key's first answer, a decline included (a declined key retried was a
  second served record; no counters workload declines).
- **Two records of one key could form a cycle.** The site's record was
  placed at its own cause, 2·H+1, on the key's thread; a request from
  an earlier head served after a later one's (a request delayed past a
  restarted poller's retry, two nodes' pollers) put a higher `order.pos`
  before a lower one on one thread, a cycle (qtrace's `cycle` issue,
  the trace's, not Reconstruct's own list), and would put a replay before
  the application it repeats. `grist.Local` keeps each key's last place
  and records at the later of the two (`TestSiteOrderPerKey`, which
  fails with the cycle without it); still after the trigger's commit.
  `TestSiteAfterItsCommit` checked only Reconstruct's returned issues,
  where a cycle never is: it now checks the trace's too.
- **The site does not judge the request** (left as built): in slice 1
  both ends record the same bytes in grist's own code, and the caller's
  step is recorded for every outcome but a crash during the call, so a
  request that does not follow from its trigger is judged at the caller
  (`wrongAmount`). It becomes a gap where the site is recorded by another
  process or for a request no grist caller sent (slice 3's
  `ServeCalls`): there the site's action should judge its request
  against the rule too, accepting that a caller's wrong request is then
  named at the site first. Keyed admits is enforced at the site as
  built.
- **A response's zeros** without a response (a refusal, an unknown
  outcome) are told apart by `result`, which every record carries; an
  invariant reading a response field conditions on `result == "ok"` (the
  record type's comment says so).
- **API**: `Dependency.Calls` and `Call.Dependency`, used nowhere, are
  removed (the catalog lists a dependency's calls); `Do`'s error for a
  dependency with no transport names the fix (`With(ctx,
  grist.Local(handlers))`).
- **Cost**: `wrongAmount` runs at `paying`'s shape (a prologue creating
  and retiring counter a, so every run pays), `All(Rejects("bankPay"))`
  at one seed per push instead of `Some` at two (the mutant's test 21.6
  to 13.1 CPU s here). TestPayoutAmount's contrasts (without faults, and
  the faithful bank under a duplicate) are full mode: per push
  TestPayoutTwoNodes' runs, which already ran, claim the call answered ok
  and applied without faults, and under faults the bank's lost requests
  and answers, an unknown call and a replay, all conforming. The new
  per-push claim (a new-receipt replay rejected) is one seed. The two
  tests together 41.1 to 37.7 CPU s; counters -short 392 and 402 CPU s
  before and after, in the noise of a shared machine. TestPayoutAmount
  never reached a lost answer (0 of 10 runs at 0.3 among
  the other faults): its comment no longer claims one.

## 1. Two things grain gets wrong that grist must make impossible

Both were read in grain's code (research notes, 2026-10-27); slice 1
(§8.1) reproduces both before it fixes them.

1. **The unique-name race.** grain's example `create-counter` checks
   that no counter has the name by projecting a read model, then appends
   with no `:cas`: two concurrent creates both append, and the invariant
   its spec claims (`UniqueCounterName`) is false, with nothing in grain
   to find it. The safe version is opt-in; the unsafe one is the short one.
2. **The duplicated effect.** grain's example processor calls
   `process-command` directly and returns `{}`: the command's events and
   the checkpoint are two appends, and a crash between them runs the
   command again. grain's spec accepts duplicates.

grist's answers: **an append is conditioned on everything its decision
read, unless the code says otherwise in words**; and **a processor's own
appends carry its checkpoint in the same transaction**, so the only
duplicates left are external effects, whose delivery guarantee is
declared, recorded and judged.

## 2. The blocks

Registration is typed generic Go: package-level values built by
constructors on an `*grist.App`, no code generation for the runtime
(grain's macros become constructors; their def-site facts become what
the analyzer reads, §4). Code generation is for the model and the
binding only (§3.2), from the registry, by `cmd/grist` (§7).

### 2.1 Events

An event is a Go struct registered under a stable name and a version.
The name, not the Go type's, is what is stored, so a type can be
renamed. Tags are fields marked `grist:"tag"`; a tag is a (key, value)
pair, the key the field's JSON name. A tag that exists to make a value
unique, DCB's `name:x`, is an ordinary tag: `CounterCreated`'s `Name`
is tagged, so "no counter has this name" is a read of one tag value,
not of every name (§2.3). The schema is the type: JSON through
`encoding/json`, a JSON Schema derived by reflection for the catalog
and the API, and a schema hash per (name, version) kept in `grist.lock`
(checked in). Field types are what the model can hold: strings,
integers, booleans, slices, maps, structs, string enums
(`grist:"enum"`). `float32`, `float64` and `time.Time` are refused at
registration, with the fix: integers in a stated unit (cents, basis
points, Unix milliseconds); `grist.Meta` carries the append's time.

```go
// proposed: package counters, an application
type CounterCreated struct {
	Counter string `json:"counter" grist:"tag"`
	Name    string `json:"name" grist:"tag"`
}

type CounterIncremented struct {
	Counter string `json:"counter" grist:"tag"`
	By      int    `json:"by"`
}

var (
	App         = grist.New("counters")
	Created     = grist.NewEvent[CounterCreated](App, "counter-created")
	Incremented = grist.NewEvent[CounterIncremented](App, "counter-incremented")
)
```

**Versioning** (slice 3). A change to a registered (name, version)'s
schema hash fails `grist verify` and boot ("counter-created v1 changed:
register v2 and an upcaster"). Old versions stay registered with a pure
upcaster to the next (`grist.UpcastFrom(Created, func(v1
CounterCreated) CounterCreatedV2 {...})`); reads upcast before any fold
or handler sees an event, appends write the latest version only.
**Upcasters must not change tags**: the index is written at append time
from the stored version's tags, so an upcaster that adds, drops or
changes one makes queries disagree with folds. `grist verify` runs each
upcaster over generated values and refuses one whose output's tags
differ from its input's, unless the new version is registered with
`grist.Reindex()`, which rewrites the index under the tenant's lock at
boot. A stored event's envelope (`grist.Meta`) is the store's: tenant,
position, time, and the command or trigger that caused it.

### 2.2 Read models

A read model is a fold: an initial value and one handler per event type
it consumes. The consumed types are the handlers' types, so they cannot
drift from a declaration.

```go
// proposed
type Names map[string]string // counter name -> counter id

var CounterNames = grist.NewReadModel(App, "counter-names", 1, func() Names { return Names{} },
	grist.On(Created, func(s Names, e CounterCreated, m grist.Meta) Names {
		s[e.Name] = e.Counter
		return s
	}))

var Totals = grist.NewKeyedReadModel(App, "totals", 1, grist.Keys("counter"), func() int { return 0 },
	grist.On(Incremented, func(s int, e CounterIncremented, m grist.Meta) int { return s + e.By }))
```

A plain read model folds the tenant's events of its types; a keyed one
folds only the events carrying the given values of its key tags
(`grist.Keys("counter")`, or, from slice 2, several: `grist.Keys("account", "currency")`
folds the events tagged with both), so a decision about counter c reads,
and is conditioned on, counter c's events alone. The version is the
cache's: bump it when the fold changes. Slices 1 and 2 fold from the log
on every read (no cache); snapshots are a gap (§9).

### 2.3 Reads: one position, a set of queries

`*grist.Reads` is a decision's only access to state. **Its first read
pins the position**: the head at that read, P. Every later read of the
same `Reads` folds through P, never past it, so a decision sees one
state, whatever commits while it runs. Every read is recorded as a
query: an event type set and a tag set (all must match), through P.
The reads are:

- `X.Get(r)`, a plain read model: a query of its types;
- `X.For(r, keys...)`, a keyed read model: its types and those tag
  values;
- `r.Exists(Event, tags...)`: whether an event of that type with those
  tags exists through P, a query that folds nothing (the uniqueness
  check);
- `grist.Events(r, Event, tags...)`: the events of one type and those
  tags, decoded;
- `r.Select(q)`: a raw selection, `grist.Selector{Types, Tags, Reverse,
  Limit}` (newest first with a limit is the "latest of" read grain has).

**Every read is checked against the App** (§0.2): a type it does not
register, or a tag key the type has no tag of (the Go field's name where
the JSON name is the key, a field not marked `grist:"tag"`), would match
nothing and condition nothing, so it is the decision's error, naming the
fix, never an empty answer; `Exists` and `Events` refuse another App's
event type, and `NewKeyedReadModel` a key that is no tag of every
handler's type.

The **condition** of the decision's append is DCB's form, one position
and the union of the queries: it commits only if no event matching any
of them was appended after P. A read with `Reverse` or `Limit`
conditions on its whole query, not on the events it returned: a later
event matching it would have changed the answer.

### 2.4 Commands

A command is a struct (its arguments) and a decision: **reads and a
command in, events or an error out**.

```go
// proposed
type CreateCounter struct {
	Counter string `json:"counter"`
	Name    string `json:"name"`
}

var ErrNameTaken = grist.Rejection("name-taken")

func decideCreate(r *grist.Reads, c CreateCounter) ([]grist.Event, error) {
	if r.Exists(Created, grist.Tag("name", c.Name)) {
		return nil, ErrNameTaken
	}
	return []grist.Event{Created.Of(CounterCreated{Counter: c.Counter, Name: c.Name})}, nil
}

var Create = grist.NewCommand(App, "create-counter", decideCreate, grist.Authorize(grist.Role("editor")))
```

**The append is conditioned on the read set** (§2.3). When the
condition fails, the runtime reads again at the new head and decides
again (5 tries by default, `grist.ConditionTries(n)`; then
`grist.ErrConflict`, a 409 at the API, committed as the key's answer).
This is the dynamic consistency boundary of the "kill the aggregate"
pattern, and it is the default because it is derived, not declared: a
decision that reads names is conditioned on names whether or not its
author thought about concurrency. Contention follows the reads: the
create above conflicts only with a create of the same name, an
increment read through `Totals.For(r, c)` only with increments of c.
grain's race cannot be written by accident; it can be written on
purpose:

```go
// proposed: the unsafe variant, which gristflow flags (§4.1) and slice 1's exploration rejects (§8.1)
var CreateBlind = grist.NewCommand(App, "create-counter", decideCreate,
	grist.Unconditioned("names are advisory here"))
```

**No isolation level protects an `Unconditioned` decision**, by design
(§0.8). Its reads are pinned at P, each a store read of its own, and its
append is a transaction that begins after them: the store's level
orders appends, never a decision's reads with another's append. Under
Serializable and Snapshot a loser whose append overlaps the winner's
gets a transient conflict and decides again on fresh reads, which
narrows the window; a decision that read before the winner's commit and
began its append after it commits blind, as at ReadCommitted. Reading
inside the append's transaction would close it at Serializable, and is
not done: the condition gives that protection at every level, on every
store, and `Unconditioned` exists to admit the race on purpose.

Two consistency options are designed, not built (no such symbols yet:
`Unconditioned` is the one way to make reads advisory today):
`grist.Within(Totals)` would narrow the condition to some of the reads
(the rest advisory, the option's reason saying why); `grist.Also(q)`
would add a query the decision did not read. No option widens beyond the tenant: there are no cross-tenant
transactions, as in grain.

**Transient errors are not failed conditions.** A lost race for the
tenant's lock (the simstore's ErrConflict under Snapshot or
Serializable; SQLSTATE 40001 and 40P01 on PostgreSQL; SQLITE_BUSY after
`busy_timeout`) and a commit of unknown outcome are retried from the
idempotency check (below), with backoff in virtual time, on a budget of
their own (10 by default, `grist.TransientTries(n)`), and never count as
one of the condition tries. Only `*grist.ConditionFailed` (an event matching the read set
after P) means "decide again".

**Rejections and errors.** A decision returns either events or an
error. A `grist.Rejection` (a named, expected refusal) is a result:
recorded as the step's outcome, a 422 with its name at the API. Any
other error is a fault of the code (a 500), recorded with its text.

**Idempotency.** Every command carries an envelope (`grist.Envelope`:
command id, tenant, actor, time), the id chosen by the caller (the API
requires it as `Idempotency-Key`). **A key's first answer is final**
(§0.2): every committed outcome writes a marker keyed by the command
id, holding the hash of the command (its name, version and canonical
JSON arguments) and its answer: a commit's receipt, or a refusal's name
(`Marker.Outcome`: a rejection, committed with the marker alone and
conditioned on its reads as a commit is; `conflict`, committed
unconditioned once the condition failed every try). The store checks
that the marker is new under the tenant's lock, so a key never has two
outcomes. Every try, the first and each retry, begins by reading the
marker:

- a marker with the same hash: the first attempt was answered (a lost
  response, a late duplicate of the request, the case payd's dedupe is
  for); its answer is returned, the receipt or the refusal, and nothing
  is appended or recorded;
- a marker with another hash: a 422 `idempotency-key-reused`, recorded
  as a refusal that commits nothing (the key's own answer stands);
- no marker: decide.

When the transient budget runs out the marker is read once more: a
commit whose answer was lost answers there. Otherwise a command whose
append may have committed returns `grist.ErrOutcomeUnknown` (retry with
the same id), one that never appended `grist.ErrUnavailable`. An error
of the decision itself (the code's fault) is recorded with its text and
not remembered.

**A command's result** (a `grist.Receipt`) is what its marker holds: the batch's last
position and an `EventRef{Tenant, Pos}` per event (§10.3), never a value
the decision computes, so the marker needs nothing of the application
and a retry's answer is the first attempt's exactly. **Rejections are
remembered** (the first version of this note decided otherwise, to keep
refusals off the tenant's lock; slice 1's review found a refused
command committing after its client was told no, §0.2): a refusal takes
the tenant's lock as a commit does, the price of a final answer. A key
names at most one outcome. Event ids a
decision needs (a new counter's id when the caller gives none) come
from `r.ID(n)`: a UUIDv5 of the command id and n, so a decision stays a
function of its inputs and its retries mint the same ids.

**Composition.** grain's `skip-event-storage` (a parent command running
children) becomes `grist.Sub(r, Child, c)`: the child decides on the
parent's `Reads` (the same P), its queries join the parent's condition,
its events the parent's batch. One append, one step (the parent's,
naming the children).

### 2.5 Queries

A query is reads and arguments in, a result out; it never appends.

```go
// proposed
var Show = grist.NewQuery(App, "show-counter",
	func(r *grist.Reads, q struct{ Counter string }) (CounterView, error) {
		return CounterView{ /* CounterNames.Get(r), Totals.For(r, q.Counter) */ }, nil
	}, grist.Authorize(grist.Role("viewer")))
```

Its step (§3.1) records the result and the pinned position P, so the
model checks that the answer is the fold of the log through P.

### 2.6 Processors

A processor subscribes to event types and handles each event (in
position order, per tenant) after its checkpoint. **Its handler reads
as of the trigger's position T, unconditioned**: positions are dense and
committed in order (§2.10), so the prefix through T never changes, and a
read through it needs no condition. The only condition on a processor's
own commit is its checkpoint's. The results:

| result | what commits | guarantee |
| --- | --- | --- |
| `grist.Emit(events...)` | the events and the checkpoint, one transaction, conditioned on the checkpoint alone | exactly once in the log |
| `grist.Dispatch(cmd, c)` | the command decided on fresh reads at the head, conditioned on them as any command is, with the checkpoint in the same transaction | exactly once in the log |
| `grist.Effect(f, grist.AtLeastOnce)` | f runs, then the checkpoint (and an outcome event, `grist.OnSuccess(e)`) | at least once outside the log |
| `grist.Effect(f, grist.AtMostOnce)` | the checkpoint first, then f (once), then the outcome event in a commit of its own (under a marker of the effect key): a crash between loses the effect or the outcome (§0.6) | at most once outside the log |

`Dispatch` decides at the head, not at T, because a command's rule is
about the state it commits into (a welcome command must not create a
second account just because the trigger is old); a failed condition is
retried as a command's is, under the idempotency key (processor,
tenant, T), so a crash and a retry dispatch once. `grist.Skip()`
checkpoints and does nothing.

**Errors.** A handler's or an effect's error is retried with backoff (3
tries by default, `grist.EffectTries(n)`, in virtual time in a
simulation), then checkpointed with a `grist.failed` event naming the
trigger, so a poison event stops nothing and is visible. A result grist
cannot carry out is such an error too: the zero `Result`, an `Effect`
without a function or of a delivery slice 1 lacks. Under at least once
the committed run decides the outcome: a run that had an ok effect, then
crashed before its checkpoint and failed every try on the restarted
node, checkpoints `grist.failed`; the checkpoint's step records `ok`
(which outcome it commits), and the model admits either (§3.3) (grain checkpoints a failed effect silently and
never retries). `grist.OnError(grist.RetryForever)` is grain's
`retry-on-error?`: the processor stops at the event and retries it until
it succeeds, its lag growing (§9's metrics show it). Transient store
errors (§2.4) are retried apart and never count toward the handler's
tries: a failover must not turn into `grist.failed` events.

```go
// proposed
var Welcome = grist.NewProcessor(App, "welcome", Created,
	func(ctx context.Context, r *grist.Reads, e CounterCreated, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			return Mailer.Send(ctx, grist.EffectKey(ctx), "created "+e.Name)
		}, grist.AtLeastOnce)
	})
```

`grist.EffectKey(ctx)` is (processor, tenant, T): the idempotency key an
at-least-once effect hands its dependency, so the dependency can make
"at least once" "once" where it supports keys.

**Checkpoints are rows** (§10.2): `t/<tenant>/c/<processor>` in the
simstore, `grist_checkpoints(tenant, processor, pos)` in SQL, read with
`GetForUpdate` (`SELECT ... FOR UPDATE`) and written in the same
transaction as the processor's events: a compare-and-set, so two
pollers of one processor (two nodes, before leases) cannot both advance
it; the loser's transaction sees another position and re-reads. The
checkpoint is in every processor step's observations, so the model sees
it move. The poller reads batches of 100 after the checkpoint, every
250 ms or when the store signals an append (a `qenv.Signal` in a
simulation). An `Emit` batch commits several triggers' events and the
checkpoint in one transaction, but records **one step per trigger**
(§3.1), so the model's handling is per event whatever the batching.

### 2.7 Periodic tasks (slice 3)

`grist.NewPeriodic(App, "daily-totals", grist.Every(24*time.Hour),
handler)`, the handler given the tick's time and a `Reads`. Its commit
for (task, tenant, slot) carries a tick row and is conditioned on its
absence, so every node may wake for every slot (as in grain) and one
commit wins. A tick is written only when the task appends or runs an
effect: a `Skip` writes nothing, and running it twice is harmless. A
periodic effect runs **only after its tick commits**, so it is at most
once per slot. Tenants run one by one, each in its own transaction;
missed slots run once, for the latest (`grist.CatchUp(grist.Latest)`);
schedules are `Every(d)` and `Cron(spec)`, in virtual time in a
simulation.

### 2.8 Screens (slice 3)

A screen is queries and commands in a page (§5): its declaration names
the queries it renders and the commands its actions send, and
`Render(results) string` is a pure function.

### 2.9 Topology and boot verification

The registry is the topology (no separate EDN model to keep in step):
each block's edges are what its constructor was given and what the
analyzer proves the code emits and reads (§4), checked in as
`grist.topology` (`grist topology -w`, diffed in CI). `app.Verify()`
runs at boot and in every test's setup, and refuses to start, naming
the block and the fix, when an edge breaks grain's flow grammar
(command → event; event → read model | processor; read model → command
| query | processor; query → screen; screen → command; processor and
periodic → command | event); an event is produced or consumed by nothing
(a warning; `grist.Sink` for audit-only events); the analyzer's edges
differ from `grist.topology`; a schema differs from `grist.lock`, an old
version lacks an upcaster, or an upcaster changes tags without
`Reindex`; an event field is a float or a time; a command or query has
neither `Authorize` nor `Internal()`; or two blocks share a name, or a
name is not a valid Quint identifier once mapped (`create-counter`
becomes `createCounter`; `to` is reserved).

### 2.10 The store

grist owns its store interface; it does not reuse hallgren/eventsourcing's
`core.EventStore`, which appends by aggregate and version. grist has no
aggregates: its condition is a set of selectors through a position, its
order one sequence per tenant, and a batch carries a marker, a
checkpoint and its step. The contract is package `grist/store` (it
imports nothing of grist, so a tier depends on it alone); its interface,
as built (slice 1):

```go
// as built: package grist/store (abridged; Name, Marker and Checkpoint too)
type Store interface {
	CreateTenant(ctx context.Context, tenant string) error // its head, at 0
	Tenants(ctx context.Context) ([]string, error)
	// Read: events matching q through at (0: the head, returned), in position
	// order or newest first (q.Reverse), at most q.Limit; the reader's copies.
	Read(ctx context.Context, tenant string, q store.Selector, at store.Position) ([]store.Stored, store.Position, error)
	// Append commits the batch at the next positions if its condition holds,
	// its marker is new and its checkpoint as expected; *store.ConditionFailed
	// names the selector and event that broke it (or the marker), anything
	// else is transient.
	Append(ctx context.Context, tenant string, b store.Batch) (store.Position, error)
	Heads(ctx context.Context) (map[string]store.Position, error) // every tenant's: a change feed's poll
	Wait(ctx context.Context, tenant string, p store.Position) error
}
```

Methods added later are optional interfaces, found by a type assertion,
so a store written against this one keeps compiling. The first is
`store.StepLog`: **a commit's step is written in its own transaction**
(`Batch.Step`, the step's fields but those the head gives, `"v": 1`) and
read back with `Steps(ctx, tenant, after, limit)` (each row timed by the
store under the tenant's lock, `StepRow.Time`), `WaitSteps` waiting for
a row (a refusal's step moves no head) and `StepCount` (a relay's
distance behind, without reading the rows). The second is
`store.TenantFeed`, `WaitTenants(ctx, n)`: a node starts the relay and
pollers of a tenant created after it started (§0.4). A transient error
before a store's COMMIT is `store.NotCommitted`: nothing committed, so
grist need not answer "outcome unknown" (§0.4). Slice 1 recorded a commit's step from
`Batch.OnCommit`, which memstore and simstore ran at the commit even when
the caller was then told the outcome is unknown: the simulation's
property, not database/sql's, where a node that dies between COMMIT and
the hook loses the step. **Since slice 2 every tier records from the
rows** (§0.3): `grist.Relay` reads each tenant's committed rows in order
and records them, the hook is gone, and a node needs a StepLog.

Positions are int64, dense and assigned under the tenant's lock, so
within a tenant the position order is the commit order. This is the
property processors' trigger-position reads and checkpoints rest on,
and the reason positions are not a database sequence: a `bigserial` is
drawn before commit, so a reader can see 12 committed while 11 is in
flight, and a checkpoint at 12 skips 11 forever. Time is one value per
append, `qenv.Now` (virtual in a simulation).

One contract suite (`grist/storetest`) runs against every tier, and
holds what the model takes for granted (§3.3): dense positions,
conditions honoured (checked against a brute-force filter over every
event, on small random histories), checkpoints' compare-and-set,
markers (a commit's receipt, a refusal's outcome, a key that cannot
commit twice), reverse and limit, concurrent appends (tasks appending at
once with conditions, compare-and-sets and markers, the committed log
replayed through `store.CheckCondition`), `Wait` woken by an append,
no slice shared with a reader or a writer, the step log, and **tenant
isolation**: a read of t1 never returns t2's events, an append to t1
never moves t2's head. Mutant stores show each check sees what it is
for: one that ignores the tenant in `Read` fails tenant isolation, one
that checks conditions outside its lock fails the concurrent check, one
whose `Wait` returns at once fails the wake-up check.

- **memstore**: maps and a mutex, for unit tests of a fold or a
  decision. Not for simulations (a real mutex).
- **simstore** (slice 1): over `qenv.Store`. Keys `t/<tenant>/head`,
  `t/<tenant>/e/<position, 20 digits>`, index keys
  `t/<tenant>/i/<type>/<position>` and
  `t/<tenant>/g/<type>/<key>=<value>/<position>`, markers
  `t/<tenant>/m/<command id>`, checkpoints `t/<tenant>/c/<processor>`,
  step rows `t/<tenant>/s/<seq>`.
  An append takes the head with `GetForUpdate` (the tenant's lock),
  checks the marker, the checkpoint and the condition (`Scan` over each
  selector's index prefix), writes (the step row among the rows), and
  commits; grist records the step from the row, as on every tier. The head lock serializes a tenant's
  appends; under Snapshot and Serializable the loser of a race gets
  `ErrConflict` from `GetForUpdate`, transient (§2.4). The Store's
  checker (`GOTOGETHER_CHECK=1`) and fault kinds apply. **Cost**: `Scan`
  walks every row of the store (no range), and a Serializable commit
  walks them again per scanned prefix: an append is O(rows in the store),
  every tenant's included. Fine for explorations of hundreds of events;
  a ranged `Scan` in `qenv.Store` is the fix if §10.4's measurements say
  so.
- **sqlstore** (slice 2, built: grist/sqlstore, §0.3): `database/sql`,
  one SQL that PostgreSQL, SQLite (3.35: `RETURNING`) and qsql all run,
  the driver the application's (pgx's stdlib, modernc.org/sqlite: no
  cgo); grist imports none, and a `Dialect` (`Postgres`, `SQLite`,
  `QSQL`) says only the default isolation level, the per-transaction
  settings and how errors read. Tables `grist_heads(tenant, head,
  steps)`, `grist_events(tenant, pos, type, version, at_ms, cause, tags,
  body)`, `grist_index(tenant, k, pos)` (k `i/<type>` or
  `g/<type>/<key>=<value>`, segments query-escaped),
  `grist_markers(tenant, id, hash, outcome, pos, events)` (the receipt is
  its last position and event count), `grist_checkpoints(tenant,
  processor, pos)`, `grist_steps(tenant, seq, head, step)`, every key a
  composite `PRIMARY KEY` (`grist_steps(tenant, seq, head, at_ns, step)`
  since §0.4, `at_ns` the row's time under the lock). An append's
  **first statement** is `UPDATE
  grist_heads SET head = head + $1, steps = steps + $2 WHERE tenant = $3
  RETURNING head, steps`: it takes the row's lock and reads the new head
  and the step row's number in one round trip (no advisory locks, which
  are invisible to the simulation and to `qhistory`). Then the marker, the
  checkpoint and the conditions (`SELECT pos FROM grist_index WHERE
  tenant = $1 AND k = $2 AND pos > $3 AND pos <= $4 ORDER BY pos ASC
  LIMIT 1`; a selector of several tags reads the events' other tags),
  then the rows and COMMIT. Taking the lock after the condition instead
  fails storetest's concurrent check at read committed, on qsql and on
  PostgreSQL. sqlstore retries nothing itself: a transient error goes back
  to grist, whose retry starts again at the key's marker, as with
  simstore. Errors no retry fixes (SQLSTATE classes 42, 0A, 22, 3D, 3F,
  28; SQLite's SQLITE_ERROR, MISUSE, ...) are `store.Permanent`, so a
  missing table is not ten tries and "outcome unknown".
  - **PostgreSQL**: `BeginTx` with `sql.LevelReadCommitted` stated, not
    the server's default: after the lock every statement sees every
    commit before it, which the condition check needs (repeatable read
    and serializable pass the contract too, a lost race for the lock a
    40001). 40001, 40P01 and 55P03 are transient (§2.4), never failed
    conditions. Each transaction sets `lock_timeout` (5 s, grain's) and
    `idle_in_transaction_session_timeout` (60 s) with `set_config(...,
    true)` (`SET LOCAL` as one statement with parameters), so a stuck
    holder is an error, not a hang (`TestPostgresLockTimeout`), and a
    crashed node's open transaction releases the tenant's lock; local to
    the transaction rather than the pool's connections, so no connection
    hook is needed. Row-level security (`SET LOCAL grist.tenant`) is
    slice 3's second fence.
  - **SQLite**: one writer for the whole database, whatever the tenant,
    so tenants do not append in parallel. `sqlstore.SQLiteDSN(path,
    busy)` is modernc's spelling of WAL (readers do not block the
    writer) and `busy_timeout` (a writer waits for the lock instead of
    failing at once); `Open` checks both and names the fix. Transactions
    begin deferred: an append's first statement is a write, so it takes
    the write lock before reading and never upgrades a stale read
    snapshot (the SQLITE_BUSY no timeout waits through); the
    `_txlock=immediate` the DSN once set for that reason bought nothing
    (§0.4). Every isolation level is serializable there: the dialect
    states none, and `Open` refuses one stated other than serializable.
  - **Change feeds**: `Wait`, `WaitSteps` and `WaitTenants` wake on
    this Store's own COMMITs (every one, its answer lost or not: one of
    unknown outcome may have committed) and read the database every
    `Options.Poll` (250 ms) for other processes' appends; on qsql they
    wait on the database's own signal (`qsql.StoreOf`,
    `qenv.Store.Changed`), notified at every commit whoever made it, and
    no timer runs, so a second Store over the database wakes them and a
    settled system is quiet. A wait returns as soon as its read holds.
    `LISTEN/NOTIFY` is an optimisation for later.
- **sqlstore on qsql** (slice 2, built): the same code on `qsql.Open`
  over a `qenv.Store` (`sqlstore.QSQL`, `gristsim.Config.SQL`), so the
  SQL tier is explored with qsql's anomalies, SQLSTATEs and commit faults.
  qsql's extension came first (§8.2); the statements needed no other
  dialect, since `set_config` is PostgreSQL's alone.

**Retention and erasure** (a gap, §9). History is kept forever by
default. grain's one bounded policy is inert until an operator
activates it durably; compaction then deletes old events of a tagged
kind under the tenant's lock and appends a receipt, and processors may
not subscribe to bounded events (a deleted trigger cannot be handled).
grist takes that shape: positions are never renumbered, and erasure of
personal data is such a deletion or crypto-shredding (fields encrypted
with a per-subject key, the key deleted). **State in tables** comes
later: `grist.Table(rm)` writes a read model's state in the append's
transaction, conditioned on the rows' versions; the model is unchanged.

## 3. What a grist application gets from gotogether

This is the reason for grist. Each subsection is something an
application gets by being built of grist's blocks, with nothing written.

### 3.1 Every block records its step, by construction

grist records through `qobs` at the place each effect takes effect (as
payd's adapters do), never by annotation. **Every step of tenant t has
actor t and process the store's name**, whatever node or task recorded
it: commits happen at the store, so the store is the process, and a
node's crash and restart change no step's process. The binding has no
`restart` action (a process change would be an error, binding.go's
`Restart`, and none can happen), and each tenant's trace is checked as
its own instance of the model from init.

**Order: one domain clock on every tier.** Each step carries
`quint.order.pos` (`"order.pos"` in `qobs.Record`'s pairs):

- a step that commits a batch ending at position P: **2·P**;
- a step that reads through P and commits nothing (a query, a reused
  key, a decision's error): **2·P+1**, after the commit of P and before
  the commit of P+1, concurrent only with other reads through P, which
  commute;
- a command's refusal (a rejection, a conflict), which commits its
  marker alone: **2·H+1**, H the head at its commit (at or after the P
  it read through: its condition held between, or, for a conflict, did
  not);
- a processor step that commits only its checkpoint (`Skip`, an
  effect's checkpoint, a `failed` with no event): **2·H+1**, H the head
  its own transaction read, not its trigger T: a poller that emitted at
  7 for trigger 5 then checkpoints trigger 6 records 14 then at least
  15, in its program order (with T, 13: a cycle with its thread);
- an effect step (`<processor>Effect`): 2·H+1, H the head the worker
  read before running it (its checkpoint's step may share that value:
  `order.p.<processor>` puts the effect first);
- in an `Emit` batch of several triggers, each trigger's step gets 2·
  the last position of its own events, or, if it emitted none, 2·(the
  last position before it in the batch)+1: one step per trigger, in
  trigger order, inside one transaction.

Each processor's steps also carry `quint.order.p.<processor>`: 2·T for
T's effect steps, 2·T+1 for its handling (since slice 2, §0.3: the
handling step is the relay's, on another thread than the effect's, so
program order no longer puts the effect first), ordering them among
themselves across nodes and incarnations. A commit's steps are
recorded by the relay in commit order, on one thread of their own
recorder (`grist/<store>/<tenant>`). Both
domains agree with program order (a task's steps have increasing
positions), so `qtrace.build` finds no cycle, and the steps of a
simulation, a SQLite file or a fleet of PostgreSQL nodes reconstruct to
one order without trusting a clock.

| step action | recorded | arguments | observations |
| --- | --- | --- | --- |
| the command's name | from its step row, written in the append's transaction and recorded by the relay (§2.10, §0.3) | the command's fields, command id, the read set it read (`readPos`, `reads`: the condition too, unless `Unconditioned`) | the events appended (types, fields, causes), the head; the result |
| the command's name, with outcome | from the step row of its marker's commit, when refused | as above | the rejection's name, or `conflict` after the tries; the head, no events |
| the command's name, with outcome | on return, for a reused key or the code's error | as above | `idempotency-key-reused`, or the error's text |
| the query's name | on return | the query's fields | the result; the position read through |
| `<processor>` | from its batch's step row (one step per trigger) | the trigger's position; an effect's checkpoint, `ok` (which outcome it commits) | the events appended (types, fields, causes), the checkpoint |
| `<processor>Effect` | grist's wrapper, after f returns (at least once before the checkpoint, at most once after it) | the trigger, the effect key | f's outcome (`ok`) |
| `<processor>Outcome` | from the step row of an at-most-once outcome's commit (§0.6) | the trigger, the effect key, `ok` | the events appended, the head |
| `<periodic>` | at its commit (slice 3) | slot | the events appended |

A refusal and a commit are recorded at their commit, because the caller
may never be told (an 08006, a crash
between commit and response), and the model must see it. A retried
command that finds its marker records nothing: its effect was recorded
once. **A raw append** (application code calling the store, which
gristflow flags, §4.1) records nothing: the model's log then lacks
events the store holds, and the first later step whose recorded head or
events the model computes otherwise is a Mismatch: a raw append is
caught by the next step of its tenant, not where it happened.

**The read set is a step argument**: `readPos` (P) and `reads`
(a set of (type, tag key, tag value) triples; key "" for a type-only
read), so grist.qnt's conditional-append property (§3.3) is checked
against what the decision read, not assumed. Without them the property
would hold vacuously; and since they are the runtime's own report, the
model also states which reads a decision's rule needs (`covers`, §3.3),
so a runtime that drops a read from the set (and so from the condition)
is caught at every commit. The read set is recorded whatever the
condition: an `Unconditioned` command's step records what it read, and
its model leaves `conditionHolds` out. **The encoding is slice 2's
already** on the model's side: grist.qnt's `Read` is `{ typ: str, tags:
Set[(str, str)] }` (a read of several tags is one record); slice 1's
binding builds it from the triples (`readsOf({{toList .reads}})`), and
slice 2's typed template function records the records directly, the
model unchanged.

**Client steps.** gristsim's workload (§3.6) also records each command
and query where the client sees it: `qobs.Invoke` before the
request, `Call.Done` with the answer, the idempotency key as `id`, every
retry of a lost or late response inside the one call; checked with
`qtest.Linearize()`, a duplicate a retry created shows as a later read's
Mismatch, a call never answered as `Pending` (`qtest.Completes()`).
Since slice 2 every run records both sides (§0.3): the clients' calls
under their own actor, `<t>/clients` (`gristsim.ClientsActor`: no tenant
name holds a slash, `grist.CheckTenant`), the store's steps under t, and
the two bindings say `actors: ['!*/clients']` and `actors: ['*/clients']`
(§8.2, item 3), the client side checked by a
second binding of the same model (`counters.calls.binding.yaml`: no
positions, no processors' eventual vals) over the same runs
(`System.WithBinding`). Slice 1's `gristsim.Config.Record` (`AtCommits`
or `AtClients`, one side a run) is gone.

### 3.2 The model and its binding

**The model's state is one tenant's log**: `var log: List[Event]` and
`var checkpoints: str -> int`, each tenant's trace its own instance.
Read models are defs over the log (`counterNames(log)`), not variables:
the model cannot disagree with itself about what a read model holds,
and a query's observation is checked against the def at its position.
Cross-tenant properties are none of the model's (§3.3).

**Verdicts follow one convention.** Hand-written command and processor
actions keep their preconditions, so a committed step the model's
precondition disables is **Rejects** (the model cannot take it), and a
step whose observations differ from the model's (`expect:` over the
events appended, the head, a query's result) is **Mismatch**. Violates
is for what the model admits and an invariant forbids: a property of
the design, or a test-only invariant stating what a declared guarantee
allows but an application must not do (§8.1's `welcomedOnce`). Claims
then say `Some(Rejects("createCounter"))`, `Some(Mismatch("average"))`.
The precondition is evaluated in the step's state, the log at its
commit: for a conditioned decision that equals its state at P for every
query it read (nothing matching was appended between), and for a blind
one it is the check that the blind decision skipped. A command
declared `Unconditioned` on purpose must have a precondition that does
not depend on the advisory reads.

```quint
// model/counters.qnt, hand-written in slice 1 (a sketch)
action createCounter(counter: str, name: str, readPos: int, reads: List[(str, str, str)]): bool = all {
  not(exists(log, "counter-created", "name", name)),
  grist::conditionHolds(log, readPos, reads),
  append([created(counter, name)]),
}
// the design guarantees it: every created name is created once
val uniqueNames = createdNames(log).size() == createdCount(log)
```

**The binding** maps each step action to its model action, every
parameter the same-named step field, with `expect:` over the
observations. Slice 1's is hand-written: its events' fields are scalars
and its read sets lists of triples, which `toList` renders today (JSON
arrays become tuples). Slice 2's generator (`grist model`, in
`cmd/grist`) writes it with a model skeleton (`model/<app>_gen.qnt`: a
sum type per event, one action per block, one def per read model), and
needs a **typed template function** in binding, `{{ toQuint
"List[Event]" .obs.events }}`: the step's JSON decoded by a Quint type of
the model (records, sum types by tag, collections of them, aliases
resolved) through `itf`'s values, and used by `Param.DefaultTemplate`
for record- and variant-typed parameters. Today `jsonElem` renders an
object as a quoted string and truncates a float. The function refuses a
number with a fraction and a time string, naming the field and the fix
(an integer unit). A generated stub of an action the application has
not written appends what the step recorded and checks nothing; `grist
verify` lists stubs, and `gristtest` reports them through
`qtest.Optional`, so `GOTOGETHER_STRICT=1` fails on them. (Built, §0.9: the generator is the app's
binary's `model` command, it writes no stubs of actions but leaves the
rules as holes of the app's own module, and the event encoding stays
slice 1's record, `batchIs` comparing it.)

### 3.3 grist's guarantees as a Quint module

`grist/model/grist.qnt`, vendored into an application's `model/` (as
examples/twophase vendors choreo.qnt), states what the framework
promises, in the per-tenant state above:

- **Positions**: dense, increasing, commit order (a commit's recorded
  head is `log.length()` after it).
- **Conditional append**: `conditionHolds(log, readPos, reads)`, `0 ≤
  readPos ≤ head` and no event after `readPos` matching a recorded read;
  part of every conditioned commit's precondition (a refusal's
  included), with the read set from the step. A conflict requires it
  false.
- **Coverage**: `covers(reads, need)`, some read selecting every event
  `need` selects; an application's action requires it of what its rule
  reads (`nameRead(name)` for create-counter), so the read set is
  checked, not only the condition it implies.
- **Keys**: `freshKey(answered, id)`, a key names at most one outcome:
  every committed step of a command (a commit, a rejection, a conflict)
  requires its id unanswered and adds it to the model's `answered`; a
  reused key requires it answered.
- **Processor reads**: a handler's action evaluates its reads on
  `log.slice(0, trigger)`, so the model states the trigger-position
  rule rather than assuming it; `Dispatch` evaluates the command's
  action on the current log, with the command's precondition.
- **Checkpoint monotonicity**: a processor's checkpoint never moves back
  and never passes an event it has not handled: exactly one handling
  step per subscribed event (`Emit`, `Dispatch`, `Skip`, `failed`).
- **Effects** (`AtMostOnce`'s defs since §0.6: `atMostOnceFirst`, `atMostOnceEffect`, `atMostOnceDone`): `AtLeastOnce` admits one or more effect steps per trigger,
  every one under the trigger's key (`keyStable`), its checkpoint step
  only after them, committing what its run reached (`atLeastOnceDone`:
  the success event after an ok effect step, `grist.failed` after
  `EFFECT_TRIES` failed ones; a run with both admits either, the
  checkpoint's `ok` saying which), and **eventually** (after a settle)
  each trigger has an ok effect step or a `grist.failed`; `AtMostOnce` admits zero or one, and **an outcome
  event, if present, agrees with the one effect step** (it may be absent:
  a crash between checkpoint and effect loses it, by right). An
  application whose effect must not repeat states it with an invariant
  over its own effect steps.
- **Periodic** (slice 3): at most one tick per (task, slot).
- **Leases** (slice 3, §3.4): checkpoints advanced only under the
  newest term.

**Tenant isolation is not a model invariant**: each tenant is its own
instance, so the model cannot see one tenant's events in another's log.
It is held by the store contract suite (§2.10), with a mutant store
shown to fail it, and by every read and append taking its tenant from
the envelope, never from a handler's argument.

This module is checked in grist's own tests (grist/model/model_test.go:
it typechecks, its defs hold on small values under `quint test`, and a
small machine over them keeps its invariants under `quint run`) and
against grist's own runtime (examples/counters' explorations), so an
application's failure is its own or its model's, not the framework's.

### 3.4 Leases, in plain Quint (slice 3)

Several nodes need one tenant's processors run by one node at a time.
Each tenant's lease term is written into **that tenant's own log**
(`grist.lease{node, term}`, a conditioned append), and every processor
commit's condition includes "no lease event with a newer term": a node
that lost its lease cannot advance a checkpoint, whatever its clock
says, because the fence is in the transaction it commits. Heartbeats and
the coordinator's assignments are events in the reserved tenant `grist`.

The maintainer asked for Quint, or Choreo for distributed parts. This
protocol is plain Quint (`grist/model/leases.qnt`): its only
communication is the shared log, read and appended under the store's
serialization, which is Quint's state-machine form, and Choreo's
strengths (local states, messages in flight) have nothing to model.
Choreo becomes the language if message-passing parts appear (routing to
the owner, a gossip of heads, a node-to-node handoff), each transition a
named action of its node (`choreo::step_with`, the twophase gotcha).

### 3.5 Every seam simulated

grist's runtime is written against qenv's seams, and `gotogether lint
-sim` holds grist's packages to it, as `TestCoreStaysInTheSimulation`
holds the core. Pollers, the effect worker and the scheduler are tasks
(`qenv.Go`) waiting with `qenv.Sleep` and `qenv.Wait` on the store's
signal, marked `qenv.Daemon`; a node is a `qenv.Process`
(`Sim.Process(name, true, start)`), restarted from its checkpoints; the
store's isolation, commit faults and lost answers are the qenv.Store's
or qsql's; an effect's dependencies are `qenv.Service`s or HTTP clients
through `qhttp.NewTransport`; the API runs on `qhttp.NewServer` (slice
2). The effect's crash points are one declared kind,
`qenv.DeclareFaults("grist.<processor>.effect", "crashBefore",
"crashAfter")`, chosen with `Sim.FaultOf` before f (through
`Only("crashBefore")`) and after it, the crash made with
`ProcessOf(ctx).Crash()`; `qenv.FaultPoint` cannot name two crashes (two
kinds, `...effect.before` and `...effect.after`, are the alternative).
At most once the same two options follow the checkpoint (§0.6).
The crash that duplicates an at-least-once effect is then one named
option, which a `qenv.Script` asks for and `Exhaust` reaches with one
fault.

### 3.6 gristsim.Run, and settling

`gristsim.Run(c qenv.Chooser, app, cfg)` builds a simulation from c, a
simstore (or, since slice 2, sqlstore over qsql: `cfg.SQL`) at `cfg.Isolation`, n
nodes, a workload of clients, and runs it. Its end is ordered so the
eventual vals are judged:

the clients finish; **schedulers stop** (periodic tasks would append
forever, and no settle would find the system quiet); `Sim.Settle`
(processors, daemons on the signal, drain to the head; faults stay off
from here); probes run every read model through its queries; and
**`Sim.Settle` again**, so the run's last record is a settle. A step
after a settle voids it, on its recorder or on any other later in time
(`qtrace.Reconstruct`), so probes on a separate recorder work only while
they take no virtual time; the second settle always works.

Its signature is a `qtest.System`'s `Run`:

```go
// proposed: an application's test
func system(cfg gristsim.Config) qtest.System {
	return qtest.System{Binding: "counters.binding.yaml", Rates: qenv.DefaultRates.Set("nemesis", 0.02),
		Run: func(c qenv.Chooser) error { return gristsim.Run(c, App, cfg) }}
}
```

The workload defaults to clients sending random commands of the
registry, arguments drawn from the command's type by `gristsim.Gen`
(strings from a small alphabet so names collide, which is what finds
races); an application overrides one command's generator, never writes
a workload loop. Clients send through `qobs.Invoke`/`Call.Done` (§3.1),
and retry a lost response with the same key.

### 3.7 Explorations, Hegel, shrinking and mutants

Explore and Exhaust with every claim of today across the Store's three
isolation levels; `qhegel.Shrink` on a System's runs; qweave `-mutate`
on the application's folds and decisions (`System.Mutate`) and
`qtest.MutateSteps` on its observations. Later: `gristhegel` (a module
of its own) turns the command registry into a `hegel.RunStateful`
machine judged through qhegel's `Case`; `gristtest.ShrinkCommands` is
`qtest.ShrinkActions` over a real run's commands replayed through the
API; qrole roles per processor (its lag as the queue) bring qload and
qmeta to an effect retrying against a slow dependency.

### 3.8 The real tier, checked by the same binding (slice 2)

`grist.Main(app)` serves the application on sqlstore and writes its
steps with `qobs.FileSink(path, qobs.PerStep)`, validated with the same
binding. Commits are recorded from the store's step rows (a
`store.StepLog`, written in each commit's transaction, §2.10), not from
a post-commit hook, and ordered by the domain clock across nodes, so a
node killed between its COMMIT and its record loses no committed step;
an effect
step lost between f and its record is one the model allows to be unseen.
`qnemesis.FromScript` replays a simulation's fault script against it.
Built so far (§0.3, §0.4): the store and the recording; examples/counters'
`TestRealSQLite` runs the node and four clients as goroutines on a
SQLite file, the clients recording their calls (`gristsim.Do`), records
to a `PerStep` file sink, kills the first node mid-phase (its sink dies
at once, its relay behind) and starts a second with a relay of its own
(which records what the first never did, and whose copies of the first
node's records Reconstruct drops), creates new counters in the second
phase, waits for the processors and the relay (`Relay.Behind`,
`Relay.Err`), probes, records the settle, checks every client's answer
against the store's steps, and validates the file with
`counters.binding.yaml` and, linearized, `counters.calls.binding.yaml`.
Since §0.5 it is `TestRealHTTP`: the clients call `grist.NewHandler` on a
real `http.Server` over `grist/client`, the second node taking the
server's traffic when the first is killed; `grist.Main` serves an app's
binary (`serve -db sqlite:PATH -steps FILE`).

## 4. Constraints enforced

### 4.1 Analyzers (qanalysis, `quintvet`)

Two analyzers in qanalysis (it has x/tools and the golangci plugin),
recognizing grist's constructors by import path and type:

- **gristflow** (slice 2): from each decision's and handler's returns,
  the event types it can emit, the read models it reads, the commands
  it dispatches; written to `grist.topology` and compared at boot
  (§2.9). It reports **a decision that rejects under a condition on a
  read its append is not conditioned on** (`Unconditioned`, or a
  `Within`, once built, leaving the read out): "create-counter refuses ErrNameTaken
  on Exists(counter-created, name), which its append is not conditioned
  on: two concurrent create-counter can both pass; remove Unconditioned,
  or exempt with //grist:advisory <reason>". And a processor handler or
  decision that reaches a `grist.Store` (a raw append, grain's
  duplicate) instead of returning `Dispatch` or `Emit`.
- **gristpure** (slice 3): decisions, folds, upcasters, queries and
  renders (and what they call in their package) call no clock, rand,
  I/O or `qobs`, start no goroutine, use no channel, write no
  package-level variable, reach no store or other `Reads`, and range
  over no map where the order reaches the result (unless the keys were
  sorted). **The map rule is new**: qlint has none (its `-sim` rules
  are `go` statements, wall clocks, rand functions, background contexts
  and selects); it is written for gristpure and offered to `lint -sim`
  after. An unseen call is unverifiable, exempted with `//grist:pure
  <reason>`.

### 4.2 Boot verification

§2.9's list, run by `app.Verify()` at `grist.Main`'s start, by
`gristsim.Run` before every run (cheap: maps of names), and by `grist
verify` in CI with the analyzers' output.

### 4.3 Authorization, denied by default

Every command and query is denied at every adapter unless it declares
`grist.Authorize(p)`, a predicate over the envelope's actor and the
tenant (`grist.Policy`; `grist.Authenticated`, `grist.Actors(...)`);
`grist.Internal()` marks one no adapter serves (only processors
dispatch it); the adapter's boot (`NewHandler`) refuses an App with a
block that has neither (§0.5: `App.Verify` does not, for in-process
callers). The check is in the adapters (as grain's), so a `Dispatch` is
not subject to a user's roles. A denial is a 403 that records nothing
at the store (nothing ran); its client records the outcome `denied`.
An Internal block is answered 404, as an unknown one (§0.7).

## 5. The API layer and Datastar

**HTTP/JSON** (slice 2, built: `grist.NewHandler`, package grist's,
`net/http` only, §0.5): `POST /t/{tenant}/commands/{name}` with
`Idempotency-Key` (the command id; 400 without): 200 with the marker's
result (`{"pos", "events": [{"tenant", "pos"}]}`, on every retry of the
key), 422 with a rejection's name (or `idempotency-key-reused`), 409 on
conflict after the tries, 403 when denied, 503 with `Retry-After` when
the outcome is unknown or nothing committed (retry under the same key;
later, when the tenant's lease is elsewhere), 400 for arguments grist
refuses (`ErrBadArgs`) or a key it refuses, 413 for a body past
`MaxBody`, 404 for an unknown or Internal block or an unknown tenant,
500 for the decision's error; a retry answered from the key's marker
carries `Idempotent-Replayed: true` (§0.7). `GET /t/{tenant}/queries/{name}?field=value` returns `{"result",
"pos"}`, the position read through; `GET /catalog`, behind
`grist.DevEndpoints`, the registry. Authentication is the application's
middleware (`grist.WithActor`; `grist.Middleware`, or
`grist.ActorFromHeader` behind a proxy). A plain `http.Handler`, so
`qhttp.NewServer` serves it in a simulation (`gristsim.Config.HTTP`).
`grist/client` calls it and records each call as a client's.

**Datastar** (`grist/screen`, slice 3, the only importer of
`github.com/starfederation/datastar-go/datastar`): a screen's SSE stream
renders its queries, waits on the tenant's head and re-renders when a
consumed read model moved; its actions post signals through the command
path. Its steps are its queries' plus `screen.<name>.patch` with the
positions rendered, and after a settle the last patch shows the head (an
eventual val).

## 6. The agent workflow

A skill, `skills/grist/SKILL.md` (symlinked into `.agents/skills` and
`.claude/skills`), grain's `/grain` loop with gotogether's checks:
**elicit** the blocks and invariants; **declare** them with stub
decisions (`grist.Todo`) until `grist verify` is clean; **model first**
(preconditions, invariants, `quint run`); **implement red first** (the
stub's exploration fails by `Rejects`); **prove each check can fail**
(a mutant per claim); **tend** (verify, analyzers, exploration on every
change). An app's binary (`grist.Main`) has `catalog`, `verify`, `model`
(the generator, §0.9) and `serve`; `grist new` (`cmd/grist`) starts an
app. Later, in the app's binary: `topology -w`, `invoke`, `query`,
`events`, `fold`, against a memstore or a SQLite file.
`gotogether-start`'s table gets a row.

## 7. Module layout

```text
grist/                 module github.com/chucklehead-dev/gotogether/grist (go.mod; published like qhegel)
  grist.go             App, events, read models, commands, queries, processors, periodic, Reads, Result, Option,
                       Catalog; the node (App.Start: per tenant its relay and pollers, a watcher for new
                       tenants), the relay (Relay), aliases of store's types
  store/               the store contract: Store, Batch, Condition, Marker, Selector, StepLog, the errors,
                       CheckCondition and Select (the reference evaluations); imports nothing of grist
  internal/            the step fields (rec), position keys (poskey)
  runtime/             (later) the node: pollers, effect worker, scheduler, dispatcher (qenv seams only)
  storetest/           the contract suite
  memstore/ simstore/ sqlstore/
  (api)                the HTTP/JSON adapter is package grist's (NewHandler, §0.5); Main is too
  client/              the API's client, recording each call as a client's
  screen/              Datastar adapter (the only datastar-go import)
  gristsim/            Run(chooser, app, cfg): nodes, store, workload, settles, probes
  gristtest/           Generated (the generated files' CI diff), Mutants (overlay builds), Tier (the real
                       tier), Answered; later ShrinkCommands (imports qtest)
  model/               grist.qnt (vendored by the generator, which is package grist's: Generate, Main's model)
  cmd/grist/           grist new (the scaffold); later the CLI below
grist/gristhegel/      module of its own: registry-driven Hegel machines (requires grist, qhegel)
qanalysis/             + gristflow, gristpure (recognize grist by import path; no dependency on grist)
skills/grist/          the skill
examples/counters/     slice 1's application, a module with replace => ../..
```

**A production binary links** `grist`, `runtime`, `store`, its tier
and `api`, and of the core qenv, qobs and qrole; never binding, validate
or qtest, imported only by `cmd/grist`, `gristsim` and `gristtest`
(`internal/linkcheck` fails if `grist`'s or `api`'s graph reaches them).
No Hegel (so `internal/simdeps`'s rule holds), no SQL driver, no
Orchestrion. It joins `go.work`, gets `api/grist.txt`, and CI tests it
with `GOWORK=off`.

## 8. Three slices

### 8.1 Slice 1: the thesis

That a grist application is checked by its model, and that grain's two
bugs are caught, on a simulated store.

**Core prerequisites**: none. `Txn.OnCommit`, `qenv.Signal`,
`DeclareFaults`, `Sim.FaultOf`, `Process.Crash`, `qenv.Service`,
`Script`, `System`, `qobs.Invoke` and the claims exist; slice 1's
binding needs only scalars and `toList`.

**grist**: events with one version (no upcasting); plain and keyed read
models (no cache); pinned `Reads` with `Get`, `For`, `Exists` and
`Events` (reverse, limit); commands with read-set conditions,
`Unconditioned`, transient retries apart from condition retries, and
idempotency (marker first, payload hash, results as positions and
refs); queries; processors with `Emit` and `Effect(AtLeastOnce)`,
trigger-position reads and checkpoints in a table; memstore and
simstore, with the store contract suite over both (and its mutant);
`gristsim.Run` with workload clients (`AtCommits`, and `AtClients` with
`Invoke`/`Done`), stopped schedulers and two settles; every step with
actor tenant, process the store's name and `order.pos`; boot
verification of names and edges. One node. No API, no periodic tasks,
no generator, no analyzers.

**examples/counters**, grain's counter example, without float averages:

- events `counter-created` (tags `counter`, `name`),
  `counter-incremented` (tag `counter`), `average-computed` (`sum`,
  `count`: integers, the mean is the reader's), `notified`;
- read models `counter-names` (plain) and `totals` (keyed by counter);
- commands `create-counter` (`Exists` on the name tag) and
  `increment-counter` (refused for an unknown counter, read through
  `Totals.For`); query `show-counter`;
- processors `average` (`Emit`: on each increment, the tenant's sum and
  count as of the trigger) and `welcome` (`Effect(AtLeastOnce)` calling
  a `qenv.Service` "mailer", `OnSuccess(notified)`);
- two tenants, clients sending to both, names from three letters;
- a hand-written `model/counters.qnt` over a hand-written first
  grist.qnt, and `counters.binding.yaml`, with `uniqueNames` (the
  design's, `simulate: holds`), `averageAsOfTrigger` (each
  `average-computed` is the sum and count at its trigger) and the eventual
  val `everyCounterWelcomedOrFailed`;
- mutants (overlays of the example's files, as payd's): `blind`
  (create `Unconditioned`), `dispatchDirect` (average appends through the
  store itself and returns `Skip`, grain's example).

Claims, each a test with `.Expect`, its rate measured and seeds chosen
for a miss chance under 5% (payd's discipline):

1. **The race is found in the unsafe variant.** `blind`, two clients,
   no faults, names colliding: `qtest.Some(qtest.Rejects("createCounter"))`:
   the second create commits where the model's precondition is false.
   At ReadCommitted, 30 of 40 runs (measured after slice 1's review);
   under Serializable rarely (0 of 120 runs in three configurations
   after the review, 2 of 40 before it; 2 of 60 at four clients and
   three creates each, §0.8): the race's loser gets a transient conflict
   whose retry decides again only when the two appends' transactions
   overlap, and a decision whose reads came before the other's commit
   and whose append began after it commits blind at every level. The
   claim is ReadCommitted's, where every race is lost. **Its contrast**: the
   faithful app at the same configuration,
   `qtest.None(qtest.Rejects("createCounter"))` (0 of 40 at both
   levels), which fails in 13 of 20 runs with the runtime's condition
   taken out of the append (§0.2): the claim tests grist's mechanism,
   not only the mutant.
2. **The safe default passes, through every fault.** The faithful app
   at Serializable and ReadCommitted (Snapshot's runs are Serializable's,
   byte for byte, every append writing the head), one tenant and names
   from two letters, with store commit faults and node crashes:
   `qtest.Conforms()`, `qtest.All(qtest.Settles())`, and
   `qtest.Reaches("store.counters.commit").Faulted()` so the faults
   happened. The same under `AtClients` with `qtest.Linearize()`. And
   **a key's first answer is final** through the API duplicating and
   delaying three requests in ten (`rpc.api.request`, `rpc.api.delay`):
   both sides conform (0 of 40 each), where the runtime without its
   markers fails in 40 of 40 runs at the commits and 33 of 40 at the clients, and without refusal markers in 17 of 20 (§0.2).
3. **The duplicated welcome is the declared guarantee's, seen and
   judged.** A `qenv.Script` asking for `grist.welcome.effect`'s
   `crashAfter` (and `Exhaust` with one fault reaching it): the
   restarted node sends the welcome again under the same effect key. The
   model admits it (`qtest.Conforms()`); a test-only model variant with
   the invariant `welcomedOnce` (one ok effect step per trigger) rejects
   exactly those runs: `qtest.Some(qtest.Violates("welcomedOnce"))` (13
   of 105 runs within one fault). The script's `crashAfter` followed by
   three lost sends on the restarted node checkpoints `grist.failed`
   after an ok send, which the model admits (`atLeastOnceDone`, §0.2).
4. **grain's processor duplicate is caught.** `dispatchDirect`, with and
   without crashes (24 and 25 of 30 runs): `qtest.Some(qtest.Mismatch("average"))`: the
   processor's step records no events where the model's action appends
   one, and the raw append shifts every later head (§3.1). The faithful
   `Emit` never does (claim 2).
5. **The store keeps its contract.** `storetest` over memstore and
   simstore passes; over the mutant store that ignores tenants, it
   fails, naming the isolation check; over one checking conditions
   outside its lock, the concurrent check; over one whose `Wait` returns
   at once, the wake-up check.

Done when the five pass in CI, the README's table names which check
catches which mutant, `run-demo.sh` shows claims 1 and 3, and the
skill's first version walks through the example.

### 8.2 Slice 2: the real tier

That one binding checks a simulation and a real database, and that the
model and binding are generated.

**Core prerequisites**, each its own item, before grist uses it:

1. **qsql's extension.** Today: one table at a time, a single-column
   `PRIMARY KEY`, equality only, `ORDER BY pk [ASC|DESC]`, `LIMIT n`,
   `ON CONFLICT [(col)] DO NOTHING`, `UPDATE ... SET c = c + ?`. sqlstore
   needs: a key of several columns (a composite `PRIMARY KEY (tenant,
   pos)`, encoded into the Store's key so that `Scan` returns rows in key
   order; or, smaller, an encoded `TEXT` key with a prefix range, which
   the sqlstore would then write itself); a range (`>`, `>=`, `<`, `<=`)
   on the last key column, with the others equal, planned as a `Scan` of
   the key prefix and seen by the Store's history checker as a predicate
   read; `UPDATE ... RETURNING` (the head) and `ON CONFLICT (cols) DO
   UPDATE` (checkpoints) as the store's statements need. Parse and plan
   tests, and an `...AgainstBruteForce` test of the range plan against a
   filtered full scan on small random tables, failing with the plan
   turned off.
   **Done** (qenv/qsql): `PRIMARY KEY (a, b, c)` as a table constraint,
   encoded into the Store's key component by component (each value's
   order-preserving encoding, NUL escaped, then NUL 0x01), so a scan is
   in tuple order and the rows whose leading key columns are fixed are
   one prefix; a range (`<`, `<=`, `>`, `>=`, `BETWEEN`) on the next key
   column is a `qenv.KeyRange` within it, scanned by the new
   `Txn.ScanRange` and seen by Serializable's validation and the history
   checker as a predicate read of the range alone (a phantom in the
   range aborts the scanner, an insert beside it does not:
   `TestRangePhantom`, in qsql and in qenv with the validation broken);
   `ORDER BY` the key's columns after those fixed, one direction, with
   `LIMIT`; `INSERT ... ON CONFLICT (cols) DO NOTHING | DO UPDATE SET c =
   ? | c = excluded.c | c = t.c ± ? [WHERE t.c ...]` (a column on the
   value side or in the WHERE qualified, `t.c` or `excluded.c`, as
   PostgreSQL requires: a bare one is 42702, ambiguous); `RETURNING` on INSERT,
   UPDATE and DELETE. Every statement §2.10 names runs as written
   (`TestEventStoreStatements`). Checked by `TestRangePlanAgainstBruteForce`
   (the plan and a scan-only connector against a brute force in Go,
   rows sorted as tuples; each range scanned holds exactly the rows its
   key conditions select, which fails with the plan off), the encoding's
   own brute force, and examples/payd's `TestQsqlAgainstSQLite`, a
   differential test of random statements of these shapes against
   modernc.org/sqlite. A one-column key keeps its encoding.
2. **The typed template function** (`toQuint`, §3.2), with float and
   time refused, its tests against `itf`'s round trip.
   **Done** (binding): `{{ toQuint "T" .field }}`, T in Quint's syntax
   (`int`, `str`, `bool`, `Set[T]`, `List[T]`, `K -> V`, tuples,
   records, the model's aliases, parametric ones applied, and a sum's
   alternatives written out, `Some(str) | None`), resolved against the
   model's typedefs (quint typecheck's IR); the field's JSON is decoded by
   it into an `itf.Value` and printed. A record is an object whose keys
   are its fields (exactly, or as Go names them), every one and no
   other; a sum's value is `"Tag"`, `{"Tag": v}`, `{"tag": "Tag",
   "value": v}` (ITF's) or `{"tag": "Tag", ...the payload's fields}` (an
   event as grist writes it), `null` its `None`; a map an object or a
   list of pairs; a nil slice empty. A number with a fraction or an
   exponent (a float recorded whole included: templates now see floats
   as floats, printed truncated as before) and a time string where an int
   goes are refused with the path and the fix (an integer unit); a `str`
   takes any string, a time's text included. `Param.Type` prints the
   full type (it printed `{...}` for a record), and `DefaultTemplate` is
   `toQuint` for a parameter whose type holds a record or a sum, or a
   collection the older functions misrender (a map's non-scalar values),
   so a generated binding needs no template for the read set (`Set[Read]`)
   or a batch (`List[Event]`). Tests: binding's on testdata/typed.qnt
   (every parameter's default, a qobs-recorded run planned to values
   equal to itf's and parsing back with `itf.ParseQuint`, the refusals),
   a random round trip of types, values and encodings
   (`TestToQuintRoundTripAgainstBruteForce`), and validate's
   `TestTypedFieldsChecked` (a conforming run and a mismatch).
3. **An actor filter** for a System's binding (`qtest.Actors(...)`, or a
   binding's `actors:` pattern), so one run checks commit steps and
   client steps with their two bindings (§3.1).
   **Done, decided: a binding field, not a qtest option.** `actors:` is
   a list of globs (`*` any characters, `/` included: an actor's name is
   no path; path.Match's `*`, the first version's, missed `t/x.clients`),
   `!p` excluding what p matches, none meaning every actor
   (`Binding.ChecksActor`, `Binding.Reconstruct`). Which steps a binding is
   about is the binding's to say, and so every reader honours it: qtest,
   `gotogether validate` on a real log that has both sides, qhegel; a
   qtest option would have left the CLI checking the other side. The
   filter applies after reconstruction (validate's `reconstruct`, at
   every check and search): every step reconstructs, so a gap or a settle
   is judged over the whole run (dropping the other side's steps first
   would have made seq gaps of them), and the other actors' traces are
   left out, named with their step counts in an info issue; a binding
   whose patterns match none of a run's actors has no verdict and a
   warning naming them. `StepsToITF` (qtest's artifacts, `gotogether
   itf`) and qhegel's case checker filter the same way. One System has one binding still:
   `System.WithBinding(path, opts...)` is the same System judged by
   another binding (with Linearize for the clients), whose explorations
   run the same seeds and so the same runs, simulated twice (cheap next
   to the checks) instead of one run carrying two reports and claims that
   would need to name their binding. qtest's `TestTwoBindingsOneRun`:
   an account service recording its commits (actor `acct`) and its
   clients' calls (`acct.clients`); both conform for the faithful
   service, a service that lies to its clients is a Mismatch for the
   clients' binding and conforms for the commits', and without the
   filter the commits' binding meets steps without its fields. grist's
   part (slice 2): gristsim records both sides at once, the clients
   under `<tenant>.clients`.

**grist**: sqlstore on qsql (explored like the simstore, claims 1–4
again on it) and on SQLite (`real_test.go`: `grist.Main` on a SQLite file
driven by a Go client over HTTP, validated by the same binding; `blind`
caught there by concurrency alone, at a rate stated in the test); the
HTTP API on `qhttp`, with lost and duplicated requests; `AtMostOnce`,
with the mutant `checkpointFirst` (welcome declared `AtMostOnce` while
the model says at least once): `qtest.Some(qtest.Rejects("welcome"))`,
its checkpoint step before any effect step, and with a crash between
them `qtest.Some(qtest.Violates(validate.EventuallyName("everyCounterWelcomedOrFailed")))`
in a model variant whose welcome is at most once;
the generator (`grist model`) and its diff in CI; gristflow's
unconditioned-rejection and raw-append checks, with analysistest
fixtures of the blind create and dispatchDirect.

**Done (the SQL store, §0.3)**: grist/sqlstore with the contract over
qsql at every level, SQLite and PostgreSQL 16; the relay; gristsim's SQL
tier; examples/counters' claims 1–4 again on it (`TestSQLTier`: claim 2
at Serializable and ReadCommitted, judged at the store and at the
clients from one run; `TestSQLDuplicateRequests`;
`TestSQLFaithfulAtClaim1`, claim 1's contrast, 11 of 20 runs rejecting
with the runtime's condition taken out; `blind` 25 of 40 and
`dispatchDirect` 18 of 30 on the SQL tier) and `TestRealSQLite`, the
real tier on a SQLite file judged by `counters.binding.yaml`, its
clients goroutines calling `App.Execute` through `gristsim.Do` (since
§0.4, which moved the SQL tier's repeated claims to full mode).
**Done (`AtMostOnce`, §0.6)**: the runtime, grist.qnt's defs, examples/counters'
`payout` with its claims and the `checkpointFirst` mutant.
**Done (the API, §0.5)**: `grist.NewHandler` with deny-by-default
policies, `grist/client`, gristsim's HTTP tier (`TestHTTPTier`, claim 2
over qhttp with its network's faults, both sides; `TestHTTPDuplicateRequests`),
`TestRealHTTP` (the real tier over real HTTP), `grist.Main` and
examples/counters' `cmd/counters`. **Done (the generator, §0.9)**:
`grist model` (the app's binary's), `grist new`, grist/gristtest, its
diff in CI (`gristtest.Generated`). **Left**: gristflow, and `blind`
measured on the real tier.

### 8.3 Slice 3: the rest of grain

Periodic tasks (`Every`, `Cron`, ticks, catch-up, a claim of one report
per slot under two schedulers and crashes); upcasting with the tag rule
and `Reindex`; read-model snapshots (§9); gristpure, with the new
map-range rule and a `Facts` export of each pure function for
gristflow; gristhegel; PostgreSQL (row-level security, `lock_timeout`,
the idle timeout, a claim under a real failover); leases (§3.4) and
several nodes; screens and Datastar.

## 9. Gaps a real app hits first

Not designed yet, in the order an application would meet them:

- **Snapshots, before the second app.** Folding the log on every read
  is fine for counters and slow at 10⁵ events. A cache stores a read
  model's state encoded (JSON) with its position, keyed by (name,
  version, tenant, key), decoding a fresh value per read so a fold that
  mutates in place is safe; the model is unchanged.
- **Schema evolution beyond upcasting**: splitting an event, merging
  kinds, a field whose meaning changed: a migration appending new
  events, a read-model version bump, and an answer for processors that
  handled the old ones.
- **Cross-tenant queries** (an operator's report): never in a decision;
  a read-only loop over `Tenants`, or an export, outside the model.
- **Per-entity timers and sagas** ("cancel if unpaid in 24 h"): a
  `timer-set{due}` event and a processor polling due timers, firing a
  `Dispatch` keyed by (timer, due) so it fires once.
- **Inbound webhooks**: the command id is the provider's event id,
  namespaced (`stripe:evt_...`), so §2.4's marker dedupes redeliveries.
- **Erasure**: §2.10's retention shape, or crypto-shredding.
- **Operations**: each processor's lag (head minus checkpoint, per
  tenant) and oldest unhandled event's age as qrole metrics;
  `grist.failed` events as a dead-letter list (`grist events -type
  grist.failed`), with a command to re-run one after a fix.

## 10. Open questions, with defaults

Each default is the review's recommendation; **default, the maintainer
may override** each.

1. **Read-set conditions by default** (default, the maintainer may
   override). Contention is handled by the reads: keyed reads (`For`),
   existence reads (`Exists`) and uniqueness tags make a decision
   conflict only with writes to what it read. A per-command declaration
   with no default would bring grain's race back.
2. **Checkpoints in a table** (default, the maintainer may override): a
   row per (tenant, processor), compare-and-set in the processor's
   transaction, its position in every processor step. Ticks are written
   only when a periodic task appends or runs an effect. The log stays
   the application's events.
3. **`EventRef{Tenant, Pos}`, never renumbered** (default, the maintainer
   may override); retention deletes, never renumbers. A global id
   (grain's UUIDv7) waits for export or cross-tenant references.
4. **The log model only** (default, the maintainer may override; measured
   in slice 1: one tenant's run judged in 7 s at 203 steps and 35 s at
   1003, a 4-core machine, the explorer's determinism rerun included;
   the simulation itself 0.03 s and 0.35 s; examples/counters'
   `TestValidationCost`. Re-measured after slice 1's review on a loaded
   4-core machine, both trees side by side: before, 5.1 s at 203 steps and
   30.7 s at 1003; after, 6.0 s at 214 and 25.3 s at 999, `uniqueNames`
   linear in the log where it compared every pair of creates; 5.3 s and
   18.2 s without the eventual val, which the validator judges in a
   replay of its own: judging it at the end of the fused replay is a core
   item, docs/ROADMAP.md. The generated text was left as it is: its
   per-step `batchIs` expects are what the model compares): read
   models as defs over the log, validation time measured at 200 and 1000
   steps per tenant (slice 1's explorations and a long SQLite run)
   before any variant keeping folds as variables (a mechanism-level model
   with a Hegel-tested lemma, the gotogether-patterns way).
5. **"grist" is a codename** (default, the maintainer may override): it
   also names gristlabs' spreadsheet (grist-core); renamed before the
   module's first tag.

## 11. Rejected, and why

- **hallgren/eventsourcing's store, or goes, or eventhorizon as the
  core**: aggregate- and version-based concurrency, the wrong boundary
  for a decision over read models; owning a small interface costs less
  than adapting one.
- **Watermill for processors**: a second delivery semantics to model;
  the log, read from a checkpoint, is the bus.
- **Temporal, Restate, DBOS, River for processors and periodic tasks**:
  each brings its own scheduler, clock or server, which qenv cannot
  simulate; River's transactional enqueue is what `Dispatch` gives.
- **Optimistic version checks per stream**: streams bring back
  aggregates; a condition is a set of queries.
- **Advisory locks for the tenant's serialization**: invisible to the
  simulation's Store and to qhistory; the head row's update is a lock
  both see.
- **A database sequence for positions**: drawn before commit, so not in
  commit order (§2.10).
- **Checkpoints as events in the log** (the first version of this note,
  and grain's): one mechanism, but the log grows by a checkpoint per
  batch per processor, and a processor's read of its checkpoint joined
  every condition on the log; a row's compare-and-set is the same
  guarantee (§10.2).
- **One model instance for every tenant** (`log: str -> List[Event]`,
  the first version of this note): each tenant's trace is independent,
  so an instance per tenant is smaller and its order exact; isolation is
  the store's contract, tested where it is implemented.
- **Annotations (`//quint:action`) on grist's blocks**: the framework
  calls every block, so it records them; an annotation could only
  disagree.
- **Allium or prose specs linked by name, a REPL for agents** (grain's):
  a spec gotogether cannot run is documentation; the REPL's tools are
  CLI subcommands.

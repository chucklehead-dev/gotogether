# grist: an event-sourced application framework checked by its model

Status: design, nothing built. Decisions taken 2026-10-27 (a module
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
- **qsql already has `ORDER BY pk [ASC|DESC]` and `LIMIT`.** It lacks
  keys of several columns (42P16, "qsql keys rows by one column": the
  first note's tables were keyed by two and three), ranges, `RETURNING`
  and `DO UPDATE`; its isolation is store-wide (§8.2).
- **`qenv.FaultPoint` has one crash option**, `crash`: the effect's two
  crash points are a `DeclareFaults` kind and `Sim.FaultOf` (§3.5).
- **qobs encodes structs with `fmt.Sprint`**, not JSON, and binding's
  `jsonElem` renders objects as quoted strings and floats truncated:
  grist encodes events itself, and binding needs a typed function (§3.2).
- **One System has one binding, and qtest has no actor filter**: a
  run records commit steps or client steps, not both (§3.1).
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

Decided differently from the review: rejections are not remembered
under their idempotency key (§2.4); `leakTenant` is dropped, tenant
isolation being a contract-suite test with a mutant store (§3.3).

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
(`grist.Keys("counter")`, or several: `grist.Keys("account", "currency")`
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
- `r.Events(q)`: a raw query, `grist.Query{Types, Tags, Reverse,
  Limit}` (newest first with a limit is the "latest of" read grain has).

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
again (5 tries by default, then `grist.ErrConflict`, a 409 at the API).
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

The other consistency options: `grist.Within(Totals)` narrows the
condition to some of the reads (the rest are advisory, the option's
reason says why); `grist.Also(q)` adds a query the decision did not read.
No option widens beyond the tenant: there are no cross-tenant
transactions, as in grain.

**Transient errors are not failed conditions.** A lost race for the
tenant's lock (the simstore's ErrConflict under Snapshot or
Serializable; SQLSTATE 40001 and 40P01 on PostgreSQL; SQLITE_BUSY after
`busy_timeout`) and a commit of unknown outcome are retried from the
idempotency check (below), with backoff in virtual time, on a budget of
their own (10 by default), and never count as one of the 5 condition
tries. Only `*grist.ConditionFailed` (an event matching the read set
after P) means "decide again".

**Rejections and errors.** A decision returns either events or an
error. A `grist.Rejection` (a named, expected refusal) is a result:
recorded as the step's outcome, a 422 with its name at the API. Any
other error is a fault of the code (a 500), recorded with its text.

**Idempotency.** Every command carries an envelope (`grist.Envelope`:
command id, tenant, actor, time), the id chosen by the caller (the API
requires it as `Idempotency-Key`). Each committed batch writes a marker
keyed by the command id, holding the hash of the command's canonical
JSON and the command's result. Every try, the first and each retry,
begins by reading the marker (a keyed read, so the marker is in the read
set and the append's condition covers "no marker with this id"):

- a marker with the same hash: the first attempt committed (a lost
  response, the case payd's dedupe is for); its result is returned and
  nothing is appended or recorded;
- a marker with another hash: a 422 `idempotency-key-reused`, recorded
  as a rejection;
- no marker: decide.

**A command's result** is what its marker holds: the batch's last
position and an `EventRef{Tenant, Pos}` per event (§10.3), never a value
the decision computes, so the marker needs nothing of the application
and a retry's answer is the first attempt's exactly. **Rejections are
not remembered**: a refusal commits nothing (remembering it would turn
every refusal into a contended append on the tenant's lock), so a retry
of a refused command decides again on fresh reads, and may succeed if
the state changed. A key names at most one committed outcome. Event ids a
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
| `grist.Effect(f, grist.AtMostOnce)` | the checkpoint first, then f, then the outcome event | at most once outside the log |

`Dispatch` decides at the head, not at T, because a command's rule is
about the state it commits into (a welcome command must not create a
second account just because the trigger is old); a failed condition is
retried as a command's is, under the idempotency key (processor,
tenant, T), so a crash and a retry dispatch once. `grist.Skip()`
checkpoints and does nothing.

**Errors.** A handler's or an effect's error is retried with backoff (3
tries by default, in virtual time in a simulation), then checkpointed
with a `grist.failed` event naming the trigger, so a poison event stops
nothing and is visible (grain checkpoints a failed effect silently and
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
aggregates: its condition is a set of queries through a position, its
order one sequence per tenant, and a batch carries a marker and a
checkpoint. The interface:

```go
// proposed
type Store interface {
	CreateTenant(ctx context.Context, tenant string) error // its head, at 0
	Tenants(ctx context.Context) ([]string, error)
	// Read: events matching q through at (0: the head, returned), in position
	// order or newest first (q.Reverse), at most q.Limit.
	Read(ctx context.Context, tenant string, q grist.Query, at grist.Position) ([]grist.Stored, grist.Position, error)
	// Append commits the batch at the next positions if its condition holds and
	// its checkpoint is as expected, with its marker; *grist.ConditionFailed
	// names the query and event that broke it, anything else is transient.
	Append(ctx context.Context, tenant string, b grist.Batch) (grist.Position, error)
	Heads(ctx context.Context) (map[string]grist.Position, error) // every tenant's: a change feed's poll
	Wait(ctx context.Context, tenant string, p grist.Position) error
}
```

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
markers, reverse and limit, and **tenant isolation**: a read of t1 never
returns t2's events, an append to t1 never moves t2's head. A mutant
store that ignores the tenant in `Read` fails the suite, so the test is
shown to see it.

- **memstore**: maps and a mutex, for unit tests of a fold or a
  decision. Not for simulations (a real mutex).
- **simstore** (slice 1): over `qenv.Store`. Keys `t/<tenant>/head`,
  `t/<tenant>/e/<position, 20 digits>`, index keys
  `t/<tenant>/i/<type>/<position>` and
  `t/<tenant>/g/<type>/<key>=<value>/<position>`, markers
  `t/<tenant>/m/<command id>`, checkpoints `t/<tenant>/c/<processor>`.
  An append takes the head with `GetForUpdate` (the tenant's lock),
  checks the condition with `Scan` over each query's index prefix,
  writes, and commits; the step is recorded in `Txn.OnCommit`, whose
  hooks run before `ErrUnknownOutcome` is returned, so a commit whose
  answer is lost is recorded. The head lock serializes a tenant's
  appends; under Snapshot and Serializable the loser of a race gets
  `ErrConflict` from `GetForUpdate`, transient (§2.4). The Store's
  checker (`GOTOGETHER_CHECK=1`) and fault kinds apply. **Cost**: `Scan`
  walks every row of the store (no range), and a Serializable commit
  walks them again per scanned prefix: an append is O(rows in the store),
  every tenant's included. Fine for explorations of hundreds of events;
  a ranged `Scan` in `qenv.Store` is the fix if §10.4's measurements say
  so.
- **sqlstore** (slice 2): `database/sql`, two dialects, the driver the
  application's (pgx's stdlib, modernc.org/sqlite: no cgo). Tables
  `grist_heads(tenant, head)`, `grist_events(tenant, pos, type, version,
  at, meta, body)`, `grist_index(tenant, k, pos)` (k a type or a
  type/tag=value), `grist_markers(tenant, id, hash, result)`,
  `grist_checkpoints(tenant, processor, pos)`. An append's **first
  statement** is `UPDATE grist_heads SET head = head + $1 WHERE tenant =
  $2 RETURNING head`: it takes the row's lock and reads the new head in
  one round trip (no advisory locks, which are invisible to the
  simulation and to `qhistory`). Then the conditions (`SELECT pos FROM
  grist_index WHERE tenant = $1 AND k = $2 AND pos > $3 ORDER BY pos
  LIMIT 1`), then the rows.
  - **PostgreSQL**: `BeginTx` with `sql.LevelReadCommitted` stated, not
    the server's default: after the lock every statement sees every
    commit before it, which the condition check needs. 40001 and 40P01
    are transient (§2.4), never failed conditions. Each transaction
    sets `lock_timeout` (5 s, grain's) so a stuck holder is an error,
    not a hang, and the pool's connections set
    `idle_in_transaction_session_timeout` so a crashed node's open
    transaction releases the tenant's lock. Row-level security (`SET
    LOCAL grist.tenant`) is the second fence.
  - **SQLite**: one writer for the whole database, whatever the tenant,
    so tenants do not append in parallel. `_txlock=immediate` (`BEGIN
    IMMEDIATE`: the write lock before the first read, so the deferred
    upgrade that fails with SQLITE_BUSY cannot happen), WAL (readers
    do not block the writer), `busy_timeout` (a writer waits for the
    lock instead of failing at once). `RETURNING` needs SQLite 3.35.
  - **Change feeds**: processors and screens learn of appends by
    polling `grist_heads` (`Heads`), one query for every tenant, every
    250 ms; `LISTEN/NOTIFY` is an optimisation for later.
- **sqlstore on qsql** (slice 2): the same code on `qsql.Open` over a
  `qenv.Store`, so the SQL tier is explored with qsql's anomalies,
  SQLSTATEs and commit faults. It needs qsql extended first (§8.2), and
  a `qsql` dialect that leaves out the `SET` statements qsql does not
  parse.

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
- a step that reads through P and commits nothing (a query, a
  rejection): **2·P+1**, after the commit of P and before the commit of
  P+1, concurrent only with other reads through P, which commute;
- a processor step that commits only its checkpoint (`Skip`, an
  effect's checkpoint, a `failed` with no event): **2·H+1**, H the head
  its own transaction read, not its trigger T: a poller that emitted at
  7 for trigger 5 then checkpoints trigger 6 records 14 then at least
  15, in its program order (with T, 13: a cycle with its thread);
- an effect step (`<processor>.effect`): 2·H+1, H the head the worker
  read before running it;
- in an `Emit` batch of several triggers, each trigger's step gets 2·
  the last position of its own events, or, if it emitted none, 2·(the
  last position before it in the batch)+1: one step per trigger, in
  trigger order, inside one transaction.

Each processor's steps also carry `quint.order.p.<processor>` = T,
ordering them among themselves across nodes and incarnations. Both
domains agree with program order (a task's steps have increasing
positions), so `qtrace.build` finds no cycle, and the steps of a
simulation, a SQLite file or a fleet of PostgreSQL nodes reconstruct to
one order without trusting a clock.

| step action | recorded | arguments | observations |
| --- | --- | --- | --- |
| the command's name | in the append's commit hook (`Txn.OnCommit`, qsql's, the sqlstore's post-commit) | the command's fields, command id, the read set (`read.pos`, `read.queries`) | the events appended, the head; the result |
| the command's name, with outcome | on return, when refused | as above | the rejection's name, or `conflict` after the tries |
| the query's name | on return | the query's fields | the result; the position read through |
| `<processor>` | at its commit | the trigger's position and type | the events appended, the checkpoint |
| `<processor>.effect` | grist's wrapper, after f returns, before the checkpoint | the effect key | f's outcome |
| `<periodic>` | at its commit (slice 3) | slot | the events appended |

A rejection is recorded on return because nothing committed; a commit
at the commit, because the caller may never be told (an 08006, a crash
between commit and response), and the model must see it. A retried
command that finds its marker records nothing: its effect was recorded
once. **A raw append** (application code calling the store, which
gristflow flags, §4.1) records nothing: the model's log then lacks
events the store holds, and the first later step whose recorded head or
events the model computes otherwise is a Mismatch: a raw append is
caught by the next step of its tenant, not where it happened.

**The read set is a step argument**: `read.pos` (P) and `read.queries`
(a list of (type, tag key, tag value) triples; key "" for a type-only
query), so grist.qnt's conditional-append property (§3.3) is checked
against what the decision read, not assumed. Without them the property
would hold vacuously.

**Client steps.** gristsim's workload (§3.6) can instead record each
command and query where the client sees it: `qobs.Invoke` before the
request, `Call.Done` with the answer, the idempotency key as `id`, every
retry of a lost or late response inside the one call; checked with
`qtest.Linearize()`, a duplicate a retry created shows as a later read's
Mismatch, a call never answered as `Pending` (`qtest.Completes()`).
Every binding sees every actor of a run, so a run records one side
(`gristsim.Config.Record`: `AtCommits`, the default, or `AtClients`,
commands and queries only), the client side checked by a second binding
of the same model (`counters.calls.binding.yaml`: no positions, no
processors' eventual vals). Both sides in one run need an actor filter
in qtest (§8.2).

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
`qtest.Optional`, so `GOTOGETHER_STRICT=1` fails on them.

### 3.3 grist's guarantees as a Quint module

`grist/model/grist.qnt`, vendored into an application's `model/` (as
examples/twophase vendors choreo.qnt), states what the framework
promises, in the per-tenant state above:

- **Positions**: dense, increasing, commit order (a commit's recorded
  head is `log.length()` after it).
- **Conditional append**: `conditionHolds(log, readPos, reads)`, no
  event after `readPos` matching a recorded query; part of every
  conditioned commit's precondition, with the read set from the step.
- **Processor reads**: a handler's action evaluates its reads on
  `log.slice(0, trigger)`, so the model states the trigger-position
  rule rather than assuming it; `Dispatch` evaluates the command's
  action on the current log, with the command's precondition.
- **Checkpoint monotonicity**: a processor's checkpoint never moves back
  and never passes an event it has not handled: exactly one handling
  step per subscribed event (`Emit`, `Dispatch`, `Skip`, `failed`).
- **Effects**: `AtLeastOnce` admits one or more effect steps per trigger,
  its checkpoint step only after one, and **eventually** (after a settle) each trigger has an ok effect step
  or a `grist.failed`; `AtMostOnce` admits zero or one, and **an outcome
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

This module is checked once, in grist's own tests, against grist's own
runtime, so an application's failure is its own or its model's, not the
framework's.

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
The crash that duplicates an at-least-once effect is then one named
option, which a `qenv.Script` asks for and `Exhaust` reaches with one
fault.

### 3.6 gristsim.Run, and settling

`gristsim.Run(c qenv.Chooser, app, cfg)` builds a simulation from c, a
simstore (or, slice 2, a qsql-backed sqlstore) at `cfg.Isolation`, n
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
a workload loop. Clients send through `qobs.Invoke`/`Call.Done` under
`AtClients` (§3.1), and retry a lost response with the same key either
way.

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
binding. Commits are recorded at the commit and ordered by the domain
clock across nodes, so a killed node loses no committed step; an effect
step lost between f and its record is one the model allows to be unseen.
`qnemesis.FromScript` replays a simulation's fault script against it.

## 4. Constraints enforced

### 4.1 Analyzers (qanalysis, `quintvet`)

Two analyzers in qanalysis (it has x/tools and the golangci plugin),
recognizing grist's constructors by import path and type:

- **gristflow** (slice 2): from each decision's and handler's returns,
  the event types it can emit, the read models it reads, the commands
  it dispatches; written to `grist.topology` and compared at boot
  (§2.9). It reports **a decision that rejects under a condition on a
  read its append is not conditioned on** (`Unconditioned`, or a
  `Within` leaving the read out): "create-counter refuses ErrNameTaken
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
tenant; `grist.Internal()` marks one no adapter serves (only processors
dispatch it); boot refuses a block with neither. The check is in the
adapters (as grain's), so a `Dispatch` is not subject to a user's roles.
A denial is a refused step (`outcome: denied`).

## 5. The API layer and Datastar

**HTTP/JSON** (`grist/api`, slice 2, `net/http` only): `POST
/t/{tenant}/commands/{name}` with `Idempotency-Key`: 200 with the
marker's result, 422 with a rejection's name (or
`idempotency-key-reused`), 409 on conflict after the tries, 403 when
denied, 503 with `Retry-After` when the tenant's lease is elsewhere.
`GET /t/{tenant}/queries/{name}` returns the result and the position
read through; `GET /catalog`, behind `grist.DevEndpoints`, the registry.
Authentication is the application's middleware (`grist.WithActor`). A
plain `http.Handler`, so `qhttp.NewServer` serves it in a simulation.

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
change). The CLI (`grist`, `cmd/grist`, which alone links the generator,
binding and validate): `catalog`, `verify`, `model`, `topology -w`,
`invoke`, `query`, `events`, `fold`, against a memstore or a SQLite
file. `gotogether-start`'s table gets a row.

## 7. Module layout

```text
grist/                 module github.com/chucklehead-dev/gotogether/grist (go.mod; published like qhegel)
  grist.go             App, events, read models, commands, queries, processors, periodic, Reads, Result
  runtime/             the node: pollers, effect worker, scheduler, dispatcher (qenv seams only)
  store/               Store, Query, Batch, Position, EventRef; storetest (the contract suite)
  memstore/ simstore/ sqlstore/
  api/                 HTTP/JSON adapter
  screen/              Datastar adapter (the only datastar-go import)
  gristsim/            Run(chooser, app, cfg): nodes, store, workload, settles, probes
  gristtest/           ShrinkCommands, stub reports, storetest helpers (imports qtest)
  model/               grist.qnt, leases.qnt, the generator's templates
  cmd/grist/           the CLI and the generator (imports binding, validate)
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
2. **The safe default passes, through every fault.** The faithful app
   at each of the simstore's three isolation levels, with store commit
   faults and node crashes: `qtest.Conforms()`,
   `qtest.All(qtest.Settles())`, and
   `qtest.Reaches("store.counters.commit").Faulted()` so the faults
   happened. The same under `AtClients` with `qtest.Linearize()`.
3. **The duplicated welcome is the declared guarantee's, seen and
   judged.** A `qenv.Script` asking for `grist.welcome.effect`'s
   `crashAfter` (and `Exhaust` with one fault reaching it): the
   restarted node sends the welcome again under the same effect key. The
   model admits it (`qtest.Conforms()`); a test-only model variant with
   the invariant `welcomedOnce` (one ok effect step per trigger) rejects
   exactly those runs: `qtest.Some(qtest.Violates("welcomedOnce"))`.
4. **grain's processor duplicate is caught.** `dispatchDirect`, with and
   without crashes: `qtest.Some(qtest.Mismatch("average"))`: the
   processor's step records no events where the model's action appends
   one, and the raw append shifts every later head (§3.1). The faithful
   `Emit` never does (claim 2).
5. **The store keeps its contract.** `storetest` over memstore and
   simstore passes; over the mutant store that ignores tenants, it
   fails, naming the isolation check.

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
2. **The typed template function** (`toQuint`, §3.2), with float and
   time refused, its tests against `itf`'s round trip.
3. **An actor filter** for a System's binding (`qtest.Actors(...)`, or a
   binding's `actors:` pattern), so one run checks commit steps and
   client steps with their two bindings (§3.1).

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
4. **The log model only** (default, the maintainer may override): read
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

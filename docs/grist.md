# grist: an event-sourced application framework checked by its model

Status: slices 1 and 2 built (grist/, the SQL store, the HTTP API,
`grist/client`, `grist.Main`, `AtMostOnce`), the generator, `grist new`
and the test harnesses (GR2), GR3's declared calls (simulated and on
the real tier over HTTP) and GR10's slice 1 (identity, probes, the
drain) and slice 2 (graceful termination, the lagging router,
`gristsim.Cluster`); gristflow is design, as is slice 3. What building each slice and
each review changed here is in docs/HISTORY.md ("grist's build notes",
§0 to §0.16, cited below as §0.x). Decisions taken 2026-10-27 (a module
inside this repository, event sourcing first, Quint and Choreo as the
specification languages, an API layer and Datastar, the G track goes on
alongside), revised the same day after a review (§0). This note says
what to build and in what order (§8: three slices); its Go parses, and
every name it takes from an existing package exists today. "grist" is a
codename (§10.5).

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
command id, tenant, caller, time), the id chosen by the caller (the API
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

### 2.4.1 Rules (P1)

A rule is a requirement on every append of an event, whichever command
makes it ("no deposit until KYC cleared", "at most three loans"): stated
once, beside the event, instead of in each decision that can append it.

```go
var ErrLimit = grist.Rejection("limit")

var LoanLimit = grist.NewRule(App, "loan-limit", Borrowed, func(r *grist.Reads, e BookBorrowed) error {
	if len(Loans.For(r, e.Member)) >= Limit {
		return ErrLimit
	}
	return nil
}, grist.Refuses(ErrLimit))

var BorrowCmd = grist.NewCommand(App, "borrow", decideBorrow, grist.Appends(Borrowed),
	grist.Refuses(ErrNotMember, ErrUnavailable), grist.Authorize(grist.Authenticated))
```

- **When it runs.** After a command's decision returns events, before
  the append, in the same try: each event of the batch in order, an
  event's rules in registration order. The first refusal wins and the
  rules after it do not run. A decision's own refusal comes first (no
  rule runs on a refused decision).
- **Its reads are the command's.** It reads through the command's
  `Reads`, pinned at the same position, and its reads join the read set:
  a concurrent change to what it read fails the condition, and the
  command decides again, its rules with it. `Unconditioned` makes them
  advisory with the decision's.
- **Its refusal is the command's.** A `grist.Rejection` it returns is
  the command's refusal: the step's outcome, the key's committed answer.
  It must be one the rule declares (`Refuses`, required); another, or
  an error that is no rejection, is the code's error. The catalog adds
  the rule's refusals to each command it guards (`CatalogBlock.Refuses`,
  `Rules`, `RuleRefuses`; `Catalog.Rules`), so the bindings have the
  outcome and the catalog's `Exercised` wants it, with no edit to the
  command's `Refuses`.
- **Which commands it guards.** Those declaring its event in
  `grist.Appends`: a command that appends an event a rule guards without
  declaring it, or appends a type its `Appends` lacks, is the code's
  error at that append. `Verify` refuses a rule on an event no block
  appends (no command's `Appends`, no processor's `Emits` or `Delivers`
  success): a rule that can never run.
- **Processors are not guarded yet.** A processor's `Dispatch` runs a
  command, its rules with it. A processor whose `Emit` (or an effect's
  `OnSuccess`) a rule refuses fails its trigger (tried, then
  `grist.failed`), its step's error naming the rule: block-until for
  processors is later work.

In the model, each rule is two holes in `model/<app>_rules.qnt`:
`<rule>Refusal(log, e): str` (`""` admits `e`, else the refusal) and
`<rule>Needs(e, refusal): Set[Read]`. The generator guards each guarded
command's action with `<command>Ruled` (its decision's outcome, then,
when ok, the first refusal of `rulesOf` over `<command>Events`), and
requires the step's read set to cover every rule's needs that ran, as
it does the decision's. gristtest's `Mutants{App: App}` adds a generated
mutant per rule, `drop-<rule>` (the app with the rule dropped, no
overlay), and `DroppedRule(App)` the claim that catches it: some run
rejects a command it guards.

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

**What regenerating cannot see** is the app's own rules: a fold over a
read model's selection still typechecks when the read model starts
folding a new event type (its `else` branch skips the type silently), and
a rule reading an event still typechecks when the event gains a field.
So the generator compares the events file it is about to write with the
one already there and warns (`<app> model: warning: ...`; in
`gristtest.Generated`'s failure, and logged when `GRIST_UPDATE=1`
regenerates) of each read model folding a type it did not, or no longer
folding one, and of each event whose fields changed, with the read
models folding it.

**The model's files** import each other one way only, each the ones
below it, since quint's REPL refuses a file that a file it imports
imports back (QNT101; `quint typecheck` and `run` accept it):
`model/<app>.qnt` (the app's invariants, modules `<app>` and
`<app>Calls`, which the bindings name), `model/<app>_steps.qnt`
(generated: `<app>Gen`, `<app>CallsGen`), `model/<app>_rules.qnt` (the
app's holes, `<app>Rules`), `model/<app>_gen.qnt` (generated:
`<app>Events`), `model/grist.qnt`. Generate refuses a cycle, and names
the move out of the layout before this one, which had the rules in
`model/<app>.qnt` (docs/HISTORY.md, the acyclic model layout).

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
  each trigger has an ok effect step or a `grist.failed` (generated,
  §3.3.1); `AtMostOnce` admits zero or one, and **an outcome
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

### 3.3.1 Obligations, generated from the catalog (P7)

What the catalog declares a block will do eventually is an eventual val
the generator writes into module `<app>Gen` (`model/<app>_steps.qnt`),
named from the processor or call, and the store's default binding judges
at a settle beside the app's own eventual vals. So does a variant that
judges every invariant of the default binding, a call's obligation only
where the variant also judges a val reading the call's served records
(`<call>Records`, a site claim, directly or through the model's defs);
one that judges only its own vals does not:

| block | obligation | meaning, after a settle |
| --- | --- | --- |
| an `Emits` processor | `<p>EventuallyHandled` | its checkpoint passed every trigger: the events it emitted, a Skip's none or a Fail's `grist.failed` committed with it |
| an `AtLeastOnce` effect | `<p>EventuallyHandled` | its checkpoint passed every trigger, and the trigger was skipped (`<p>Skips`), had an ok effect step, or failed every try (its `grist.failed`) |
| an `AtMostOnce` effect | `<p>EventuallyHandled` | its checkpoint passed every trigger |
| a `Keyed` call a processor `Uses` | `<call>EventuallyServed` | every call its caller saw answered (ok or a declared refusal) has the dependency's served record of its key; an unknown answer promises nothing |

**What legitimately prevents progress is no violation.** At most once
the checkpoint commits first, and a crash after it loses the effect, or
after the effect its outcome, by right (§0.6): no outcome is promised,
so the obligation is the checkpoint's, and an outcome logged stays a
test-only claim (`atMostOnceOutcomesLogged`, counters' `exactly`). A
call whose answer was lost (unknown) may never have reached its
dependency: nothing is promised of it, which a lost request, a lost
answer and a crash during the call all leave. An unkeyed call has no
obligation: its dependency need keep nothing per key, and a third
party's records no site (counters' mailer on the real tier). And the
obligations are a live system's: a settle is judged only when nothing
but daemons is at work, and a node down for good is no settled system.
gristsim's supervisor restarts every node (none stays down), the real
tier's `Settle` waits for every processor to reach its last trigger and
fails the test instead of recording a settle that did not happen, and a
run whose settle finds work pending is `Unsettled`, which
`qtest.Settles()` judges, not a violation. A simulation of one's own
that leaves a node down for good (one outside `SettleCore`'s core, a
process not restarted) must not record its settle as one: qenv counts no
task of a down process as pending, so its triggers would be judged.

A hand-written eventual val that is exactly an obligation is removed (a
val of a generated name is the generator's error): counters'
`everyCounterWelcomedOrFailed` and `everyRetirementHandled`,
wallet-grist's `allPosted`, `grist new`'s `everyAdditionTallied`. One
that says more stays: library's `everyBorrowReceipted` (the mailer's ok
record of every borrow), counters' `everyPaymentAnswerServed` (the
served record equal to the caller's, field for field) and the test-only
exactly-once vals. A processor whose handler never returns (its poller
waiting as it waits for appends, a daemon, so the run settles) violates
its obligation, and a dependency answering a Keyed call it never
applied violates the call's (gristsim's `TestObligations`); nothing
else in the model says a trigger must be handled. A name of an
obligation defined by the rules (`model/<app>_rules.qnt`, which the steps
module imports) is the generator's error too: quint would refuse it in
the generated file. examples/wallet-grist's `ledgerAcksUnposted` is the
walkthrough: a proxy acknowledging every post and forwarding none takes
only steps the actions admit, and only `ledgerPostEventuallyServed`
catches it.

**Limits, as reviewed.** The processor obligations reduce to the
checkpoint over the model's reachable states (its checkpoint and skip
actions already require the outcome), so what they add is liveness: a
processor that stops short of a trigger. A handler's `Fail` in a
`Delivers` processor is a Mismatch of its skip (above), so the
at-least-once `grist.failed` is an effect's that failed every try. A
poller is a daemon whether it waits for appends or holds a trigger, so
a settle does not wait for work in hand: its duration must cover it
(gristsim's minute, against the backoff's 320 ms cap and
`DependencyTimeout`'s second; an effect whose tries outlast it would be
judged mid-retry; ROADMAP's GR10b would mark a poller a daemon only
while it waits on the store). A node left down for good outside
`SettleCore`'s core has its triggers judged by a plain `Settle`, which
names no down process (ROADMAP's GR10c). A variant judges a call's
obligation only where one of its own vals reads the call's served
records (H4, `TestCallObligationsFollowSiteClaims`): a `real` variant
holds only what the real tier can judge (docs/grist-instrumentation.md
§5, decision 4), and a `Keyed` dependency recording no site there (a
third party's, behind `OverHTTP`) would violate its
`<call>EventuallyServed` in every run. counters' `real` keeps
`bankPayEventuallyServed` (it judges `everyPaymentAnswerServed`, the
bank's records: the bank runs behind `ServeCalls`); a variant judging no
site claim of a call has none of its obligation.

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

Its signature is a `qtest.System`'s `Run`, and `gristsim.System` is
the System (with the mutant the binary was built with, `qtest.Mutant`):

```go
// an application's test
func TestFaithful(t *testing.T) {
	gristsim.System("counters.binding.yaml", App, gristsim.Config{}).WithRates(qenv.DefaultRates.Set("nemesis", 0.02)).
		Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 3}).Expect(t, qtest.Conforms())
}
```

The workload defaults to clients sending random commands of the
registry, arguments drawn from the command's type by `gristsim.Gen`
(strings from a small alphabet so names collide, which is what finds
races; integers from -1 to 3, `Config.Ints`, so zero and a negative,
which validations refuse, are drawn too); an application overrides one
command's generator, never writes a workload loop. Clients send through `qobs.Invoke`/`Call.Done` (§3.1),
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
`grist.Authorize(p)`, a predicate over the envelope's caller and the
tenant (`grist.Policy`; `grist.Authenticated`, `grist.Callers(...)`);
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
middleware (`grist.WithCaller`; `grist.Middleware`, or
`grist.CallerFromHeader` behind a proxy). A plain `http.Handler`, so
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
  model/               grist.qnt (vendored by the generator, grist/internal/gen's, run by Main's model command)
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

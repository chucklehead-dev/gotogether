# grist: an event-sourced application framework checked by its model

Status: design, nothing built. Decisions taken 2026-10-27 (a module
inside this repository, event sourcing first, Quint and Choreo as the
specification languages, an API layer and Datastar, the G track goes on
alongside). This note says what to build and in what order; the Go
below is **proposed API** of packages that do not exist yet (`grist`,
`grist/simstore`, ...), written so that it parses. Every name it takes
from an existing package (`qenv`, `qobs`, `qtest`, ...) exists today.

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

Both were read in grain's code (scratchpad research, 2026-10-27), and
the spike (§8) reproduces both before it fixes them.

1. **The unique-name race.** grain's own example `create-counter`
   checks that no counter has the name by projecting a read model, then
   appends with no `:cas`. Two concurrent creates both see no counter
   and both append: the invariant its spec claims (`UniqueCounterName`)
   is false, and nothing in grain can find it (no concurrent test, no
   exploration). In grain the safe version is opt-in (a hand-written CAS
   query that must cover what the handler read); the unsafe one is the
   short one.
2. **The duplicated effect.** grain's example processor calls
   `process-command` directly and returns `{}`: the command's events and
   the processor's checkpoint are two appends, and a crash between them
   runs the command again on restart. grain's spec accepts duplicates,
   which is how the gap was papered over.

grist's answers, which shape everything below: **an append is
conditioned on everything its decision read, unless the code says
otherwise in words**; and **a processor's own appends carry its
checkpoint in the same transaction**, so the only duplicates left are
external effects, whose delivery guarantee is declared, recorded and
judged.

## 2. The blocks

Registration is typed generic Go: package-level values built by
constructors on an `*grist.App`, no code generation for the runtime
(grain's macros become constructors; their def-site facts become what
the analyzer reads, §4). Code generation is for the model and the
binding only (§3.2), from the registry.

### 2.1 Events

An event is a Go struct registered under a stable name and a version.
The name, not the Go type's, is what is stored, so a type can be
renamed. Tags (grain's `#{[entity-type uuid]}`) are fields marked
`grist:"tag"`; the tag's name is the field's JSON name. The schema is
the type: JSON encoding through `encoding/json`, a JSON Schema derived
by reflection for the catalog and the API, and a schema hash per
(name, version) kept in `grist.lock` (checked in).

```go
// proposed: package counters, an application
type CounterCreated struct {
	Counter string `json:"counter" grist:"tag"`
	Name    string `json:"name"`
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

**Versioning.** A change to a registered (name, version)'s schema hash
fails `grist verify` and boot: "counter-created v1 changed (field name
renamed to title): register v2 and an upcaster". Old versions stay
registered as their own types with an upcaster to the next version:

```go
// proposed
var CreatedV2 = grist.NewEvent[CounterCreatedV2](App, "counter-created", grist.Version(2),
	grist.UpcastFrom(Created, func(v1 CounterCreated) CounterCreatedV2 {
		return CounterCreatedV2{Counter: v1.Counter, Title: v1.Name}
	}))
```

Reads upcast stored events to the latest version before any fold or
handler sees them; appends write the latest version only. Upcasters are
pure (§4). A stored event's envelope (`grist.Meta`) is the store's:
tenant, position, time, the command or trigger that caused it, and the
transaction it belongs to.

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

var Totals = grist.NewKeyedReadModel(App, "totals", 1, "counter", func() int { return 0 },
	grist.On(Incremented, func(s int, e CounterIncremented, m grist.Meta) int { return s + e.By }))
```

A plain read model folds the tenant's events of its types; a keyed one
(`NewKeyedReadModel`, keyed by a tag) folds only the events carrying one
value of that tag, so a decision about counter c reads, and is
conditioned on, counter c's events alone. The version is the cache's:
bump it when the fold changes. The spike folds from the log on every
read (no cache). The cache, when it comes, stores snapshots encoded, as
JSON with the position they fold through, and decodes a fresh value per
read, so a fold that mutates its state in place (a map, as above) is
safe and no state is shared between two decisions.

### 2.3 Commands

A command is a struct (its arguments) and a decision: **reads and a
command in, events or an error out**.

```go
// proposed
type CreateCounter struct {
	Counter string `json:"counter"`
	Name    string `json:"name"`
}

var ErrNameTaken = grist.Rejection("name-taken")

var Create = grist.NewCommand(App, "create-counter",
	func(r *grist.Reads, c CreateCounter) ([]grist.Event, error) {
		if _, taken := CounterNames.Get(r)[c.Name]; taken {
			return nil, ErrNameTaken
		}
		return grist.Emit(Created.Of(CounterCreated{Counter: c.Counter, Name: c.Name})), nil
	},
	grist.Authorize(grist.Role("editor")))
```

`r` is the decision's only access to state. Every read through it
(`CounterNames.Get(r)`, `Totals.For(r, id)`, a raw `r.Events(q)`) is
recorded as a query (types, tags) folded through a position P. **The
append is conditioned on that read set**: it commits only if no event
matching any of those queries was appended after P. Otherwise the
runtime folds again from the new position and decides again (5 tries by
default, then `grist.ErrConflict`, a 409 at the API). This is the
dynamic consistency boundary of the "kill the aggregate" pattern, and
it is the default because it is derived, not declared: a decision that
reads names is conditioned on names whether or not its author thought
about concurrency. grain's race cannot be written by accident; it can be
written on purpose:

```go
// proposed: the unsafe variant, which the analyzer flags (§4) and the spike's exploration rejects (§8)
var CreateBlind = grist.NewCommand(App, "create-counter", decideCreate,
	grist.Unconditioned("names are advisory here"))
```

The other consistency options: `grist.Within(Totals)` narrows the
condition to some of the reads (the rest are advisory: the decision is
still correct if they are stale, and the option's reason says why);
`grist.Also(q)` adds a query the decision did not read (rare: a
command whose correctness depends on an event's absence it does not
fold). No option widens beyond the tenant: there are no cross-tenant
transactions, as in grain.

**Rejections and errors.** A decision returns either events or an
error. A `grist.Rejection` (a named, expected refusal) is a result:
recorded as the step's outcome, a 422 with its name at the API. Any
other error is a fault of the code (a 500), recorded with its text.

**Identity and idempotence.** Every command carries an envelope
(`grist.Envelope`: command id, tenant, actor, time), the id chosen by
the caller (the API requires it as `Idempotency-Key`). The transaction
marker the store writes with each batch (§2.9) is tagged with the
command id, and the append's condition includes "no marker with this
id". A retried command whose first attempt committed (a lost response,
the case payd's dedupe is for) gets the first attempt's result back,
read from its marker, and appends nothing. Event ids that a decision
needs (a new counter's id when the caller gives none) come from
`r.ID(n)`: a UUIDv5 of the command id and n, so a decision stays a
function of its inputs and its retries mint the same ids.

**Composition.** grain's `skip-event-storage` (a parent command running
children) becomes `grist.Sub(r, Child, c)`: the child decides on the
parent's `Reads`, its reads join the parent's condition, its events the
parent's batch. One append, one step per command (the child's step
records its parent).

### 2.4 Queries

A query is reads and arguments in, a result out; it never appends.

```go
// proposed
type CounterView struct {
	Name  string `json:"name"`
	Total int    `json:"total"`
}

var Show = grist.NewQuery(App, "show-counter",
	func(r *grist.Reads, q struct{ Counter string }) (CounterView, error) {
		/* look the counter up in CounterNames.Get(r), total from Totals.For(r, q.Counter) */
		return CounterView{}, nil
	},
	grist.Authorize(grist.Role("viewer")))
```

Its step (§3.1) records the result as an observation and the position
it was read through, so the model can check that the answer is the
fold of the log at that position.

### 2.5 Processors

A processor subscribes to event types and handles each event (in
position order, per tenant) from its checkpoint. Its handler returns
one of four results, grain's four delivery shapes with the gap of the
fourth closed:

| result | what commits | guarantee |
| --- | --- | --- |
| `grist.Emit(events...)` | the events and the checkpoint, one append, conditioned on the handler's reads and on the checkpoint | exactly once in the log |
| `grist.Dispatch(cmd, c)` | the command's decision, run on the processor's reads, its events and the checkpoint in one append | exactly once in the log |
| `grist.Effect(f, grist.AtLeastOnce)` | f runs, then the checkpoint (and f's outcome event) | at least once outside the log |
| `grist.Effect(f, grist.AtMostOnce)` | the checkpoint first, then f, then f's outcome event | at most once outside the log |

`grist.Skip()` checkpoints and does nothing. A handler error is retried
with backoff (3 tries by default, in virtual time in a simulation),
then checkpointed with a `grist.failed` event naming the trigger, so a
poison event stops nothing and is visible (grain checkpoints a failed
effect silently and never retries). `Dispatch` is what grain's example
should have done: the command's events and the checkpoint cannot be
split by a crash.

```go
// proposed
var Welcome = grist.NewProcessor(App, "welcome", Created,
	func(ctx context.Context, r *grist.Reads, e CounterCreated, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			return Mailer.Send(ctx, grist.EffectKey(ctx), "created "+e.Name)
		}, grist.AtLeastOnce)
	})
```

`grist.EffectKey(ctx)` is (processor, tenant, trigger position): the
idempotency key an at-least-once effect hands its dependency, so the
dependency can make "at least once" "once" where it supports keys.

**The checkpoint is an event**, `grist.checkpoint`, tagged with the
processor's name, carrying the position it is through. A processor's
read of its own checkpoint is a read like any other, so the CAS on the
checkpoint is the read-set condition of §2.3, not a second mechanism:
two pollers of one processor (two nodes, before leases) cannot both
advance it; the loser's append fails its condition, and it re-reads.
Monotonicity rests on positions being dense and in commit order within
a tenant, which the store guarantees (§2.9). The poller reads batches of
100 after the checkpoint, every 250 ms or when the store signals an
append (a `qenv.Signal` in a simulation), and checkpoints per batch for
`Emit`, per event for `Dispatch` and effects.

### 2.6 Periodic tasks

```go
// proposed
var Report = grist.NewPeriodic(App, "daily-totals", grist.Every(24*time.Hour),
	func(ctx context.Context, r *grist.Reads, tick time.Time) grist.Result {
		/* fold Totals, emit a report event */
		return grist.Skip()
	})
```

A periodic task runs once per tenant per slot, by construction: its
result's append is conditioned on "no `grist.tick` event for (task,
slot)", and the tick event is in the batch. Every node may wake for
every slot (as in grain); only one append wins, whatever the handler
does. Tenants are run one by one, each in its own transaction, so one
tenant's failure is that tenant's `grist.failed` event, not a skipped
round. Missed slots (a node down over midnight) run once, for the
latest missed slot (`grist.CatchUp(grist.Latest)`, the default;
`grist.All` runs each). Schedules are `Every(d)` and `Cron(spec)`, read
in virtual time inside a simulation (`qenv.Now`, `qenv.Sleep`).

### 2.7 Screens

A screen is queries and commands in a page (§5): its declaration names
the queries it renders and the commands its actions send, and
`Render(results) string` is a pure function. Screens are optional;
the spike has none.

### 2.8 Topology and boot verification

There is no separate model file to keep in step with the code (grain's
EDN event model). The registry is the topology: each block's edges are
what its constructor was given (a read model's handlers, a processor's
subscriptions, a screen's queries and commands) and what the analyzer
proves the code can emit and read (§4), checked in as `grist.topology`
(one line per edge, generated by `grist topology -w`, diffed in CI so a
change of wiring is a reviewed change). `app.Verify()` runs at boot and
in every test's setup, and refuses to start, naming the block and the
fix, when:

- an edge breaks grain's flow grammar: command → event; event →
  read model | processor; read model → command | query | processor;
  query → screen; screen → command; processor → command | event;
  periodic → command | event;
- an event is produced by nothing, or a produced event consumed by
  nothing (a warning, `grist.Sink` silences it for audit-only events);
- a block's analyzer-derived produces or reads differ from
  `grist.topology`;
- a (name, version)'s schema differs from `grist.lock`, or an old
  version lacks an upcaster;
- a command or query has neither `Authorize` nor `Internal()` (§4.3);
- two blocks share a name, or a name is not a valid Quint identifier
  once mapped (§3.2: `create-counter` becomes `createCounter`).

### 2.9 The store

grist owns its store interface; it does not reuse hallgren/eventsourcing's
`core.EventStore`. That interface appends by aggregate and version
(`Save` of an aggregate's events at an expected version, `Get` by
aggregate id after a version). grist has no aggregates: its condition is
a query over types and tags through a position, its order is one
sequence per tenant, and a batch carries a transaction marker and
checkpoints. An adapter over hallgren's would have to fake all four. The
interface is small enough to own:

```go
// proposed
type Store interface {
	// Read returns the tenant's events matching q, in position order.
	Read(ctx context.Context, tenant string, q grist.Query) ([]grist.Stored, error)
	// Append commits the batch at the next positions of the tenant, if
	// every condition holds, atomically with its transaction marker, and
	// returns the batch's last position. A failed condition is
	// *grist.ConditionFailed naming the query and the event that broke it.
	Append(ctx context.Context, tenant string, b grist.Batch) (grist.Position, error)
	// Head is the tenant's last position; Wait returns when it passes p.
	Head(ctx context.Context, tenant string) (grist.Position, error)
	Wait(ctx context.Context, tenant string, p grist.Position) error
}
```

`Query` is types, tags (all must match), after and through positions,
and a limit. A `Condition` is a query and a position: "no event matching
it after this position". Positions are int64, dense and assigned under
the tenant's lock, so within a tenant the position order is the commit
order. This is the property processors' checkpoints rest on, and the
reason positions are not a database sequence: a PostgreSQL `bigserial`
is drawn before commit, so a reader can see position 12 committed while
11 is still in flight, and a checkpoint at 12 skips 11 forever. Time is
one value per append, `qenv.Now` (virtual in a simulation, the clock's
outside). Each append writes a `grist.tx` marker event listing the
batch, the command or trigger, and the result.

Four tiers, one contract test suite (`grist/storetest`) run against each:

- **memstore**: maps and a mutex per tenant, for a unit test of a fold
  or a decision. Not for simulations (a real mutex).
- **simstore**: over `qenv.Store`. Keys `t/<tenant>/head`,
  `t/<tenant>/e/<position, 20 digits>`, and index keys
  `t/<tenant>/i/<type>/<position>` and `t/<tenant>/g/<tag>=<value>/<position>`.
  An append takes the head with `GetForUpdate` (the tenant's lock),
  checks each condition with `Scan` over its index prefix, puts the
  events, the indexes and the head, and commits; the step is recorded
  in `Txn.OnCommit` (§3.1). At every isolation level the head lock
  serializes a tenant's appends, and the Store's own checker
  (`GOTOGETHER_CHECK=1`) judges every commit. The store's fault kinds
  apply unchanged: `store.<name>.commit` (applied, answer lost: the
  command's dedupe makes the retry safe) and crashes.
- **sqlstore**: `database/sql`, two dialects (PostgreSQL, SQLite), the
  driver the application's (pgx's stdlib, modernc.org/sqlite: no cgo).
  Tables `grist_heads(tenant, head)`, `grist_events(tenant, pos, type,
  version, at, meta, body)` keyed by (tenant, pos), and
  `grist_index(tenant, k, pos)` keyed by all three (k a type or a
  tag=value). An append's **first statement** is `UPDATE grist_heads SET
  head = head + ? WHERE tenant = ?`: in PostgreSQL it takes the row's
  lock (no advisory locks, which are invisible to the simulation and to
  `qhistory`), in SQLite it takes the write lock before any read, so the
  deferred-to-write upgrade that returns SQLITE_BUSY cannot happen.
  Then the head is read back, the conditions are checked (`SELECT pos
  FROM grist_index WHERE tenant = ? AND k = ? AND pos > ? ORDER BY pos
  LIMIT 1`, then the event's other tags), and the rows inserted.
  PostgreSQL runs at read committed: every statement after the lock
  sees every commit before it.
- **sqlstore on qsql**: the same code on `qsql.Open` over a
  `qenv.Store`, so the SQL tier is explored with qsql's anomalies,
  SQLSTATEs and commit faults. This needs one extension of qsql's
  subset, which is the first item of the build (§8.1): a range
  condition on the last primary-key column, with `ORDER BY` that column
  and `LIMIT`, planned as a `Scan` of the key prefix. Everything else
  the store says is in the subset today (`c = c + ?`, equality, `ON
  CONFLICT DO NOTHING`).

Tenants: every read and append names its tenant, taken from the
envelope, never from a handler's argument. PostgreSQL row-level security
(`SET LOCAL grist.tenant`) is the real tier's second fence, added after
the spike. One reserved tenant, `grist`, holds the control plane
(leases, §3.4).

**The path to state in tables.** A read model is a fold; a table is a
fold persisted. Phase two adds `grist.Table(rm)`: the read model's state
written in the append's own transaction (the sqlstore's), keyed by its
key, so queries read rows and decisions stay as they are. Its condition
becomes the rows' versions instead of a query over events. The model
does not change: the table is the fold, and §3.2's binding reads it the
same way. Storing state alone (no events) is a third phase, an
`outbox`-style event table kept only for processors; this note does not
design it.

## 3. What a grist application gets from gotogether

This is the reason for grist. Each subsection is something an
application gets by being built of grist's blocks, with nothing written.

### 3.1 Every block records its step, by construction

grist records through `qobs` at the place each effect takes effect (as
payd's adapters do), never by annotation:

| step action | recorded | arguments | observations |
| --- | --- | --- | --- |
| the command's name | in the append's commit hook (`Txn.OnCommit`, qsql's `OnCommit`, the sqlstore's post-commit) | the command's fields, tenant, actor, command id | the events appended (type, position, tags, fields); the result |
| the command's name, with outcome | on return, when refused | as above | the rejection's name, or `conflict` after the tries |
| the query's name | on return | the query's fields, tenant | the result; the position read through |
| `<processor>` | at the checkpoint's commit | trigger position and type | the events appended; the checkpoint |
| `<processor>.effect` | at the effect's site: the double or service, or grist's wrapper when the dependency records nothing | the effect key | the effect's outcome |
| `<periodic>` | at the tick's commit | tenant, slot | the events appended |
| `grist.lease` | at the lease event's commit | node, tenant, term | |

A rejection is recorded on return because nothing committed; a commit
is recorded at the commit because the caller may never be told (an
08006, a crash between commit and response), and the model must see it
(AGENTS.md: an effect the model must see whether or not its caller
survives belongs at the effect's site). A retried command that finds its
marker records nothing: its effect was recorded once. Steps carry the
tenant as the actor's suffix (`counters/t1`), so tenants are separate
actors and tenant isolation is a property of the binding's actors, not
of hope.

### 3.2 A model skeleton and a binding, generated

`grist model` writes two files from the registry (never edited by hand,
regenerated in CI and diffed):

- `model/<app>_gen.qnt`: a sum type per event (its fields, Quint types
  from Go: `string` → `str`, ints → `int`, slices → `List`, maps →
  `a -> b`, structs → records, a `grist:"enum"` string → a sum type);
  the state, which is **the log** (`var log: str -> List[Event]`, per
  tenant), the checkpoints and the effects; one action per command,
  processor and periodic task, with its arguments as parameters and
  `nondet` picks for `step`; and one `def` per read model, its fold
  over the log. Read models are defs, not variables: the model cannot
  disagree with itself about what a read model holds, and a query's
  observation is checked against the def at the recorded position.
- `<app>.binding.yaml`: each step action to its model action, every
  parameter the same-named step field (so no templates), the tenant
  actors, observations to the defs they must equal, and the eventual
  vals for processors (below).

The application writes `model/<app>.qnt`, which imports both the
generated module and grist's (§3.3) and says what the generator cannot:
each command's precondition (when it is allowed) and the events it
appends, as a function of the read-model defs, and the invariants.
For the counter, the whole hand-written part of `createCounter`:

```quint
action createCounter(t: str, counter: str, name: str): bool = all {
  not(counterNames(t).has(name)),
  append(t, [CounterCreated({ counter: counter, name: name })]),
}
// every created counter has a name of its own: the names' fold has one entry per creation
val uniqueNames = tenants.forall(t => counterNames(t).keys().size() == createdCount(t))
```

A rejected step maps to the action's refusal (the precondition false),
as payd's refusals do. The generated module's stub of an action the
application has not written appends what the step recorded and checks
nothing, so a new command is accepted until its author states it; `grist
verify` lists such stubs, and `GOTOGETHER_STRICT=1` fails on them.

### 3.3 grist's guarantees as a Quint module

`grist/model/grist.qnt`, vendored into an application's `model/` by
`grist model` (as examples/twophase vendors choreo.qnt), states what the
framework promises and every generated model includes:

- **Positions**: per tenant, dense, increasing, commit order.
- **Conditional append**: an append whose condition was false is not in
  the log (the model's `append` takes the read set; the binding gives it
  from the step).
- **Checkpoint monotonicity**: a processor's checkpoint never moves back
  and never passes an event it has not handled (`Emit`, `Dispatch`:
  exactly one handling step per event; `Skip` and `failed` count).
- **Effects**: per trigger, `AtLeastOnce` admits one or more effect steps
  and requires one after a settle (`eventually`); `AtMostOnce` admits
  zero or one and, after a settle, requires the outcome event to agree
  with the effect seen. An application whose effect must not repeat
  says so with an invariant over its own effect steps; the framework's
  module allows exactly what the declared guarantee allows.
- **Tenant isolation**: a step of tenant t reads and appends only t's
  log (an event tagged with another tenant's id is no violation; an
  event in another tenant's log is).
- **Periodic**: at most one tick per (task, tenant, slot).
- **Leases** (once there are nodes, §3.4): at most one holder per tenant
  per term, and a processor's checkpoint advanced only by the holder.

This module is checked once, in grist's own tests, against grist's own
runtime (the contract suite's runs bound to it), so an application's
failure is its own or its model's, not the framework's.

### 3.4 Choreo for the multi-node parts

Single-node grist has no distributed protocol: the store serializes.
The parts with one are the control plane's (grain's): heartbeats,
coordinator election (the live node with the smallest id), tenant
leases as events in the reserved tenant, and routing to the owner (503
with `Retry-After`). That protocol is modeled once, in Choreo
(`grist/model/leases.qnt`, on the choreo.qnt examples/twophase
vendors), each transition a named action of its node
(`choreo::step_with`, the twophase gotcha), and checked against grist's
node runtime by `connect` and by explorations with partitions and
crashes. Applications do not model it; they import its invariant
(one holder per term) through grist.qnt. Leases are not in the spike.

### 3.5 Every seam simulated

grist's runtime is written against qenv's seams, and the lint
(`gotogether lint -sim`) holds grist's own packages to it, as
`TestCoreStaysInTheSimulation` holds the core:

- pollers, the effect worker and the periodic scheduler are tasks
  (`qenv.Go`), waiting with `qenv.Sleep`, `qenv.Wait` on the store's
  signal, marked `qenv.Daemon`;
- a node is a `qenv.Process` (`Sim.Process(name, true, start)`); a crash
  kills its tasks; restart replays from checkpoints;
- the effect's two crash points are fault points grist declares,
  `grist.<processor>.effect` with options nominal, `crashBefore`,
  `crashAfter` (after the effect, before its checkpoint): the crash
  that duplicates an at-least-once effect is one named option, which a
  `qenv.Script` can ask for and `Exhaust` reaches with one fault;
- the store's isolation level, commit faults and lost answers are the
  qenv.Store's or qsql's;
- dependencies an effect calls are `qenv.Service`s, or real HTTP
  clients through `qhttp.NewTransport`, so lost and late responses are
  explored;
- the API handler runs on a `qhttp.NewServer`, so a client's retries,
  duplicates and timeouts reach commands as on a wire.

`gristsim.Run(c qenv.Chooser, app, cfg)` builds all of that (one
simstore or qsql-backed sqlstore, n nodes, the API server, a workload
of clients sending commands through it), settles, and probes every
read model through the queries. Its signature is a `qtest.System`'s
`Run`, so:

```go
// proposed: an application's test
func system(cfg gristsim.Config) qtest.System {
	return qtest.System{Binding: "counters.binding.yaml", Rates: qenv.DefaultRates.Set("nemesis", 0.02),
		Run: func(c qenv.Chooser) error { return gristsim.Run(c, App, cfg) }}
}
```

### 3.6 Explorations, Hegel, shrinking and mutants

- **Explore and Exhaust**: `system(cfg).Explore(t, qtest.ExploreConfig{...}).Expect(...)`
  and `Exhaust(t, qtest.Bound{Faults: 1})`, with every claim of today:
  `Conforms`, `Settles`, `Violates`, `Reaches` (grist's fault kinds by
  name).
- **Workloads from the registry**: `gristsim.Config.Workload` defaults
  to clients sending random commands of the registry, arguments drawn
  from the command's type by a `gristsim.Gen` (reflection, bounded:
  strings from a small alphabet so names collide, which is what finds
  races). An application overrides one command's generator, never
  writes a workload loop.
- **Hegel state machines from the registry**: `gristhegel` (a module of
  its own, §7) turns the command registry into a `hegel.RunStateful`
  machine: one rule per command (Hegel draws the arguments and the
  tenant), the model judging each prefix through qhegel's `Case`, the
  chooser from `qhegel.Choices` so faults shrink with the commands.
  `qhegel.Shrink(t, sys)` already shrinks a System's runs.
- **ShrinkActions over command sequences**: a failing real-tier run's
  commands (read from its steps) are the actions; `Rerun` replays them
  through the API on a fresh store. `gristtest.ShrinkCommands(t, app,
  steps)` is `qtest.ShrinkActions` with that Rerun, and its
  `ErrDiverged` for a command whose arguments name an id the shorter
  run never created.
- **Mutants over read models and decisions**: qweave `-mutate` on the
  application's package mutates folds and decisions (one build, a
  mutant per run, `System.Mutate`); `qtest.MutateSteps` over a run's
  steps asks whether the binding reads each query observation and each
  appended event (a corrupted `obs.result` that survives is a query the
  model does not check).
- **qload on processors**: a processor is a server whose queue is the
  log behind its checkpoint and whose service is its handler. grist
  registers qrole roles for each processor (`u`, its handler's
  utilization; `v`, the lag behind the head waiting for retry) and for
  the API, so `qload` simulations and `qmeta` alert rules apply to a
  processor whose effect retries against a slow dependency, the shape
  of a retry storm.

### 3.7 The real tier, checked by the same binding

`grist.Main(app)` serves the application on sqlstore (PostgreSQL or
SQLite) and writes its steps with `qobs.FileSink(path, qobs.PerStep)`;
a real deployment's runs go through `gotogether validate` with the same
binding. `qnemesis` kills and pauses nodes and cuts links; grist's
recording sides follow §52's rule (commits recorded at the commit, so a
kill loses no committed step and `Process.Recorders` is not needed for
commands; an effect recorded by grist's wrapper on return is declared
lost on a kill, `Step.LostBefore`). `qnemesis.FromScript` replays a
simulation's fault script against the real deployment and
`qnemesis.Unmade` names what it could not make.

## 4. Constraints enforced

### 4.1 Analyzers (qanalysis, `quintvet`)

Two analyzers, `gristpure` and `gristflow`, in qanalysis (it already has
x/tools and the golangci plugin), recognizing grist's constructors by
type:

- **gristpure**: the functions given as decisions, folds, upcasters,
  query functions and screen renders (and every function of the package
  they call, transitively) do not: call `time.Now`, `qenv.Now`,
  math/rand or crypto/rand, I/O (`os`, `net`, `database/sql`, `io`
  writers other than strings.Builder and bytes.Buffer), `qobs`; start
  goroutines or use channels; write package-level variables; range over
  a map where the result depends on the order (a map range whose body
  appends to a slice or emits, unless the keys were sorted: the lint's
  existing rule); reach a store or the `Reads` outside its own
  parameter. A call it cannot see (a function value, an interface
  method of another package) is reported as unverifiable, exempted with
  `//grist:pure <reason>`. Handlers of processors and periodic tasks are
  not pure (their effects are the point) but their `Reads` are, and
  their effect is only through `grist.Effect`'s function.
- **gristflow**: from each decision's and handler's return statements,
  the event types it can emit (`Created.Of(...)` literals reaching a
  return through `grist.Emit`), the read models it reads (`X.Get(r)`,
  `X.For(r, ...)`), the commands it dispatches; written to
  `grist.topology` by `grist topology -w` and compared at boot (§2.8).
  It also reports: **a decision that returns a rejection or an error
  under a condition on a read whose append is not conditioned on it**
  (`Unconditioned`, or a `Within` that leaves the read out): "create-counter
  refuses ErrNameTaken on counter-names, which its append is not
  conditioned on: two concurrent create-counter can both pass; remove
  Unconditioned, or exempt with //grist:advisory <reason>". This is
  grain's race as a lint; §8 asserts it fires on the unsafe variant.
  And it reports a processor handler that calls a command's function or
  the store directly instead of returning `Dispatch` (grain's
  duplicate).

### 4.2 Boot verification

§2.8's list, run by `app.Verify()` at `grist.Main`'s start, by
`gristsim.Run` before every run (cheap: maps of names), and by `grist
verify` in CI with the analyzers' output. A refusal is an error naming
the block, what differs, and the command that fixes it.

### 4.3 Authorization, denied by default

Every command and query is denied at every adapter (HTTP, Datastar)
unless it declares `grist.Authorize(p)`, a predicate over the envelope's
actor and the tenant (`grist.Role`, `grist.Any`, `grist.All`, or a
function). `grist.Internal()` marks one no adapter serves (only
processors dispatch it). Boot refuses a block with neither. As in grain
the check is in the adapters, not in the decision, so a processor's
`Dispatch` is not subject to a user's roles; unlike grain, an internal
command is unreachable from HTTP by construction, not by omission. A
denial is recorded as a refused step (`outcome: denied`), so a model can
state who may do what, and an exploration with actors of several roles
checks it.

## 5. The API layer and Datastar

**HTTP/JSON** (`grist/api`, `net/http` only):
`POST /t/{tenant}/commands/{name}` with the command as JSON and
`Idempotency-Key`; 200 with `{position, result}`, 422 with a rejection's
name, 409 on conflict after the tries, 403 when denied, 503 with
`Retry-After` when the tenant's lease is elsewhere.
`GET /t/{tenant}/queries/{name}?...` (arguments as query parameters by
their JSON names) or `POST` with a body; the response carries the
position read through. `GET /catalog` serves the registry as JSON
(schemas, topology), disabled unless `grist.DevEndpoints` is set.
Authentication is the application's middleware, which puts the actor in
the context (`grist.WithActor`). The handler is a plain `http.Handler`,
so `qhttp.NewServer` serves it in a simulation and `qobshttp.Handler`
records its client-facing steps into a test's run.

**Datastar** (`grist/screen`, the only package that imports
`github.com/starfederation/datastar-go/datastar`, v1.2.2 or the latest
when built; its API: `NewSSE(w, r)`, `PatchElements`, `PatchSignals`,
`ReadSignals`, `ExecuteScript`). A screen is:

```go
// proposed
var CounterPage = screen.New(App, "counter",
	screen.Queries(Show),
	screen.Commands(Create),
	screen.Render(func(v CounterView) string { return "<div id=\"counter\">" /* ... */ + "</div>" }))
```

`GET /t/{tenant}/screens/counter` opens an SSE stream: grist runs the
screen's queries, patches the rendered elements, then waits on the
tenant's head (`Store.Wait`) and re-renders when a consumed read model's
position moved. An action posts signals (`ReadSignals`) as a command's
arguments, through the same command path (authorization, idempotency,
recording). Rendering is pure (gristpure), so what a screen shows is a
function of its queries' results, which are recorded. A screen's steps
are its queries' (with the screen's name), plus `screen.<name>.patch`
with the positions rendered. The model checks a screen through them:
every patch shows a fold at a recorded position, and after a settle the
last patch shows the head (an eventual val: a screen that stops
following is caught, as webjobs' page is). The browser itself is the
real tier's (examples/webjobs' Bombadil route, §54 E5), not the
simulation's: qhttp passes whole responses, so in a simulation the
stream is driven by a test client that reads patches as a page would,
through `screen.Client`.

## 6. The agent workflow

A skill, `skills/grist/SKILL.md` (symlinked into `.agents/skills` and
`.claude/skills`, front-matter `name: grist`), grain's `/grain` loop
with gotogether's checks in place of generated tests:

1. **Elicit** the blocks as a table (commands, events, read models,
   queries, processors with delivery, periodic tasks, screens) and the
   invariants in words.
2. **Declare** them in Go (events, read models, command signatures with
   stub decisions that return `grist.Todo`), run `grist verify` until
   the topology is clean, `grist model` to generate the skeleton and the
   binding.
3. **Model first**: write each action's precondition and the invariants
   in `model/<app>.qnt`; `quint run` the model's invariants.
4. **Implement red first**: `gristtest.Explore` on the stub fails by the
   model (`Rejects`); write decisions and folds until it conforms.
5. **Prove the checks can fail**: one mutant per invariant (an overlay
   or a qmut schema), `.Expect(t, qtest.Some(qtest.Violates(...)))`.
6. **Tend**: on every change `grist verify`, the analyzers, the
   exploration; a changed event is a new version with an upcaster.

References under `skills/grist/references/`: blocks.md (the Go shapes),
delivery.md (choosing a processor's result), consistency.md (reads,
`Within`, `Unconditioned`), testing.md (the Systems and claims).
`gotogether-start`'s table gets a row: an event-sourced application
from scratch → grist.

**The catalog CLI** is grist's own, `grist` (`grist/cmd/grist`: the core's
`gotogether` cannot import grist, which requires the core).
`grist.Main(app)` gives every application the subcommands, and `grist
<sub> ./cmd/app` builds and runs the application's main with them (argv,
no shell): `catalog` (blocks, schemas, topology, delivery and
authorization, as text or `-json`), `verify`, `model`, `topology -w`,
`invoke <command> <json>` and `query <name> <json>` against a memstore
or a SQLite file, `events -tenant t` and `fold <read model> -tenant t`.
These are grain's nREPL tools as commands an agent can run and read.

## 7. Module layout

```text
grist/                 module github.com/chucklehead-dev/gotogether/grist (go.mod; published like qhegel)
  grist.go             App, events, read models, commands, queries, processors, periodic, Reads, Result
  runtime/             the node: pollers, effect worker, scheduler, dispatcher (qenv seams only)
  store/               Store, Query, Batch, Condition, Position; storetest (the contract suite)
  memstore/ simstore/ sqlstore/
  api/                 HTTP/JSON adapter
  screen/              Datastar adapter (the only datastar-go import)
  gristsim/            Run(chooser, app, cfg): nodes, store, API, workload, settle, probes
  gristtest/           ShrinkCommands, contract claims, storetest helpers
  model/               grist.qnt, leases.qnt (Choreo), the generator's templates
  cmd/grist/           the CLI
grist/gristhegel/      module of its own: registry-driven Hegel machines (requires grist, qhegel)
qanalysis/             + gristpure, gristflow (recognize grist by import path; no dependency on grist)
skills/grist/          the skill
examples/counters/     the spike (§8), a module with replace => ../..
```

grist requires the core (qenv, qobs, qtrace, itf for value encoding,
binding and validate for the generator, qtest and qrole) and nothing
else but datastar-go, imported by `screen` alone. No Hegel (gristhegel
is its own module, so gosim's `internal/simdeps` rule, nothing that
imports qenv depends on Hegel, holds for grist), no SQL driver (the
application's), no Orchestrion (grist's blocks record themselves; there
is nothing to weave), no OTel beyond the core's. It joins `go.work`,
gets `api/grist.txt` from internal/apisurface, and CI tests it with
`GOWORK=off` like qhegel.

## 8. The first spike

### 8.1 Prerequisites in the core

1. qsql: a range on the last primary-key column with `ORDER BY` it and
   `LIMIT` (a `Scan` over the key prefix), with the Store's history
   checker seeing the scan as a predicate read; its parse and plan
   tests, and an `...AgainstBruteForce` test of the range plan against a
   filtered full scan.
2. Nothing else: Txn.OnCommit, qenv.Signal, FaultPoint, Service, Process,
   System and the claims exist.

### 8.2 Scope

grist with: events (one version, no upcasting yet), plain and keyed
read models (no cache), commands with the read-set condition,
`Unconditioned`, idempotency by command id, queries, processors with
`Emit` and `Effect(AtLeastOnce)` and `Effect(AtMostOnce)`, periodic
`Every`, boot verification of names and edges (not the schema lock),
memstore and simstore (sqlstore on qsql and SQLite in the spike's second
week), the HTTP API without Datastar, `gristsim.Run`, the model
generator, `gristflow`'s unconditioned-rejection check (gristpure
after). One node. No leases, no screens, no gristhegel, no qload.

`examples/counters`, grain's counter example:

- events `counter-created`, `counter-incremented`, `average-computed`,
  `notified`;
- read models `counter-names` (plain) and `totals` (keyed by counter);
- commands `create-counter` (unique name, the read-set default),
  `increment-counter` (refused for an unknown counter);
- query `show-counter`;
- processor `average` (`Emit`: on each increment, the tenant's average
  as `average-computed`) and processor `welcome` (`Effect(AtLeastOnce)`
  calling a `qenv.Service` "mailer" that records `mailer.send` with the
  effect key);
- periodic `report` (`Every(time.Minute)`, emits the tenant's total);
- two tenants, `t1` and `t2`, with clients sending to both, names drawn
  from three letters so they collide;
- the model `model/counters.qnt` over the generated skeleton and
  grist.qnt, with invariants `uniqueNames`, `averageIsMean` (each
  `average-computed` equals the mean at its position), `oneReportPerSlot`
  (grist.qnt's), tenant isolation (grist.qnt's), and the eventual val
  `everyCounterWelcomed`;
- mutants (overlays of the example's files, as payd's): `blind` (create
  with `Unconditioned`), `checkpointFirst` (welcome declared
  `AtMostOnce` while the model says at least once: a crash after the
  checkpoint loses the welcome), `leakTenant` (the average folds both
  tenants' increments: a fold given the wrong tenant's reads, through a
  test-only hook), `dispatchDirect` (average calls the store itself and
  returns `Skip`, grain's example).

### 8.3 Claims it must demonstrate

Each a test, with `.Expect`, its rate measured and its seeds chosen for
a miss chance under 5% (payd's discipline):

1. **The race is found in the unsafe variant.** `blind`: two clients,
   no faults, `Explore` with names colliding:
   `qtest.Some(qtest.Violates("uniqueNames"))`.
2. **The analyzer flags it.** gristflow's analysistest fixture of the
   blind create reports the unconditioned rejection, with the message
   of §4.1; the safe create reports nothing.
3. **The safe default passes.** The faithful app, at every isolation
   level of the simstore and through all faults (crashes, commit faults,
   lost and duplicated API requests): `qtest.Conforms()`,
   `qtest.All(qtest.Settles())`, and `qtest.Reaches("store.counters.commit").Faulted()`
   so the faults happened.
4. **The duplicated effect is visible and judged.** `Exhaust` with one
   fault reaches `grist.welcome.effect`'s `crashAfter`: the restarted
   node sends the welcome again under the same effect key. The model
   admits it (`AtLeastOnce`: `qtest.Conforms()`), and an application
   invariant `welcomedOnce` added in a test-only model variant rejects
   exactly those runs (`qtest.Some(qtest.Violates("welcomedOnce"))`,
   `qtest.Reaches("grist.welcome.effect").Faulted()`): the duplicate is
   the declared guarantee's, seen and judged, not hidden.
5. **At most once loses, visibly.** `checkpointFirst`, with one fault:
   `qtest.Some(qtest.Violates(validate.EventuallyName("everyCounterWelcomed")))`.
6. **Tenants stay apart.** `leakTenant`: `qtest.Some(qtest.Violates("tenantIsolation"))`,
   and `averageIsMean` in the faithful runs.
7. **grain's processor duplicate is gone.** `dispatchDirect` with one
   crash duplicates `average-computed` for one increment:
   `qtest.Some(qtest.Violates("oneHandlingPerEvent"))` (grist.qnt's);
   the faithful `Emit` never does (claim 3), and gristflow flags the
   direct store call.
8. **The periodic task runs once per slot** under two schedulers (the
   spike's one node runs the scheduler twice, as two nodes would) and
   crashes: `qtest.None(qtest.Violates("oneReportPerSlot"))`.
9. **One binding, two tiers.** The same binding validates a run of
   `grist.Main` on SQLite driven by a Go client (`real_test.go`), and
   the `blind` mutant is caught there by concurrency alone, at a rate
   stated in the test.

Done when the nine pass in CI, the README's table names which check
catches which mutant, `run-demo.sh` shows claims 1 and 4, and the skill's
first version walks through the example.

## 9. Open questions for the maintainer

1. **Read-set conditions by default, even for writes that need none.**
   An increment conditioned on its counter's events conflicts with
   every concurrent increment of that counter (retried, but contended).
   The recommendation is to keep the default and let `Within` narrow
   it; the alternative is a per-command declaration with no default.
   Is contention on hot keys acceptable as the price of the safe
   default?
2. **Checkpoint events in the log.** One `grist.checkpoint` per batch
   per processor grows the log (grain does the same). Keep them as
   events (one mechanism, visible to the model and to `grist events`),
   or move checkpoints to a table of their own updated in the same
   transaction?
3. **Positions without global ids.** Events are identified by (tenant,
   position); grain also gives each a UUIDv7. Is a global id needed
   (cross-tenant references, export), or can it wait for retention?
4. **The model's state is the log.** Read models as folds over the log
   make the model faithful but its states long; validation of long runs
   may need a model variant that keeps the folds as variables (a
   mechanism-level model with a Hegel-tested lemma, the
   gotogether-patterns way). Generate both from the start, or only when
   a run is slow?
5. **The name.** "grist" also names gristlabs' spreadsheet product
   (grist-core); the Go import path is ours, but a search is not. Keep
   it, or pick another before the module is published?

## 10. Rejected, and why

- **hallgren/eventsourcing's store, or goes, or eventhorizon as the
  core**: aggregate- and version-based concurrency, which is the wrong
  boundary for a decision over read models; owning a four-method
  interface costs less than adapting one.
- **Watermill for processors**: an at-least-once message bus between
  the log and its readers adds a second delivery semantics to model;
  the log, read from a checkpoint, is the bus.
- **Temporal, Restate, DBOS, River for processors and periodic tasks**:
  each brings its own scheduler, clock or server, which qenv cannot
  simulate; River's transactional enqueue is what `Dispatch` gives
  without it.
- **Code generation for the runtime (Encore-style)**: typed generic
  constructors carry the same facts, and the analyzer reads them;
  generation is kept for the model and binding, which are not Go.
- **Optimistic version checks per stream**: grain's insight, kept, is
  that a condition is a query; streams would bring back aggregates.
- **Advisory locks for the tenant's serialization**: invisible to the
  simulation's Store and to qhistory; the head row's update is a lock
  both see, on PostgreSQL and SQLite alike.
- **A database sequence for positions**: drawn before commit, so not in
  commit order; a checkpoint would skip an in-flight event (§2.9).
- **Annotations (`//quint:action`) on grist's blocks**: the framework
  calls every block, so it records them; an annotation could only
  disagree.
- **Allium or prose specs linked by name** (grain's): the user's
  decision is Quint, and Choreo for distributed parts; a spec gotogether
  cannot run against the code is documentation.
- **A REPL for agents** (grain's nREPL tools): Go has none worth
  relying on; the same tools are CLI subcommands of the application's
  own binary.

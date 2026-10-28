# grist: the user guide

grist is an event-sourced Go framework whose applications are checked
against a Quint model with nothing annotated. You write events, read
models, commands, rules, queries and processors in Go; grist records
every block's step itself, generates the model's skeleton and both
bindings from your App's catalog, and gotogether explores the app in a
simulation (crashes, commit faults, lost and late requests) with every
run judged by the model. What you write in Quint is only your rules and
invariants.

This guide is for people building an app on grist. It describes what
is built, with the APIs as they are (module
`github.com/chucklehead-dev/gotogether/grist`; `api/grist.txt` lists
every exported name). The design note, [grist.md](grist.md), is for
maintainers: why each block is the way it is, what is designed and not
built, and the build notes. The agent skill (`skills/grist`) is the same
material as checklists.

Contents:
1. [Your first app](#1-your-first-app)
2. [The blocks](#2-the-blocks)
3. [The HTTP API](#3-the-http-api)
4. [The binary: grist.Main](#4-the-binary-gristmain)
5. [The model: generated and written](#5-the-model-generated-and-written)
6. [Testing](#6-testing)
7. [Versions: changing an event](#7-versions-changing-an-event)
8. [Snapshots](#8-snapshots)
9. [Running in production](#9-running-in-production)
10. [Troubleshooting](#10-troubleshooting)
11. [What is not built](#11-what-is-not-built)

The worked examples: [examples/counters](../examples/counters) (every
feature, with a table of which test catches which mutant),
[examples/library](../examples/library) (grown from `grist new`, an
event versioned, snapshots), [examples/wallet-grist](../examples/wallet-grist)
(money moved once by command id, a ledger at least once).

## 1. Your first app

You need Go 1.26.1 or later and quint 0.32 or later on PATH
(`npm i -g @informalsystems/quint`). Outside any Go module:

```sh
go run github.com/chucklehead-dev/gotogether/grist/cmd/grist@latest new inventory
cd inventory
go get -tool github.com/chucklehead-dev/gotogether/cmd/gotogether   # go tool gotogether: simulate, validate, nav
go test -short -v ./...    # about a minute; the first run downloads the toolkit (about 2 minutes)
```

`grist new NAME` takes `-dir DIR` (default `./NAME`), `-module PATH`
(default `example.com/NAME`), `-replace REPO` (build against a checkout
of gotogether instead of a released version) and `-no-generate` (write
the files only). The name is lower-case words joined by dashes.

It writes a module that passes its tests on the first run: a store of
items holding at most five of each.

| file | what it is | who writes it |
|---|---|---|
| `app.go` | the events (`item-added`, `item-tallied`) and a keyed read model (`stock`) | you |
| `commands.go`, `rules.go`, `queries.go`, `processors.go`, `deps.go` | `add-item` (refused `invalid`), the `capacity` rule (refusing `full`), `show-item`, the `tally` and `shelve` processors, the warehouse it calls | you |
| `model/<name>_rules.qnt` | the rules the generated skeleton calls: each command's outcome and events, each fold | you |
| `model/<name>.qnt` | the invariants, each marked for its binding | you |
| `model/<name>_gen.qnt`, `model/<name>_steps.qnt`, `model/grist.qnt` | the events, one action per block, grist's guarantees | generated |
| `<name>.binding.yaml`, `<name>.calls.binding.yaml` | the store's side and the clients' side | generated |
| `grist.lock` | every stored event version's shape, every read model's fingerprint | generated, committed |
| `cmd/<name>` | the binary (`grist.Main`) | you |
| `sim_test.go`, `mutants_test.go`, `real_test.go` | explorations, mutants, the real tier on SQLite | you |
| `testdata/mutants/noInvalid` | a planted bug, as a file overlay | you |
| `run-demo.sh`, `README.md` | the demo and the table of which test catches which mutant | you |

`go test -short -v ./...` runs every claim at a few seeds and prints
each test's summary ("20 runs, all conform"); without `-short`, more
seeds and Hegel's shrinks take about five minutes more.

**The loop.** Change one block at a time:
1. Write the block in Go, declaring its shape (`grist.Refuses`,
   `grist.Appends`, `grist.Emits` or `grist.Delivers`, `grist.Uses`).
2. `go run ./cmd/<name> model`. It regenerates the skeleton and the
   bindings, and names each new hole in `model/<name>_rules.qnt` with a
   stub to paste.
3. Fill the hole; add an invariant to `model/<name>.qnt` if the block
   promises something; check the model alone:
   `go tool gotogether simulate -binding <name>.binding.yaml`.
4. Add a claim to a test and a mutant that breaks it (§6).
5. `go test -short ./...`.

No binding is written by hand.

## 2. The blocks

Every block is registered on one App, a package-level variable:

```go
var App = grist.New("inventory")
```

Its name is the store's (tables are per app), the model's module's and
the recorder's. Blocks are package-level variables too: registration
happens at init, and `App.Verify()` (run at boot, by `<app> verify` and
by every test harness) names every problem with its fix.

### 2.1 Events

An event is a Go struct registered under a name:

```go
type ItemAdded struct {
	Item string `json:"item" grist:"tag"`
	Qty  int    `json:"qty"`
}

var Added = grist.NewEvent[ItemAdded](App, "item-added")
```

- **Fields** are plain values the model can say: strings, integers,
  booleans, and structs, slices and maps of them. No floats and no
  times: an integer in a stated unit (cents, milliseconds), or an average
  as a sum and a count. Each field is named by its JSON name, which is
  also its Quint name: a Quint keyword or built-in (`item` is fine as a
  field, `to`, `from`, `size` are not as parameters) is renamed by the
  generator with a trailing underscore (`of_`), the binding's template
  naming the field.
- **Tags** (`grist:"tag"`) are what reads select by: `grist.Tag("item",
  "a")`, keyed by the field's JSON name. A keyed read model's key must be
  a tag of every event it folds. A tag never changes for a stored event
  (§7).
- `Added.Of(ItemAdded{...})` makes the `grist.Event` a decision returns;
  `Added.Decode(stored)` reads one back.
- Events are immutable once stored. Changing the struct is a new version
  (§7).

### 2.2 Read models

A read model is a fold of events into a state:

```go
// plain: one state for the tenant
var Names = grist.NewReadModel(App, "counter-names", 1, func() map[string]bool { return map[string]bool{} },
	grist.On(Created, func(s map[string]bool, e CounterCreated, m grist.Meta) map[string]bool { s[e.Name] = true; return s }))

// keyed: one state per value of a tag
var Stock = grist.NewKeyedReadModel(App, "stock", 1, grist.Keys("item"), func() int { return 0 },
	grist.On(Added, func(s int, e ItemAdded, m grist.Meta) int { return s + e.Qty }))
```

- The version (1 above) is yours to bump when the fold's meaning
  changes in a way generated events may not show (§8).
- A handler gets the event and its `grist.Meta` (tenant, position,
  time, cause). Folds are pure: no clock, no I/O, no map iteration
  whose order matters.
- A keyed read model is read for one key (`Stock.For(r, "a")`), and a
  decision about key `a` is conditioned on `a`'s events alone: contention
  follows the keys.
- The state must survive JSON (exported fields of plain values) to be
  snapshotted (§8).

### 2.3 Reads

Commands, rules, queries and processors read through a `*grist.Reads`,
pinned at one position of the tenant's log:

| read | what it returns |
|---|---|
| `Names.Get(r)` | a plain read model's state |
| `Stock.For(r, "a")` | a keyed read model's state for one key |
| `r.Exists(Created, grist.Tag("name", n))` | whether an event of a type, with those tags, exists |
| `grist.Events(r, Added, grist.Tag("item", "a"))` | the events of a type, typed |
| `r.Select(grist.Selector{...})` | raw events (types, tags, after, reverse, limit) |
| `r.Caller()`, `r.Tenant()`, `r.Position()` | the envelope's caller, the tenant, the position read through |

Every read joins the decision's **read set**. A command's append is
conditioned on it: it commits only if no event any of its reads would
have returned was appended since the position it read (§2.4). A read of
a type the App does not register, or of a tag key the type has none of,
is the decision's error, never an empty answer.

### 2.4 Commands

A command is an arguments struct and a decision: reads and arguments
in, events or a refusal out.

```go
type AddItem struct {
	Item string `json:"item"`
	Qty  int    `json:"qty"`
}

var ErrInvalid = grist.Rejection("invalid")

func decideAdd(r *grist.Reads, c AddItem) ([]grist.Event, error) {
	if c.Qty <= 0 {
		return nil, ErrInvalid
	}
	return []grist.Event{Added.Of(ItemAdded{Item: c.Item, Qty: c.Qty})}, nil
}

var Add = grist.NewCommand(App, "add-item", decideAdd,
	grist.Appends(Added), grist.Refuses(ErrInvalid), grist.Authorize(grist.Authenticated))
```

- **Refusals** are `grist.Rejection(name)` values, declared with
  `grist.Refuses`. A refusal commits a marker (the command's id is
  answered) and no event; the model gets an outcome branch for each
  declared one. A decision returning a rejection it did not declare is
  its error, naming the fix.
- **The condition.** When another commit invalidates the read set, the
  runtime reads again at the new head and decides again, up to
  `grist.ConditionTries(n)` times (default 5); then `grist.ErrConflict`
  (409 at the API), which is the key's final answer. This closes the
  unique-name race at every isolation level. `grist.Unconditioned(reason)`
  takes the condition off on purpose; no isolation level protects such a
  command, and the model judges each of its commits.
- **Idempotency by command id.** Every call carries a `grist.Envelope`
  (`ID`, `Tenant`, `Caller`). A key's first answer is final, a refusal's
  included: a retry under the same id gets the same receipt or refusal,
  and the same id with other arguments is refused
  `idempotency-key-reused` (`grist.ErrKeyReused`).
- `grist.Appends(types...)` declares what the command appends, so the
  rules on those events guard it (§2.5).
- `grist.TransientTries(n)` (default 10) bounds transient store errors.
- An unknown outcome (an append that may have committed) is
  `grist.ErrOutcomeUnknown`: retry with the same id.
- In process: `Add.Execute(ctx, store, env, AddItem{...})`, or by name
  `App.Execute(ctx, store, env, "add-item", argsJSON)`.

### 2.5 Rules

A rule is a requirement on every append of an event, whichever command
makes it:

```go
var ErrFull = grist.Rejection("full")

var CapacityRule = grist.NewRule(App, "capacity", Added, func(r *grist.Reads, e ItemAdded) error {
	if Stock.For(r, e.Item) > Capacity-e.Qty {
		return ErrFull
	}
	return nil
}, grist.Refuses(ErrFull))
```

It runs after an ok decision, on the command's own reads (which join the
condition), and its refusal becomes the command's. Every command that
appends the event must declare it (`grist.Appends(Added)`); Verify
refuses a rule on an event no block appends. A processor's emit that a
rule refuses fails its trigger, naming the rule. Each rule gets a
generated mutant, `drop-<rule>` (§6.4).

### 2.6 Queries

```go
var Show = grist.NewQuery(App, "show-item", func(r *grist.Reads, q ShowItem) (ItemView, error) {
	return ItemView{Stock: Stock.For(r, q.Item)}, nil
}, grist.Authorize(grist.Authenticated))
```

A query never appends. Its step records the result and the position it
read through, so the model checks the answer is the fold of the log
through that position. Run it with `Show.Run(ctx, store, env, ShowItem{...})`
or `App.Query(...)`.

### 2.7 Processors

A processor handles each event of a type, in position order per tenant,
after its checkpoint. Its handler reads as of the trigger's position, so
a late handling (a poller behind, a restart) computes what an on-time one
would have. It returns one of four results:

| result | what commits | guarantee |
|---|---|---|
| `grist.Emit(events...)` | the events, with the checkpoint, in one transaction | exactly once in the log |
| `grist.Skip()` | the checkpoint only | |
| `grist.Effect(f, grist.AtLeastOnce, opts...)` | `f` runs, then the checkpoint (and `grist.OnSuccess`'s event) | at least once: a crash between them runs `f` again under the same key |
| `grist.Effect(f, grist.AtMostOnce, opts...)` | the checkpoint, then `f` (then `OnSuccess`'s event) | at most once: a crash between them loses `f` |
| `grist.Fail(err)` | nothing at first: the trigger is handled again (`EffectTries` tries, with backoff), then checkpointed with a `grist.failed` event | a poison event stops nothing |

```go
var Tally = grist.NewProcessor(App, "tally", Added,
	func(ctx context.Context, r *grist.Reads, e ItemAdded, m grist.Meta) grist.Result {
		return grist.Emit(Tallied.Of(ItemTallied{Item: e.Item, Stock: Stock.For(r, e.Item), At: int64(m.Pos)}))
	}, grist.Emits(Tallied))

var Payout = grist.NewProcessor(App, "payout", Retired,
	func(ctx context.Context, r *grist.Reads, e CounterRetired, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			_, err := Pay.Do(ctx, PayRequest{Counter: e.Counter, Amount: e.Total})
			return err
		}, grist.AtMostOnce, grist.OnSuccess(Paid.Of(PaidOut{Counter: e.Counter, Retired: int64(m.Pos)})))
	}, grist.Delivers(grist.AtMostOnce, Paid), grist.Uses(Pay))
```

- Declare the result: `grist.Emits(types...)` or
  `grist.Delivers(guarantee, successType)`. Another result fails the
  trigger. The model's action for the processor is generated from it.
- An effect's key (`grist.EffectKey(ctx)`) is the same on every try: a
  dependency deduplicates by it.
- A handler or effect error is tried `grist.EffectTries(n)` times
  (default 3), then the trigger is checkpointed with a `grist.failed`
  event (`grist.Failed`); an at-most-once effect whose outcome is unknown
  records `grist.unknown` (`grist.Unknown`). Both are in the model.
- `grist.BatchSize(n)` (default 100) is how many triggers a poller reads
  at a time.
- A processor that needs a command run uses an `Internal` command (§2.9)
  and Emit, never a raw append to the store: a raw append is a
  `Mismatch` (counters' `dispatchDirect` mutant).

### 2.8 Dependencies and calls

A remote party an effect calls is a declared dependency with typed
calls. grist records the caller's view and the dependency's application
of each call, models both from the declaration, and in a simulation
serves the dependency as a lossy `qenv.Service`.

```go
var Bank = grist.NewDependency(App, "bank")

var ErrDeclined = grist.Rejection("declined")

var Pay = grist.NewCall[PayRequest, PayReceipt](Bank, "pay", grist.Keyed(), grist.Refuses(ErrDeclined))
```

- Requests and responses are structs of plain values. `grist.Keyed()`
  says the dependency applies each key once (the effect's key, sent with
  every try); `grist.Refuses` names its answers that say nothing
  happened (`Do` returns them as `grist.NotApplied` of a
  `*grist.Rejected`).
- The processor declares `grist.Uses(Pay)` beside its `Delivers`, and in
  its effect calls `Pay.Do(ctx, req)`. Do outside an effect, or of a call
  the processor does not list, is the effect's error.
- **Doubles.** In a simulation each dependency is served by its
  *default double* (`grist.DefaultHandlers(dep)`: a keyed call applied
  once per key, a retry answered from the first answer) when its calls
  answer no fields (`struct{}`). A call whose response has fields has no
  default: give one in `gristsim.Config.Doubles`, built with
  `Pay.Handler(func(ctx context.Context, r PayRequest) (PayReceipt, error) {...})`.
  A deduplicating double answering a retry from its first answer calls
  `grist.Replayed(ctx)`; `grist.CallKey(ctx)` is the key.
- **Transports**, attached where the node starts (`grist.Dependencies`
  in the binary, `TierConfig.Deps` in a test):
  `Bank.With(ctx, grist.OverHTTP(baseURL, nil))` calls
  `POST base/<call>` with the key as `Idempotency-Key` (2xx the
  response, 422 `{"rejected": name}` a refusal, anything else an
  unknown outcome; one attempt, never retried);
  `Bank.With(ctx, grist.Local(handlers))` calls handlers in process.
- **Serving a dependency your team owns**: `grist.ServeCalls(dep,
  handlers)` is its HTTP handler, which records its side;
  `grist.ServeDependency(dep, handlers)` has `grist.Main` run it beside
  the node on a loopback port. It authenticates nobody: serve it where
  only the App's nodes reach it.
- **A third party** (a real bank, a mail provider) records nothing on its
  side: judge the real tier with a test-only binding variant whose vals
  leave out the site claims (counters' `counters.real.binding.yaml`).
- What is no call (a clock, a config value, an SDK object) is a typed
  `grist.NewDep[T](name, fallback)`, read with `Dep.Of(ctx)` in an
  effect and set with `Dep.With(ctx, v)` where the node starts.
  `grist.NewSite[T](App, name)` records a double's own effect, an escape
  hatch no example needs.

### 2.9 Authorization and callers

Every command and query is denied by default. Each declares one of:
- `grist.Authorize(policy)`: served to the callers the policy admits.
  `grist.Authenticated` admits any caller; `grist.Callers("ops", ...)`
  the named ones; a `grist.Policy` is any `func(caller, tenant string) bool`.
- `grist.Internal()`: served by no adapter (the API answers 404, as for
  an unknown block); only processors and tests call it.

The caller is the authenticated principal: your authentication middleware
sets it (`grist.WithCaller(ctx, caller)` inside a `grist.Middleware`),
or, behind an authenticating proxy only, `grist.CallerFromHeader(name)`
takes it from a header. A policy sees only the caller and the tenant. A
rule about who asks (only a counter's creator may retire it) is the
decision's: read `r.Caller()`, which every command's and query's step
records as `caller`, so the model judges it too.

`grist.NewHandler` refuses an App with an open block:
"grist: inventory cannot be served: [add-item] declare neither Authorize
nor Internal, and every block is denied by default: give each
grist.Authorize(policy) (grist.Authenticated, grist.Callers(...)) or
grist.Internal()".

### 2.10 The catalog and Verify

`App.Catalog()` is the registry as data (`<app> catalog` prints it as
JSON): every event and version, read model and fingerprint, command with
its refusals and rules, query, processor with its shape, dependency and
call. The generator, the test harnesses, `catalog -diff` and the sizing
tool all read it. `App.Verify()` checks it (keys that are tags, rules on
appended events, declared shapes, upcasters, authorization) and names
every problem at once.

## 3. The HTTP API

`grist.NewHandler(app, store, opts...)` is a plain `http.Handler`
(net/http only):

```
POST /t/{tenant}/commands/{name}                  body: the arguments (JSON); header Idempotency-Key: the command id
GET  /t/{tenant}/queries/{name}?field=value...    the arguments by JSON name (a string as is, anything else as JSON)
GET  /catalog                                     with grist.DevEndpoints()
```

| code | command | query |
|---|---|---|
| 200 | the receipt `{"pos", "events": [{"tenant", "pos"}]}`; a retry's carries `Idempotent-Replayed: true` | `{"result", "pos"}` |
| 400 | no `Idempotency-Key`, a key grist refuses, or arguments it refuses (`grist.ErrBadArgs`: a field the type lacks, a required one missing, not UTF-8...) | the same |
| 403 | the policy refuses the caller (`grist.ErrDenied`) | the same |
| 404 | an unknown block, an Internal one, an unknown tenant | the same |
| 409 | a conflict after the condition's tries (the key's final answer) | |
| 413 | a body past `grist.MaxBody(n)` (default 1 MiB) | |
| 422 | a refusal `{"rejected": "full"}`, or `idempotency-key-reused` | a refusal |
| 503 | `{"retry": "outcome-unknown" \| "unavailable"}` with Retry-After: send it again under the same key | `unavailable` |
| 500 | the decision's own error | the same |

Every argument field is required unless it is a pointer. The handler
records nothing itself: a command's step is its commit's (relayed from the
store's step log), a query's its own.

**The client** (`grist/client`) calls the API:

```go
c := &client.Client{App: inventory.App, URL: "http://127.0.0.1:8080", CallerHeader: "X-Caller"}
env := grist.Envelope{ID: "k1", Tenant: "t1", Caller: "alice"}
err := c.Do(ctx, env, "add-item", []byte(`{"item":"a","qty":2}`))
```

`Do` records the call where the client sees it (actor
`<tenant>/clients`: invoked before the first request, done with the
answer), so the clients' binding judges what callers were told. It
sends a request that got no answer, a 503 or a 5xx that is not grist's
again under the same id, with jittered backoff (`RetryDelay` doubling to
`MaxRetryDelay`), at most `MaxAttempts` (50) times and while its
`Budget` holds a retry. It returns nil, the refusal (a
`*grist.Rejected`, `grist.ErrConflict`, `grist.ErrDenied`) or the last
error. `Command` and `Query` send one request each and record nothing.

## 4. The binary: grist.Main

An app's binary is one line:

```go
func main() {
	grist.Main(inventory.App, grist.Dependencies(func(ctx context.Context) context.Context {
		return inventory.Warehouse.With(ctx, grist.Local(grist.DefaultHandlers(inventory.Warehouse)))
	}))
}
```

It imports its database driver (`modernc.org/sqlite`,
`github.com/jackc/pgx/v5/stdlib`; grist imports none) and runs the
command its arguments name, exiting 2 on a usage error and 1 on any
other:

| command | what it does |
|---|---|
| `catalog` | the catalog as JSON |
| `catalog -diff deployed.json` | compares with the deployed release's catalog: `safe` (exit 0), `readers first` (exit 3), `breaking` (exit 4), each change with its fix (§7.3) |
| `verify` | `App.Verify` and `App.CheckUpcasters`, as the node does at boot |
| `model [-dir DIR] [-check] [-init]` | generates the model's skeleton, the bindings and grist.lock; `-check` writes nothing and fails on a difference (CI); `-init` writes a first `_rules.qnt` and `.qnt` where there are none |
| `serve -db DB [flags]` | the HTTP API and the node (§9) |
| `sizing [flags]` | runs `<app>-sizing`, the app's sizing binary (`gristload.Main`), found beside the binary or on PATH, so the app's binary links none of the load tooling (§9.6) |

Its options: `grist.Middleware`, `grist.CallerFromHeader`,
`grist.DevEndpoints`, `grist.Dependencies`, `grist.ServeDependency`,
`grist.MaxBody`, `grist.WithIdentity`, `grist.WithDrain`. A `kube`
command is designed, not built (docs/grist-platform.md).

## 5. The model: generated and written

The model is one tenant's log. `go run ./cmd/<app> model` writes, from
the catalog:

- `model/<app>_gen.qnt`: the events as Quint (a constructor per type,
  `itemAdded(item, qty)`, accessors `itemAddedQty(e)`, a read model's
  event filter `stockEvents(log, item)` and its read set `stockReads(item)`);
- `model/<app>_steps.qnt`: one action per command, query, processor,
  call and site, with its outcome branches and frame conditions, `step`,
  the store's module and the clients' module, and the **obligations**
  the catalog implies, eventual vals judged after a settle:
  `<processor>EventuallyHandled` (every trigger handled) and
  `<call>EventuallyServed` (every answered call of a keyed dependency
  served), with `<call>Admits` (what a keyed call may do on a retry);
- `model/grist.qnt`: grist's guarantees (conditions, triggers,
  checkpoints, effects at least and at most once), vendored;
- `<app>.binding.yaml` (the store's steps: actor the tenant, every
  commit's head and batch and every query's answer compared with the
  model's) and `<app>.calls.binding.yaml` (the clients' calls, actor
  `<tenant>/clients`, judged linearized), with generated `possible:`
  entries, one per command outcome, query, processor step, call and site;
- `grist.lock` (§7).

You write two files. **`model/<app>_rules.qnt`** holds the holes the
skeleton calls. Add a `remove-item` command (refusing `short`) with a new
event `item-removed` folded by `stock`, and the generator says:

```
inventory model: warning: stockEvents folds item-removed now, which it did not when model/inventory_gen.qnt was generated: every fold over it in model/inventory_rules.qnt must handle the new type (a foldl's else branch skips it silently)
inventory model: warning: read model stock folds otherwise than when grist.lock was written (its fingerprint changed: ...) ...
inventory model: model/inventory_rules.qnt does not define removeItemOutcome, removeItemEvents, removeItemNeeds, which the skeleton calls; in module inventoryRules, for instance:
  // remove-item's rule: the outcome its decision reaches on log ("ok" or
  // "short")
  pure def removeItemOutcome(log: List[Event], c: RemoveItemArgs): str = "ok"
  // the events remove-item appends when ok (their causes are stamped by its
  // action)
  pure def removeItemEvents(log: List[Event], c: RemoveItemArgs): List[Event] = []
  // the reads remove-item's rule needs for result: its committed step's read
  // set must cover each (covers)
  pure def removeItemNeeds(c: RemoveItemArgs, result: str): Set[Read] = Set()
```

Per command its outcome (`addItemOutcome`), events (`addItemEvents`)
and the reads it needs (`addItemNeeds`: the read set a correct decision
must cover); per rule its refusal and needs (`capacityRefusal`,
`capacityNeeds`); per query its answer; per processor its emits or
whether it skips a trigger, and the request it makes of each call it
uses (`shelveWarehouseReceive`); the read models' folds, and `STRS` and
`INTS`, the values the model on its own draws arguments from.

**`model/<app>.qnt`** holds the invariants, each marked for the bindings
that judge it:

```quint
  // grist: invariant
  val withinCapacity = withinCapacityIn(log)

  // grist: eventually
  val everyAdditionShelved = /* ... */
```

`invariant` holds in every state; `eventually` after a settle; `violable`
is expected to fail somewhere (a design-level claim). A name after the
mark (`// grist: invariant default retiring`) puts the val in the
bindings named: a name other than `default` is a test-only variant,
`<app>.<name>.binding.yaml` (counters' `once` binding judges a welcome
mailed exactly once, which the app does not promise).

Check the model alone before any code runs:

```sh
go tool gotogether simulate -binding inventory.binding.yaml
```

It samples the model, checks the invariants, and reaches each generated
`possible:` entry or fails: a model whose rules can never refuse `full`
fails here, before a test could pass vacuously.

When a block changes, regenerate, and read every warning: the first
above says `stock`'s Quint fold must now subtract removals, which no
check would notice otherwise (removals would never lower the model's
stock, and the app's and the model's would agree only while no run
removed anything). An event appended and folded by no read model gets
no warning: check that its read models fold it.

## 6. Testing

The scaffold's tests are the pattern. The bindings are constants:

```go
const (
	commits = "inventory.binding.yaml"
	calls   = "inventory.calls.binding.yaml"
)
```

### 6.1 Generated

```go
func TestGenerated(t *testing.T) {
	if qtest.Mutant() != "" {
		t.Skip("a mutant's binary: its catalog is not the app's")
	}
	gristtest.Generated(t, App)
}
```

It fails when the checked-in generated files are not what the catalog
generates now, naming each and the fix (`go run ./cmd/<app> model`, or
`GRIST_UPDATE=1 go test -run '^TestGenerated$'`), or when a hole is
missing.

### 6.2 Explorations

`gristsim.System(binding, App, cfg)` is a `qtest.System`: each run
deploys the app on a simulated store, its clients calling it, under the
faults the rates choose; every run is judged by the binding.

```go
var faulty = qenv.DefaultRates.Set("nemesis", 0.02).Set("store.inventory.commit", 0.15)

var contended = gristsim.Config{Tenants: []string{"t1"}, Clients: 3, Ops: 4, CommitFaults: true, Alphabet: []string{"a", "b"}}

func TestFaithful(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	gristsim.System(commits, App, contended).WithRates(faulty).
		Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 3}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()),
			qtest.Reaches("store.inventory.commit").Faulted(), gristtest.Catalog(App).Exercised())
}

func TestFaithfulAtClients(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	gristsim.System(commits, App, contended).WithRates(faulty).WithBinding(calls, qtest.Linearize()).
		Explore(t, qtest.ExploreConfig{Seeds: 20, ShortSeeds: 3}).Expect(t, qtest.Conforms())
}
```

`gristsim.Config` chooses the workload and the tier:

| field | what it sets |
|---|---|
| `Tenants`, `Clients`, `Ops` | how many tenants, clients per tenant, operations per client |
| `Send` | which commands and queries clients send (a name listed twice is sent twice as often) |
| `Alphabet`, `Ints`, `Gen` | the arguments: strings and integers drawn from these, or a generator per block |
| `Prologue` | calls made in order, each answered, before any client's first (a member joined before borrows) |
| `Isolation` | the store's level (Serializable, ReadCommitted) |
| `CommitFaults` | commits of unknown outcome (`store.<app>.commit`) |
| `SQL` | the app on grist/sqlstore over qsql: the real tier's statements |
| `HTTP` | the API on a simulated network, clients through `grist/client` |
| `Nodes`, `Cluster` | several nodes; a cluster with a lagging router, drains, a rolling restart (§9.5) |
| `Doubles`, `Setup` | dependencies' handlers per run; a `NewDep`'s value |
| `SnapshotEvery` | decisions made from snapshots (§8) |

Fault kinds are named as reports print them: `nemesis` (crashes, per
scheduling step), `store.<app>.commit`, `store.<app>.snapshot`,
`rpc.api.request`, `rpc.api.delay`, `rpc.api.response` (the HTTP tier),
`rpc.<dependency>.*`, `grist.<processor>.effect` (a scripted crash before
or after an effect: `qenv.Script`).

Under `-short` an exploration runs `ShortSeeds` and tops up while a kind
its rates set was reached and faulted by no run. A failing run's report
prints `reproduce:` (one seed alone, `GOTOGETHER_RUN=...`) and
`navigate:` (`gotogether nav`, the run step by step against the model).

### 6.3 Exercised and the catalog

A run that reaches none of the app's blocks passes every other claim.
`gristtest.Catalog(App).Exercised(except...)` claims every block was
taken in some run: each command ok and refused by each declared refusal
(its rules' too), each query answered, each processor's handling, effect
and checkpoint, each call and site. A miss names how to reach it:

```
addItem with outcome full (0 of 3 runs): no add-item was refused full by its rules (capacity): send it where its decision says ok and a rule refuses what it appends (Config.Gen's arguments, the state Config.Prologue leaves), or except "addItem.full" with the reason
```

`except` names a block (`"receipts"`), a command's outcome
(`"borrow.limit"`) or a command whole, each with its reason in a
comment. `gristtest.Catalog(App).Take("add-item")` claims one block
(`.InAtLeast(share)` in a share of the runs). A contrast test with a
narrow workload claims the one outcome it needs instead:
`qtest.Takes("borrow").Outcome("limit")`.

### 6.4 Scenarios and possibles

An acceptance scenario is written in the catalog's own values:

```go
var fillUp = gristtest.Scenario("fill up",
	Add.With(AddItem{Item: "a", Qty: 3}),
	Add.With(AddItem{Item: "a", Qty: 3}).Refused(ErrFull))

func TestScenariosPossible(t *testing.T) {
	qtest.Model(t, commits).Expect(t, qtest.Possible(fillUp))
}
```

`qtest.Possible` asks the model on its own whether it can follow the
scenario (arguments within `STRS` and `INTS`): a model too strict for it
fails before any run is judged. Runs then claim it like any expectation,
with its quantifier: `qtest.Some(fillUp)`, `qtest.All(fillUp)`,
`qtest.None(fillUp)` (alone it panics, naming them). A miss
says, per run, how far it got.

### 6.5 Mutants

A mutant is a file in `testdata/mutants/<name>/` replacing the app's
file of the same name (an overlay). Each rule also gets a generated one,
`drop-<rule>`, the app with that rule dropped:

```go
func TestMutant(t *testing.T) {
	mutant := qtest.Mutant()
	if mutant == "" {
		t.Skip("the app as it is: TestMutants runs this test built with each mutant's overlay")
	}
	qtest.RequireQuint(t)
	if _, caught, ok := gristtest.DroppedRule(App); ok {
		gristsim.System(commits, App, fill).WithRates(qenv.NoFaults).
			Explore(t, qtest.ExploreConfig{Seeds: 6, ShortSeeds: 3}).Expect(t, caught)
		return
	}
	switch mutant {
	case "noInvalid":
		gristsim.System(commits, App, contended).WithRates(qenv.NoFaults).
			Explore(t, qtest.ExploreConfig{Seeds: 6, ShortSeeds: 3}).Expect(t, qtest.Some(qtest.Rejects("addItem")))
	default:
		t.Fatalf("mutant %s: say what catches it (a case of TestMutant)", mutant)
	}
}

func TestMutants(t *testing.T) {
	t.Parallel()
	qtest.Mutants{Generated: gristtest.RuleMutants(App)}.Run(t)
}
```

`TestMutants` builds the test binary once per mutant and runs
`TestMutant` in it. Pair every mutant with a faithful test at the same
configuration claiming the opposite (`qtest.None(qtest.Rejects("addItem"))`),
so the claim is shown to be about the bug. What each kind of bug looks
like:
- a commit the model forbids (a check left out, an `Unconditioned`
  create racing): `Rejects("<command>")`;
- a step whose events or head differ from the model's (a raw append from
  a processor, a fold disagreeing with its def): `Mismatch("<block>")`;
- an effect's duplicate or loss the guarantee admits: a test-only
  eventual val in a variant binding, `Violates("<val>")`;
- a declaration contradicting the model (an effect declared at most once
  where the model says at least once): rejected at its first step,
  before what it would cause.

### 6.6 The real tier

`gristtest.NewTier(t, App, cfg)` runs the app on a SQLite file served
over real HTTP, its clients calling through `grist/client`, and judges
the step files with the same bindings:

```go
func TestRealHTTP(t *testing.T) {
	qtest.RequireQuint(t)
	warehouse := grist.Local(grist.DefaultHandlers(Warehouse))
	tier := gristtest.NewTier(t, App, gristtest.TierConfig{
		Deps: func(ctx context.Context, _ grist.Store) context.Context { return Warehouse.With(ctx, warehouse) }})
	tier.Start("node")
	ctx := qobs.Thread(tier.Clients())
	tier.Do(ctx, grist.Envelope{ID: "c1", Tenant: "t1", Caller: "alice"}, "add-item", AddItem{Item: "a", Qty: 2})
	tier.Settle(func(ctx context.Context) {
		tier.Do(ctx, grist.Envelope{ID: "probe", Tenant: "t1", Caller: "probe"}, "show-item", ShowItem{Item: "a"})
	})
	tier.Check(commits, calls)
}
```

`TierConfig.LoseEvery` loses every nth answer after it was served (the
client retries under its key); `Serve` serves a dependency over HTTP
with its own step file; `Current().Kill()` kills a node and `Start`
starts another. `Check` judges both bindings and that every answer a
client got has its store-side step (`gristtest.Answered`).

### 6.7 Versions and snapshots in tests

- `gristtest.Upcasts(t, App)`: every upcaster total, pure, keeping tags
  and fields (§7).
- `gristtest.Snapshots(t, App, store)`: every stored snapshot, and one at
  each position, plus the events after it, equal a fold from the start
  (§8).
- `gristtest.Upgrade(t, binding, deployed.App, App, cfg)`: a rolling
  update from the deployed release (§7.3).

### 6.8 What it costs

The scaffold's suite: about a minute under `-short`, about six without.
Judging a run costs more than simulating it: counters judges a run of
about 200 steps in about 6 s, most of it quint's (a passing run is
decided by gotogether's Go evaluator, a failing one by quint). Keep
`-short` per push and the full run nightly (docs/ci.md).

## 7. Versions: changing an event

A stored event never changes: a version 1 event stays version 1 in the
log, and every read upcasts it to the newest struct before any fold,
rule or handler sees it.

### 7.1 Upcasters and grist.lock

`go run ./cmd/<app> model` keeps `grist.lock`: every version of every
event type the app has stored, by shape (its fields' JSON names and
schema types, its tags), and every read model's fingerprint. Commit it.
A version's shape never changes, except by a field added (old events
read it as zero, old nodes ignore it), which needs no new version. Any
other change is a new version with an upcaster:

```go
var Added = grist.NewEvent[BookAdded](App, "book-added",
	grist.Upcast(1, func(v BookAddedV1) BookAdded {
		return BookAdded{Book: v.Book, Count: v.Copies, Shelf: "main"}
	}))
```

The rules:
- one upcaster per old version, each to the next, never edited again (a
  third version adds `grist.Upcast(2, ...)` from a `BookAddedV2`);
- pure and total: Verify runs it over generated values at boot;
- every tag kept, key and value (the store indexed the event by them: a
  tag that must change is a new event type);
- every field of the old struct carried to the next unless it says
  `.Drops("field")`.

A stored version newer than the app knows (a version 2 event read by a
version 1 node during a rollout) is a `*grist.NewerVersionError`: the
node's command or query answers 503 (a newer node serves the retry) and
its processor waits.

### 7.2 Tutorial: book-added from version 1 to version 2

examples/library did this. Version 1:

```go
type BookAdded struct {
	Book   string `json:"book" grist:"tag"`
	Copies int    `json:"copies"`
}
```

Version 2 renames `copies` to `count` and adds the shelf.

**1. Keep a log of the deployed version.** Before changing anything,
record a history with the app as deployed and keep it in testdata
(library's `TestRecordLog`, run once with `LIBRARY_RECORD_LOG=testdata/logs/v1.jsonl`;
never record over it):

```go
st := gristtest.Capture(memstore.New(App.Name))
/* st.CreateTenant, then App.Execute for each command of the history */
if err := gristtest.WriteLog(st.Log(), path); err != nil {
	t.Fatal(err)
}
```

**2. Change the struct in place, and see the generator refuse.**
`go run ./cmd/library model`:

```
event book-added version 1 changed shape since grist.lock was written (fields [book string tag, copies integer], then [book string tag, count integer]): its stored events would decode into the new struct silently (a renamed field reads as its zero value). Keep the old struct (BookAddedV1), and register the new shape as version 2 with an upcaster from it: grist.NewEvent[BookAdded](App, "book-added", grist.Upcast(1, func(v BookAddedV1) BookAdded { ... })), or Upcast's chain extended by one. If no event of version 1 was ever stored (it was never deployed), delete its entry from grist.lock
```

**3. Do what it says.** The old struct under its own name, its fields
and tags exactly as stored, and the upcaster (library's `upcast.go`):

```go
type BookAddedV1 struct {
	Book   string `json:"book" grist:"tag"`
	Copies int    `json:"copies"`
}

type BookAdded struct {
	Book  string `json:"book" grist:"tag"`
	Count int    `json:"count"`
	Shelf string `json:"shelf"`
}

var upcastBookAdded = grist.Upcast(1, func(v BookAddedV1) BookAdded {
	return BookAdded{Book: v.Book, Count: v.Copies, Shelf: MainShelf}
})

var Added = grist.NewEvent[BookAdded](App, "book-added", upcastBookAdded)
```

**4. Regenerate.** grist.lock records both versions; the generated
model knows only the newest (`bookAdded(book, count, shelf)`), and the
generator's warnings name the folds and rules reading it: fix them in
`model/library_rules.qnt`.

**5. Check the upcaster.** `gristtest.Upcasts(t, App)`. An upcaster that
forgets `Copies` (library's `dropsCopies` mutant) fails:

```
event "book-added": the upcaster from version 1 ignores copies (library.BookAddedV1.Copies): every event stored at version 1 loses it on the way to version 2; copy it to the next version's field (renamed, converted), or say it is dropped on purpose: Upcast(1, ...).Drops("copies")
```

**6. Judge the old log by today's model.** The node relays the old
steps with their events re-rendered in today's shapes, so the store's
binding judges the version 1 log and today's steps after it:

```go
func TestOldLogConforms(t *testing.T) {
	qtest.RequireQuint(t)
	log := gristtest.ReadLog(t, "testdata/logs/v1.jsonl")
	tier := gristtest.NewTier(t, App, gristtest.TierConfig{Log: &log})
	tier.Start("node")
	/* today's calls, then tier.Settle(...) */
	tier.Check(commits, calls)
	gristtest.Snapshots(t, App, tier.Store)
}
```

With `dropsCopies` the old log is a `Mismatch("addBook")` at its first
addition: the upcast count is 0 where the command added 2.

**7. Snapshots.** A read model folding book-added has a new fingerprint
(the event's shape is in it, and the upcasters too: fixing an upcaster
refolds), so the deployed version's snapshots are never read, and the
first reads fold from the start and take new ones. `gristtest.Snapshots`
checks the snapshots of the old log agree with a fold from the start.
grist's own `snapshotPastItsPosition` mutant (a snapshot labelled one
position past what it folded) is what that check catches.

**8. Roll it out readers first** (next section).

### 7.3 A new version in a rolling update: readers first

A release that adds a version must not write it while nodes of the
release before it run: they would refuse its events. It ships in two
releases:

1. **Release N+1 reads version 2 and writes version 1**: the upcaster,
   plus a downcaster:

   ```go
   var Added = grist.NewEvent[BookAdded](App, "book-added",
   	grist.Upcast(1, func(v BookAddedV1) BookAdded { return BookAdded{Book: v.Book, Count: v.Copies, Shelf: "main"} }),
   	grist.WritesVersion(1, func(v BookAdded) BookAddedV1 { return BookAddedV1{Book: v.Book, Copies: v.Count} }))
   ```

   Blocks decide in the newest shape; the append is downcast, and fails,
   naming the fix, when version 1 cannot hold the event (a shelf other
   than "main"): in this release, decide only what version 1 can say.
2. **Release N+2 drops `WritesVersion`** once no node of release N runs.

Before each release ships:
- `go run ./cmd/<app> catalog -diff deployed.json`, the deployed
  release's `<app> catalog` kept in CI: `safe` (exit 0), `readers first`
  (exit 3: a version written at once, a new event type) or `breaking`
  (exit 4: a version or an event removed, a shape changed in place, a
  tag, a block removed).
- `gristtest.Upgrade(t, binding, release1.App, App, cfg)`, the deployed
  release kept as a package of its own (counters' `release1`), explored
  with a rolling `gristsim.Cluster` so runs pass through every mix of
  nodes:

```go
func TestRollingUpdate(t *testing.T) {
	up := gristtest.Upgrade(t, "shop.binding.yaml", release1.App, App, rolling)
	up.Explore(t, qtest.ExploreConfig{Seeds: 10, ShortSeeds: 4}).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	up.WithBinding("shop.calls.binding.yaml", qtest.Linearize()).Explore(t, qtest.ExploreConfig{Seeds: 10, ShortSeeds: 4}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Completes()), qtest.All(gristtest.ReadersFirst()))
}
```

`gristtest.ReadersFirst()` claims no call was refused by an older node
for an event it cannot read; a release writing the new version at once
shows its negation in some run, `qtest.Some(qtest.Not(gristtest.ReadersFirst()))` (counters' `eagerV2`
mutant).

## 8. Snapshots

A read folds from the read model's latest snapshot (its state as JSON at
a position) and the events after it; a read that folded more than
`grist.DefaultSnapshotEvery` events (1000) takes a new one. A read model
with fewer events reads as it always did. Snapshots live in the store
(`grist.SnapshotStore`: memstore, simstore, sqlstore's `grist_snapshots`
table), keyed by the read model's **fingerprint**: its name, version,
keys, the shapes of the events it folds, its upcasters and what its fold
makes of generated events. A fold changed in a way generated events show
gets a new fingerprint by itself; bump the read model's version when the
fold's meaning changes in a way they may not (the generator warns when
the fingerprint changed at the same version). A snapshot whose checksum
fails, past the read's position, or of another fingerprint is never
read.

- `grist.SnapshotEvery(ctx, n)` changes the threshold for reads under
  ctx (`grist.Dependencies` sets it for a node; 0 turns snapshots off).
- A deployed node audits one in `grist.DefaultSnapshotAudit` (100) reads
  from a snapshot: it refolds the key, compares, answers from the fold
  and replaces a wrong snapshot. `grist.SnapshotAudit(ctx, n)` changes
  the rate; alert on `grist_snapshot_audit_mismatches_total`.
- In tests: `gristtest.Snapshots(t, App, st)`, and
  `gristsim.Config{SnapshotEvery: 2}` with `store.<app>.snapshot` faults
  (torn, lost) claimed `qtest.Reaches("store.<app>.snapshot").Faulted()`.

examples/library's `TestSnapshotsMakeStartFast`: a fresh node's first
read of a member with 10⁴ events folds all of them without snapshots,
and at most 1000 with them.

## 9. Running in production

### 9.1 Serving

```sh
inventory serve -db sqlite:/data/inventory.db -tenant t1 -steps /data/steps/ -health-addr :8081 -grace 30s
```

| flag | what it sets |
|---|---|
| `-db sqlite:PATH`, `-db postgres:DSN` | the store (grist/sqlstore; the binary imports the driver) |
| `-addr HOST:PORT` | where to listen (default 127.0.0.1:8080; in a pod a loopback address is refused) |
| `-health-addr HOST:PORT` | `/livez`, `/readyz`, `/metrics` on a side port (default :8081 in a pod, else off) |
| `-grace DURATION` | the orchestrator's kill deadline after SIGTERM, which the drain is budgeted into |
| `-tenant NAME` | creates the tenant if it does not exist (repeatable) |
| `-steps PATH` | records every step for validation; a directory gets a file per node and start, `<app>.<node>.<incarnation>.jsonl` (in a pod give a directory: a restart truncates a file) |
| `-caller-header NAME` | the caller from a header: behind an authenticating proxy only |
| `-poll DURATION` | how often the store reads other processes' appends (default 250ms) |
| `-dependency NAME=URL` | calls dependency NAME over HTTP at URL (`grist.OverHTTP`) |

Several processes may serve one database: each appends under the
tenant's head row (positions dense, in commit order), each runs every
processor's poller, and a processor's checkpoint is moved by one of them
at a time. A tenant's appends serialize on that row, by design.

The startup line on stderr says what the node is and what its drain
needs: `inventory: node inventory-0, incarnation 3f2a...; drain needs a
grace of ...; GOMEMLIMIT ...`. With GOMEMLIMIT unset, the runtime's
memory limit is 90% of the cgroup's.

The database's schema is created at boot (`CREATE TABLE IF NOT EXISTS`);
the DSN is a flag.

### 9.2 Identity

A node's steps are recorded under `<app>/<node>/<incarnation>`. The
node is `GRIST_NODE`, else `POD_NAME`, else the host name; the
incarnation is fresh on every start; `POD_NAMESPACE`, `NODE_NAME` and
`GRIST_ZONE` are printed. Never name a node by its process id (it is 1
in every container): two pods under one recorder make their merged
step files unjudgeable (counters' `pidIdentity` mutant: "two different
records", no verdict). `grist.WithIdentity` sets it elsewhere.

### 9.3 Probes and metrics

`/livez` fails when a poller has worked on one trigger far longer than
any handler takes (a deadlock), `/readyz` until the store has answered
and the node runs, and from the first SIGTERM. `/metrics`, in
Prometheus' text format:

| metric | what it says |
|---|---|
| `grist_ready`, `grist_draining`, `grist_store_up` | the node's state |
| `grist_api_requests_in_flight`, `grist_effects_in_flight` | work in hand |
| `grist_relay_backlog` | committed steps not yet recorded to the steps file |
| `grist_processor_lag`, `grist_processor_oldest_trigger_age_seconds` | per processor and tenant: the head minus the checkpoint while a trigger waits, and its age |
| `grist_snapshot_audits_total`, `grist_snapshot_audit_mismatches_total` | §8 |

### 9.4 The drain and -grace

After SIGTERM (`grist.Drain`, `grist.WithDrain`):
1. **Delay**: `/readyz` fails at once and the server keeps serving
   (`grist.DefaultDrainDelay`, 5 s, on an orchestrator): endpoints take a
   few seconds to stop sending a terminating pod requests.
2. **Shutdown**: the server drains its requests.
3. **Settle**: once the processors are idle and the relay caught up, a
   settle is recorded in the steps file, where the bindings' eventual
   vals are judged.
4. **Flush**: the node stops and the relay records every committed step.

`-grace` (the pod's `terminationGracePeriodSeconds`) budgets the phases
from the deadline backwards: the flush keeps its share first, at most
half the grace, the rest shared in proportion. The startup line says
what the drain needs; give a grace above it. A second signal ends the
process at once. A processor's effect in flight finishes: never derive
the node's context from `grist.Terminating` (counters'
`stopNodeAtSignal` mutant: a payout cancelled mid-call, its outcome
unknown though the bank was asked).

### 9.5 Rolling restarts

`gristsim.Config.Cluster` simulates one: nodes behind a router whose view
lags (`EndpointLag`), each drained as the binary drains (`Drain`, `Grace`),
`Disruptions` at a time, `Rolling` from a point the run chooses. Claim
`qtest.All(qtest.Completes())` and `qtest.All(gristtest.NoClientErrors())` at the
clients: with a drain delay shorter than the router's lag, tries are
refused (`noDrainDelay`: `qtest.Some(qtest.Not(gristtest.NoClientErrors()))`).
examples/counters runs the same restart on three real pods
(`TestRealRollingRestart`, `ROLLING=1 ./run-demo.sh`).

### 9.6 Sizing

Write `cmd/<app>-sizing/main.go` as `func main() { gristload.Main(<pkg>.App) }`,
then `<app> sizing -steps steps.jsonl -rate 60/s -burst 3x30s` measures
each command's, query's and call's service time from a recording of the
real tier and writes `deploy/sizing.yaml`: retries per layer, timeout,
replicas, a hot tenant's limit, processors' lag, pool, grace, CPU and
memory, each with its reason, formula, margin and caveat. A gateway's
retries multiplying the client's are advised against (counters'
`postRetries3`).

### 9.7 Validating production's steps

Every step file `serve -steps` writes is judged by the same bindings:

```sh
go tool gotogether validate -binding inventory.binding.yaml /data/steps/*.jsonl
```

Merge the clients' recordings (`grist/client`) to judge
`inventory.calls.binding.yaml` too; without them that binding has no
verdict ("NO VERDICT: checks the actors */clients").

## 10. Troubleshooting

| You see | It means | Do |
|---|---|---|
| `inventory model: model/inventory_rules.qnt does not define X, which the skeleton calls` | a new block's hole | paste the stub into `model/<app>_rules.qnt`, then write the rule |
| `XEvents folds T now, which it did not when ... was generated` | a fold's inputs changed | handle T in every fold over XEvents (a foldl's else skips it silently) |
| `generated files differ from what the inventory App's catalog generates` | a block changed without regenerating | `go run ./cmd/<app> model`, commit |
| `event X version N changed shape since grist.lock was written (fields [...], then [...])` | a stored version's struct changed | keep the old struct as `XVN`, add version N+1 with an upcaster (§7.2); if never deployed, delete its lock entry |
| `the upcaster from version N ignores F` | an old field reaches no later version | copy it, or `.Drops("F")` on purpose |
| `... an upcaster keeps every tag (key and value), and a tag that must change is a new event type` | the upcast changed a tag | keep it; a new tag is a new event type |
| `X is version N, and this app reads X up to version M: a newer version of the app appended it` | a node older than the event | finish the rollout; next time ship readers first (`grist.WritesVersion`, §7.3) |
| `read model R folds otherwise than when grist.lock was written` | its fingerprint changed at the same version | expected after a fold change; bump the version if its meaning changed |
| `its stored snapshot plus the events after it gives ...; a fold from the start gives ...` | a snapshot of another fold, or taken at the wrong position | a fold changed without a new fingerprint: bump the read model's version |
| `cannot be served: [...] declare neither Authorize nor Internal` | an open block | `grist.Authorize(...)` or `grist.Internal()` on each |
| `record <id> is not a transition of the model` with `reject <command>` (`Rejects`) | a commit the model forbids | `the model expects:` is the model's answer to that command (its outcome, what it would append); then the decision, its rule, its condition (`Unconditioned`?), a read the needs say it must cover |
| the failing `record:` line: `counters/counters-0/fc95… (node counters-0, incarnation fc95…)` or `grist/counters/t1` | who recorded the step: a node's API (its pod and start), or the relay of the store's commits for tenant t1 (no pod: the store does not say which node committed) | the pod's logs at the record's time; for a commit, the command's id in its arguments |
| `validate -json`, `-report junit=F` / `sarif=F` | the same report as data (`gotogether.report/1`) and for CI | route alerts on `verdict` and `reason`; `brief` is the line for the page |
| `show a mismatch after <block>` (`Mismatch`) | head, batch or answer differ from the model's | a raw append, a fold disagreeing with its def, a query answering otherwise |
| `X with outcome Y (0 of N runs): no X committed: send it where its rule says ok (gristsim Config.Prologue ..., Config.Gen ..., a name listed twice in Config.Send ...)` | the workload never reached it | widen `Send`, `Prologue`, `Gen`, or except it with the reason |
| `do not settle`, naming tasks | work left at the end of a run | a node left down, a dependency that never answers, a trigger no poller reaches |
| `grist: conflict: the decision's reads changed on every try` (409) | contention past `ConditionTries` | the key's final answer: retry under a new id, or read less (a keyed read model) |
| `grist: the command's outcome is unknown (an append may have committed): retry it with the same id` (503) | an append's commit was not confirmed | retry with the same id; the marker answers |
| `NO VERDICT`, `checks the actors */clients` | the calls binding judges clients' recordings | record the clients (`grist/client`) or validate the commits binding alone |
| `missing go.sum entry` running `gotogether` | the CLI is not a tool of the app | `go get -tool github.com/chucklehead-dev/gotogether/cmd/gotogether`, then `go tool gotogether` |
| `two different records` from validate, no verdict | two nodes recorded under one recorder id | a node identity per pod (`POD_NAME`, §9.2) |

When a run fails, follow its `navigate:` line: `gotogether nav` shows
the model's state at every step, what was enabled, and the first false
conjunct of a disabled action. Do not guess what the model admits; ask
it (`gotogether nav -binding <app>.binding.yaml -json`).

## 11. What is not built

Designed in [grist.md](grist.md) and
[grist-platform.md](grist-platform.md), not in the code yet (their
names are marked `not built` there): periodic tasks and screens (grain's
slice 3), dispatching a command from a processor in one step, a
condition narrowed to some reads, Kubernetes manifests and the `kube`
command, configuration from secrets' files, store failover
(`ErrRewound`), retention of steps and markers, schema migrations.
ROADMAP's I24 and I25 are the next for operations.

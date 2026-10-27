# Changelog

Every published module (the core, qhegel, qweave, qanalysis, qgosim, grist) is
versioned together; the examples are not published. A change to the
core's exported API updates `api/core.txt` and adds a line here.

## Unreleased

The first release. What a user meets, by module:

- grist, the default double (GR3's slice 2, docs/grist.md §0.13;
  api/grist.txt): `grist.DefaultHandlers(dep)`, a dependency's handlers
  applying a `Keyed` call once per key (a later try replayed from the
  first answer) and an unkeyed one every time, each acknowledged; gristsim serves it to a dependency `Config.Doubles`
  gives none (until now the run's error). `grist new`'s starter declares
  a dependency. examples/library's and examples/counters' mailers and
  examples/wallet-grist's ledger are declared calls served by it (their
  hand-written doubles, sites and dedupe rules gone).
  After its review (docs/grist.md §0.13): the generated `<c>Served`
  judges its request against the processor's rule, as the caller's
  action does (a caller crashed during the call left the served record
  the request's only one, unjudged); `grist.DefaultHandlers` panics, and
  gristsim's run is an error, for a call whose response has fields,
  which a default double cannot know (it answered the zero response).
- grist, declared calls on the simulated tier (GR3's slice 1,
  docs/grist.md §0.12; api/grist.txt): `grist.NewDependency`,
  `grist.NewCall[Req, Resp]` (`Keyed`, `Refuses`), `grist.Uses` on a
  processor (enforced), `Call.Do` in an effect (the caller's view
  recorded as an invocation and its completion), `Call.Handler`,
  `grist.Handlers` and `grist.CallHandler`, `grist.Local`,
  `grist.Transport`, `grist.CallInfo`, `grist.CallKey`,
  `grist.Replayed`, `App.Dependencies`, the catalog's `Dependencies` and
  processors' `Uses`; the site's record placed at its cause (2·H+1);
  `gristsim.Config.Doubles` and `gristsim.Doubles` (each dependency a
  `qenv.Service` named after it), `gristsim.DependencyTimeout`; the
  generator's call types, variables, caller and site actions, rule hole
  `<p><Call>` and generated `<c>Admits`; grist.qnt's `CallKey`,
  `callKeyStable`, `keyCalled`; `gristtest.Exercised` taking each call.
  examples/counters' bank is a declared call (its `BankDep`, `SimBank`,
  `BankPays` gone).
  After its review (docs/grist.md §0.12): an answer a handler gives with
  `grist.Replayed` is recorded as `<c>Replayed` and judged by the
  generated action of that name (an earlier served record, answer alike);
  `grist.Local` never places a key's site record before an earlier one
  of the key; `Dependency.Calls` and `Call.Dependency` removed.
- grist, the library trial's left items (docs/grist.md §0.11;
  api/grist.txt): `gristtest.Exercised(app, except...)` and
  `gristtest.Takes(app)` (every block of the catalog taken, each command
  by `ok` and each declared refusal, each missed named with how to reach
  it; the starter's `TestFaithful` claims it); `gristsim.Config.Prologue`
  and `gristsim.Call` (calls in order before the clients start); a
  Delivers processor's Skip or Fail recorded as its skip step
  (`<p>Skip`), with a generated hole `<p>Skips` and action; a refusal of
  a command that declares none carries an error naming `grist.Refuses`.
- qtest: `Take.Outcome(o)` (a run counts only if it took the action by a
  step recorded with outcome o: a contrast test claims the refusal it
  reached), `Take.Hint` (said when no run took it), `Take.Name`, and
  `TakesEach(takes...)` (`TakeEach`: every one holds, each one missed
  named with its hint). An exploration's summary lists the untaken
  actions the binding names apart from the model's others, the first
  never cut short.
- grist's generator and onboarding (docs/grist.md §0.9; api/grist.txt):
  `grist.Generate` and `grist.Starter`, an app's binary's `model` command
  (`grist.Main`: `-check`, `-init`), writing the model's skeleton
  (`model/<app>_gen.qnt`), the vendored grist.qnt and the bindings of
  both sides and of test-only variants from the App's catalog, the app
  writing only its rules and its marked invariants (`// grist: invariant`);
  the declarations it reads, enforced at run time: `grist.Refuses` on a
  command, `grist.Emits` and `grist.Delivers` on a processor (the
  catalog's `Refuses`, `Shape`, `Emits`, `Delivery`, `Success`);
  `grist.NewSite` (a dependency's record of its effect at its site, in
  the catalog's `Sites`) and `grist.NewDep` (typed dependencies);
  grist.qnt's `UNCAUSED`, `caused`, `triplesOf`. Package `grist/gristtest`:
  `Generated` (the generated files' CI diff), `Mutants`, `Mutant`,
  `NewTier` (the real tier: `Start`, `Kill`, `Do`, `Settle`,
  `Check`), `Answered`. `grist/cmd/grist`: `grist new <name>`, an app
  module green on its first run. examples/counters and
  examples/wallet-grist are generated and use the harnesses. Its review
  (docs/grist.md §0.10): an app named `grist` is refused (its model was
  the vendored library's file, overwritten by every `model`); `Refuses`
  refuses a rejection named `ok`, `conflict`, `denied` or `error`;
  `gristtest.Answered` matches commands by tenant and id, skips denied
  calls and allows a key answered twice or reused; `model` without the
  app's model says `-init`. grist.qnt's read sets are sets
  (`readsOf`, `triplesOf`, `covers`, `conditionHolds` over
  `Set[Read]`; a command action's `reads: Set[(str, str, str)]`), and
  each branch of a generated `step` draws its own arguments, so the
  model is in the Go evaluator's fragment. A field no step records (a
  list, a struct) of an event, arguments, a result or a site record is
  the generator's error unless tagged `grist:"-"`; `grist new -replace`
  checks the checkout first and writes the replace relative to the
  module. qtest: a claim `Violates("v")` of an eventual val says to name
  it `validate.EventuallyName("v")`.
- A binding's module may re-export another whole (`export lib.*`): its
  actions, variables and vals are the exported module's too
  (`binding.LoadModel` follows the exports transitively, its own names
  winning), so a generated model skeleton (grist model) is bound through
  the application's module that exports it.
- A run an external driver made, read as a request log: the protocol
  `gotogether.requests/1` (JSONL a driver writes: a header, then
  `request`, `response` and `step` records) and package `qdriver`
  (`Read`, `ReadFile`, `New`, `Log.Steps`, `Log.Write`, `Sniff`): each
  request an invocation record, completed only by its own response.
  `gotogether validate` and package input read it by its header
  (`input.Requests`, `-format requests`). Package `qdriver/bombadil`
  reads Bombadil's trace with a page that logs its requests (`Log`,
  `LogFile`, `Options`; `States`, `State` and `Request` experimental);
  it was examples/webjobs' bombadiltrace. adapters/playwright writes the log from a Playwright
  page's routed requests (DESIGN §55).
- grist, a new module (api/grist.txt): an event-sourced framework whose
  applications are checked against their Quint model with nothing
  annotated (docs/grist.md, slice 1): events, read models, commands
  conditioned on their pinned reads and idempotent by command id,
  queries, processors (`Emit`, `Skip`, `Fail`, `Effect` at least once),
  the stores `memstore` and `simstore` with the contract suite
  `storetest`, and `gristsim.Run` for a `qtest.System`;
  examples/counters is its first application.
- grist, after slice 1's review (docs/grist.md §0.2; api/grist.txt): a
  key's first answer is final (a rejection commits its marker,
  `store.Marker.Outcome`, and a conflict too; a late duplicate gets the
  same refusal), the marker's hash covers the command's name and
  version; reads are checked against the App (an unregistered type, a
  tag key the type lacks, another App's event are the decision's error;
  `NewKeyedReadModel` refuses a key that is no tag; `Exists` takes an
  `EventName`, which only an `*EventType` is); `ErrOutcomeUnknown` and
  `ErrUnavailable` when the transient budget runs out (the marker read
  once more first); the zero `Result` and `Option` are invalid, an
  `Effect`'s delivery checked. The store contract moves to package
  `grist/store` (`Store`, `Batch`, `Condition`, `Marker`, `Selector`, was
  `grist.Query`, the errors, `CheckCondition`, `Select`, `NewReceipt`),
  with `store.StepLog` (a commit's step written in its transaction,
  `Batch.Step`) kept by memstore and simstore; grist keeps aliases of the
  types an application meets. `Reads.Events` is `Reads.Select`, beside
  the typed `grist.Events[E]`. The tries and the batch size are options
  (`ConditionTries`, `TransientTries`, `EffectTries`, `BatchSize`, was
  constants) of one `grist.Option`; `App.Catalog()` is new; `PosKey`,
  `ParsePosKey`, `StepFields`, `Quiet` and gristsim's `Request` and
  `Response` are internal; `storetest.Checks` is a function, and the
  suite checks concurrent appends, `Wait` and copies. Steps record each
  event's cause (`obs.causes`) and an effect checkpoint's `ok`; grist.qnt
  has `covers`, `freshKey` (was `freshCause`), `keyStable`,
  `atLeastOnceDone` and `readsOf`, its `Read` is `{ typ, tags }`.
- grist records every commit's step from the store's step log (docs/grist.md
  §0.3; api/grist.txt): `grist.Relay` (`NewRelay`, `Flush`, `WithRelay`,
  `RecorderID`) reads each tenant's committed step rows in order and
  records them under a recorder of their own (`grist/<store>/<tenant>`,
  numbered by the row and timed by the batch, so a second relay's copies
  are Reconstruct's duplicates), started by `App.Start` (one daemon per
  tenant), which now needs a `store.StepLog`. `store.Batch.OnCommit` is
  gone: a node that dies between its COMMIT and a hook loses no step.
  `store.StepLog` gains `WaitSteps` (a refusal's step moves no head);
  `store.Permanent` (`*PermanentError`) is an error no retry fixes, which
  `store.Transient` excludes. A processor's steps are ordered by
  `order.p.<processor>` 2·T for T's effect steps and 2·T+1 for its
  handling (was T for both, ordered by the poller's thread). gristsim
  records both sides of every run, the clients under
  `gristsim.ClientsActor` (`<tenant>.clients`): `Config.Record`,
  `AtCommits` and `AtClients` are gone, a binding's `actors:` picks its
  side. The storetest suite requires a step log and checks `WaitSteps`.
- grist/sqlstore, a new package (docs/grist.md §2.10; api/grist.txt): a
  `store.Store` and `store.StepLog` over database/sql in three dialects,
  `Postgres`, `SQLite` and `QSQL`, running one SQL (`Open` creates the
  tables; `Options`: `Name`, `Isolation`, `LockTimeout`, `IdleTimeout`,
  `Poll`). An append's first statement is the head lock and the new head
  (`UPDATE grist_heads ... RETURNING head, steps`); positions are dense
  under it; PostgreSQL states read committed and sets `lock_timeout` and
  `idle_in_transaction_session_timeout` in each transaction; SQLite runs
  single-writer (`SQLiteDSN`: `_txlock=immediate`, WAL, `busy_timeout`,
  checked by `Open`). Errors no retry fixes are `store.Permanent`; 40001,
  40P01, 55P03 and SQLITE_BUSY are transient. It imports no driver.
  storetest passes on it over qsql at every isolation level, on SQLite
  and on PostgreSQL 16 (`GRIST_POSTGRES`). `gristsim.Config.SQL` runs an
  application on it over qsql. `grist.Relay.Behind` says how far a relay
  is behind, for a real tier's settle.
- examples/counters on the SQL tier and the real one: `TestSQLTier`,
  `TestSQLDuplicateRequests`, `TestSQLFaithfulAtClaim1` and both mutants
  on sqlstore over qsql, and `TestRealSQLite` (a SQLite file, steps to a
  `PerStep` file sink, validated by `counters.binding.yaml`); its
  bindings name their side (`actors: ['!*.clients']`, `['*.clients']`).
- grist, after the review of slice 2's first part (docs/grist.md §0.4;
  api/grist.txt): a node starts the relay and pollers of tenants created
  after it (`store.TenantFeed`, `WaitTenants`, required by `App.Start`);
  `App.Start(ctx, s, grist.WithRelay(r))` takes a `StartOption` (was
  `WithRelay(ctx, r)`, a context; a relay of another store is Start's
  error); `Relay.Err` reports a row it cannot read (step rows carry
  `"v": 1`; another version stops the tenant's relay) and transient
  errors; `Relay.Flush` may run beside a node of the same Relay;
  `Relay.Behind` counts rows (`store.StepLog.StepCount`, new); a relay
  with no recorder escapes in a strict run. `store.StepRow.Time` (the
  commit's, stamped under the lock: `store.StepTime`) times relayed
  steps (was the decision's); sqlstore's `grist_steps` gains `at_ns`.
  `store.NotCommitted` (`*NotCommittedError`) and `store.MayHaveCommitted`:
  an error before the COMMIT committed nothing, so `Execute` answers
  `ErrUnavailable`, and a context cancelled during a commit is
  `ErrOutcomeUnknown`. `grist.CheckTenant`: tenants, ids, tags and names
  must be valid UTF-8 without NUL, tenants without a slash; the clients'
  actor is `<tenant>/clients` (was `<tenant>.clients`). `RecorderID`
  escapes its names. simstore and sqlstore on qsql wait on the
  database's own signal (two Stores over one database wake each other:
  `storetest.TwoStores`); sqlstore's waits return once satisfied, `Open`
  validates its options and the isolation level, and `SQLiteDSN` drops
  `_txlock=immediate`. storetest draws selectors of two tags and checks
  `TenantFeed`. `gristsim.Do` runs and records a client's call in
  process, and `gristsim.Run` returns a relay's error.
- grist's HTTP API and binary entry (docs/grist.md §0.5, §5;
  api/grist.txt): `grist.NewHandler(app, store, opts...)`, a plain
  `http.Handler` (`POST /t/{tenant}/commands/{name}` under an
  `Idempotency-Key`, the command id; `GET /t/{tenant}/queries/{name}`
  with the arguments as URL parameters, `QueryArgs`; `GET /catalog` with
  `DevEndpoints`): 200 with the receipt (the key's first answer on every
  retry), 422 with a rejection's name or `idempotency-key-reused`, 409 on
  a conflict, 503 with `Retry-After` when the outcome is unknown or
  nothing committed, 400, 403 (`ErrDenied`), 404, 500. Every command and
  query is denied by default: options `Authorize(Policy)` (policies
  `Authenticated`, `Actors`) and `Internal()`, and `NewHandler` refuses
  an App with a block that has neither; the actor is the request's
  (`WithActor`, `ActorOf`; `Middleware`, `ActorFromHeader`,
  `Dependencies`). Package `grist/client` calls the API and records each
  call where its client sees it (`Client.Do`, retrying under the same
  key), as gristsim's clients do. `gristsim.Config.HTTP` serves the
  node's API on a `qhttp.Server` and its clients call it over qhttp's
  Transport. `grist.Main(app, opts...)` (`MainArgs`, `UsageError`) is an
  application binary's entry: `catalog` (the registry as JSON, each type
  a JSON Schema: `Schema`, the catalog's `MarshalJSON`), `verify`,
  `serve -db sqlite:PATH|postgres:DSN` (the API and the node on
  grist/sqlstore, every step to a `-steps` file). The catalog's types
  carry JSON tags, and its blocks `Authorized` and `Internal`. Command
  ids starting with `grist:` are refused (grist's own markers).
- grist's `Effect(f, AtMostOnce)` (docs/grist.md §0.6; api/grist.txt):
  the checkpoint first (its step's `ok` false), f once, then its outcome
  event in a commit of its own under a marker of the effect key (the
  step `<processor>Outcome`); a crash between the checkpoint and f loses
  the effect, one after f its outcome, and no restart runs either again.
  `EffectFaults`' `crashBefore` and `crashAfter` are just before and
  after f for both deliveries. grist.qnt gains `triesOf`, `holds`,
  `atMostOnceFirst`, `atMostOnceEffect`, `atMostOnceDone` and
  `atMostOnceOutcome`. examples/counters gains `retire-counter` and the
  at-most-once `payout` processor, with its claims, and the
  `checkpointFirst` mutant.
- grist's API after its review (docs/grist.md §0.7; api/grist.txt):
  `ActorFromHeader` names only an actor the application's middleware
  left unset; `ErrBadArgs` (400) for arguments grist refuses (not UTF-8,
  a required field missing or null: `Schema` makes pointer fields
  optional and pointers, slices and maps nullable; a NUL byte; an
  integer past int64), steps' fields decoded exactly; `MaxBody(n)` is a
  `ServeOption` (the constant is `DefaultMaxBody`) and a larger body is
  413; an Internal block is 404; `MaxIDBytes` bounds ids;
  `ReplayedHeader` (`Idempotent-Replayed`) marks an answer from the
  key's marker; the marker's hash covers the arguments' canonical value
  (a field added with its zero, or fields reordered, keep a key's
  answer); `QueryArgs` and `client.Params` are inverse, pointers
  included. `grist.Main` drains requests before stopping the node
  (`ShutdownTimeout`, `FlushTimeout`) and bounds its server's reads,
  writes, idle connections and headers. `grist/client` backs off
  exponentially with jitter within a retry `Budget` (`DefaultBudget`,
  `Client.MaxRetryDelay`, `Client.Budget`, `DefaultMaxRetryDelay`), and a
  5xx that is not grist's is an unknown outcome. sqlstore: PostgreSQL's
  class 54 is permanent.
- grist's at-most-once outcomes after the review (docs/grist.md §0.7;
  api/grist.txt): `grist.NotApplied(err)` marks an effect's error as
  known (`grist.failed`); any other error's outcome is `grist.unknown`
  (`Unknown`: processor, error, trigger), and the outcome step records
  `known` and `error`. grist.qnt: `unknown`, `holdsUnknown`,
  `atMostOnceOutcomesLogged`, and `atMostOnceOutcome` takes `known` and
  `error`. An outcome is appended after the node's context ended.
  `gristsim.Config.Nodes` runs several nodes on one store. grist logs a
  checkpoint left to another poller to the simulation's log.
  examples/counters: the bank records its payments (`bankPays`;
  `Bank.Pay` takes the tenant), `everyPayoutOutcomeLogged`,
  `TestPayoutTwoNodes`, the `silentPayout` mutant.
- grist's actor in decisions (docs/grist.md §0.7; api/grist.txt):
  `Reads.Actor()`, and every command's and query's step records the
  actor as `principal` (a reserved argument name), client-side steps
  too. examples/counters: `counter-created` records its `creator`,
  `retire-counter` refuses another actor (`not-creator`), the model judges
  both, the calls model knows `denied`; `TestRetireByCreatorOnly` and the
  `anyoneRetires` mutant.
- grist after wallet-grist's trial (docs/grist.md §0.8; api/grist.txt):
  `Unconditioned` is documented as unprotected at every isolation level
  (its reads precede its append's transaction; Serializable only makes
  a loser whose append overlapped decide again), pinned by
  `TestUnconditionedAtEveryLevel`; the skill and examples/counters said
  the window closed under Serializable. `grist.Main serve` records a
  settle on shutdown once the node's pollers are idle and its relay
  caught up (`SettleTimeout`), so a served run's eventual vals are
  judged. The grist skill's model rules and multi-entity commands are
  reference pages. examples/wallet-grist's `readThenWrite` is claimed at
  Serializable too.
- An exploration's summary names the model actions no run took ("model
  actions taken 5 of 11 (never: ...)"), frame helpers left out of the
  count; `qtest`'s "no model-level ITF" log says the verdict is
  unaffected (only the `Run.ITF` file needs literal arguments).
- qsql for grist's sqlstore (docs/grist.md §8.2, item 1): keys of
  several columns (`PRIMARY KEY (a, b, c)`, a table constraint; was
  42P16), ranges (`<`, `<=`, `>`, `>=`, `BETWEEN`) on a key column whose
  earlier key columns are fixed by equality, planned as a range of the
  Store's keys, `ORDER BY` the key's columns (after those fixed) in one
  direction, with `LIMIT`; `ON CONFLICT (cols) DO UPDATE SET c = ? |
  c = excluded.c | c = t.c ± ? [WHERE ...]` (21000 for a row hit twice);
  `RETURNING` on INSERT, UPDATE and DELETE. Anything else still fails
  with 42601 naming the subset. The Store scans a range as a predicate
  read of the range alone: `qenv.KeyRange` (From and To whole keys,
  not suffixes after the prefix), `Txn.ScanRange` (every Txn has it; an
  adapter's without range reads gives `qenv.ScanPrefix`, a declared read
  of the whole prefix); Serializable aborts a phantom in the
  range and not beside it, and the history checker sees the same. Keys
  of one column keep their encoding. Checked against brute force in Go
  and differentially against SQLite (examples/payd).
- `qenv.Store.Changed` (a signal notified at every commit that writes,
  applied, an unknown outcome's included, and every `Load`) and
  `Store.Version` (the last such commit's number), and `qsql.StoreOf`
  (the Store a qsql database runs on): two clients of one database in a
  simulation (two grist stores) wait for each other's commits, which a
  signal of their own slept through.
- binding's `toQuint` template function (docs/grist.md §8.2, item 2):
  `{{ toQuint "List[Event]" .obs.events }}` decodes a step field's JSON
  by a type of the model (records, sums, tuples, sets, lists, maps, its
  aliases) into a Quint value, refusing a number with a fraction and a
  time with the fix (an integer unit). A parameter whose type holds a
  record or a sum defaults to it; `Param.Type` prints the whole type
  (`{ typ: str, tags: Set[(str, str)] }`, was `{...}`); a float field
  reaches templates whole (printed truncated, as before).
- A binding's `actors:` (docs/grist.md §8.2, item 3): globs (`!p`
  excludes) naming the actors whose traces it checks, so one run that
  records two sides (commits and clients' calls, under actors of their
  own) is checked by two bindings of one model; `Binding.ChecksActor`,
  `Binding.Reconstruct`. Every
  step still reconstructs (gaps, settles), the other actors' traces are
  left out, and a binding matching none of a run's actors has no verdict
  and says so. `System.WithBinding(path, opts...)`: the same runs judged
  by another binding.
- `actors:` after its review: `*` matches `/` (an actor's name is no
  path: path.Match's `*` missed `t/x.clients`); the actors left out are
  named with their step counts in an info issue whenever any are;
  `StepsToITF` (qtest's artifacts, `gotogether itf`) keeps only the
  binding's actors; `Binding.Checks` is `ChecksActor`, and
  `Binding.Reconstruct` is the filter every reader uses.
- toQuint after its review: a `str` takes any string (a time's text was
  refused; a time is refused only where an int goes); ITF's encodings
  (`#bigint`, `#tup`, `#set`, `#map`) are read; an integer past int64 is
  whole at the top level too (qtrace reads it as a `json.Number`); two
  map keys that decode to one (`"1"`, `"01"`) and a key matched by two
  record fields are refused; `.step.seq` and `.outcome` can be passed; a
  float field compares as text in a template's `eq`, as every field does;
  aliases are the model's module's own and its imports' (`lib::T`), not
  the last module's of a name; a type variable prints by its name
  (`Param.Type`, was `?`) and its parameter defaults to the field.
- Strings are written as Quint writes them: their characters between
  quotes, as they are (`itf.Value.String`, a template's string field,
  `toQuint`, `toSet`, `toList`, `toMap`, `itf.ParseQuint`). They were
  Go's escapes (`%q`), which Quint has none of: `"a\tb"` is a backslash
  and a t there, and a quote ended the literal. A string holding a double
  quote, which no Quint literal can, is refused naming the field or the
  path (`itf.UnquotableError`, `itf.Value.Literal`). A replay's comment
  naming a step keeps a recorded newline out of the module's code.
- qdriver after its review: a record's kind is a typed `qdriver.Event`
  (`Request`, `Response`, `Step`, was the string `Observed`); a request
  argument named `code` is refused (it was replaced by the answer's
  status); an answer's field the request sent too is kept as
  `answer_<field>` (`qdriver.AnswerPrefix`) beside the request's, with a
  warning when they differ (`Log.Warnings`, printed by `gotogether
  validate`; it was dropped, so no model saw a server apply another
  value); nested arguments and observations are JSON text, as qobs
  records them (they were Go maps and slices), and an integer past int64
  is a `json.Number`, whole, in a request log and a step log alike
  (`qtrace.Step.Args`; binding prints it). `bombadil.Read` and
  `ReadFile` are gone: `bombadil.Log` or `LogFile`, then `Steps`. The
  Playwright adapter writes each record when its event happens (a
  response once its body is read), where every line after a response
  waited for its body and a killed test lost them; its recorder is the
  header's (default `playwright`) with `-<pid>-<n>` after it
  (`uniqueRecorder: false` keeps it as given); its README's limits say
  what a mocked answer and cookies do.
- qsql after its review: in `ON CONFLICT ... DO UPDATE` a column on the
  value side or in the WHERE is `t.c` or `excluded.c`; a bare one fails
  with 42702 naming both (PostgreSQL refuses it; qsql accepted `SET n =
  n + 1` and `WHERE n = 1`). `LIMIT NULL` (or a nil argument) is no
  limit; `ORDER BY` may name a column fixed by equality anywhere, in
  either direction. A table name with `/` or `!`, or the `_qsql` prefix,
  is refused (42602, 42939): its keys would collide with another
  table's. `qenv.ScanRange` (a free function falling back, silently, to
  a scan of the whole prefix for a Txn without range reads) and
  `RangeScanner` are `Txn.ScanRange`, a method of every Txn, the
  fallback the adapter's own `qenv.ScanPrefix`; `KeyRange.From` and `To`
  are documented as whole keys.
- Two records of one (recorder, seq) that differ are no copy: an error
  issue (`duplicate`) of each actor of either, naming both and the fix,
  where `qtrace.Reconstruct` kept the first and said so as info (two
  driver logs both recorder `ui` had the second's steps dropped, a
  failing second run passing). A copy stays info. `gotogether validate`
  (and `steps`, `itf`) refuses two request logs naming one recorder
  (`input.Distinct`, `input.File.Recorder`).
- API trim before the tags (G6). `validate.Command`, `Judged` and
  `Replay` move to package `validate/cli` as `cli.Check`, `cli.Judged`,
  `cli.Replay`: the check reads OTLP/JSON, and in validate it brought
  otelio and OpenTelemetry's SDK into qtest and every test binary (qtest
  now links neither, `TestQtestDependencies`). `Check.Format` is an
  `input.Format`; `RequireSettle` is `RequireFinish`; `Check.Name`,
  `Driver` and `Artifacts` give the manifest what `RunInfo`'s file
  gives. `Judged.Temp` is `Temporary()`, and `Close` removes a temporary
  `Dir` (a Judge or Replay without one left a directory per call);
  `FinishWhy` (flag text) is `FinishByBinding`; `Incomplete` (a map
  keyed by result) is gone: an unfinished result is a copy whose new
  `validate.Result.Unfinished` says why, which `Reason` and the verdict
  read. `cli.Replayed` has `Got` (the replay's `*Judged`) and `Want`,
  `Same`, `Differs` (was `Note`) and `Notes`. `validate.VerdictOf` is
  `Report.Verdict()`; the manifest builders (`NewManifest(o)`,
  `SetBinding`, `SetSpec`, `AddConvert`, `AddInput`, `AddArtifact`) and
  `Checker.Manifest` are one `validate.NewManifest(RunRecord{...})`;
  `ModuleVersion` and `QuintVersion` are `validate.CurrentTools(o)`;
  `Options.NoEnvBackend` is exported (a replay's). The manifest format
  is `gotogether.run/2` (`requireFinish`, was `requireSettle`): a
  `run/1` manifest is refused with the fix. `gotogether validate
  -require-finish` names the check (qtest.Finishes'); `-require-settle`
  still works, warning that it is deprecated. qshrink: `Shown`'s values
  are `Reproduced`, `NotReproduced`, `Undecided` (were `Yes`, `No`,
  `Unknown`), as Result counts them; `Keep` has `Why`; `Result` marshals
  as shrink.json (`Set` and its length, where `WriteArtifacts` recorded
  the length of whatever it was given) and the files' layout is the
  callers' (internal/shrinkfiles). SameFailure reads a mismatch's fields
  from `binding.ObsComparisons`, the binding's own analysis of an
  expect, not a second parser. A corruption changes lists (`shorten`,
  `grow`) and maps (`drop`), and the step mutants' table names the
  fields only removed (`qtrace.UncorruptedFields`). The evidence format,
  roles, the file check and the CLI's view are internal/evidence's, for
  qtest and the CLI alike. `qtest.JudgeStepMutants` is gone
  (`validate.Checker.MutateSteps` outside a test); `StepMutationReport`
  embeds `*validate.StepMutants`, its table's methods, beside its own
  `Original` run and `Expect`.
- After the review of G3–G5: `validate -replay -itf` keeps the ITF it
  names; `gotogether shrink` sets each command's `WaitDelay` (a child
  left running with the driver's output open once hung it), takes
  `-rerun-timeout` (a rerun or reset past it is killed, a failed rerun
  named), cleans up on Ctrl-C, hands the driver absolute paths, counts
  and names torn reruns' step logs (`-v` prints each), and refuses
  `-diverged-exit 0`; a step log torn in its first line reads as torn,
  not as an unknown format. `qobshttp.Handler` returns a `*Recording`
  whose `Wait` waits for the requests it serves, hijacked connections'
  included, which `srv.Close()` does not. `qobs.Recorder.Close` and
  `Late`: qtest.Record closes its run's recorder when the scenario
  returns, and a later step fails the test, named (after the test, it is
  logged as escaped) instead of vanishing. An escaped step is told to
  use qenv.Go only when its goroutine is a simulation's or one started
  it, not whenever a simulation is open in the process. "Did you mean"
  suggests within one edit for names under 8 bytes, two for longer (a
  transposition is one), and a claim naming an action as an invariant
  (or the reverse) says so. qtest's own tests leave nothing in the
  temporary directory but the evaluator's cache.
- Shrinking and replay outside `go test` (G5): the CLI no longer imports
  qtest, and with it `testing` and qenv. Package `qshrink` is
  ShrinkActions' core: `Shrink{Actions, Rerun, Reset, Judge, Keep,
  Calibration}.Run(ctx)` over indices, `Result` (`Set`, `Steps`, the
  counts, `Baseline`, `Rate`, `Minimal`, `NotMinimal`, `Keep`,
  `WriteArtifacts`: actions.json and shrink.json), `Keep` and `Shown`
  (`Yes`, `No`, `Unknown`), `SameFailure(report)`, `Calibration`, `Rate`,
  `Resetter`, `ErrDiverged`; qtest's `Calibration`, `Rate`, `Resetter`
  and `ErrDiverged` are aliases of them, and `ShrinkActions` (same name,
  same behaviour, messages) wraps it, stopping before the test's
  deadline through its context. `gotogether shrink -binding b.yaml
  -actions journey.json [-out DIR] [-reset PROG] [-diverged-exit n]
  [calibration flags] -- DRIVER ... {actions} ... {steps}` shrinks an
  external driver's journey by rerunning the command per candidate (argv,
  never a shell), writing the shrunk actions, shrink.json, the
  reproducing rerun's steps.jsonl and a manifest `validate -replay`
  judges again; exit 0 1-minimal, 1 not minimal, 2 a refusal.
  `validate.Checker.MutateSteps(ctx, steps, opt, check, mopts...)` is
  MutateSteps' core (`StepMutants`, `StepMutation` and the `Fate*`
  constants, which qtest's alias); `qtest.MutateSteps` and
  `JudgeStepMutants` keep their names. `validate.Command{...}.Judge(ctx)`
  is the check `gotogether validate` makes (the flags as fields, a
  `Judged` with the inputs read, the judgment, ITF results, the finish
  check, `OK`, `Verdict`, and `Manifest()`, `-run-info` included), and
  `validate.Replay(ctx, manifest, opts)` is `validate -replay` in Go
  (`Replayed`: the judgment, `Want`, `Same`, `Note`, `Warnings`,
  `Notes`); a run judged on the default backend replays on it whatever
  QUINT_BACKEND says, without touching the environment. webjobs' tests
  replay their manifest through it, per push.
- A `qobs.Recorder`'s seq order is its steps' time order: two goroutines
  recording at once could take seq and time in opposite orders (a
  double-clicked submit's two answers), so a `within` bound judged along
  seq order read time going back and gave no verdict, and Linearize took
  the records' order for a real-time order it was not. Seq and time are
  now one stamp.
- qtest's failure reports name files that outlive the test: Record's and
  Validate's `Run.Expect` (and a `Runs` claim's) copies the run's step
  log, ITF and modules out of the test's `t.TempDir()` into
  `$TMPDIR/gotogether-*` and says so, as explorations' claims did; a
  report that cannot copy them says they will be gone
  (`GOTOGETHER_KEEP=dir` keeps them). A claim naming an invariant or
  action the run cannot have says so before any verdict, with the name
  it most likely meant (`nonNegativ is not an invariant of the run (did
  you mean nonNegative?; ...)`), where `Run.Expect` said "want a
  violation of nonNegativ, got: ...". `StepMutation.Skipped` names the
  outcome of a mutated step the binding skips (`outcomes: { error: skip
  }`), and `-mutate-steps`' report says such a survivor is skipped; the
  binding's error for an unmapped failed outcome says to model the
  refusal first, and to skip only an outcome the model does not judge.
- A failure that stands beside a gap or a lost tail is a FAIL, whatever
  another check's failure after it (no verdict) says: `Result.Reason`
  returned at the first invariant with no verdict, so the CLI printed
  `invariant underLimit: FAIL` above `RESULT: NO VERDICT`. Each check
  says its own cause (a within or an eventual val a gap explains is
  `no verdict (dropped steps)`, as `Finished` says, not `lost steps`).
  Under a domain clock (`quint.order.*`) or a linearization, no failure
  after the initial state stands beside a gap or a lost tail: the lost
  step's clock value, or its thread and invocation, may place it before
  any recorded step (DESIGN §53). qtest agrees: `SameFailure` and
  `ShrinkActions` know a FAIL that stands before every gap, and a FAIL
  of any actor kills a mutant though another actor has no verdict.
- `qtrace.Mutations` (and `-mutate-steps`) makes the gap op's mutants
  only when named (`MutateOps("gap")`, `-mutate-ops gap`): they are
  only ever undecided.
- `gotogether scaffold -out`: a path through a symlinked directory is
  written absolute where a relative one would read differently from
  the link and its target; a missing -out directory fails before quint
  runs, saying so. `validate`'s report names a passing actor's module
  no more (the CLI removes its temporary directory).

- A run's verdict (`validate.VerdictOf`, the manifest's, the CLI's
  result line) takes its reason and failing step from its first FAILing
  actor, else its first with no verdict: an earlier actor's no verdict
  gave a defect's FAIL its reason ("FAIL (no verdict (dropped steps))").
- `qtest.MutateSteps` and `gotogether validate -mutate-steps`: a mutant
  the binding refuses (a field it reads removed) is `FateMalformed`, in
  no score, not killed: a real log like it is a tool error (exit 2), no
  verdict, as a mutant Reconstruct refuses already was.
- `gotogether validate -replay` says "not reproduced" (exit 1) for a
  verdict of another cause or at another action: `RunVerdict.Same`
  compares incomplete's and no verdict's cause (no settle is not
  pending calls, a lost tail not uncollected steps; a cause reworded
  keeps its kind) and every failing actor's action
  (`ActorVerdict.Action`, new; an older manifest's from its reason),
  not the reason's first word and the first failing actor's alone. The
  replay prints where the verdicts differ (`differs:`).
- Recording an HTTP service in a test: `qobshttp.Handler(h, rec)` (new
  package `qobs/qobshttp`) puts rec in every request's context and makes
  the handler's goroutine rec's run while it serves the request, so a
  server started inside `qtest.Record` (a ShrinkActions rerun, a qhegel
  case) records its handlers' steps into that run, in parallel with other
  tests, where `qtest.Global()` serialised them. A step that escapes a
  strict run says what fixes it where it came from: an HTTP handler's
  names `qobshttp.Handler`, one outside any simulation
  `qobs.WithRecorder`; only a process simulating is told `qenv.Go`.
- `gotogether validate`'s result line says `RESULT: NO VERDICT (...)`
  when the run's verdict is no verdict (every failing actor's a lost
  tail, dropped steps or an uncollected window; exit 1, being no pass),
  as its manifest's verdict already did: it said FAIL for any run not
  OK, though only a defect is a FAIL.
- External shrinking: `qtest.ShrinkActions(t, actions, ActionShrink[A]{Binding,
  Rerun, Reset, Keep, Calibration, Opts})` shrinks a failing sequence of
  a driver's actions to a 1-minimal one (ddmin) that still fails the
  same way, each candidate rerun on the system from the state a
  `qtest.Resetter` leaves (its contract checked first: two reruns of no
  actions record the same steps, up to names and times, so an
  environment's own shutdown steps pass and a leak does not; the
  original's first two reruns fail alike)
  and its steps checked against the binding; `ShrunkActions[A]`
  (`Actions`, `Run`, `Original`, `Reruns`, `Reproduced`,
  `NotReproduced`, `Undecided` (reruns with no verdict for the failure,
  never a miss), `Diverged`, `Errors`, `Baseline`, `PerCandidate`,
  `Rate`, `Minimal`, `NotMinimal`, `Expect`), `qtest.AtMostActions(n)`
  (a `ShrinkClaim`), the minimal run's manifest under
  GOTOGETHER_EVIDENCE with actions.json and shrink.json among its
  artifacts. `qtest.SameFailure(run)`, the default failure kept (each
  failing actor's first failure in the replay's order, its kind and
  name, a state mismatch with the observed fields that differ, whatever
  seqs, ids and arguments; no verdict with a lost tail, an undecided
  actor or a conforming actor's invariant unjudged), Keep's default from
  the original's first rerun with a decided failure;
  `qtest.ErrDiverged`, what a rerun returns for actions it could not do
  (never a pass nor a failure); `qtest.Calibration` (reruns from the
  original's rate at its lower bound, the fewest that miss a failure
  with chance at most α; a budget and a time) and `qtest.Rate`.
  `ShrunkActions.Minimal` holds only with the shrunk sequence's own
  rate measured and the last rerun of no actions made (a budget that
  ran out right after the search once left it true at a rate of 0 of
  0): a `Budget` keeps `Baseline`+1 reruns back for them.
- Absence diagnostics: each step a run was expected to show and does
  not (a within not met or not judged, an eventual val not judged or
  false at the settle, a call never completed) is in the report's
  `absent:` section with its cause (`validate.Absence`, `Result.Absent`,
  `Report.Absent()`, `Result.PendingCause`; `validate.Cause`: `Dropped`,
  `Uncollected`, `Defect`), only a defect a FAIL. A watermark record
  (`qtrace.KeyCompleteThrough`, `Step.CompleteThrough`,
  `qtrace.MarkComplete`, `qtrace.AllRecorders`, `qtrace.Watermark`,
  `Trace.Collected`, `Trace.CompleteThrough()`, `qtrace.CollectedThrough`
  (a watermark raised by the recorder's dense seqs),
  `qtrace.CarryWatermark` (a search's order's);
  `qobs.CompleteThrough`, `Recorder.CompleteThrough`; `gotogether
  validate -complete-through r=T`, in the manifest as
  `completeThrough`) judges a within whose window it reaches though the
  run never settled, leaves one it does not reach no verdict, and voids
  a settle it does not reach. `qtrace.Gap` and `Trace.Gaps`,
  `Result.Gaps` and `Result.Collected`: a failure a gap may explain is
  no verdict, a conformance failure or an invariant's included (one at
  or after a gap of the actor's recorders: `no verdict (dropped
  steps)`, in `Reason`, `Report.Finished` and the CLI's result line; it
  was a FAIL, and `Mismatch`, `SameFailure` and `Finishes` read it so;
  a failure before every gap stands, and recorded steps that pass beside
  a gap are no verdict too). `qtest.Absent(name, cause)`; `Violates`,
  `Completes` and
  `Pending` explain absences (for `Undecided`).
- Observation mutants: which recorded fields the model and the binding
  constrain. `qtrace.Mutations(steps, opts...)` makes a log's single
  mutations (`StepMutant`: `Op`, `Index`, `Action`, `Field`, `Change`,
  `Detail`, `Steps`; `MutationOps`): a step dropped, duplicated (at the
  next seq, later ones renumbered), swapped with its recorder's next,
  an argument or observation corrupted (±1, flipped, emptied, changed,
  removed), retimed past its neighbour, moved to another actor, each
  keeping the recorder's contract, or lost leaving a gap (`gap`: its
  recorder's seqs left with the hole, a mutant of the absence path,
  judged undecided, never killed); `MutateOps`, `MutateLimit`.
  `qtest.MutateSteps(t, binding, steps, opts...)` judges each as
  Validate would (the verdict cache shared), killed, survived or
  undecided (`FateKilled`, ...; `StepMutation`, `StepMutationReport`
  with a table by op), with claims `qtest.Killed(sel)` and
  `qtest.Survived(sel)` over `qtest.StepOp(op)` selectors
  (`.Action`, `.Field`, `.Change`, `.Index`); `qtest.StepMutations`
  narrows it (refused by the other workflows); `qtest.JudgeStepMutants`
  outside a test. `gotogether validate -mutate-steps [-mutate-ops ...]
  [-mutate-limit n]` prints the table after a pass. A mutant Reconstruct
  would refuse (a Lamport cycle) is left out, and one refused anyway is
  `FateMalformed` (`Malformed()`), never killed; a retime stays before
  its actor's next step, settles and watermarks; a survivor whose
  bounds went unjudged is undecided.
- `qtest.Undecided(e, because...)`: the claim that a run has no verdict
  for an expectation (a lost tail, a search out of its budget), its
  explanation naming each cause given, so a test states a run that must
  neither pass nor fail without walking its report. `qtrace.Reconstruct`
  reports an error issue (kind `invocation`) for a completion timed
  before its invocation record (by more than `RealTimeSlack`), or
  numbered at or before it: an invocation record's time is taken as the
  call's start, and was not checked.
- Run manifests: `gotogether.run/1` (`validate.Manifest`), so a failure
  of a run gotogether did not drive travels: tool versions, the binding,
  spec, rules and inputs with their sha256 and losses, constants,
  invariants, how the run was judged, the verdict (`validate.VerdictOf`)
  and failing step with its ref, and the driver and artifacts the caller
  names. `gotogether validate -manifest run.json [-run-info
  driver.json]` writes one, `validate -replay run.json` judges it again
  (refusing a changed input, binding or spec, naming it; exit 1 if the
  verdict is not the manifest's). Under `GOTOGETHER_EVIDENCE` each
  `qtest.Validate` and `qtest.Record` run writes its manifest
  (`run<N>.json`, beside a copy of its step log); `qtest.Driver`,
  `qtest.Artifacts` and `Run.Manifest()` for tests; `gotogether
  evidence` lists manifests (`qtest.RoleExternal`,
  `qtest.ManifestEvidence`) and `-expect` names them. `binding.Binding.Path`
  is the file a binding was loaded from. Verify checks the spec the
  binding resolves now and the files it imports (`RunModel.Imports`); an
  ITF input records its instance (`Input.Model`, `validate.ITFModel`);
  `Manifest.Bundle` copies a manifest's files beside it (qtest's are
  bundled in `run<N>/`); the check recorded is the one judged; the
  backend is kept on replay; `RunVerdict.Same` compares kinds of claims
  and notes rewording; `ErrManifestFormat`; `Report.WithoutRefs` (what
  qhegel caches). A manifest records the driver's args, paths and refs
  verbatim, and a bundle the whole step log: sanitize before sharing.
  A step with no time is written with no `quint.time_unix_nano` (it was
  an overflowed one). A cached verdict's failing step names the run's own
  recorder and process, found with no seq too; qhegel caches its
  summaries without refs.

- Step references: `quint.ref` (`qtrace.KeyRef`, `Step.Ref`), a step's
  link to its artifact (an external driver's trace line, a screenshot),
  recorded with `qobs.Ref(s)` in a key's place (a typed value: a
  parameter named ref stays an argument), or a log record's `quint.ref`.
  Kept by step logs, OTel span events (the event's or its span's, never
  a resource's), a convert rule's `ref:` (default: the span's own
  `quint.ref`), and from an invocation record to its completion; printed
  at a failing step (`ref:`), in `-history`'s titles and `steps`'
  listing. No verdict or cache key reads it, and a verdict taken from the
  cache shows the run's own failing step's ref and source. `Filter`
  drops refs when it sanitizes (free text no pseudonym reaches) unless
  `KeepRefs` (`steps -keep-refs`).

- Strict mode: `GOTOGETHER_STRICT=1` turns every toolkit skip into a
  failure with its message (`qtest.RequireQuint`, `System.Mutate` with no
  mutants woven, `SkipIfEmpty`'s in Record, Explore and `qhegel.Shrink`,
  a scaffold's unwritten mutant test); `qtest.Optional(t, reason)` does
  the same for a test's own gated skips. `gotogether evidence -expect
  checks.txt` fails, naming it, on a listed test or exploration that
  wrote no evidence (`Evidence.Import`, the test package's import path,
  names a package of any module: `examples/hello TestX`); a test that
  skips clears its earlier evidence. `GOTOGETHER_STRICT` takes 1 or
  true, and fails on any other value but 0 or false. CI runs strict, the
  core's own tests too (internal/skip). Reported by a pilot outside the
  repository.
- `qtest.Finishes()`: a run that is a finished journey, not a prefix of
  one: it conforms, settled, every eventual val was judged and holds, and
  every invoked call completed; three-valued (a lost tail is no verdict),
  and its failure names the part missing and the fix. `gotogether
  validate -require-settle` makes the same judgment
  (`validate.Report.Finished`, a `validate.Finish`): it exits 1 for a
  passing run that never settled ("incomplete (no settle)") or left a
  call pending, one `-resolve-pending` placed included ("incomplete
  (pending calls)"), and for a lost tail ("no verdict (lost tail)") or an
  ITF input ("no verdict (ITF trace)"); a binding with no eventual val is
  refused (`validate.Finishable`). A binding's `complete: true` makes
  that its default. A conforming three-step prefix passed with Conforms
  alone. Reported by a pilot outside the repository.
- A spec's imports (the model cache's key, `Manifest.Verify`, `Bundle`)
  are read by token, not by a regular expression that missed an
  instance's with a string constant (`import a(N = Set("u")) as A from
  "./a"`) and an import split over lines: a change to such a file once
  left the cached model stale, passed Verify, and stayed out of a bundle.
- `qtrace.ReadJSONL` rejects a line whose object is followed by more (a
  second object, garbage), naming the line: it read the first object and
  dropped the rest, losing a record or hiding a corruption. A cut-off
  last line is still a `*TornTail`; trailing whitespace is still allowed.
  Reported by a pilot outside the repository.
- The CLI says what it did: `scaffold -out` writes `spec:` and
  `Binding:` relative to the file written, renders before creating it
  (a failed scaffold leaves no file), and its -h shows `-system`; a
  binding whose spec is not where it says fails in every command
  (`Binding.CheckSpec`) naming the file, the spec as resolved and the
  path to write, and `binding.LoadModel` names a missing spec instead of
  quint's chdir failure; plan errors
  number steps from 1 with their seq and source, and a process change
  without a restart says to add `restart: <action>` with the model's
  candidates; -h prints no zero default nor one its usage states; an
  unknown command says so (did you mean); the usage lists every flag of
  validate; validate's temporary directory is made only when there is
  something to judge and removed when the run passes (kept on failure,
  and with -dir or -itf).
- `qnemesis`: a nemesis for real deployments, standard library only.
  `Nemesis` runs a `Schedule` (qenv Script's names and partial order:
  `nemesis.crash.<p>`, `pause`/`resume`, `partition`/`heal`, `isolate`,
  `latency`, `loss`; `At`, `After` steps of the watched logs (`StepsIn`),
  `Follows`, `For`) on `Process` targets (a process group per
  incarnation, SIGKILL and reap, SIGSTOP/SIGCONT, no orphans) and
  `Container`s (the docker CLI), and `Toxiproxy` links (one proxy per
  directed link); `Stop` heals, resumes and starts the down; `Misnamed`
  refuses what cannot happen, with the fix. Faults are recorded as model
  steps (`Recording`, `DefaultRecording`, `NewRecorder`): a crash after
  the reaping, a cut once in effect, a heal before the call. `Random` is
  Jepsen's schedule, `FromScript` a trace's; the journal (`ReadJournal`)
  becomes a qenv Script (`Journal.Script`), so a real run's faults re-run
  in a simulation.
  A crash of a target naming its recorders (`Recorded`;
  `Process.Recorders`, `Logs`) declares their tails lost, so a kill
  between an effect and its step is no verdict, not a false FAIL.
  `StepsIn` counts the system's steps only (never `RecorderID`'s, nor
  loss records). Pause waits for the whole group to stop; a crash ends a
  pause. `Process.PIDFile` sweeps what a killed test binary left of a
  group. `Journal.Script` keeps heals out of the order (qenv heals a cut
  after `CutLength`), leaves out heals of links never cut, and
  `Journal.LongestCut` and `Unreproducible` say what to set and what it
  cannot make.
  `Process.Pause` and `Resume` of a process not running say whether it
  never started, was killed, or exited by itself (its incarnation and
  exit status): a schedule's pause of one fails naming the fault, and
  nothing is journaled or recorded of a fault not made. (raftkv's real
  tier once ran with n3 never up: its port, picked by listening on
  ":0", was a toxiproxy proxy's by the time it started, and the proxy
  answered for it; nodes now take ports below the ephemeral range, an
  agreement needs each node at its own address, and a node down by
  itself fails the run.)
- `qtest.Runs`: claims over runs made one by one (a real deployment's,
  each validated), judged and checked as an exploration's (`Some`, `All`,
  `None`; misspelled names, eventual vals judged in no run, no runs, and
  runs of two instances fail).
- `qobs.Recorder.Actor`. `qtrace.Reconstruct`: a loss record of a
  recorder with no step reaches the actor it names.
- A lost tail bounded in time (`qtrace.Step.LostBefore`,
  `quint.lost_before_unix_nano`, `Lost.Before`; a qnemesis crash's, by
  its reaping) voids only the settles a lost step may follow: a settle
  after the bound settles the run, so Recorders and `Settles()` go
  together. Unbounded losses void every settle, as before.
  `qnemesis.Unmade` says which faults of a journal's script a
  simulation did not make, and why (for a nemesis qenv makes only when
  configured, the configuration: `Sim.Stalls.Pause`, `PartitionNemesis`).
  `Journal.Script` keeps the faults' order only (a real run's step
  counts are not a simulation's); `Journal.StepScript` keeps their
  After. `Journal.Unreproducible(sim)` compares pauses and cuts with the
  simulation's, within `PauseTolerance`.
- `qenv.Unwinding(r)`: whether a recovered panic is qenv unwinding a
  killed task, for an adapter whose caller releases a lock only on a
  normal return (qsql under database/sql's Tx.QueryContext). A seam in
  code that runs while Close unwinds a task (a deferred call) unwinds it
  on, where it once handed control back early and left the task parked.
- `qenv/qsql`: a database/sql driver over a `qenv.Store`, for an
  unmodified database/sql service in a simulation (`Open`, `Options`,
  `Change`, `Error`, `Table`): a SQL subset (CREATE TABLE, INSERT [ON
  CONFLICT DO NOTHING], SELECT [FOR UPDATE], UPDATE with `c = c ± ?`,
  DELETE; equality only), the Store's isolation level and history check,
  PostgreSQL's SQLSTATEs (`SQLState()`, for retry loops), the fault kind
  `sql.<name>.conn` (`badConn`), commit faults as 08006, and a reaper
  task for transactions whose context is cancelled. A key another
  transaction committed is 23505 at every level (40001 only after
  reading it absent at serializable, or for ON CONFLICT DO NOTHING's
  unseen row), a lookup by a UNIQUE column at read committed sees one
  state, and statements after a cancel fail with sql.ErrTxDone. A task
  unwound inside a statement (its process crashed, or left at Close) gets
  08006, so database/sql releases the Tx's lock its deferred Rollback
  needs; its later calls fail at once, and a loop of them is unwound
  after 100.
- `qenv/qhttp`: an unmodified `http.Handler` and `http.Client` in a
  simulation. `NewServer` (its handler installed by `Handle` at each
  incarnation, connections refused before it and while the process is
  down) over a `qenv.Service` carrying wire bytes; `NewTransport`, an
  `http.RoundTripper` whose outcomes are a real client's (a dial
  `*net.OpError` with ECONNREFUSED only when nothing was sent, a read one
  with ECONNRESET for a request that found no one listening, a
  `TimeoutError` after virtual time, 503 on overload), a request
  context's deadline converted to virtual time once per simulation (its
  remaining time rounded up to a grid of round timeouts, so the test
  process's jitter does not reach the run; `WithTimeout` for an exact
  one) and used up there (a retry under it fails at once, unsent), and
  the fault kind `http.<name>.conn` (`Transport.Refuse`).
  `qenv.FaultKind.At` puts a double's kind built outside qenv at its
  process, for `Sim.SettleCore`.
- `Sim.Hang` (30 s from New, zero off): a task that runs that long in
  wall time without reaching a seam (blocked on a real channel or lock,
  or in database/sql's pool at SetMaxOpenConns) makes Run panic naming
  the task and its stack, where the test hung until go test's timeout.
- Linearize's candidates are judged by a Quint evaluator in Go, over the
  IR quint compiles the binding's evaluation module to: an order it finds
  is confirmed by quint (a disagreement searches again with quint, and
  says so), and a search that finds none is confirmed from its proof
  (`qtrace.Linearization.Proof`, `qtrace.Proof`: every prefix it dropped as
  rejected, every pair it merged as one state) in one quint batch; what
  the evaluator cannot evaluate is quint's. quorumkv's faithful
  exploration took 13 s, 20 s with quint alone; its contended mutants
  62 s, 337 s. `binding.EvalModule` writes the module the evaluator
  reads. `validate.Checker.Judge` leaves an order (or a resolution:
  ResolvePending's placements are the evaluator's too) to the run's
  check, and checks with quint the orders of the actors that check
  fails, searching again with quint alone if one is wrong; qtest and the
  CLI judge through it. quorumkv's faithful exploration then took
  4 s, its contended mutants 43 s, wallet's pending transfers 12 s (44 s
  before). `GOTOGETHER_EVAL=quint` turns it off; `GOTOGETHER_EVAL=shadow`
  checks every batch it judges with quint too, and judges each actor of
  every run checked with both, failing on a disagreement (nightly). Two prefixes merge
  only when their processes are numbered alike.
- A linearization search the evaluator judged that runs out of its budget
  is no verdict at once, not searched again with quint alone at the same
  budget (minutes, for the same no verdict): raftkv's faithful
  exploration with two such runs took 82 s, now 5 s.
- `None(...)` claims fail over runs with no verdict (a search out of its
  budget): they once held of runs judged by nothing. A mutant with no
  failing run and some without a verdict is `MutationReport.Undecided`
  (it counted as killed), and `NoSurvivors` fails on it.
- A claim's invariant names are checked against the binding's
  invariants, eventual vals and bounded-response obligations, not only
  the names some report carried: with none judged, a misspelled name held
  for nothing.
- A hidden-action search cut by `hiddenDepth` (one more hidden action
  reaching a new state) is no verdict for a step it cannot take, an
  invariant no path keeps or an eventual val no final state has: it was
  a rejection.
- A checker made with no `Options.Dir` writes each check under one
  temporary directory of its own, which `Checker.Close` removes: each
  check made a temporary directory that nothing removed.
- `validate.Options.Eval` sets how a search judges its candidates ("go",
  "quint", "shadow"; GOTOGETHER_EVAL is its default): validate's tests
  run in parallel, 138 s from about 300.
- A binding's templates are parsed once per process and its functions
  bound once per plan, not per render; a search plans its candidates with
  one `binding.Planner` (the history interned and its constants rendered
  once: `Binding.Planner`, `Planner.Plan`), and the Go evaluator judges a
  candidate from the state of the longest prefix it accepted before
  (each was replayed from init). raftkv's 200-operation history
  linearized in 15 s, from 50 s.
- A pending call placed by a search keeps its own process (it rendered
  `{{.process}}` as the step before it's): it infers no restart, and
  qtrace's overlap check skips it.
- `within` judges calls recorded with their invocations on their
  intervals: in time only if every placement of the two effects is, late
  only if every one is, else no verdict (it judged completions alone).
- ResolvePending says when quint rejects the evaluator's resolution (it
  fell back in silence).
- `Checker.Key` holds the evaluator's mode and quint's backend.
- The evaluator refuses `range(a, b)` with a > b: quint's backends differ.
- A run's marked report copies its invariants: a search's mark once wrote
  n/a into the shared verdict cache's, for every later run of the same
  steps.
- A sleep or wait timed on a drifting clock lasts its interval rounded up
  in true time: rounded down, a wait of a nanosecond on a fast clock
  lasted none, and a loop that waits until its own monotonic clock
  reaches a point (raft's tick loop) spun at one instant until
  `ErrMaxSteps`.
- `qtest.Linearize()` with `qtest.ResolvePending()` (and the CLI's
  `-linearize -resolve-pending`), refused together before, search pending
  calls inside the linearization: each may take effect once anywhere real
  time allows (after the steps recorded before its invocation, before
  those after a settle that followed it) or not at all, Jepsen's
  indeterminate calls (`qtrace.LinearizeOptions.Pending`). Checked against brute
  force (every subset, every order), its "no order" confirmed by the same
  proof.
- A binding's `hidden` actions: model actions that run unrecorded (a
  gossip round), any sequence of them, up to `hiddenDepth` (default 4),
  between recorded steps. Such a trace is judged by the Go evaluator's
  search over the states they reach (quint cannot replay steps whose
  choices are unknown), and the report says so. Checked against brute
  force; searches (Linearize, ResolvePending) refuse it.
- A binding's `within` (bounded response): each time a val becomes true,
  another must hold within a bound of virtual (or recorded) time, judged
  on the replay's timed states; a run cut short before its bound is not
  judged. `validate.Checker.Key` keys a verdict cache, with the steps'
  times for such a binding (qtest's cache once gave a slow run a fast
  one's verdict).
- qhistory checks strict serializability: `StrictSerializable`, with
  `Txn.Invoked` and `Completed`, orders a transaction completed before
  another began first, and names a cycle through real time Elle's way
  (G-single-realtime: a stale read). Checked against brute force (every
  serial order real time allows).
- Steered exploration branches a run from where it first found something
  new (the choices that led to it, kept), not from anywhere in it: the
  journal's rare restart in 43 of 200 runs, 7 before.
- `gotogether lint -sim` (and quintvet) flags math/rand's, math/rand/v2's
  and crypto/rand's package functions, and a select of two or more
  cases: neither replays. It names `math/rand/v2` as `rand`, not `v2`.
- Rolling upgrades in qenv: `Sim.Upgrades(version, start, procs...)`
  registers a nemesis per process (`nemesis.upgrade.<name>`) that crashes
  it and restarts it as `version` running `start`, so explorations and
  `Exhaust` pass through every mix of versions; `Process.Version`,
  `Process.RestartAs` and `Process.Upgrade`.
- `qtrace.HistorySVG` and `validate.Report.HistorySVG` draw a history
  (a lane per client, calls as bars, the order checked numbered, pending
  calls dashed, the failing step red); a kept failing exploration run
  that was searched writes `<run>.history.svg` beside its judged order,
  and the CLI's `validate -history out.svg` writes one.
- `qtrace.ForcedOrder`: the one order real time allows a history, when
  there is one. A linearization (and each partition of `LinearizeByKey`,
  now searched side by side) of such a history checks it without a
  search: the wallet's per-account history took 15 s, not 28.
- A Rates key, a Bound.Without kind, a sweep's FaultKind or an
  Exercised exception is checked against what the exploration's own runs
  declared and chose (and what was declared outside any run, a woven
  fault point's), not the process's: a name only another test's double
  declared fails, whatever ran first.
- `System.Exhaust` makes its nominal run twice, as an exploration does
  its first seed, and fails on a system whose runs differ: none of its
  schedules would replay.
- A partition the nemesis cut no longer keeps a run from settling: its
  healer, asleep until the cut would end, is a daemon (Settle heals
  every cut at once). Found by running every example under
  `GOTOGETHER_ALL_FAULTS`.
- `binding.Binding.PlanWithin`: a plan of some of a history's steps in
  the history's instance and naming (ids interned in its order, constants
  from all its steps). The searches plan their candidates so: a prefix's
  verdict and state were judged in its own smaller instance, with ids
  named in its own order, and carried to extensions in another. An ITF
  keeps the qualifier of a variable name two modules share.
- `qtrace.Linearize`'s check also gives each
  accepted order's model state; its complete search keeps one prefix of
  the same steps and state (Lowe's memo for Wing and Gong's search), so
  overlapping operations cost as many prefixes as states, not orders.
- qweave passed the go command its overlay in place of the first
  argument after the command: `qweave go test ./sim/` tested the current
  directory, `qweave go test -short ./x/` ran without -short, and
  `go test -c` ran the tests. Every argument now reaches go.
- `GOTOGETHER_ALL_FAULTS=1` turns every fault family on in every
  simulation: clock faults, stalls and a partition nemesis between every
  two processes, which are opt-in otherwise. `Network.PartitionNemesis`
  of a pair that is one already adds none.
- `ResolvePending` placed a pending call before the step after the last
  one recorded before a settle, which a step recorded after the settle
  could precede (a step at its instant on another recorder ordered
  later): a resolution the settle rules out could make a run conform.
  It now bounds the call by the first step recorded after the settle,
  and, as Reconstruct and Linearize do, takes two recorders' steps for
  concurrent within `RealTimeSlack`: a step just after a quiet settle on
  another machine bounds nothing.
- A claim some runs meet says, in its "as expected" line, when its
  seeds would miss it more than one time in a thousand at the rate it
  measured, and the seed count that would not.
- `GOTOGETHER_CHECK`'s store history checker classifies cycles as
  qhistory does (one classifier): it reports G-nonadjacent at snapshot
  isolation, which it took for write skew and let through.
- The toolkit's own mutation testing: `qweave -mutate -coverpkg` names a
  core package to mutate (never qmut, qrun, qcover or internal/hooks),
  `GOTOGETHER_MUTANT=<id>` turns one mutant on for a whole process
  (`=list` prints them), and `ci/mutate-core.sh <pkg>` runs its tests once
  a mutant and lists the survivors (nightly, for qtrace, qhistory,
  qsweep and internal/depgraph, each with its bar). Its first finding: Linearize's verdicts were unchecked by its
  unit tests, and a brute-force comparison then found a complete search
  cut short, with the guided search stopped, reported as a proof of no
  order: it is no verdict again (kvstore's stale read is proven on loads
  small enough for the default budget).
- An exploration's summary counts the runs that settled and those that
  left calls pending. A failing run's step log is what it recorded (the
  CLI's validate, with the same search, judges it as the test did), and
  a search's order is beside it (`.judged.steps.jsonl`);
  `GOTOGETHER_KEEP` keeps every run's step log (`runs/`), not only those
  a report describes.
- `gotogether scaffold -system` for a binding with eventual vals writes a
  Run that settles and claims `All(Settles())`. A client history (steps
  with invocation times) that fails in its recorded order says to check
  it linearized.
- A step log keeps an assumed step (`quint.assumed`, `Step.Ambiguous`):
  a resolution written out and validated again had its expect checked.
  `validate.InvariantResult.Eventually` marks an eventual val's result;
  `qtest.Run.Recorded` holds the steps as recorded beside `Steps`, the
  steps judged; `Chooser`'s doc gives the ChooseTask and ChooseNemesis
  protocols the scheduler looks for.
- Linearize remembers a complete order's verdict: the guided search's
  orders were asked again in the complete search (found by qtrace's
  mutation testing, S8, whose survivors fell from 55 to 16 of 236, each
  left one an equivalent the nightly names).
- A newcomer's frictions (S7): `Network.OnHeal` runs once per cut that
  ends (Heal, the nemesis, CutLength, Settle), so heals are recorded as
  cuts are; `gotogether lint -binding a.yaml,b.yaml -src .` lints one
  tree against several bindings (`binding.LintSources`, `binding.Bound`),
  a step an error only when no model has it; `binding.Model.Helpers` are
  the frame conditions (`sameNet`) other actions call and step does not,
  which scaffold and lint's lists of actions leave out; a failing run
  whose faults duplicated a request says so in its report. The skills
  say how a heal reaches the trace, two models of one system, handlers'
  state across a restart (`OnCrash`), recording from a hook
  (`s.Context()`), that `qenv.Retry` commits, each pending call
  multiplying a search, the delay faults' options, the default actor,
  and running the CLI from a module that replaces gotogether.
- Each check writes into a directory of its own under `Options.Dir`
  (`check-*`): checks with one Dir once wrote the same module and ITF
  files, so two at once replayed each other's modules and took each
  other's verdicts (found by the verdict-path matrix, validate's
  TestVerdictMatrix).
- binding plans, internal/replay writes: the replay modules
  (RenderModule, RenderBatch, ModuleSpec, Generated, Generate) are the
  validator's own, no longer binding's API; `binding.PlanWithin` is
  `Planner(m, whole).Plan(t)`; `binding.Call` has `String`.
- One Linearize: `qtrace.Linearize(steps, LinearizeOptions{Budget,
  Pending, Settles}, check)` returns a `*Linearization` (the order, a
  `LinearizeVerdict`: Linearizable, NotLinearizable with its Proof, or
  Undecided when the budget ran out; how many orders it tried) and the
  check's error, which ends the search: Linearize, LinearizeStates,
  LinearizeProof and LinearizePending were four, with four results and
  errors carried by panics through the callback.
  `qtrace.DefaultLinearizeBudget` is validate's. qtest's
  DefaultLinearizeBudget and DefaultResolveBudget are gone (validate's
  are the ones); `qtest.Search`, an exhaustive search's extent, is
  `qtest.Extent`, in `Evidence.Exhaust` (its JSON key still `search`).
  An option a workflow ignores is refused by its identity, not its name
  as a string.
- One way to judge: `validate.Checker.Judge(ctx, steps, SearchOptions)`
  orders a run's steps by the search, checks them, confirms the Go
  evaluator's orders with quint where the check fails (searching and
  checking again where quint disagrees) and marks the report;
  `JudgeBatch` judges many runs, with a `BatchCheck` hook for a caller
  that caches verdicts. qtest's Record, Validate and explorations
  (Linearize, LinearizeByKey, ResolvePending) and the CLI judge through
  it, where each once wrote out search, check, confirm, check again and
  mark (a caller that forgot to confirm took the evaluator's orders
  unconfirmed). The CLI's flags: `gotogether validate
  -linearize`, `-linearize-key key`, `-linearize-budget n`,
  `-resolve-pending`. `validate.DefaultLinearizeBudget` and
  `DefaultResolveBudget` (qtest's are the same).
- Linearize memoizes its candidates by prefix: an order that fails at a
  step accepts its prefixes before it and rejects the one through it, so
  the complete search starts from what the guided one learned and never
  replays an extension of a rejected prefix (quorumkv's Repick 209 s to
  138 s, BlindRepair 268 s to 116 s).
- `validate.Options.VerdictsOnly`: verdicts and where each failed, no ITF
  and no model state, the failing runs replayed on the batch's backend.
  Linearize's and ResolvePending's candidates are checked so (they read
  only where each failed; the detail phase had forced ITF and Rust back
  on), and an exploration's runs are searched side by side: quorumkv's
  Repick from 297 s to 209 s on four cores.
- Linearize prefers a conforming order in which the invariants hold: the
  first conforming order was taken, and a run could violate an
  invariant by the order picked (a mutant "caught" for nothing).
- qhistory: a transaction's own appends read back out of order are
  `internal` (the count alone was compared), and a value no transaction
  appended is garbage in any read, not "G1b of transaction 0" in a
  shorter one.
- Linearize's complete search is cut by its budget alone: a beam of 64
  prefixes made five concurrent operations no order explains no verdict
  whatever the budget, while the report said to raise it.
- ResolvePending places a pending call's effect before the first settle
  after its invocation: one placed after a probe that saw it missing made
  an eventual val hold and the run conform.
- A settle settles a trace only if no other recorder recorded a step
  later in time (a warning names it): a harness's settle while a server
  was still at work settled the run, and `Settles()` held.
- `Sim.ResumeFaults`: faults stay off after `Settle` (a probe runs on the
  settled system), which made every phase after a run's first settle
  fault-free without a word; it starts a faulty phase again, and Settle
  says so.
- Eventual vals no run judged are said so: a trace whose last record is
  no settle carries an issue naming the binding's eventual vals, an
  exploration in which none settled logs it, and a claim on an eventual
  val no run judged fails with the fix (a binding with only eventual
  vals let `None(Violates(...))` hold for nothing).
- A linearization (or resolution) cut by its budget is no verdict for
  the invariants and eventual vals of the order shown either: one that
  conformed step by step but failed an eventual val was reported as the
  run's violation.
- Linearize keeps the real-time order a recorder knows: a completion
  recorded by `Call.Done` takes its invocation record's time (no
  `"invoked"` needed; it overrides one), and steps of one recorder are
  ordered by seq, exactly. In a simulation calls take no virtual time
  unless something sleeps, and within `RealTimeSlack` every call was
  concurrent: a client's read that missed its own write, or a stale read
  after a signal, passed linearized. An actor of which no step has an
  invocation time is warned of (sequential consistency, not
  linearizability).
- Partial logs (S11): a recorder whose log starts after seq 1 lost a
  prefix, an error of its own (`lost-prefix`, naming the fix), not a gap;
  `qtrace.Trace.Lost` lists each recorder's losses (`qtrace.Lost`). A loss
  record (`quint.lost`, `Step.Lost`) declares one a log cannot show:
  `qtrace.MarkLost`, and `ReadJSONL` for a torn tail, after each of the
  log's recorders' last seq. A lost tail leaves the run unsettled.
- A snapshot start (S11): a binding's `snapshot: <action>` names a model
  action whose parameters are the whole state; a trace whose first step
  is its starts from that state instead of `init` (`binding.Plan.Start`),
  in every check: the replays, the Go evaluator's and the hidden actions'
  searches, Linearize (`qtrace.LinearizeOptions.First` keeps it first in
  every order), ResolvePending and key partitions. Its recorder's lost
  prefix is then an info (`judged from the state recorded by A at seq
  N`). Lint refuses an unknown snapshot action or one that leaves a
  variable unassigned (`binding.Model.Assigns`); a snapshot step not
  first is skipped, with an issue (`binding.Plan.Issues`).
  `Checker.Key` names the snapshot.
- A lost tail is judged (S11): a failure at a step recorded at or after
  a recorder's cut, by a recorder that went on, may depend on the lost
  steps, and is no verdict (`no verdict (lost steps)`, an issue naming
  the recorder and its seq), not a FAIL; a pass says the recorded steps
  conform and what was lost; eventual vals are not judged. `gotogether
  validate -lost-suffix` and `-lost-prefix` declare losses a log cannot
  show (`qtrace.MarkLost`), and a torn log's warning says what its lost
  tail leaves unjudged. `qtrace.Filter` keeps loss records.
- Partial logs, reviewed (S11): `gotogether itf` starts a snapshot's
  trace at its state (state 0), and `ValidateITF` replays from it;
  `binding.Binding.SnapshotFirst`, `StartIssues`. A within obligation
  whose window reaches a lost tail's cut, or a step within
  `RealTimeSlack` of it, is no verdict. `validate.Result.Lost` names a
  result's lost tails; `None(...)` over an invariant or eventual val a
  run left without a verdict fails, and such a run kills no mutant.
  `Checker.Key` of a run with a loss record has its steps' times. A
  linearized step overlapping the snapshot makes a failure no verdict
  (`no verdict (snapshot overlap)`). A snapshot alone that is disabled
  is `rejects restore`, and counts as a step. A lost tail's settle bounds
  no pending call; `Filter` drops the loss records of a recorder it
  drops; a search out of budget no longer says a failure stands.
- Resume (S11): a binding's `resume: <action>` names a nullary action whose
  nondet picks give the states a log that lost its head may start in. A
  trace whose first recorder lost its prefix, with no snapshot, is judged
  from every admitted state the invariants keep, by the Go evaluator's
  search (with hidden actions too); quint confirms a pass from its
  witness state and a failure from every start (at most 200), and a
  disagreement is an error. Lint checks the action; `Checker.Key` names
  it; Linearize and ResolvePending refuse such a trace; a bounded
  response open in the start state is not judged. A call invoked before
  a lost tail's cut is neither pending nor complete for `Completes()`
  and `Pending`. `gotogether steps` lists each trace's losses.
- Resume, reviewed (S11): a search's state carries the start it came
  from at that step (a state recurring across steps once took another
  start's, and the witness failed in quint). Conformance, invariants and
  eventual vals of a search over hidden actions or a resume's states are
  judged on one path (eventual vals were judged on another path than the
  invariants': a false PASS with hidden actions alone). Claims are
  three-valued wherever read: `Holds` is false, `Run.Expect` says no
  verdict, and `AnyOf` holds if a part does. Lint refuses a snapshot or
  resume that reads a state variable (`binding.Model.Reads`); a run only
  a ruled-out start explains says so; a resume's failure is said as the
  resume's, not hidden actions'.
- **The core** (`github.com/chucklehead-dev/gotogether`). The model drives
  Go (`connect`); trace validation (`qobs`, `qtrace`, `otelio`, `convert`,
  `binding`, `validate`, `input`); simulated environments with fault
  injection (`qenv`, `qtest.System`); metastability (`qload`, `qmeta`,
  `qrole`); coverage, mutants and sweeps (`qcover`, `qmut`, `qsweep`); the
  `gotogether` CLI and the agent skills. Its dependencies: OpenTelemetry
  and yaml.v3.
- **qhegel**: Hegel drives and the model judges and shrinks.
- **qweave**: weaving into a `go -overlay` (`//quint:action`, qenv's
  sync, coverage, mutants).
- **qanalysis**: go/analysis passes (`quintvet`, a golangci-lint plugin)
  and the static action map.
- **qgosim**: qenv and whole programs in gosim, on the fork
  `github.com/chucklehead-dev/gosim`.

Each published module's API is recorded in api/ (core.txt, qhegel.txt,
qweave.txt, qanalysis.txt, qgosim.txt with its `sim` side), fields
(with tags), methods, constants' and variables' values included;
`internal/apisurface` fails on any unrecorded change.

Added to the core since api/core.txt was first recorded:

- Workflow shapes settled before tags: the environment's fault rates are
  the System's (`System.Rates`, `WithRates`) for Explore, Sweep, Mutate,
  Fuzz and qhegel.Shrink, where they were `ExploreConfig.Rates` and
  positional arguments of Fuzz and Shrink; `ShrunkTo` is a `RunClaim`
  (Run.Expect and a Violation's take it, an exploration's does not
  compile with it); `MutationReport.Expect` takes several claims;
  `Signal.Wait` takes a duration (0: no limit), as `WaitFor` does; a
  run's identity is `Run.ID()` ("seed=17", "schedule=17:1",
  "corpus=<file>"), its seed, schedule and corpus fields unexported.
  `System.FaultRates()` is the rates its workflows draw at (its Rates, or
  DefaultRates when zero): encode a shrunk failure for the fuzz corpus at
  them. `System.Judge` fails on steps recorded outside the run and on a
  Rates key that covers nothing, as an exploration does, and no longer
  skips an empty run (`CaseCheck.SkipIfEmpty` says whether the caller
  should). A negative `Signal.Wait` duration times out at once. A
  `Synced` step log on a writer without `Sync`, and a step recorded
  after `Close`, are the sink's error, not silently less durable or lost.
- One seam between qtest and a package that makes runs its own way
  (qhegel): `qtest.CheckCases(t, workflow, binding, opts...)` and
  `System.CheckCases` give a `CaseCheck` (the checker, how each case
  records), refusing what such a check ignores; `System.Judge(t,
  chooser)` runs and checks one run outside any exploration (Shrink's
  replays and its nominal run, sharing explorations' verdicts); and
  `Expectation.Holds(*Run)` judges a claim by its type. They replace
  `qtest.Settings` and `Resolve`, `ExploreConfig.Replay`, and qhegel's
  fake test and its string match on a claim's description, which
  misjudged `AnyOf(Conforms(), ...)` when Hegel found nothing.
  `qhegel.CheckAll` is unexported.
- `qenv.KnownFault` is internal: a process-wide query of what is
  declared would freeze that scope in the API. The workflows still fail
  a Rates key, a sweep's FaultKind or an exception that covers nothing.
- connect is model-based testing only: its fault scripts move next to
  FollowTraces, as `qtest.FaultMap`, `qtest.Fault` and
  `qtest.FaultScript`, and connect no longer links the simulator.
- `sktrace` leaves the core: SimKube's trace format is not the core's to
  freeze. It is examples/sktrace, an unpublished module fleetrollout and
  retrystorm use (`github.com/chucklehead-dev/gotogether/examples/sktrace`).
- Plumbing out of the public API, into the core's internal packages:
  `qobs.Strict`, `Escaped`, `SetDiscard`; `qrun.SetResolver`,
  `GoroutineID`, `Task`; `qenv.Current`, `TaskPanic`, `WithPanicStack`
  and the eight `LockKey`-style functions woven sync calls (qenv/weave
  reaches them through qenv/internal/keyed). Gone, with no caller:
  `qenv.FaultOptions`, `qcover.Default`, `qcover.From`; unexported:
  `qobs.Normalize`, `qobs.Attributes`, `connect.Diff`,
  `validate.Validate` (use a Checker), `qenv.WaitUntil` (use `WaitFor`);
  `qtrace.Fields` moved into the CLI. The five step-log sink
  constructors are two, by durability: `qobs.FileSink(path, d)` and
  `NewJSONLSink(w, d)` with `PerStep` (the old FileSink), `Synced` and
  `Buffered`. `qenv.Swarm` stays, now tested.
- A more faithful environment: a `qenv.Service`'s request may arrive
  late (`rpc.<name>.delay`: `late`, just after its caller's timeout;
  `veryLate`, a timeout after it, after a retry that backed off;
  `lateDuplicate`, a copy that late; `slow`, late but in time), so a
  late original can land after its retry; a second double of one kind and name in a
  simulation panics (they shared their fault kinds); `Sim.Stalls`
  pauses a process (`nemesis.pause`) or keeps it alive but slow
  (`nemesis.slow`); a `Disk` fills (`ErrNoSpace`), fails an fsync
  (`ErrIO`, whose writes a power loss then drops, as Linux does) and
  returns corrupt reads; an exploration of
  seeds runs its first seed twice and fails on the first choice or step
  that differs.
- Liveness: `Sim.Settle` returns a `*qenv.Unsettled` (`ErrUnsettled`)
  when its time runs out before the system is quiet, a task that is no
  daemon still at work or waiting; an exploration judges such a run
  rather than fail it (`Run.Unsettled`, `validate.Report.Unsettled`): it
  does not conform, and `qtest.Settles()` states it alone.
- examples/quorumkv: a replicated store (ABD over three replicas, hinted
  handoff) whose mutants each need one of SettleCore, Completes,
  `eventually`, PCT or LinearizeByKey; its first version re-picked a
  version on retry, which a linearized history showed.
- `qtest.Pending(action)`: a run left a call of the action invoked and
  never completed (`Some(Pending("put"))`).
- Linearize finds an order its guided search misses: when that search
  finds none, every order real time allows is searched, prefix by prefix
  (rejected prefixes pruned), and finding none there, within budget, is a
  proof. A candidate carries the run's settle, so an order whose eventual
  val fails (the probe saw another last write) is not taken.
- ResolvePending: an assumed step is in the process of the step it
  follows (no inferred restarts), re-sequenced steps drop their order
  keys and call ids (a completed call's id named another record), a call
  takes effect only after every step recorded before its invocation, and
  a resolution the binding cannot replay is skipped. Linearize drops call
  ids on re-sequencing too.
- Steer refuses a Chooser factory (its choosers pick tasks and nemeses
  the prefix cannot replay) and System.Mutate (each mutant would steer
  its own sample); it replays entropy chunks (gosim); a steered run's file
  is under its package's directory and replays only in its exploration.
  The determinism self-check of a Chooser factory's runs replays the
  first run's choices, whatever the factory hands out.
- qhistory: G-nonadjacent (two or more rw edges, never adjacent:
  forbidden under snapshot isolation, which allows write skew),
  duplicate elements in a read, and a transaction id 0 refused.
- A step recorded in a simulation carries the simulation's virtual time,
  not the wall clock's (its tasks' and its creator's: `internal/hooks`
  Now), so a client history recorded there has its intervals; and
  explorations run Linearize, LinearizeByKey and ResolvePending, each
  run's search on its own (they were refused).
- `qtest.ResolvePending()`: Record and Validate search what each pending
  call may have done (no effect, or an effect after a step recorded
  since its invocation, unobserved: `qtrace.Step.Ambiguous`, no expect)
  and accept the run if a resolution conforms, saying which.
- `ExploreConfig.Steer`: runs steered toward what the seeds rarely
  reach, each replaying a run that found a new step, observed state,
  fault or code block up to a random choice, then on a fresh seed; a
  steered run's choices are saved, and `GOTOGETHER_RUN=...:choices=<file>`
  runs it alone.
- `qhistory`: a database's isolation level checked from its clients'
  history: Elle's list-append workload (`Workload.Generate`) and
  `Check(level, txns)`, which infers each key's version order from the
  reads and reports Adya's anomalies (G0, G1a, G1b, G1c, G-single, G2,
  incompatible orders, garbage, internal), forbidden or allowed by the
  level. Checked against qenv's Store at each of its levels.
- `qtest.LinearizeByKey(arg)` partitions each actor's history by an
  argument's value and linearizes each partition alone, as its own actor
  (Porcupine's partitioning), for models whose keys are independent.
- `qenv.NewPCT(base, seed, depth, steps)` schedules by Probabilistic
  Concurrency Testing (random task priorities, depth-1 change points):
  bugs that need one task to run far ahead of another, which a uniform
  schedule practically never makes. A chooser that implements
  `ChooseTask(runnable []int)` picks the task by id; its picks are
  recorded as `sched.task` choices, so its runs replay without it.
- `qobs.Invoke(ctx, action, kv...)` records an operation when it is
  invoked and `Call.Done(err, kv...)` when it completes: a trace keeps
  an invocation never completed (`qtrace.Trace.Pending`,
  `validate.Result.Pending`, "invoked, never completed" in a report),
  and `qtest.Completes()` states that every call completed. Steps carry
  `Invocation` and `Call` (`quint.invocation`, `quint.call`).
- `Sim.Settle` takes runnable tasks in turn (a task spinning until
  another acts no longer starves it), and `Sim.SettleCore(d, core...)`
  keeps faulting every process outside a core while the core must finish.
- A settle is in the trace: `Sim.Settle` records one (a step with
  `Settled`, `quint.settled`) when it finds the system quiet, and
  `qobs.Settled(ctx)` records one where a deployment knows. A trace whose
  recorder recorded nothing after it is `qtrace.Trace.Settled`
  (`validate.Result.Settled`); only its eventual properties are judged,
  by every check, and `qtest.Settles()` requires it. A Settle that
  finds the system stuck returns an `*Unsettled` too (`Unsettled.Stuck`).
- qhegel: `Shrink` judges a run whose system does not settle as a
  violation (an exploration's verdict) instead of failing the case as one
  that "did not run"; an eventual property's failure rejects no prefix in
  the case checker's cache; a case's notes and `Violation.Last` skip
  invocation and settle records.
- A binding's `eventually` names vals that must hold when a run ends
  (liveness obligations: no work pending), checked in its last state;
  a result names each `validate.EventuallyName(val)`, and an exploration
  judges them only on a run whose system settled.
- `qtest.NoSurvivors` marks an equivalent mutant by function, change and
  code (`"(Counter).Add: drop call: qobs.Record(...)"`), as a survivor's
  report now prints it: a line added above no longer moves the mark. A
  mark of that form naming two mutants (two `if err != nil` in one
  function) fails, asking for the position.
- `qenv.Script.Misnamed` says which scripted faults cannot happen as
  written (a kind no double declared, matched exactly: no prefix, no
  nemesis family; an option the kind lacks; the nominal option; a Follows
  of no other fault), and an exploration fails on each; it never fired,
  and the claim it was for failed as if the code survived it. Script no
  longer panics in the run on them. A well-named scripted fault a run
  never made (`Run.Unscripted`) fails the exploration too, whatever its
  claims: a crash "after step 300" of a shorter run passed without
  crashing.
- `gotogether validate` ends `RESULT: PASS (3 actors conform to the
  model and satisfy its 2 invariants)` or `RESULT: FAIL (2 of 3 actors: 1
  mismatch after deposit, 1 rejects withdraw)`, in the claims' words
  (`validate.Result.Reason`), where it said `VIOLATION` for any failure;
  it names its one artifacts directory, a step log's step names its file
  and line (`Step.Source` `runs/a.jsonl:5`, `input.Read`), and an input of
  no known shape is refused with its keys and a step log's
  (`input.StepLogExample`).
- A state mismatch's report prints the binding's expect as written
  (`binding.Call.ExpectTemplate`, `validate.Failure.ExpectTemplate`)
  under the rendered one, and the model's state after the step in a
  batch's report too (a chunk replayed on TypeScript has its failures
  replayed on Rust, which writes their ITF).
- Two incarnations of a process overlapping (an old one acting after its
  successor started) is an `error overlap` issue, not a warning: a run
  that passed with it fails. Fence the old incarnation, or order the
  steps with a domain clock.
- A Queue's broker process that is down is unreachable, and its crash
  drops its consumers' connections; held messages come back oldest first.
- `connect.Config.Dir` is one run's: its earlier traces are removed.
- `itf.DecodeProjection`, which connect's state check decodes with: a
  StateDriver's state type with an unexported field, or a field the
  model's record lacks (a misspelling), fails the check instead of
  comparing nothing.
- `qtest.Takes(action).InAtLeast(p)` and `qtest.Exercised(except...)`:
  claims that an exploration's runs took the model's actions, each option
  of the fault kinds they reached and each nemesis they enabled, by name
  (`Run.Nemeses`, `qenv.ChoiceLog.Nemeses`; a heal is not wanted);
  `Evidence.Model` lists the
  actions never taken. `Expect` and `System.Mutate` judge a claim by one
  verdict, so NotVacuous kills mutants too.
- Evidence is a format (`Evidence.Format`, `qtest.EvidenceFormat`
  `gotogether.evidence/2`, whose qenv.Coverage keys are lower case,
  `IsEvidence`) with a role (faithful, mutant, replay: `System.Mutant`,
  `ExploreConfig.Replay`); `GOTOGETHER_EVIDENCE=dir` writes it as
  dir/<package>/<test>/x<N>.json, cleared per test; `gotogether evidence` and
  `qcover.ReadSummary` read by format, and the suite's gaps count
  faithful explorations only.
- An option a workflow ignores fails it, saying why: `Global()` or
  `Linearize()` in a System's Opts, `SkipIfEmpty()` in Validate,
  `Linearize()` in qhegel's Opts.
- Nemeses are chosen per family (`fault.nemesis.crash`, `.partition`,
  `.jump`, a double's own), each at its rate: Rates keys
  `nemesis.crash`, `nemesis.partition`, `nemesis.jump` (`nemesis` covers
  them). A crash-only run draws the same values as before, but its
  choices' kind is renamed (`fault.nemesis` to `fault.nemesis.crash`), as
  is its coverage point (`Coverage.Point("nemesis.crash")`): a corpus
  entry or a recorded run with crashes no longer replays and fails as
  stale, with the seed that finds its run again. One with partitions or
  jumps takes other choices than before.
- `System.Exhaust(t, qtest.Bound{Delays, Faults})`: every run within the
  bound, fewest deviations first, as an Exploration; a search `MaxRuns`
  cuts short fails unless `Bound.Partial`; `Evidence.Search` and
  `Run.Schedule`.
- `GOTOGETHER_RUN='TestX#2:seed=17'` (or `:schedule=`, `:corpus=`) runs
  one run of one exploration alone, as a failing run's report prints it,
  judged by the claims about every run only; it replaces
  `GOTOGETHER_SEED` and `GOTOGETHER_SCHEDULE`, which every exploration of
  the test read. A failure's step log and module are copied out of the
  test's temporary directory, and a cached verdict is checked again so
  its module is there.
- Real-time order: `qtrace.Step.Invoked` (`qobs.Record`'s `invoked`
  key, a converted span's start); Reconstruct reports an order its keys
  force that real time contradicts; `qtest.Linearize()` and
  `qtrace.Linearize` search the orders operations' intervals allow.
- `System.Mutate(t, cfg, claims...)`: claims the baseline meets and a
  mutant is killed by failing; a mutant whose runs record nothing is
  killed. examples/hello's mutation test ends in `NoSurvivors`.
- `ExploreConfig.Corpus` (and `GOTOGETHER_CORPUS`): saved runs replayed
  before the seeds, a failed claim's run saved; `Run.Corpus`. An entry
  (format `gotogether.run/1`) names the exploration that saved it, which
  alone replays it; one that no longer replays fails as stale, with the
  seed that finds its run again.
- qhegel: `Shrink` removes the faults the shrunk failure does not need
  (Hegel's shrinking stopped at a local minimum); `GOTOGETHER_HEGEL_SEED`
  overrides a search's seed.
- Vacuity: `binding.Model.Antecedents` (A of each `A implies B`
  invariant), a binding's `antecedents`, `Exploration.Vacuous`,
  `Evidence.Vacuous` and the claim `qtest.NotVacuous()`, which fails
  when it cannot judge (no antecedent, or one quint cannot evaluate).
- `qtest.LinearizeBudget(n)` and `DefaultLinearizeBudget`: a search that
  runs out of its budget is no verdict (conformance n/a), not a
  rejection. `qtrace.Linearize` returns whether the budget cut it short.
- `qenv.Disk` (writes, fsync, and a crash's "disk.<name>.crash" fault:
  `powerLoss` or `tornWrite` of unsynced writes) and partitions between
  processes, on the simulation's `Network` (`Sim.Net()`: `Partition`,
  `Heal`, `Partitioned`, `OnCut`, `PartitionNemesis`, `CutLength`), for
  `qenv.Service` and for a `qenv.Queue` whose broker runs in a process
  (`NewQueue(s, name, p)`: a broker given at construction, not a field
  to forget); a nemesis's cut heals by itself after `CutLength`.
- Fault kinds per instance: `queue.<name>.ack`, `store.<name>.commit`,
  `disk.<name>.crash`, as `rpc.<name>.*` (`"queue"` covers every queue's;
  `"queue.ack"` matches none, and an exploration whose Rates key, or a
  sweep whose FaultKind, covers no kind declared fails, naming it:
  `qenv.KnownFault`). `Process.Spawn` starts a task in a
  process from outside it. The new fault models run on the native
  runtime (`TestNativeFaultModels`).
- `qobs.SyncedFileSink`, `qobs.NewSyncedJSONLSink`: an fsync per step.
  `qtrace.ReadJSONL` keeps the steps before a torn last line and returns
  a `*qtrace.TornTail`; `input.File.Torn`.
- Clock faults: `Sim.Clocks{Skew, Jump, Drift}`. A process has a wall
  clock (`qenv.Now`: skewed, jumping by a nemesis) and a monotonic one
  (`qenv.Mono`, `qenv.Since`: what `time.Since` measures; a drift moves
  both); `qenv.WaitFor`, `Process.ClockOffset`, `Process.ToVirtual`/`ToReal`.
  examples/lease.
- A run that panics fails as a run (`qtest.RunPanic`, with the task's
  stack through `qenv.WithPanicStack`), named, kept in the corpus and
  with its reproduce line; it used to end the test binary.
- Under `GOTOGETHER_CHECK=1` every `qenv.Store` checks each commit's
  dependency cycles against its isolation level (G1a, G1c, G-single, G2),
  and every `qenv.Queue` its at-least-once contract after each operation.
- CheckBatch replays a chunk of several traces with quint's TypeScript
  backend unless one was chosen ($QUINT_BACKEND, Options.Backend): Rust
  starts an evaluator per test.
- `gotogether simulate -binding`: the model's simulation against each
  invariant the binding's new `simulate` section (`binding.Simulation`)
  says it guarantees (`holds`) or lets the code break (`allows`);
  `-verify N` checks every trace of up to N steps with quint verify
  (Apalache) instead of sampling.
- `gotogether evidence DIR`: a suite's explorations, from the evidence
  each writes (under GOTOGETHER_EVIDENCE, as below), and what no faithful
  exploration tried; `-strict` fails on a gap. Evidence names its `Test`
  (TestX#N), `Package` and `Spec`; `qtest.ReadEvidence`,
  `qcover.ReadSummary` (a Summary or evidence, read by `gotogether cover
  -sim`).

Before this, the project was quint-connect-go
(github.com/casselc/quint-connect-go); docs/HISTORY.md has the path here.

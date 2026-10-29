# The first hour and the loop: blocks, model, bindings, explorations

From the grist skill (its SKILL.md says when to read this).

## grist: an application checked by its model

grist (module `github.com/chucklehead-dev/gotogether/grist`) records
every block's step itself: each commit and committed refusal from the
row it wrote in its own transaction (the store's step log, read back by
`grist.Relay`), each query on return, each effect. The application
writes its decisions and folds; its model is one tenant's log.
examples/counters is the walk-through: read its README's table first.
The person-facing account of all of this, block by block, is
[the user guide](https://github.com/chucklehead-dev/gotogether/blob/main/docs/grist-guide.md)
(docs/grist-guide.md); point a user there rather than restating it.
This page is the agent's checklist of the same mechanics; the design
and its reasons are docs/grist.md.

## The first hour

`go run github.com/chucklehead-dev/gotogether/grist/cmd/grist@latest new
<name>` (`-module`, `-dir`; from a checkout, `cd grist && go run ./cmd/grist new
-replace <checkout> -dir DIR <name>`; `@latest` lets it run outside any module)
writes a module green on its first run: an App (two events, a keyed
read model, a command with declared refusals, a query, a processor), its
binary, `model/<name>_rules.qnt` with its rules filled and
`model/<name>.qnt` with its invariants, the generated skeleton
and bindings, a gristsim test with `Expect` claims, a mutant, a
real-tier test, `run-demo.sh` and a README (the first run downloads the
toolkit, about 2 minutes). `go test -short -v ./...` runs every claim in
about a minute, each summary shown; without `-short` Hegel's shrinks add
about 5 minutes. Then change it one block at
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
   [`references/entities.md`](entities.md) when a command
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
   the processor does not list in `Uses`, is the effect's error. **A peer**
   is a dependency that is itself a grist App: `var Bank =
   grist.NewPeer(App, bank.App)` (named after the peer), `var BankPay =
   grist.NewPeerCall(Bank, bank.Pay)` (its request the command's
   arguments, no response, `Keyed`, the command's refusals): each try of
   `BankPay.Do` is one command of the peer under the effect's key, in the
   caller's tenant, as the caller `grist.PeerCaller("<app>")`
   (`service:<app>`, which the peer's `Authorize` admits), so the peer's
   marker makes an at-least-once effect one command; a conflict or a
   denial is an unknown outcome to the caller. Attach
   `client.Peer(&client.Client{App: bank.App, URL: u})` where the node
   starts; in a simulation `gristsim.Config{Peers: []*grist.App{bank.App}}`
   runs the peer on its own store, node and API (`rpc.<peer>.*`,
   `nemesis.crash.<peer>`). For a graph, list all reachable Apps in
   `Peers`, including peers of peers; shared peers run once, list order
   does not matter. Cycles and colliding service/dependency names are
   errors; per-peer clusters, rollouts and custom doubles are not built.
   See [the composition experiment](../../../docs/grist-composition.md).
   The caller's model has the caller's step
   alone (no site, no `Admits`, no served obligation); judge the peer's
   side with `sys.WithBinding("bank/bank.binding.yaml")` and relate the
   two with `gristtest.Linked(t, App, bank.App, run.Recorded)` for each
   run of an exploration; `qtest.Mutants{Generated:
   gristtest.PeerMutants(App)}` adds `freshKey` (each try under a new
   id: the peer's invariant and `Links` catch it, the caller's model
   cannot). examples/payouts is the pair. What is
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
   [`references/model.md`](model.md) before writing or
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
4. **Explore**: `gristsim.System(binding, App, cfg).WithRates(...)`
   (`ExploreConfig{Seeds: 20, ShortSeeds: 3}` for a quick -short pass);
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
   effect with `actor` `<app>/<tenant>` and `process` the App's name:
   [`references/model.md`](model.md)). Every run records
   both sides: the store's steps (actor `<app>/<tenant>`,
   `gristsim.Actor`: commits relayed from the step log) and the clients'
   calls (`gristsim.ClientsActor`, `<app>/<tenant>/clients`: no App's or
   tenant's name holds a slash, `grist.New`, `grist.CheckTenant`); the
   commits binding says `actors: ['<app>/*', '!*/clients']`, a client-side
   one `actors: ['<app>/*/clients']`, judged with
   `sys.WithBinding(calls, qtest.Linearize())` over a client-side model
   module.
   Duplicated and late requests: `rpc.<app>.request` and
   `rpc.<app>.delay` rates (the App's API is named after it). `HTTP:
   true` serves the node's HTTP API on qhttp and its clients call it with
   `grist/client` (the API's codes and the network's faults,
   `rpc.<app>.response` among them, under the same kinds).
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
   checked possible in the model first (`qtest.Model(t, commits).Expect(t, qtest.Possible(scenario))`, arguments in the model's STRS and INTS) and claimed of
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

## Lints: what the model cannot see

`gotogether lint -sim .` checks the app's non-test files (those
importing grist) for the wall clock (use the event's `Meta.Time`),
math/rand's and crypto/rand's functions (a command's argument instead),
`go` statements (a processor instead) and multi-case selects;
`//qenv:ok <reason>` exempts a line. quintvet's `quintraw` (`go run
github.com/chucklehead-dev/gotogether/qanalysis/cmd/quintvet
-quintweave=false -quintaction=false ./...`) reports an effect calling
`net/http`'s client, a gRPC `ClientConn` or `database/sql` directly:
declare the dependency (`grist.NewDependency`, `NewCall`, `Uses`) and
call `Call.Do`; `//grist:raw <reason>` exempts a line grist cannot
declare yet. examples/library's `clockReceipt` mutant is the lint's
catch (`TestMutant`), `TestLintSim` its contrast.

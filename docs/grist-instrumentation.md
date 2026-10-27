# grist instrumentation (GR3): every seam recorded and simulated by construction

Status: design, 2026-10-27, for docs/ROADMAP.md's GR3. **Slices 1 to
3 are built** (§4; docs/grist.md §0.12, §0.13 and §0.14 say what
building them changed, and the text below is corrected where they did):
`grist.NewDependency`, `grist.NewCall`, `Keyed`, `Uses`, `Call.Do`,
`Handlers`, `Local`, `Transport`, `gristsim.Config.Doubles`,
`grist.DefaultHandlers` (the default double), `grist.OverHTTP`,
`grist.ServeCalls`, `grist.ServeDependency`, `gristtest.TierConfig.Serve`.
The rest of the Go below is **proposed API** (`client.NewPeer`, ...),
written so it parses; every name it takes from an existing toolkit
package exists today.
Read docs/grist.md first (§2.6 processors, §3.1 recording, §3.5 seams,
§0.9-§0.10 the generator); this note builds on it and cites it as "grist
§x".

The maintainer's aim: grist instruments an application automatically,
serves stream publishers and consumers, gRPC services and Kubernetes
controllers, simulates several services and systems together, and asks
little of the user. GR3 is the part every later item stands on: **a seam
is a declaration, and the declaration is what records it, simulates it
and models it.** GR5 (streams), GR6 (gRPC) and GR7 (controllers) then
add transports and inbound adapters to one shape; GR4 composes services
whose calls to each other are that shape's two ends.

## 1. Inventory: where a grist application leaves the framework

Read from grist/ and the three grist examples (counters, wallet-grist,
library) at commit 27c7ce9.

| seam | recorded today | simulated today | who wrote it |
| --- | --- | --- | --- |
| the store (appends, markers, checkpoints) | by construction: the step row in the append's transaction, relayed (`grist.Relay`, grist §0.3) | `qenv.Store` / qsql: isolation, commit faults (`store.<app>.commit`) | grist |
| inbound HTTP (commands, queries) | store side by construction; client side by `grist/client` and gristsim's workload (`internal/calls.Record`, actor `<tenant>/clients`) | qhttp or a `qenv.Service` named `api`: `rpc.api.request`, `.delay`, `.response` | grist |
| a processor's effect (`grist.Effect`) | the effect step `<p>Effect` (trigger, key, `ok` only: nothing of what f asked or got) | crash points `grist.<p>.effect` (`crashBefore`, `crashAfter`) | grist |
| what an effect calls (a bank, a mailer, a ledger) | **only if the app wrote a `NewSite` and called `Site.Record` in its double**; never the request as the caller sent it | **only if the app wrapped its double in a `qenv.Service`** | the app |
| outbound HTTP or gRPC from an effect | no | no, unless the app routes it through a double it wrote | nobody |
| another database written by an effect | no | no (qsql only if the app opens it) | nobody |
| time | `Meta.Time` is the append's `qenv.Now` (virtual in a simulation); decisions see no clock (`Envelope` has `ID`, `Tenant`, `Actor` only, command.go, though grist §2.4 still says "time") | grist's own waits use `qenv.Sleep`; an effect's `time.Now` or `time.Sleep` is real | grist; the effect's code is unchecked |
| randomness | `r.ID(n)` is a UUIDv5 of the command id (reads.go) | deterministic | grist; an effect's `math/rand` is unchecked |
| goroutines | grist's tasks are `qenv.Go` (processor.go) | yes | grist; an effect's `go` is unchecked |
| brokers, watches, gRPC servers | none exist (GR5-GR7) | - | - |

The evidence behind the two rows that matter:

- **A dependency is recorded at its site only by hand.** counters'
  `BankPays` (examples/counters/deps.go: `grist.NewSite[Payment]`, and
  `BankPays.Record` inside `NewSimBank`'s `qenv.NewService` handler),
  wallet-grist's `LedgerPosts` (deps.go, inside `Book.post`) and
  library's `Sends` (mailer.go, inside `Mailer.Send`) are each a
  hand-written double recording a hand-chosen record. The three files
  are 122, 107 and 62 lines, and none of the code is the application's
  logic. counters' mailer has no site at all: a duplicated welcome is
  seen only through effect steps.
- **What the caller asked is recorded nowhere.** The effect step records
  `ok`; the site record holds what the double chose to keep. counters'
  `bankPaysAdmits` is `true` and `everyRetirementPaidOnce` counts records
  by key (model/counters.qnt), so by reading the model a payout of the
  wrong amount or to the wrong counter conforms: no record the binding
  compares carries the request against the trigger.
- **Simulation of a dependency's network is opt-in too.** counters' bank
  and mailer and wallet-grist's ledger are `qenv.Service`s, so their
  requests and answers are lost and late; library's `Mailer` is a struct
  with a `sync.Mutex` called in process, so the `receipts` effect never
  meets a lost answer in any exploration.
- **On the real tier no site is recorded.** counters' `TestRealHTTP`
  attaches a `realMailer` that records nothing and no bank (no retire
  is sent); wallet-grist's `LocalLedger` records its posts only because
  the ledger runs in the test's process (`TierConfig.Deps`).
- **Two of three apps wrote the same rule by hand.** wallet-grist's
  `ledgerPostAdmits` and library's `mailerSendsAdmits` are "no earlier
  record of this key": the dependency deduplicates by the effect key.
  counters' is `true`.
- **A site record is ordered with nothing** (grist §0.10, library's
  TRIAL.md steps 9-10): its generated action sees only the site's own
  records, so "every receipt is for a borrow that happened" cannot be an
  invariant, and library hand-carries the trigger's position (`Receipt.At`)
  to state it as an eventual val.
- **The lints do not reach an app's code.** `gotogether lint -sim`
  checks files that import qenv (qlint/lint.go); an effect in a file
  that imports only grist may call `time.Now`, `rand.Int` or start a
  goroutine unflagged.

So the answer to "which seams are recorded only because the author wrote
a `NewSite`": every dependency an effect calls, on the simulated tier,
and none on the real tier.

## 2. What "automatic" can mean

Four ways to record and simulate a seam without the user writing the
recording, weighed against the toolkit's constraints: the core free of
heavy dependencies (grist is a published module linking qenv, qobs and
qrole into production binaries, grist §7); Orchestrion only in examples;
determinism under qenv (every choice through the Chooser); the spec as
ground truth (a generated model must judge what the code does, so a
declaration the code could contradict is enforced, grist §0.9); and a
step the model must see recorded at the effect's site (AGENTS.md).

**(a) grist-owned typed adapters.** grist owns the client an effect
calls through: a declared call with typed request and response, whose
transport is attached per run (an in-process handler, a simulated
service, HTTP, later gRPC, a broker, the Kubernetes API). Recording is
in grist's code on both ends, so it cannot be forgotten; the simulation
is a `qenv.Service` by construction, so its faults are qenv's choices
under declared names; the request and answer are typed, so the
generator writes their records and rules. Cost: the effect calls
`Pay.Do(ctx, req)` instead of the SDK. Most SDKs (AWS, Stripe, Google's)
take an `*http.Client` or a base URL, which the transport can supply; the
ones that do not (franz-go's own TCP, controller-runtime's client) are
exactly GR5's and GR7's adapters. Heavy clients (gRPC, franz-go,
client-go) live in modules of their own (`grist/gristgrpc`, ...), behind
an interface over JSON in grist itself.

**(b) Weaving third-party clients** (qweave aspects, as qenv/weave and
qenv/fault do). It reaches code grist does not own unchanged. But a
woven `http.Client.Do` knows a method, a URL and a status, not the
application's request, so the generated model would be wire-level
(`httpCall(host, path, status)`) and judge little; a woven SDK keeps its
own goroutines and pools, which qenv's one-task-at-a-time scheduler
cannot run (DESIGN §51: `database/sql`'s pool was read line by line to
run unchanged; franz-go would need qgosim); and every user's build
becomes `qweave go test`. Right for a seam that has no transport to hand
and no typed shape worth modelling; not the default.

**(c) OTel span conversion** (`convert`, for apps already traced). It
needs nothing in the code, but a client span carries no request fields
and no commit order: examples/grpcledger's conversion works because its
server stamps each span with the step's fields and `ledger.seq` at the
commit (ledger.convert.yaml's header), which is instrumentation by hand.
Converted steps have no gap detection and only time order (DESIGN §9.3).
Right as a second recorder of a declared seam on a real tier that cannot
take grist's transport; not a source of the model.

**(d) A dependency declared in the catalog, the generator writing its
actions.** Not an alternative to (a)-(c) but what each needs: without a
declaration the generator cannot name the seam's actions, its records or
its rule, and a recording no model action takes is a binding error
("outcome has no mapping"). GR2 already rests on it (`Refuses`, `Emits`,
`Delivers`, `NewSite`), each declaration enforced at run time.

**Chosen: (d) with (a) as the recorder.** A seam is declared once; grist
records it on both ends and simulates it by construction; the generator
models it. (c) is offered for a traced real tier's declared calls (slice
6), (b) is left for later (question 3).

## 3. The design

### 3.1 Two directions, one shape

Every seam is one of two kinds, and each later item adds instances:

- **Outbound**: an effect calls a dependency. A grist call (this note), a
  peer grist service's command (§3.7), a stream publish (GR5), a gRPC
  client call (GR6), a Kubernetes write (GR7). It runs inside an effect,
  so it has a processor, a tenant, a trigger and the effect key; its
  delivery is the effect's (`AtLeastOnce` or `AtMostOnce`).
- **Inbound**: a source delivers to a command. The HTTP API (built), a
  stream consumer (GR5), a gRPC server (GR6), a watch (GR7), a webhook
  (grist §9). Its command id is the source's identity of the delivery
  (the `Idempotency-Key`, a record's partition and offset, a webhook's
  event id, an object's uid and generation), so the marker deduplicates
  redelivery and the commit is recorded by the relay, as every command
  is today.

GR3 builds the outbound kind and fixes the inbound rule; inbound
adapters are GR5-GR7's.

### 3.2 What the user writes

A dependency is a remote party; a call is one of its operations, typed:

```go
// package counters (built: examples/counters/deps.go)
var Bank = grist.NewDependency(App, "bank")

type PayRequest struct {
	Counter string `json:"counter"`
	Amount  int    `json:"amount"`
}

type PayReceipt struct {
	Ref string `json:"ref"`
}

var ErrDeclined = grist.Rejection("declined")

// Keyed: the bank applies each key once (the effect's key, sent with every try);
// Refuses: the answers that say the payment did not happen
var Pay = grist.NewCall[PayRequest, PayReceipt](Bank, "pay", grist.Keyed(), grist.Refuses(ErrDeclined))

var Payout = grist.NewProcessor(App, "payout", Retired,
	func(ctx context.Context, r *grist.Reads, e CounterRetired, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			_, err := Pay.Do(ctx, PayRequest{Counter: e.Counter, Amount: e.Total})
			return err
		}, grist.AtMostOnce, grist.OnSuccess(Paid.Of(PaidOut{Counter: e.Counter, Retired: int64(m.Pos)})))
	}, grist.Delivers(grist.AtMostOnce, Paid), grist.Uses(Pay))
```

That is all the application declares. Today's `BankDep`, `noBank`,
`payRequest`, `SimBank`, `BankPays` and the `Payment` site record go.

- `grist.Uses(calls...)` on a processor says which calls its effect
  makes, and is enforced as `Emits` is: `Do` from an effect of a
  processor that does not list the call, or outside any effect (a
  decision, a fold), is the effect's error naming the fix. The generator
  needs the pairing to write each call's rule (§3.4).
- `Do` returns the answer, a `*grist.Rejected` wrapped in `NotApplied`
  for a declared refusal (the call did not happen: at most once,
  `grist.failed`), or an error of unknown outcome (a timeout, a lost
  answer, a 5xx: it may have happened; at most once, `grist.unknown`),
  so an effect that returns `Do`'s error gets grist §0.7's semantics
  without classifying anything.
- `Do` makes one attempt. An effect's retries are the effect's
  (`EffectTries`, at least once, under the same key); a transport never
  retries an unknown outcome itself, or an at-most-once effect would be
  at least once.
- `grist.CallKey(ctx)` is the call's key: in an effect, the effect key
  (grist §2.6), sent with every try; in a handler, the key it was sent
  with, so a double or a real service can deduplicate by it. A handler
  that answers a key from its first answer says so, `grist.Replayed(ctx)`,
  and its wrapper records a replay, `<c>Replayed`, instead of a served
  record (a served record is an application, below); without it a
  deduplicating double's replay would read as a second payment. The
  model admits a replay only as an earlier served record of the key,
  request and answer alike (built in slice 1's review, docs/grist.md
  §0.12), so a double replaying another answer than its first, or one it
  never gave, is rejected; a double that applies a call again and says
  `Replayed` hides it (the flag is the handler's word).
- `NewDep` stays for what is not a call (a configuration, a clock in a
  test); `NewSite` stays as the escape hatch for a site grist does not
  serve, until GR7 shows whether anything still needs it.

Which implementation answers is attached where the node starts, as
`Dep.With` is today, so it is per run:

```go
// the binary (grist.Main) and a real-tier test (built in slice 3)
var deps = grist.Dependencies(func(ctx context.Context) context.Context {
	return Bank.With(ctx, grist.OverHTTP("https://bank.internal/v1", nil))
})

// an in-process implementation (built: grist.Handlers of grist.CallHandler,
// grist.Local), recorded at its site as a double is
var local = grist.Handlers{
	Pay.Handler(func(ctx context.Context, r PayRequest) (PayReceipt, error) {
		return PayReceipt{Ref: grist.CallKey(ctx)}, nil
	}),
}
```

In a simulation the same `grist.Handlers` are the double (§3.5); an
application that gives none gets a default one.

### 3.3 What grist records

Both ends of a call are grist's code, so both are recorded by
construction. A call `pay` of dependency `bank` is the Quint name
`bankPay`:

| step | where | actor, process | arguments | observations | order |
| --- | --- | --- | --- | --- | --- |
| `bankPay` (the caller's view) | `Call.Do`: `qobs.Invoke` before the request, `Call.Done` with the answer | the tenant, the App's store | the request's fields, `processor`, `trigger`, `key`, then the response's fields (zero without a response) | none; outcome `ok`, a refusal's name or `unknown` | as the effect step: `order.pos` 2·H+1, `order.p.<processor>` 2·T (at least once) or 2·T+1 (at most once), the poller's thread, before the effect step |
| `bankPayServed` (the site) | where the call is served: `Handlers`' wrapper (`Call.Handler`, served by `grist.Local`: gristsim's doubles), or `grist.ServeCalls` on a real dependency the team owns | the caller's tenant, the App's store | the request's fields, `processor`, `trigger`, `key`, the response's fields | none; outcome as the handler answered | `order.pos` 2·H+1 (H the head the poller read, carried in the cause), or an earlier record of the key's if later, on a thread of its own (the key) |
| `bankPayReplayed` (the site's answer given again, `grist.Replayed`) | as `bankPayServed` | as `bankPayServed` | as `bankPayServed` | as `bankPayServed` | as `bankPayServed` |

(Built so in slice 1. The response's fields are step arguments, not
observations: the actions take them as parameters, each the same-named
field, and a step without a response records their zeros, so every step
of the call has every parameter; a request field and a response field of
one name, or one named `processor`, `trigger`, `key` or `result`, is a
declaration problem `Verify` reports. `processor` is recorded because a
call may be used by several processors, each with its own rule.)

The call carries a **cause**, `grist.CallInfo{App, Tenant, Processor,
Trigger, Head, Key}`, from the effect's context to wherever it is served
(a field of the simulated request; headers on HTTP: `Idempotency-Key` and
`Grist-Cause: <app>/<tenant>/<processor>/<trigger>/<head>`). That is what
lets the site record join the right tenant's trace and be **placed at its
cause**: at 2·H+1, where H is the head the poller read when it took the
trigger (H ≥ the end of the trigger's batch, as the effect step's own
2·H+1). Not 2·T+1: a commit step is ordered at 2·P, P the end of its
batch (record.go's commitOrder), so a trigger T inside a multi-event
batch [T..P] (wallet-grist's transfer appends two `moved` events, each a
post trigger) would put its site record before the commit holding T.
The site's rule is given `log.slice(0, T)`, the prefix the effect decided
on (grist §2.6: a handler reads as of T), not the whole log at 2·H+1. It
closes GR2's left item (a site record ordered after the commit that
triggered it, grist §0.10, §0.11): "every receipt is for a borrow that
happened" is an invariant, and library's `Receipt.At` is no longer the
application's to carry. Slice 1's acceptance includes a trigger inside a
two-event batch (wallet-grist's transfer), whose site record must follow
its commit: built as grist's `TestSiteAfterItsCommit` (a command
appending two events, each a trigger of a processor's call), which
fails with the site at 2·T+1. wallet-grist's ledger is ported in slice
2.

Two choices in the table, and why:

- **The caller's view is an invocation and a completion**, not a step
  on return: a node crashed during the call leaves it pending
  (`qtrace.Reconstruct`'s `Pending`, not a step), which is what
  happened. Recorded on return, a crash would lose it silently (DESIGN
  §13's interrupted steps).
- **The site record has a thread of its own per key.** A dependency
  serving trigger 9's call before trigger 5's would otherwise put
  `order.pos` 19 before 11 on one thread, a cycle (grist §0's ordering
  rule). Per key, the domain clock alone orders it; two tries of one key
  keep their program order. Two tries of one key may still be served out
  of their causes' order (a request delayed past a restarted poller's
  retry, two nodes' pollers): the later-served is placed at the earlier
  record's 2·H+1 when that is later (the transport keeps each key's last,
  `grist.Local`), which is still after its own trigger's commit.

A served record is an application of the call, not a request: a
deduplicating dependency that answers a retry from its first answer
records the retry as a replay, not a served record. The site is the effect's record of what
happened; the caller's step is what the effect saw. A payment made while its answer was lost is a
served record and a caller step with outcome `unknown`; both are in the
trace, and the model relates them.

### 3.4 What grist generates

The catalog gains `Dependencies` (name, calls) and each call's request,
response, `Keyed`, refusals and users (`Uses`). Per call `c` used by
processor `p`, the generator writes into `<app>Gen` (grist §0.9's
skeleton):

- variables `<c>Records: List[<C>Record]` (the site's records:
  processor, trigger, key, request, result, response) and `<c>Calls`
  (the caller's steps, for invariants and for the at-most-once and key
  rules), framed by every other action (built so: `<c>Served` is the
  site action's name);
- the caller action `<c>(trigger, key, ...request, result, ...response)`:
  the trigger is one of `p`'s events, the key is `p`'s for it
  (`keyStable`), the request equals the rule's, and, at most once, no
  earlier call carries the key;
- the site action `<c>Served(processor, trigger, key, ...request, result,
  ...response)`: the trigger's commit is in the log, the request equals
  the rule's (and the trigger is not one `p` skips), and
  `<c>Admits(log.slice(0, T), <c>Records, r)` holds. (Slice 1 built it
  without judging the request, so that a wrong request would be named at
  the caller rather than at the site, whose record comes first: both
  stand at 2·H+1, concurrent on the tenant's clock, reconstructed in
  recording order. Slice 2's review found the record the caller's step
  was meant to back missing: a node that crashes after the dependency
  applied the call, before the caller's completion, leaves the caller's
  step pending, and an at-most-once effect never calls again, so the
  served record is the request's only one; counters' `wrongAmount` then
  conformed in every run of its script, docs/grist.md §0.13.) A wrong
  request is therefore named at the site where the dependency applied it
  (`Rejects("bankPayServed")`) and at the caller where nothing did (a
  lost request; a third party's dependency on the real tier, which
  records no site: `Rejects("bankPay")`).

The application writes one hole per (processor, call) in `<app>Rules`,
`<p><C>` (the processor's Quint name, then the call's capitalized), as it
writes `<p>Emits` today:

```text
// model/counters.qnt: what payout asks the bank for trigger t
pure def payoutBankPay(prefix: List[Event], t: int): BankPayRequest =
  { counter: counterRetiredCounter(prefix[t - 1]), amount: counterRetiredTotal(prefix[t - 1]) }
```

`<c>Admits` is generated, not a hole, when the call is `Keyed` (no
earlier served record of the key: wallet-grist's and library's
hand-written rule) or not (always); an application that knows more (a
bank that refuses a second payment of one counter whatever the key)
overrides it in its rules, reading only the log it is given: the prefix
through the trigger, which is all the effect read (a rule over later
events would judge the site in a state the reconstruction does not put
it in). The bindings gain both actions with no
templates (every parameter the same-named field, the outcome as
`result`), on both sides: the caller's step is a store-side step of the
tenant, so `<app>.binding.yaml` judges it; the clients' binding is
unchanged.

What the generated model now catches that no grist model did: a request
that does not follow from the trigger (a payout of the wrong amount:
`Rejects("bankPayServed")` where the bank applied it, `Rejects("bankPay")`
where it did not; built, counters' `wrongAmount`), a site that
applies a key twice (`Rejects("bankPayServed")`, library's `noDedupe`
without a hand-written rule), and an at-most-once effect whose call was
sent twice (`Rejects("bankPay")`).

### 3.5 Simulated

`gristsim.Run` serves every dependency of the App: a `qenv.Service`
named after the dependency, on no process (as the examples' doubles are
today: the nemesis crashes nodes, not the bank; a crashable dependency
is an option), its handler the
`grist.Handlers` given in `Config.Doubles` (built in slice 1 as `func()
gristsim.Doubles`, called once per run, so a double's state is its
run's) or, when none is given, the **default double** (built in slice 2
as `grist.DefaultHandlers(dep)`, in grist so a real tier or a binary can
attach it in process too, `grist.Local`): a `Keyed` call is applied
once per key and acknowledged (a later try of a key gets that first
answer as a replay, `grist.Replayed`, recorded and judged as
`<c>Replayed`), an unkeyed call is applied every time. It stands in only
for calls whose response has no fields: what a dependency answers in a
field (a reference) is its own, and the zero answer, which no
dependency gives, would exercise one branch of the effect's handling of
it (slice 2's review: `DefaultHandlers` panics naming the fix, and
gristsim's run is an error). A run
that gives a dependency handlers lacking one of its calls is still the
run's error. The
dependency's faults are then the Service's, by name, as qenv's doubles
declare them (`qenv.NewService`: `rpc.<name>.request` drop and
duplicate, `rpc.<name>.delay` late and slow, `rpc.<name>.response`
lost), the same names counters' `rpc.bank.*` and `rpc.mailer.*` have
today, so Rates keys and scripts keep working; library's mailer gains
lost answers by being declared.

```go
// a test giving the bank's answers (a declined payment), served as the
// bank's qenv.Service (built: gristsim.Config{Doubles: doubles}); every
// other dependency gets the default double (built in slice 2)
func doubles() gristsim.Doubles {
	return gristsim.Doubles{Bank: grist.Handlers{
		Pay.Handler(func(ctx context.Context, r PayRequest) (PayReceipt, error) {
			if r.Amount > 100 {
				return PayReceipt{}, ErrDeclined
			}
			return PayReceipt{Ref: grist.CallKey(ctx)}, nil
		}),
	}}
}
```

The call's request crosses the Service as JSON (the same bytes every
transport sends), so a request that would not survive the wire does not
survive the simulation. Everything stays a function of the Chooser: the
Service's faults are choices, a timeout is virtual (1 s as the
examples' doubles use: `gristsim.DependencyTimeout` in slice 1; a
per-dependency option when an app needs another), and the handler runs
as the Service's task.

`gristsim.Config.Setup` stays for doubles that are not calls.

### 3.6 Recorded on the real tier

A transport is a small interface over JSON, so grist imports nothing
heavy and a module of its own can add one:

```text
// package grist: Transport and Local built in slice 1 (gristsim serves a
// dependency's Service requests through Local), the rest in slice 3
type Transport interface {
	Call(ctx context.Context, c CallInfo, call string, req json.RawMessage) (json.RawMessage, error)
}
func OverHTTP(base string, c *http.Client) Transport // POST base/<call>, Idempotency-Key, Grist-Cause
func Local(h Handlers) Transport                     // in process; records the site
func ServeCalls(d *Dependency, h Handlers) http.Handler // a dependency the team owns: records the site
func ServeDependency(d *Dependency, h Handlers) ServeOption // Main: ServeCalls beside the node, over HTTP
```

(Built so in slice 3, docs/grist.md §0.14. `ServeCalls` takes no
recorder: it records into the request context's, so a service wraps it
in `qobshttp.Handler(h, rec)` with its own (gristtest's tier does, with
a step file of its own; `ServeDependency` does, into Main's steps file
under a recorder of its own). Its records join the caller's tenant's
trace (actor the tenant, process the App's name, placed at the cause's
2·H+1, one clock per `ServeCalls` whichever node calls), and it refuses,
400 and unrecorded, a request without its cause or key, of another App,
or that does not decode; nil handlers are the default double, which
refuses a call answering fields. A request a grist caller did not send
for a trigger is then the model's rejection at `<c>Served`.)

`OverHTTP` maps answers as grist/client does (grist §0.7): 2xx the
response, 422 with `{"rejected": name}` a declared refusal, another 4xx
a refusal of no name (`NotApplied`: the effect's outcome is
grist.failed, the caller's step's `unknown`, the only outcome `Do`
records for an answer that is no declared refusal), a 5xx, a reset or a
timeout an unknown outcome (`ErrOutcomeUnknown`). The caller's steps go
to the node's recorder (Main's `PerStep` file) exactly as in a
simulation, so the same binding judges them.

The site exists on the real tier only where grist serves the call: an
in-process `Local` (wallet-grist's ledger), a team-owned service behind
`ServeCalls` (writing its own step file, merged by `gotogether
validate` as two recorders of one run), or a grist peer (§3.7, whose
commit is the site). A third party (a real bank) has no site record: the
model's `<c>Served` is never taken, its invariants over `<c>Served` hold
vacuously, and an eventual val that needs a served record cannot be
judged there (§5's decision 4: such a tier is judged by
`<app>.real.binding.yaml`, the vals marked `real`). Over HTTP the
caller's view is still checked in full: the request against the
trigger, at most one call per key. (Built in slice 3: counters' real
tier pays through its bank behind `ServeCalls` and mails through a third
party's mailer over `OverHTTP`; its site claims are
`everyPaymentAnswerServed`, marked `real`, and `everyMailAnswerServed`,
which the default binding judges in simulations and the real variant
leaves out.)

### 3.7 Service A calls service B: one seam, both sides

A peer is a dependency that is itself a grist App, and its calls are
B's commands:

```go
// proposed: package grist/client (which imports grist, so peers live here);
// service counters pays through service bank, both grist Apps
var BankService = client.NewPeer(App, "bank", bank.App)

var PayCmd = client.NewPeerCall(BankService, bank.Pay) // bank.Pay is a *grist.Command[bank.PayArgs]
```

`PayCmd.Do(ctx, bank.PayArgs{...})` is a call as §3.3 has it, whose
transport is grist/client against B's API (qhttp in a simulation, HTTP
for real), and whose site is B's own commit:

- **On A's side**, the caller's step `bankPay` (B's command's arguments,
  the key, the trigger; the receipt or refusal), in A's tenant's trace,
  judged by A's model as any call.
- **On B's side**, nothing new: the request is a command whose id is
  A's call key, so B's marker answers every retry of A's at-least-once
  effect with the first answer (grist §2.4), B's relay records the commit
  in B's tenant's trace, and B's model judges it as any command. grist's
  client records the call under B's clients actor too, as every
  grist/client call does, for B's calls binding.
- **The link** is the key: A's call key is B's command id, and B's
  events carry it as their cause (`obs.causes`, ("command", key)). The
  effect key is `<processor>/<tenant>/<trigger>` today (processor.go's
  `key`); slice 5 prefixes the App's name for every call
  (`<app>/<processor>/<tenant>/<trigger>`), so two callers' keys never
  collide in B's markers, and B's step records `principal` as the
  service (`service:<app>`), which B's policy admits by name (GR4's
  service identities).

So A's at-least-once effect becomes exactly once in B's log by B's
markers, which no part of the pair writes; A's model sees what A asked,
B's what B did, and a cross-service check relates them by key:
`gristtest.Linked(run, A, B)` (slice 5), in the manner of
`gristtest.Answered`: every call A saw answered has B's step of its key
with the same outcome, every B commit whose id names A has A's call
(pending included), and no key has two B outcomes. A system model over
both (Choreo for the protocol, GR4) reads the same records.

What GR4 then needs and GR3 already fixes: B's API server named after B
(`rpc.bank.*`), not `api`; actors that name the service, so two Apps'
tenant `t1` are two instances (question 1); and one gristsim run holding
both Apps, each on its own store, node and process (GR4's first slice).

### 3.8 How GR5-GR7 fit

| item | outbound (a transport for `NewCall`, or a call kind) | inbound (to commands) | site |
| --- | --- | --- | --- |
| GR5 streams | publish: `Keyed` by the effect key (the producer's idempotence), transport franz-go / the Kinesis SDK in `grist/griststream`, simlog in gristsim | a consumer: each record a command, id `<stream>/<partition>/<offset>`, checkpoints per partition | the consumer's read, as examples/streams learned (the one place every tier records) |
| GR6 gRPC | a generated client method as a transport (`grist/gristgrpc`, protojson at the boundary), simgrpc in gristsim | commands and queries served from the catalog, as `NewHandler` is | the server's commit (a grist peer) or `ServeCalls` |
| GR7 controllers | API writes as `Keyed` calls of a `kube` dependency (server-side apply, its field manager the key) | a watch: each object's generation a command (desired state as events) | kubesim's applied patches; on kwok the caller only |

Each adds a transport or an adapter; none changes what a call records
or how it is modelled.

### 3.9 Time, randomness and goroutines: checked, not wrapped

An effect that reads the wall clock, draws from `math/rand` or starts a
goroutine makes a simulation's run no function of its choices. grist
should not wrap them (`Meta.Time` is the time a handler needs, the
effect key and `r.ID` the identities): it should flag them.
`gotogether lint -sim` extends to files that import grist (slice 4),
with `//qenv:ok` exemptions as today. Timers are GR8's.

### 3.10 Not chosen

- **Recording an interface's methods by reflection** (wrapping a
  `NewDep[T]` value in a proxy that records every call): Go cannot make
  a value of an arbitrary interface type at run time without generated
  code, and the records would be arguments without a rule.
- **A transport that retries**: an at-most-once effect's single call
  would become several (§3.2).
- **The site record ordered by the dependency's own clock**: a third
  party's order says nothing of the tenant's log; the cause does.

## 4. Slices

Each is one agent's work, reviewed before the next; each acceptance test
names the mutant it must catch, built as an overlay
(`gristtest.Mutants`), with the faithful app's contrast claim beside it.

1. **Calls on the simulated tier** (built, 2026-10-27: docs/grist.md
   §0.12, with what it changed here). `NewDependency`, `NewCall`, `Keyed`,
   `Refuses` on calls, `Uses` (enforced), `Call.Do`, `CallKey`,
   `Handlers`, the cause in the effect's context, both steps of §3.3;
   the catalog's dependencies; gristsim serving each dependency as a
   `qenv.Service` with the given `Doubles`; the generator's variables,
   actions, rule holes and generated `Admits` (§3.4); grist.qnt's defs
   for them, checked in grist/model's tests. **Example: counters**
   (payout through `Pay`; deps.go's bank half deleted). **Acceptance**:
   the new mutant `wrongAmount` (pays `e.Total + 1`) is
   `Some(Rejects("bankPay"))` in every run that retires, where by
   reading today's model it conforms; `silentPayout` and
   `checkpointFirst` still caught; `TestPayoutAtMostOnce` and
   `TestPayoutTwoNodes` unchanged in their claims (the bank's Rates keys,
   `rpc.bank.*`, keep their names). grist's own tests:
   `Do` outside an effect or without `Uses` is an error naming the fix
   (each fails with its check removed).
2. **The default double, and every example's dependencies declared**
   (built, 2026-10-27: docs/grist.md §0.13, with what it changed here).
   gristsim's default double (§3.5); counters' mailer, wallet-grist's
   ledger and library's mailer ported (their `NewSite`s and hand-written
   `Admits` deleted); the site placed at its cause, so library's
   `Receipt.At` goes;
   `grist new`'s starter declares one dependency (`TestNewIsGreen`).
   **Acceptance**: library's `noDedupe` (now a double that applies every
   try) is `Some(Rejects("mailerSendServed"))` with no hand-written rule;
   wallet-grist's `ledgerNoDedupe` likewise (`Rejects("ledgerPostServed")`);
   library's mailer meets lost
   answers (`qtest.Reaches("rpc.mailer.response").Faulted()`), which it
   never did. Measured: the lines of deps and rules removed per example.
   Built so, with two corrections, and a third from its review: a site
   record placed before its commit is rejected by the generated site
   action itself (its trigger not yet in the log), not left to an
   invariant (wallet-grist's `TestPostAfterTransfer`); the site action
   judges its request too (§3.4), so library's `wrongReceipt` (its
   receipt now carries no position: it swaps member and book) is
   `Rejects("mailerSendServed")`, and `everyReceiptForABorrow`, which
   restated that guard, is gone (slice 2 built it caught at the caller,
   `Rejects("mailerSend")`, and kept the invariant for a tier where only
   the site is recorded; the review found that tier in a simulation, a
   caller crashed during the call); and the default double stands in
   only for calls answering no fields (§3.5).
3. **The real tier over HTTP** (built, 2026-10-27: docs/grist.md
   §0.14, with what it changed here). `Transport`, `OverHTTP`, `Local`,
   `ServeCalls`; `grist.Main` and `gristtest.TierConfig` serving a
   dependency's `Handlers` on a loopback server that loses every fifth
   answer after applying it. **Example: counters'** `TestRealHTTP`, now
   retiring counters, its bank behind `ServeCalls`. **Acceptance**: the
   mutant `payTwice` (the at-most-once payout calling `Do` again after an
   unknown answer, as a retrying SDK would) is `Some(Rejects("bankPay"))`
   on the real tier (two calls of one at-most-once key, judged from the
   steps file), and the faithful run conforms with
   `grist.unknown` appended for a lost answer and one served record per
   key. Built so, with the slice 2 review's three asks: the lost answer
   with the node killed probed over HTTP (`TestRealLostThenKilled`: the
   bank's step file the payment's only record, the payout's call
   pending), where `wrongAmount` rejects `bankPayServed`, judged against
   the processor's rule at the site; `ServeCalls`'s default refusing a
   call answering fields; and one `ServeCalls` (one site clock) and one
   `Local` per tier for every node. Three corrections: a killed tier node
   commits nothing after its kill (its at-most-once outcome append,
   which outlives the node's context by design, committed an outcome
   whose effect step had died with the node's sink); an unknown
   outcome's error is recorded with its double quotes made single
   (net/http's errors quote the URL, which no Quint string holds); and
   the real tier is judged by `counters.real.binding.yaml` (decision 4),
   its mailer a third party's. `grist.Main` gained `ServeDependency` and
   `serve -dependency NAME=URL`; the lost answer is a 502, as a proxy
   gives.
4. **Lints for an app's own code.** `lint -sim` over files importing
   grist (clock, rand, `go`, multi-case selects), with fixtures, and a
   `quintvet` check that an effect calls no `net/http` client, gRPC
   connection or `*sql.DB` directly (the seam grist cannot record), the
   escape `//grist:raw <reason>`. **Example: library**. **Acceptance**:
   a mutant mailer effect stamping its receipt with `time.Now()` fails
   the lint, naming the line and the fix (`Meta.Time`); the faithful
   tree is clean, CI runs it.
5. **Peer calls.** `NewPeer`, `NewPeerCall` over grist/client, the key
   qualified by the caller's App, `principal` `service:<app>`, B's API
   server named after B, actors naming the service (question 1),
   `gristtest.Linked`; gristsim running a peer App on its own store,
   node and process (`Config.Peers`: the smallest composition, which
   GR4 grows). **Example: counters with a `bank` grist App** in its
   module (`pay`, refused for an unknown account, an invariant "one
   payout per counter"). **Acceptance**: the mutant `freshKey` (each try
   of the at-least-once payout under a new command id) is
   `Some(Violates("onePayoutPerCounter"))` at the bank and fails
   `Linked`; the faithful pair conforms on both sides through lost
   answers and node crashes of either. (If the maintainer prefers, this
   is GR4's first slice instead.)
6. **Spans of declared calls.** A call's caller step also as an OTel
   span event in the span open in its context (qobs's OTel recording),
   and a generated `<app>.convert.yaml` turning otelhttp client spans of
   a declared `OverHTTP` call into caller steps (its route and status;
   the request's fields only if the span carries them), judged by a
   generated variant binding whose caller actions take no request.
   **Example: counters** over otelhttp (slice 3's tier). **Acceptance**:
   slice 3's `payTwice`, recorded only through spans, is
   `Some(Rejects("bankPay"))` in the spans variant (two calls of one
   at-most-once key). Lowest priority: it
   adds a recorder, not a seam.

## 5. Decisions (the maintainer, 2026-10-27)

The maintainer took the recommendation on all five; each below is now
the design, and the slices build on it.

1. **Actors that name the service** (decided: yes, in slice 5). Today a store-side step's actor is
   the tenant (grist §3.1); two Apps' tenant `t1` in one run would be one
   instance with two processes, which the binding refuses (no
   `restart`). Decision (as recommended): always `<app>/<tenant>` and
   `<app>/<tenant>/clients` (the App's name refused if it holds a slash),
   the generator writing `actors: ['<app>/*', '!*/clients']`, and the
   API server and the call keys named after the App; done in slice 5, a
   regenerate for every app, before any tag. Only in compositions
   would make a binding depend on where its app runs.
2. **Declared calls instead of instrumented interfaces** (decided: yes). The
   application writes request and response structs and `Uses`, where
   today it writes an interface. Decision (as recommended): yes; the types are what
   the generator models and what a transport sends, and grist enforces
   them at run time as it does `Emits`. Keep `NewDep` for non-calls and
   `NewSite` as an escape hatch, revisited after GR7.
3. **Weaving third-party clients** (decided: not in GR3). Decision (as recommended): not in GR3. Transports
   cover SDKs that take an `http.Client`; GR5-GR7 own the clients that
   do not; slice 4's lint names the raw calls left. Revisit if an SDK
   resists all three, with qweave aspects in grist's module (YAML and a
   small package, as qenv/fault) rather than a dependency on qweave.
4. **A third party's site on the real tier** (decided: a real variant binding, no new mechanism). It is never recorded, so
   an eventual val over `<c>Served` ("every retirement paid") cannot be
   judged there. Decision (as recommended): no new mechanism: a test-only variant
   `<app>.real.binding.yaml` through GR2's marks (the vals a real tier
   can judge marked `// grist: eventually default real`), the real tier
   judging the caller's view only, and the site claims stated in
   simulations, where grist serves the site; not
   hidden actions (DESIGN §49), whose nondets over requests would admit
   any payment.
5. **Cross-service claims** (decided: `gristtest.Linked` in slice 5, the system model in GR4). Decision (as recommended): `gristtest.Linked`'s Go
   cross-check in slice 5, and a system model in GR4, designed then:
   reading both services' records as one Quint instance needs a
   cross-actor order (each seam's records carrying a Lamport stamp, as
   glomers' do) and a binding that merges actors, a core change GR3
   should not presume.

# A grist application's model: the skeleton and the rules

The model is one tenant's log. Its skeleton and both bindings are
generated from the App's catalog by the app's binary (`go run ./cmd/<app>
model`, `grist.Main`; `-init` also writes a first `model/<app>_rules.qnt`
with every hole stubbed and a first `model/<app>.qnt`); the application
writes the rules in `model/<app>_rules.qnt` and the invariants in
`model/<app>.qnt`, which regenerating never touches. examples/counters'
and examples/wallet-grist's are complete ones: read them beside this
page.

The files import each other one way only, each the ones below it
(quint's REPL refuses a file that a file it imports imports back, and
the generator refuses such a cycle, naming it):

| file | who writes it | modules |
|---|---|---|
| `model/<app>.qnt` | you: the invariants | `<app>`, `<app>Calls` (the bindings') |
| `model/<app>_steps.qnt` | the generator | `<app>Gen`, `<app>CallsGen` |
| `model/<app>_rules.qnt` | you: the holes | `<app>Rules` |
| `model/<app>_gen.qnt` | the generator | `<app>Events` |
| `model/grist.qnt` | vendored | `grist` |

An app whose `model/<app>.qnt` still holds module `<app>Rules` (the
layout before the rules had their own file) is the model command's
error, with the move: module `<app>Rules` as it is to
`model/<app>_rules.qnt`; in `<app>` and `<app>Calls`, `import
<app>Rules.* from "./<app>_rules"` and the skeleton's modules from
`"./<app>_steps"`.

## What is generated

`model/<app>_gen.qnt` and `model/<app>_steps.qnt` (never edit them):

- module `<app>Events` (`_gen`): an `Event` constructor per event type, its
  scalar fields in Go order (`counterCreated(counter, name, creator)`;
  its cause is stamped by the action that appends it); accessors
  (`counterCreatedName(e)`); reads (`counterCreatedRead`,
  `counterCreatedByName(v)`); per read model its selection of the log
  (`totalsEvents(log, counter)`) and the reads a read of it records
  (`totalsReads(counter)`); a record type per command's and query's
  arguments with `caller` (`CreateCounterArgs`), per query's result,
  per site's record, per declared call its request, response and step
  record (`BankPayRequest`, `BankPayResponse`, `BankPayRecord`: processor,
  trigger, key, request, result, response).
- module `<app>Gen` (`_steps`), the store's side: `log`, `checkpoints`, `appended`,
  `effects`, `answered`, one list per site, and per call the caller's
  steps and the site's (`bankPayCalls`, `bankPayRecords`); per
  command an action (a reused key; a `conflict` if it is conditioned;
  ok and each declared refusal, with `freshKey`, `covers` of its rule's
  needs and `conditionHolds`; a command a rule guards takes its outcome
  from `<c>Ruled`: its decision's, then, when ok, the first refusal of
  `rulesOf` over what it appends, its read set covering every rule's
  needs that ran), per query one, per processor those of
  its declared shape (`Emits`: one action; `Delivers(AtLeastOnce)`: the
  effect step, the checkpoint over `atLeastOnceDone`, an outcome step
  that never happens; `Delivers(AtMostOnce)`: checkpoint, effect,
  outcome), per site one, per call two (below); every frame condition;
  `step`.
- module `<app>CallsGen` (`_steps`), the clients' side: no positions, no
  processors, a key's first answer final, `denied` answering nothing.
- `<app>.binding.yaml`, `<app>.calls.binding.yaml` and one
  `<app>.<variant>.binding.yaml` per test-only variant (below), and
  `model/grist.qnt` vendored.

The generator needs the shapes declared: `grist.Refuses(ErrA, ...)` on a
command (a decision returning another rejection is its error),
`grist.Emits(Type...)` or `grist.Delivers(grist.AtLeastOnce|AtMostOnce,
SuccessType)` on a processor (a result contradicting it fails the
trigger). A processor with neither is the generator's error. A refusal
may not be named `ok`, `conflict`, `denied` or `error` (each already an
outcome of grist's: Verify says so), nor the app `grist` (its model
would be the vendored library's file). A command that declares no
refusals and refuses at run time is refused, but its binding has no
outcome for it: the binding's "outcome has no mapping" carries the
step's error, which names the command and says to declare
`grist.Refuses`. A `Delivers` processor's handler that returns Skip
records the processor's skip step (`<p>Skip`: its checkpoint alone),
which the model takes where your `<p>Skips(prefix, trigger)` says the
handler skips; a handler's Fail is recorded so too, with its
`grist.failed`, which a skip's empty batch does not match (a Mismatch,
as an Emits processor's Fail is).

## The holes (module `<app>Rules`, `model/<app>_rules.qnt`)

Per command `c` (its arguments a record: `c.counter`, `c.caller`):

- `<c>Outcome(log, c): str`: the rule, `"ok"` or a declared refusal,
  decided on the log at the commit (for a conditioned command, equal to
  the log at its read position for everything it read). It must not
  depend on processors' events if the clients' side is to agree (its
  log holds the commands' events alone).
- `<c>Events(log, c): List[Event]`: what an ok appends.
- `<c>Needs(c, result): Set[Read]`: the reads the rule needs for that
  outcome; the committed step's read set must cover each, so a lost
  read is caught at every commit. A refusal decided before any read
  (an invalid request) needs `Set()`.

Per rule `r` (`grist.NewRule`, on event type T): `<r>Refusal(log, e):
str`, its verdict on `e`, a T event a guarded command appends, on the
log the command read (`""` admits it, else one of the rule's declared
refusals), and `<r>Needs(e, refusal): Set[Read]`, the reads that verdict
needs (`loansReads(bookBorrowedMember(e))`). The generated guard runs
them only after an ok `<c>Outcome`, so `<c>Outcome` and `<c>Needs`
leave the rule out (`<c>Needs` is called with `"ok"` when a rule
refused); every action of a command declaring T in `grist.Appends`
requires the step's outcome to be the guarded one and its read set to
cover each rule's needs. A run with the rule dropped (gristtest's
generated `drop-<rule>` mutant) commits where `<r>Refusal` refuses: a
`Rejects` of the command.

Per query `q`: `<q>Answer(log, q)`, the result's scalar fields as a
record (or the scalar), compared with the step's at its position.
Per processor `p`: `<p>Emits(prefix, trigger)` (Emits) or
`<p>Success(prefix, trigger)` (Delivers with a success event): `prefix`
is the log through the trigger, `prefix[trigger - 1]` the trigger; a
success event names the trigger (`paidOut(counter, trigger)`) so each
trigger's outcome is its own; and for a Delivers processor
`<p>Skips(prefix, trigger): bool`, whether its handler skips the
trigger (returns Skip: no effect step; `false` for a handler that never
does): its effect actions require it false, its skip action true. Per
processor and declared call it uses, the request it makes (below). Per
site `s` (`grist.NewSite`, the escape hatch for a double grist does not
serve): `<s>Admits(records, r)`, the dependency's own rule. A site's
records are ordered with no commit of the tenant's log (a record may be
reconstructed before the commit that asked for it), and `<s>Admits`
sees the records, not the log: a relation between them and the log is
an eventual val (`// grist: eventually`), never an invariant. A declared
call's served records are placed after the commit holding their
trigger, and the generated `<call>Served` itself takes only a request
that follows from its trigger (examples/library's mailer: a receipt for
no borrow rejects `mailerSendServed`), so no invariant need say it. A read `r.Exists(T, grist.Tag("k", v))` records the read the
generator names `<t>ByK(v)` (`r.Exists(T)`: `<t>Read`), what `<c>Needs`
returns for it. A field the model leaves out (a list in a query's
answer) is tagged `grist:"-"`; an untagged one is the generator's error. `STRS` and `INTS`: what `step` (`gotogether
simulate`) draws arguments from. Read models' folds are yours (Go
closures cannot be translated): write a def over the generated
selection (`totalsEvents(log, c).foldl(0, (s, e) => s + counterIncrementedBy(e))`).

`go run ./cmd/<app> model` names every missing hole with a stub to
start from. A new block's holes are missing until written; a changed
argument list changes nothing in a hole (it takes the record).

## Invariants and variants

Write vals in module `<app>` (the store's side) or `<app>Calls` (the
clients'), each marked on the line before it:

- `// grist: invariant [bindings]`: judged at every step, and held by
  the model on its own (`simulate: holds`);
- `// grist: eventually [bindings]`: judged after the run's last settle
  (a claim names its verdict `qtest.Violates(validate.EventuallyName("v"))`,
  "eventually v", not `Violates("v")`);
- `// grist: violable [bindings]`: a test-only invariant the design
  allows to fail (`simulate: allows`), such as one ok welcome per counter
  under at least once.

**Obligations are generated, not written** (docs/grist.md §3.3.1): the
steps module defines, and the store's default binding judges at a settle
beside your eventual vals, one eventual val per processor,
`<processor>EventuallyHandled` (its checkpoint passed every trigger; at
least once also skipped by `<p>Skips`, an ok effect step or the
trigger's `grist.failed`; at most once the checkpoint alone, since a
crash after it loses the effect or its outcome by right), and one per
`Keyed` call a processor uses, `<call>EventuallyServed` (every call its
caller saw answered, ok or refused, has the dependency's served record
of its key). Do not write them again (a val of the same name is the
generator's error): write only what says more, such as every trigger's
served record ok (library's `everyBorrowReceipted`), a served record
equal to the caller's field for field (`everyPaymentAnswerServed`), or
an at-most-once outcome logged (`atMostOnceOutcomesLogged`, test-only:
a crash after the effect loses it by right). A stalled processor (a
handler that never returns, a poller that drops its trigger) is
`Violates(validate.EventuallyName("<p>EventuallyHandled"))`, and a
dependency answering a `Keyed` call it never applied (a proxy that
acknowledges and forwards nothing: examples/wallet-grist's
`ledgerAcksUnposted`) `Violates(validate.EventuallyName("<call>EventuallyServed"))`;
a run that never settles judges none of them (`qtest.Settles()` says
so). A `real` variant judging every default invariant judges the call
obligations too: a `Keyed` third party recording no site fails them
there (docs/grist.md §3.3.1's limits).

Bindings default to `default` (`<app>.binding.yaml`); any other name is
a test-only variant, `<app>.<name>.binding.yaml`: the same actions,
judged by its own vals (counters' `once`, `exactly`, `retiring`). A
variant that judges every invariant of the default binding (`retiring`)
compares the steps' observations as the default does, and judges the
generated obligations too; one that judges
only its own vals (`once`, `exactly`) leaves them to the default
binding's claims, and compares none (each expect is evaluated at every
step of every replay). Each eventual val costs a replay per run: keep
the per-push binding's few.

A real tier whose dependency is a third party's (a real bank, a mail
provider: no `grist.ServeCalls`, so no site records) cannot judge a val
over that dependency's `<call>Records`: mark every val the real tier can
judge `real` too and validate its steps with `<app>.real.binding.yaml`,
leaving the site claims to the simulations, where grist serves every
site (docs/grist-instrumentation.md §5, decision 4). counters' bank runs
behind `ServeCalls` on its real tier and its mailer is a third party's,
so `everyPaymentAnswerServed` (every answer the payout got is the bank's
served record, or unknown) is marked `default retiring real` and
`everyMailAnswerServed` `default retiring`; judged by the default
binding, its real tier violates the second.
Both modules (in `model/<app>.qnt`) import `grist`, `<app>Events` and `<app>Rules` themselves
beside the skeleton (the starter does): quint's flattener loses names
re-exported twice, and the validator's Go evaluator, which compiles the
module from another directory, falls back to quint processes, several
times slower.

## Names

A Go field's JSON name is a Quint parameter or a record field. The
generator renames a parameter Quint reserves (`of`, `item`, `size`, ...:
`of_`, with the binding's template) and refuses a JSON name that is no
identifier, an integer tag (a tag is text the model cannot write from
an int: make it a string), and a pointer argument (recorded only when
set). In your own defs, avoid Quint's built-ins as parameter names
(`item`, `set`, `range`, `exists`...); `quint typecheck` names the one it
refuses. grist's own step fields (`id`, `caller`, `readPos`, `reads`,
`result`, `trigger`, `key`, `ok`) are refused by `App.Verify`.

## Guarantees, and what a mismatch shows

At least once, the checkpoint commits what the run reached (the success
after an ok effect step, `grist.failed` after the tries); at most once,
the checkpoint comes first, the effect once, then its outcome
(`grist.failed` after `grist.NotApplied`, else `grist.unknown`). A lost
effect or outcome is admitted; an application that needs exactly once
states it with test-only eventual vals over the site's records
(counters' `everyRetirementPaidOnce`, `everyPayoutOutcomeLogged` with
`atMostOnceOutcomesLogged`), checked with two pollers
(`gristsim.Config.Nodes: 2`). The checked-in model is generated from the
faithful app's declarations, and judges every mutant's runs: a mutant
that changes a guarantee (and its declaration with it, which the runtime
enforces) is rejected at its first trigger (`Rejects(<processor>)`:
counters' `checkpointFirst`), before what it would cause.

## A declared call, at both ends

A call (`grist.NewCall`, used by a processor with `grist.Uses`) is two
steps, both grist's: the caller's `<call>` (`bankPay`: dependency, then
call, capitalized), recorded by `Call.Do` as an invocation and its
completion, ordered as the effect step it precedes; and the site's
`<call>Served`, recorded by the handler's wrapper where the dependency
applies it, placed at its cause (`order.pos` 2·H+1, H the head the
poller read: after the commit holding the trigger, even inside a batch
of several events) on a thread of its key. Each records the processor,
the trigger, the key, the request's and the response's fields (zeros
when there is no response), the outcome as `result` (`ok`, a declared
refusal, and at the caller `unknown`). The generator writes:

- a rule hole per (processor, call), `<p><Call>(prefix, trigger):
  <Call>Request`: the request the effect makes for the trigger (counters'
  `payoutBankPay`: the retired counter and its total). Both ends'
  actions require it (below).
- the caller's action: a user's trigger, before its effect step (at
  least once: its next trigger; at most once: checkpointed, no effect
  step yet, no earlier call of the key), the key the trigger's
  (`callKeyStable`).
- the site's action: a user's trigger in the log, its request the rule's
  (as the caller's), and `<call>Admits(prefix, records, r)`, generated (a
  `Keyed` call: no earlier served record of the key; else any) unless
  your rules define it, reading only the log through the trigger. The
  site's record is recorded inside the call, before the caller's
  completion, at the same position, so a wrong request is named at the
  site first where the dependency applied it (`Rejects("bankPayServed")`:
  counters' `wrongAmount`, and the only record of it when the payout's
  node crashed during the call), at the caller where it did not (a lost
  request, a third party's dependency: `Rejects("bankPay")`).
- the replay's action, `<call>Replayed`: an earlier served record of the
  key, request and answer alike; it changes nothing.

A double that deduplicates says so: `grist.Replayed(ctx)` when it answers
a key from its first answer, so the retry records no second application
but a replay, `<call>Replayed`, which the generated action admits only
as an earlier served record of the key, request and answer alike: a
double replaying another answer than its first (a refusal included), or
one it never gave, is rejected there. Keep each key's first answer,
refusals too. The default double (`grist.DefaultHandlers`, what gristsim
serves a dependency the run gives no handlers) does so; it stands in only
for calls whose response has no fields. An invariant or
eventual val reads `<call>Records` (counters'
`everyRetirementPaidOnce`: one ok served record per retirement's
trigger) and `<call>Calls` (`everyPaymentAnswerServed`: every answer
the caller got, unknown aside, is a served record, field for field: on
a real tier that checks the transport and the site's step file); an invariant that each record's request
follows from its trigger restates the site's action, and holds in every
run that conforms.

A double grist does not serve may still record its effects with a site:
`grist.NewSite[T](App, name)` and `Site.Record(ctx, tenant, v)` in its
handler (actor the tenant, process the App's name); the generator writes
its list (`<site>Records`) and action, ordered with no commit (an
eventual val relates it to the log). It is an escape hatch, kept until
GR7: declare a call instead, which grist records, orders and models.

## Check it alone

`gotogether simulate -binding <app>.binding.yaml` runs the model's own
`step` against the binding's `simulate:` invariants before any Go runs;
`gristtest.Generated(t, App)` (a `TestGenerated`) fails when the
checked-in files are stale, a hole is missing, or a module lacks those
imports.

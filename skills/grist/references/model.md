# A grist application's model: the skeleton and the rules

The model is one tenant's log. Its skeleton and both bindings are
generated from the App's catalog by the app's binary (`go run ./cmd/<app>
model`, `grist.Main`; `-init` also writes a first `model/<app>.qnt` with
every hole stubbed); the application writes the rules and invariants in
`model/<app>.qnt`, which regenerating never touches. examples/counters'
and examples/wallet-grist's `model/<app>.qnt` are complete ones: read
one beside this page.

## What is generated

`model/<app>_gen.qnt` (never edit it):

- module `<app>Events`: an `Event` constructor per event type, its
  scalar fields in Go order (`counterCreated(counter, name, creator)`;
  its cause is stamped by the action that appends it); accessors
  (`counterCreatedName(e)`); reads (`counterCreatedRead`,
  `counterCreatedByName(v)`); per read model its selection of the log
  (`totalsEvents(log, counter)`) and the reads a read of it records
  (`totalsReads(counter)`); a record type per command's and query's
  arguments with `principal` (`CreateCounterArgs`), per query's result,
  per site's record, per declared call its request, response and step
  record (`BankPayRequest`, `BankPayResponse`, `BankPayRecord`: processor,
  trigger, key, request, result, response).
- module `<app>Gen`, the store's side: `log`, `checkpoints`, `appended`,
  `effects`, `answered`, one list per site, and per call the caller's
  steps and the site's (`bankPayCalls`, `bankPayRecords`); per
  command an action (a reused key; a `conflict` if it is conditioned;
  ok and each declared refusal, with `freshKey`, `covers` of its rule's
  needs and `conditionHolds`), per query one, per processor those of
  its declared shape (`Emits`: one action; `Delivers(AtLeastOnce)`: the
  effect step, the checkpoint over `atLeastOnceDone`, an outcome step
  that never happens; `Delivers(AtMostOnce)`: checkpoint, effect,
  outcome), per site one, per call two (below); every frame condition;
  `step`.
- module `<app>CallsGen`, the clients' side: no positions, no
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

## The holes (module `<app>Rules`)

Per command `c` (its arguments a record: `c.counter`, `c.principal`):

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

Per query `q`: `<q>Answer(log, q)`, the result's scalar fields as a
record (or the scalar), compared with the step's at its position.
Per processor `p`: `<p>Emits(prefix, trigger)` (Emits) or
`<p>Success(prefix, trigger)` (Delivers with a success event): `prefix`
is the log through the trigger, `prefix[trigger - 1]` the trigger; a
success event names the trigger (`paidOut(counter, trigger)`) so each
trigger's outcome is its own; and for a Delivers processor
`<p>Skips(prefix, trigger): bool`, whether its handler skips the
trigger (returns Skip: no effect step; `false` for a handler that never
does): its effect actions require it false, its skip action true. Per site `s`: `<s>Admits(records, r)`, the
dependency's own rule (a ledger's dedupe by key; `true` for a bank that
records every payment). A site's records are ordered with no commit of
the tenant's log (a record may be reconstructed before the commit that
asked for it), and `<s>Admits` sees the records, not the log: a relation
between them and the log ("every receipt is for a borrow") is an
eventual val (`// grist: eventually`), judged after a settle, never an
invariant (GR3, docs/grist-instrumentation.md, places a served call's
record after its trigger's commit). A read `r.Exists(T, grist.Tag("k", v))` records the read the
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

Bindings default to `default` (`<app>.binding.yaml`); any other name is
a test-only variant, `<app>.<name>.binding.yaml`: the same actions,
judged by its own vals (counters' `once`, `exactly`, `retiring`). A
variant that judges every invariant of the default binding (`retiring`)
compares the steps' observations as the default does; one that judges
only its own vals (`once`, `exactly`) leaves them to the default
binding's claims, and compares none (each expect is evaluated at every
step of every replay). Each eventual val costs a replay per run: keep
the per-push binding's few.
Both modules import `grist`, `<app>Events` and `<app>Rules` themselves
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
refuses. grist's own step fields (`id`, `principal`, `readPos`, `reads`,
`result`, `trigger`, `key`, `ok`) are refused by `App.Verify`.

## Delivery, and what a mismatch shows

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
that changes a delivery (and its declaration with it, which the runtime
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
  `payoutBankPay`: the retired counter and its total). The caller's
  action requires it, so a payout of the wrong amount is
  `Rejects("bankPay")` (counters' `wrongAmount`).
- the caller's action: a user's trigger, before its effect step (at
  least once: its next trigger; at most once: checkpointed, no effect
  step yet, no earlier call of the key), the key the trigger's
  (`callKeyStable`).
- the site's action: a user's trigger in the log, and `<call>Admits(prefix,
  records, r)`, generated (a `Keyed` call: no earlier served record of
  the key; else any) unless your rules define it, reading only the log
  through the trigger. Its request is the caller's to judge: the site's
  record is recorded inside the call, before the caller's completion, at
  the same position, so judging the request there would name the site
  for the caller's error.

A double that deduplicates says so: `grist.Replayed(ctx)` when it answers
a key from its first answer, so the retry records no second
application. An invariant or eventual val reads `<call>Records` (counters'
`everyRetirementPaidOnce`: one ok served record per retirement's
trigger) and `<call>Calls`.

A double grist does not serve may still record its effects with a site:
`grist.NewSite[T](App, name)` and `Site.Record(ctx, tenant, v)` in its
handler (actor the tenant, process the App's name); the generator writes
its list (`<site>Records`) and action, ordered with no commit (an
eventual val relates it to the log).

## Check it alone

`gotogether simulate -binding <app>.binding.yaml` runs the model's own
`step` against the binding's `simulate:` invariants before any Go runs;
`gristtest.Generated(t, App)` (a `TestGenerated`) fails when the
checked-in files are stale, a hole is missing, or a module lacks those
imports.

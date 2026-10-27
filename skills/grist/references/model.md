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
  per site's record.
- module `<app>Gen`, the store's side: `log`, `checkpoints`, `appended`,
  `effects`, `answered` and one list per site (`bankPaysRecords`); per
  command an action (a reused key; a `conflict` if it is conditioned;
  ok and each declared refusal, with `freshKey`, `covers` of its rule's
  needs and `conditionHolds`), per query one, per processor those of
  its declared shape (`Emits`: one action; `Delivers(AtLeastOnce)`: the
  effect step, the checkpoint over `atLeastOnceDone`, an outcome step
  that never happens; `Delivers(AtMostOnce)`: checkpoint, effect,
  outcome), per site one; every frame condition; `step`.
- module `<app>CallsGen`, the clients' side: no positions, no
  processors, a key's first answer final, `denied` answering nothing.
- `<app>.binding.yaml`, `<app>.calls.binding.yaml` and one
  `<app>.<variant>.binding.yaml` per test-only variant (below), and
  `model/grist.qnt` vendored.

The generator needs the shapes declared: `grist.Refuses(ErrA, ...)` on a
command (a decision returning another rejection is its error),
`grist.Emits(Type...)` or `grist.Delivers(grist.AtLeastOnce|AtMostOnce,
SuccessType)` on a processor (a result contradicting it fails the
trigger). A processor with neither is the generator's error.

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
trigger's outcome is its own. Per site `s`: `<s>Admits(records, r)`, the
dependency's own rule (a ledger's dedupe by key; `true` for a bank that
records every payment). `STRS` and `INTS`: what `step` (`gotogether
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
- `// grist: eventually [bindings]`: judged after the run's last settle;
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

## A dependency's record of its own effects

A double (a simulated bank, a ledger) records each effect where it makes
it, with a site: `var BankPays = grist.NewSite[Payment](App, "bank-pays")`
and `BankPays.Record(ctx, tenant, Payment{...})` in its handler (actor
the tenant, process the App's name, so the step joins the tenant's
trace). The generator writes its list (`bankPaysRecords`) and action.

## Check it alone

`gotogether simulate -binding <app>.binding.yaml` runs the model's own
`step` against the binding's `simulate:` invariants before any Go runs;
`gristtest.Generated(t, App)` (a `TestGenerated`) fails when the
checked-in files are stale, a hole is missing, or a module lacks those
imports.

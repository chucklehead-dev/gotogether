---
name: grist
description: Build an event-sourced Go application on grist (gotogether's framework, slice 1) so it is checked against a Quint model without annotations - events, plain and keyed read models, commands conditioned on their pinned reads (Unconditioned only on purpose), idempotency by command id (a key's first answer final, refusals included), queries, processors (Emit, Skip, Effect at least once), options, the store contract (grist/store) with memstore and simstore, gristsim.Run as a qtest.System, a hand-written model over one tenant's log and its binding. Use when writing or changing a grist application, its model or binding, when a gristsim exploration fails, or when a store must keep grist's contract (grist/storetest).
---

# grist: an application checked by its model

grist (module `github.com/chucklehead-dev/gotogether/grist`, design in
docs/grist.md, slice 1 built; §0.2 says what its review changed)
records every block's step itself: each commit and committed refusal at
the store, each query on return, each effect. The application writes
its decisions and folds; its model is one tenant's log.
examples/counters is the walk-through: read its README's table first.

## The loop

1. **Elicit** the blocks: events (structs, tags `grist:"tag"` keyed by
   the field's JSON name, integers in a stated unit, no floats or
   times), read models (`NewReadModel`, `NewKeyedReadModel` with one key,
   which must be a tag of every handler's type), commands (a decision:
   `*Reads` and arguments in, events or a `grist.Rejection` out), queries,
   processors. Reads: `X.Get(r)`, `X.For(r, key)`, `r.Exists(Event,
   grist.Tag(k, v))`, `grist.Events(r, Event, tags...)` (typed),
   `r.Select(grist.Selector{...})` (raw). A read of a type the App does
   not register, or of a tag key the type has none of, is the decision's
   error, never an empty answer. Options (one `grist.Option` type):
   `Unconditioned(reason)`, `ConditionTries(n)`, `TransientTries(n)` on
   commands (and queries), `EffectTries(n)`, `BatchSize(n)` on
   processors. `App.Verify()` names every problem and its fix;
   `App.Catalog()` is the registry as data.
2. **Model first**: `model/<app>.qnt` imports a vendored `grist.qnt`
   (copy `grist/model/grist.qnt`) and `export`s it. State: `log`,
   `checkpoints`, `appended` (the last batch), `effects` ((processor,
   trigger, key, ok)), `answered` (the command ids with an outcome). A
   command's action takes its fields, `id`, `readPos`, `reads` (Reads,
   `{ typ, tags }`) and `result` (`{{.outcome}}` in the binding). Every
   committed outcome requires `freshKey(answered, id)` and adds the id
   (a rejection and a conflict commit their marker: a key's first answer
   is final), `covers(reads, need)` for what its rule reads (so a lost
   read is caught at every commit), and `conditionHolds(log, readPos,
   reads)` (a conflict: its negation); a commit appends, a refusal
   requires `readPos <= log.length()` and its reason; `idempotency-key-
   reused` requires the id answered and commits nothing. Event causes are
   pairs, `("command", id)` or `("processor", name)`. A processor action
   takes `trigger == nextOf(log, checkpoints.get(p), type)` and reads
   `log.slice(0, trigger)`; an at-least-once effect step requires
   `keyStable`, its checkpoint `welcome(trigger, ok)` requires
   `atLeastOnceDone(effects, p, trigger, ok, EFFECT_TRIES)` and appends
   `atLeastOnceOutcome(p, trigger, ok, [success])` (the run that
   committed decides: notified after an ok send, `grist.failed` after the
   tries). Read models are defs over the log. Check it alone:
   `gotogether simulate -binding <app>.binding.yaml`.
3. **Bind**: every parameter the same-named step field, but `reads:
   'readsOf({{toList .reads}})'` (slice 1 records triples); `expect:` for
   commits and refusals is `log.length() == {{.obs.head}} and
   batchIs(appended, {{toList .obs.events}}, {{toList .obs.strs}},
   {{toList .obs.ints}}, {{toList .obs.causes}})`, for a query the read
   models' defs against `.obs.*`. Outcomes include `conflict` and
   `idempotency-key-reused`. No `restart`: every step's process is the
   store.
4. **Explore**: `qtest.System{Binding: ..., Rates: ..., Run: func(c
   qenv.Chooser) error { return gristsim.Run(c, App, cfg) }}`;
   `gristsim.Config` sets the isolation level (Serializable and
   ReadCommitted: Snapshot's runs are Serializable's, every append
   writing the head), `Tenants` and `Alphabet` (one tenant and two
   letters for contention), `CommitFaults`, `Check`, `Send`, `Setup` (the
   app's doubles, reached through a processor's ctx) and `Record`
   (`AtClients` with `qtest.Linearize()` and a client-side model module).
   Duplicated and late requests: `rpc.api.request` and `rpc.api.delay`
   rates.
5. **Prove each check can fail** with a mutant per claim (overlay files,
   as examples/counters' `blind` and `dispatchDirect`), and the faithful
   app at the mutant's configuration with the opposite claim
   (`None(Rejects("createCounter"))`): an `Unconditioned` create is
   `Rejects` when the race hits (at ReadCommitted: under Serializable the
   race's loser retries a transient conflict and decides again), a raw append
   from a processor is `Mismatch`, an at-least-once effect's duplicate
   is admitted and a test-only invariant over `effects` judges it
   (`qenv.Script` of `grist.<processor>.effect`'s `crashAfter`).

## Triage

- `Rejects(command)`: a commit the model's precondition forbids: the
  decision's rule, its condition, a read missing from the read set
  (`covers`), or a key answered twice (`freshKey`: a refusal's late
  duplicate committing).
- `Mismatch(command or processor)`: the recorded head, batch or causes
  differ from the model's: an event appended outside grist (a raw append
  shows at its tenant's next step), or a fold that disagrees with the
  def.
- `grist.ErrOutcomeUnknown`: the transient budget ran out after an
  append whose outcome is unknown; retry with the same id (gristsim's
  clients do). `grist.ErrUnavailable`: nothing committed.
- A run that does not settle names its tasks: a poller is a daemon; a
  client waiting means the node is down (gristsim restarts it from an
  `OnCrash` hook).
- A store of your own implements `store.Store` (package grist/store;
  `store.StepLog` if it keeps each commit's step in its transaction, as
  a database/sql store must): run `storetest.Run(t, harness)`; its checks
  compare conditions, reads and concurrent appends with brute force over
  every event, wake a `Wait`, and mutate readers' copies.

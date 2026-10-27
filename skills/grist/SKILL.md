---
name: grist
description: Build an event-sourced Go application on grist (gotogether's framework, slice 1) so it is checked against a Quint model without annotations - events, plain and keyed read models, commands conditioned on their pinned reads (Unconditioned only on purpose), idempotency by command id, queries, processors (Emit, Skip, Effect at least once), memstore and simstore, gristsim.Run as a qtest.System, a hand-written model over one tenant's log and its binding. Use when writing or changing a grist application, its model or binding, when a gristsim exploration fails, or when a store must keep grist's contract (grist/storetest).
---

# grist: an application checked by its model

grist (module `github.com/chucklehead-dev/gotogether/grist`, design in
docs/grist.md, slice 1 built) records every block's step itself: each
commit at the store, each refusal and query on return, each effect. The
application writes its decisions and folds; its model is one tenant's
log. examples/counters is the walk-through: read its README's table
first.

## The loop

1. **Elicit** the blocks: events (structs, tags `grist:"tag"`, integers
   in a stated unit, no floats or times), read models (`NewReadModel`,
   `NewKeyedReadModel` with one key), commands (a decision: `*Reads` and
   arguments in, events or a `grist.Rejection` out), queries, processors.
   `App.Verify()` names every problem and its fix.
2. **Model first**: `model/<app>.qnt` imports a vendored `grist.qnt`
   (copy `grist/model/grist.qnt`) and `export`s it. State: `log`,
   `checkpoints`, `appended` (the last batch), `effects`. A committed
   command's action takes its fields, `id`, `readPos`, `reads` and
   `result` (`{{.outcome}}` in the binding), and requires its rule,
   `conditionHolds(log, readPos, reads)` and `freshCause(log, id)`; a
   refusal requires `log.length() == readPos` and its reason; a processor
   action takes `trigger == nextOf(log, checkpoints.get(p), type)` and
   reads `log.slice(0, trigger)`. Read models are defs over the log.
   Check it alone: `gotogether simulate -binding <app>.binding.yaml`.
3. **Bind**: every parameter the same-named step field; `expect:` for
   commits is `log.length() == {{.obs.head}} and batchIs(appended,
   {{toList .obs.events}}, {{toList .obs.strs}}, {{toList .obs.ints}})`,
   for a query the read models' defs against `.obs.*`. No `restart`:
   every step's process is the store.
4. **Explore**: `qtest.System{Binding: ..., Rates: ..., Run: func(c
   qenv.Chooser) error { return gristsim.Run(c, App, cfg) }}`;
   `gristsim.Config` sets the isolation level, `CommitFaults`, `Check`,
   `Send` (which commands the clients send), `Setup` (the app's doubles,
   reached through a processor's ctx) and `Record` (`AtClients` with
   `qtest.Linearize()` and a client-side model module).
5. **Prove each check can fail** with a mutant per claim (overlay files,
   as examples/counters' `blind` and `dispatchDirect`): an `Unconditioned`
   create is `Rejects` (at ReadCommitted most; under Serializable a lost
   race's transient retry decides again), a raw append from a processor
   is `Mismatch`, an at-least-once effect's duplicate is admitted and a
   test-only invariant over `effects` judges it (`qenv.Script` of
   `grist.<processor>.effect`'s `crashAfter`).

## Triage

- `Rejects(command)`: a commit the model's precondition forbids: the
  decision's rule or its condition (a read left out of the read set).
- `Mismatch(command or processor)`: the recorded head or batch differs
  from the model's: an event appended outside grist (a raw append shows
  at its tenant's next step), or a fold that disagrees with the def.
- A run that does not settle names its tasks: a poller is a daemon; a
  client waiting means the node is down (gristsim restarts it from an
  `OnCrash` hook).
- A store of your own: run `storetest.Run(t, harness)`; its checks
  compare conditions and reads with brute force over every event.

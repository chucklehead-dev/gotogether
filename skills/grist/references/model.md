# A grist application's model: the rules

The model is one tenant's log, hand-written (a generator is designed,
docs/grist.md §3.2, not built). `model/<app>.qnt` imports a vendored
`grist.qnt` (copy `grist/model/grist.qnt`) and `export`s it.
examples/counters' `model/counters.qnt` and examples/wallet-grist's
`model/wallet.qnt` are complete ones: read one beside this page.

## State

- `log`: the tenant's events, each `{ typ, tags, strs, ints, cause }`.
- `checkpoints`: each processor's position.
- `appended`: the batch the last step appended (`[]` for a step that
  appends nothing).
- `effects`: the effect steps, `(processor, trigger, key, ok)`.
- `answered`: the command ids with an outcome.
- Any dependency's own record of its effects (counters' `payments`,
  wallet's `ledger`), when a double records at its site (below).

Every action assigns every variable (frame conditions: `log' = log`
where nothing is appended). Read models are defs over the log.

## Names

A command's or event's Go field becomes a Quint parameter by its JSON
name, and Quint refuses its keywords and redefinitions of its built-ins
(QNT008, QNT101, or a parse error): `to`, `of`, `in`, `not`, `then`,
`with`, `type`, `and`, `or`, `if`, `else`, `all`, `any`, `val`, `def`,
`var`, `const`, `action`, `run`, `pure`, `nondet`, `match`, `import`,
`export`, `module`, `assume`, `true`, `false`, and the operators `map`,
`filter`, `fold`, `keys`, `get`, `put`, `set`, `size`, `length`, `head`,
`tail`, `nth`, `range`, `select`, `union`, `contains`, `exists`,
`forall`, `next`, `fail`, `assert`, `reps`, `enabled`, `always`,
`eventually`, among others. Name the field otherwise (`Src`/`Dest`, not
`From`/`To`; `Kind`, not `Type`); `quint typecheck` names the one it
refuses. grist's own step fields (`id`, `principal`, `readPos`,
`reads`, `result`, `trigger`, `key`, `ok`) are refused by `App.Verify`.

## Commands

A command's action takes its fields, `id`, `readPos`, `reads` (Reads,
`{ typ, tags }`), `principal` (the actor, when its rule needs it) and
`result` (`{{.outcome}}` in the binding). A client-side model takes
`denied` (a 403: nothing appended, the key unanswered).

- Every committed outcome requires `freshKey(answered, id)` and adds the
  id: a rejection and a conflict commit their marker, so a key's first
  answer is final.
- `covers(reads, need)` for what its rule reads, so a lost read is
  caught at every commit. An outcome whose rule reads nothing (a request
  refused as invalid before any read) leaves `covers` out of its branch.
- `conditionHolds(log, readPos, reads)` for a commit and a refusal; a
  `conflict` requires its negation. An `Unconditioned` command's model
  leaves `conditionHolds` out, and its precondition then catches the
  race (no isolation level closes it: docs/grist.md §2.4).
- A commit appends its events; a refusal requires `readPos <=
  log.length()` and its reason, and appends nothing.
- `idempotency-key-reused` requires the id answered and commits nothing.
- A command that cannot conflict (it reads nothing, so its condition is
  empty) needs no `conflict` branch.
- Event causes are pairs: `("command", id)` or `("processor", name)`.

## Processors

A processor's action takes `trigger == nextOf(log, checkpoints.get(p),
type)` and reads `log.slice(0, trigger)`.

**At least once** (the default): the effect step `<p>Effect(trigger,
key, ok)` requires `keyStable`; the checkpoint `p(trigger, ok)`
requires `atLeastOnceDone(effects, p, trigger, ok, EFFECT_TRIES)` and
appends `atLeastOnceOutcome(p, trigger, ok, [success])`: the run that
committed decides, the success event after an ok effect, `grist.failed`
after the tries.

**At most once** (`grist.AtMostOnce`: checkpoint first, the effect
once, then its outcome in a commit of its own) has three actions:

- the checkpoint `p(trigger, ok)`, requiring `atMostOnceFirst(effects,
  p, trigger, ok)`, appending nothing;
- the effect step, requiring `atMostOnceEffect(effects,
  checkpoints.get(p), p, trigger)`;
- the outcome `pOutcome(trigger, ok, known, error)`, requiring
  `atMostOnceDone(log, effects, p, trigger, ok, [success])` (the success
  event names the trigger) and appending `atMostOnceOutcome(p, trigger,
  ok, known, error, [success])`: after a failed effect `grist.failed` if
  f returned `grist.NotApplied(err)` (it did not happen), else
  `grist.unknown` with the error's text (it may have: a lost answer).

A lost effect or outcome is admitted. An application that needs exactly
once states it with test-only eventual vals over the dependency's own
record of each effect, counted per key (examples/counters' `payout`,
`counters.exactly.binding.yaml`), and grist's `atMostOnceOutcomesLogged`
for outcomes owed. Check it with two pollers: `gristsim.Config.Nodes: 2`.

**The model encodes the declared delivery.** An effect declared at most
once where the model says at least once is rejected at its first
trigger (`Rejects(<processor>)`: its checkpoint step comes before any
effect step), so what the mismatch would cause (a lost effect) is never
reached; the same for any declared guarantee the model contradicts.

## A dependency's record of its own effects

A double (a simulated mailer, bank, ledger) records each effect where it
makes it, so the model sees it whether or not grist's effect step
survives: `qobs.Record(ctx, "bankPays", nil, "actor", tenant,
"process", App.Name, "key", key, ...)`. `actor` puts the step in the
tenant's trace (every grist step of a tenant has the tenant as its
actor), and `process` must be the store's name, which is the App's
(gristsim names the store after it; `sqlstore.Options.Name`): another
process would be a second incarnation of the actor, a restart the
binding has no action for. The action is a model action that adds to
the dependency's record (counters' `bankPays`, wallet's `ledgerPost`).

## Check it alone

`gotogether simulate -binding <app>.binding.yaml` runs the model's own
`step` against the binding's `simulate:` invariants before any Go runs.

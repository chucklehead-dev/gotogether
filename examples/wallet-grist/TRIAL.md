# Trial: building examples/wallet-grist as a newcomer to grist

2026-10-27. An agent new to grist built examples/wallet's bank on it,
reading only skills/grist/SKILL.md, docs/grist.md (§3.2, §3.6, §8.2 and
the status lines, skimmed for the generator), `go doc` of grist,
grist/gristsim, grist/client and grist/store (doc comments only),
grist/model/grist.qnt, examples/counters (all of it), examples/wallet's
README and model, and AGENTS.md. No implementation of grist or the core
was read. Nothing in grist/ or the core was changed.

## How it went, in one paragraph

Fast, mostly because examples/counters is a complete template: the app
(about 230 lines of Go across five files) compiled on the first try, the
model typechecked on the second, every faithful claim passed on its
first run, and all five mutants were caught on their first run. The one
claim that failed was one the docs told me to expect (an `Unconditioned`
command is safe under Serializable): it is not, here, in 2 of 20 runs.
The cost is elsewhere: the model (314 lines) and three bindings were
written entirely by hand, most of the model is frame conditions and
outcome branches a generator would write, and the test harnesses
(mutant overlays, the real tier's settle and answers check, about 250
lines) were copied from counters because no library offers them.

## Time per stage (wall clock)

| stage | time | notes |
|---|---|---|
| reading (skill, counters, go doc, wallet's README and model) | ~6 min | counters' README table and sim_test.go were what made it concrete; the skill alone was not enough |
| app (events, read model, commands, query, processor, ledger double) | ~4 min | compiled first time |
| model (commit side, client side) | ~4 min | one QNT101 (`of` is reserved) |
| bindings and `gotogether simulate` | ~2 min | three bindings, copy-and-adapt |
| simulation tests | ~3 min writing, 2.5 min running | all green on the first run |
| mutants | ~5 min | overlay harness copied from counters; each mutant binary takes ~60 s to build and run |
| real tier on SQLite over HTTP | ~3 min | adapted from counters' `TestRealHTTP`, green on the first run |
| full-mode run, README, this file | ~15 min | full mode found the Serializable discrepancy |

Total about 45 minutes. `go test -short .` costs about 170 s wall and
about 335 s CPU on this machine (each claim spawns quint; the mutants
build a binary each), so the example is
**not** added to CI's examples loop (the bar was ~60 s CPU).

## Friction log

### Places I guessed

1. **A command that touches two entities, with a one-key keyed read
   model.** A transfer moves two accounts; `NewKeyedReadModel` takes one
   key, "which must be a tag of every handler's type". Nothing in the
   docs says how to model a two-entity command. I guessed: one event
   type `moved{account, amount}` (signed), two per transfer, so
   `balances` is keyed by `account` and a transfer's read set is exactly
   the accounts its rule reads. It worked well (transfers from different
   accounts do not conflict, a transfer from the credit line reads both),
   but it is a design decision the docs should make for you: "an event
   per entity touched" is probably the pattern.
2. **Which outcomes need `covers`.** The skill says "every committed
   outcome requires ... `covers(reads, need)` for what its rule reads". An
   `invalid` refusal reads nothing (the decision refuses before any
   read), so I required `covers` only on `ok` and `insufficient-funds`.
   The model has no way to say "this outcome needs no reads" except by
   leaving `covers` out of that branch; I guessed that is intended.
3. **`conflict` for a command that reads nothing.** A deposit reads
   nothing, so its condition cannot fail; I left the `conflict` branch
   out of `deposit` (the outcomes map is per binding, not per action, so
   nothing says which actions may answer which outcome). Guessed that is
   fine; it was.
4. **The ledger's site record.** Recording the effect at the dependency
   (`qobs.Record(ctx, "ledgerPost", nil, "actor", tenant, "process",
   App.Name, ...)`) is copied from counters' `SimBank`. The `"actor"` and
   `"process"` key-value convention, and that the process must be the
   app's name for the step to join the tenant's trace, are documented
   nowhere I was allowed to read; I copied it and it worked.
5. **The probe.** examples/wallet's `observe` probe (a guard on the
   balance after the settle) has no direct grist counterpart. gristsim
   "probes run every query" after the settle, with arguments from the
   query's generator, so the `read` query's `expect` is the probe. I
   guessed probes use `Config.Gen` (they appear to: reads of held
   accounts only), and that a probe does not necessarily read every
   account (wallet's probe reads all of them).
6. **`Config.Gen`'s return type**: `func(pick func(n int) int) any`;
   guessed it must return the argument struct by value (not a pointer).
   It does.
7. **What "actions taken 6 of 11" counts.** My commit binding names 7
   actions; the report says 11. I do not know what the other four are.

### Docs that were wrong or missing

1. **`Unconditioned` under Serializable is not safe.** The skill (step 5:
   "at ReadCommitted: under Serializable the race's loser retries a
   transient conflict and decides again") and counters' README ("under
   Serializable 0 of 120") say the window closes. With `transfer`
   `Unconditioned`, two clients spending 3 of 5 from one account, under
   Serializable, 2 of 20 runs committed a transfer decided at position 0
   after the other transfer had committed (`readPos 0`, committed at
   head 4; `GOTOGETHER_RUN='TestMutant#3:seed=5'` in the
   `readThenWrite` mutant's binary reproduced it). I did not read grist's
   code, so I cannot say why; my guess is that the decision's reads and
   the append are separate transactions, so a loser whose append began
   after the winner's commit sees no conflict. Either the docs overstate
   it or grist has a bug; I dropped the assertion and wrote it down.
   *Resolved (docs/grist.md §0.8): the guess is right, by design. The
   reads are store reads pinned before the append, whose transaction
   begins after them, so no level protects an `Unconditioned` decision;
   the docs were wrong (counters' 0 of 120 was its shape's luck: 2 of 60
   at four clients). The assertion is back as `Some(Rejects("transfer"))`
   at Serializable, 80 seeds in full mode.*
2. **The skill links no reference files.** AGENTS.md's convention is
   mechanics in `skills/<name>/references/*.md`; grist's skill is one
   dense SKILL.md whose step 2 is a 30-line sentence of model rules. It
   is all correct as far as I could tell, but I understood it only by
   reading counters' model next to it.
3. **No generator.** docs/grist.md §3.2 describes `grist model` writing a
   skeleton (`model/<app>_gen.qnt`) and its binding; §8.2 says it is not
   built ("Left: the generator"). The skill says "a hand-written model",
   which is accurate, but `Catalog`'s doc comment ("what slice 2's
   generator (grist model) writes a model skeleton and its binding from")
   reads as if it exists.
4. **Quint reserved names**: AGENTS.md warns about `to`; `of` is also
   reserved (QNT101 on a parameter named `of`, from a Go field `Of`).
   The message was clear; a line in the skill ("event fields become Quint
   parameters: avoid Quint built-ins such as to, of") would save the
   round trip.
5. **grist.Main's `serve` records no settle.** `SERVE=1 ./run-demo.sh`
   validates the binary's step file, and the eventual vals (`allPosted`,
   `postedApplied`) are "uncollected (no verdict)": nothing records a
   settle when the server shuts down after its processors caught up.
   counters' demo has the same gap. The real-tier test records one by
   hand (`quiet`, then `qobs.Settled`).
6. **What a lost post looks like.** wallet's `NoOutbox` is caught by
   `eventually allPosted` (a crash lost the post). grist's analogue, the
   post declared `AtMostOnce`, is caught at its first trigger as
   `Rejects("post")` (its checkpoint step comes before any effect step),
   so the loss itself is never reached: the model encodes the declared
   guarantee, and a declaration mismatch shadows its consequences. The
   docs say so for counters' `checkpointFirst`; they could say it is
   the general case.

### Error messages that did not tell me what to do

1. `qtest.Validate` on the real tier logs: `no model-level ITF: actor t1:
   step 3 (deposit), parameter reads: quint literal "readsOf([])" at 7:
   readsOf is not a literal (only constructors, Set, List, Map and
   records are); evaluate it in Quint or write the literal`. The binding
   is the one the skill prescribes (`reads: 'readsOf({{toList
   .reads}})'`), the test passes, and I could not tell whether anything
   was lost (what is model-level ITF for? a report? a replay?). Either
   the prescribed binding should not produce it, or the message should
   say it is harmless and what it skips.
2. Nothing else: every failure report I hit (the Serializable runs) named
   the step, the Quint call, the observation, the model state before it,
   and the reproduce command. That report is excellent.

### Written by hand that a tool could have written

- **The model's skeleton** (about 200 of its 314 lines): per command, the
  `idempotency-key-reused` / `conflict` / commit / refusal branches with
  `freshKey`, `conditionHolds` and `covers`, the same for every command;
  six-variable frame conditions in every action (`log' = log,
  appended' = [], checkpoints' = ..., effects' = ..., answered' = ...,
  ledger' = ...`); the event constructors (`moved`, `posting`), which are
  the Go structs' fields in order; the processor's three actions
  (`postEffect`, `post`, `postOutcome`), which follow from its declared
  delivery; the client-side module, which is the commit side without
  positions; the `step` action for `simulate`. What I actually designed
  was `balanceOf`, `funds`, `validTransfer`, `needs`, the per-outcome
  conditions, and the eventual vals: about 60 lines.
- **The bindings**: three files whose outcomes maps are identical and
  whose `expect` templates are the same string for every command; all of
  it follows from the catalog.
- **The mutant overlay harness** (`Overlay`, `TestMutants`, 80 lines),
  copied verbatim from counters: qtest.System has a `Mutant` field, but
  building a binary per overlay is every example's own code.
- **The real tier's harness** (`quiet`, `answered`, the lost-answer
  server: about 120 lines), copied from counters. `quiet` must be told
  each processor's trigger type, which the App knows.
- **Dependencies through the context** (`Deps`, `WithDeps`, `DepsOf`):
  the same 30 lines in every grist app.

## What reproduces examples/wallet, and what does not

| wallet's check | here |
|---|---|
| `TestFaithful` (faults, crashes, linearized) | yes: `TestFaithful`, `TestFaithfulAtClients`, `TestSQLTier`; plus `TestRealHTTP` on SQLite |
| `NoDedupe`, `UnlockedRecord` | no mutant can be written: the request record is grist's marker; `TestDuplicateRequests` and `TestOneRequestExhaust` (Exhaust within one fault, 49 runs) are the faithful halves |
| `NoValidation` | yes: `Rejects("transfer")`, 6 of 6 |
| `ReadThenWrite` (lost update at RC, fine at SI) | yes as `readThenWrite` (`Unconditioned`): 19 of 20 at RC; at Serializable 2 of 20 still fail (wallet: none at SI) |
| `WriteSkew` at SI, linearized | only as `readThenWrite` at the credit line's shape, at RC (19 of 20); a conditioned read cannot be dropped from the condition, so write skew is unwritable otherwise |
| `TestWriteSkewHistory` (qhistory G2) | no: the store's history is appends to a log, not a list-append workload |
| `NoOutbox` (`eventually allPosted`) | as `noOutbox` (post `AtMostOnce`), caught as `Rejects("post")` at the first post, not as a lost post |
| the ledger's dedupe | yes: `ledgerNoDedupe` after a scripted crash, `Rejects("ledgerPost")` 6 of 6; faithful `everyMovePostedOnce` holds |
| `ReplicaRead` | yes: `Mismatch("read")`, 10 of 10 |
| `TestPendingTransfers`, `ResolvePending` | no: gristsim's clients do not crash |
| `TestAllFaults` | not tried |

## What I would want from grist (in order)

1. **The generator** (`grist model`): the model skeleton (commands'
   outcome branches with freshKey/conditionHolds, frame conditions,
   event constructors, processor actions by delivery, the client-side
   module) and the bindings, from `App.Catalog()`. The application would
   write only the rule's predicates (`validTransfer`, `funds`), the
   `needs` per command, and the read models' defs.
2. **Fix or correct the `Unconditioned`-under-Serializable claim**: either
   the loser's append always conflicts (as documented), or the skill and
   counters' README say the window narrows rather than closes.
3. **Library harnesses**: `gristtest.Mutants(t, dir)` (the overlay
   build-and-run), a real-tier `Settle(ctx, st, relay)` that waits for
   every processor's checkpoint and the relay and records the settle
   (and `grist.Main serve` doing the same on shutdown), and the
   "every client answer has its store-side step" check.
4. **Multi-entity guidance**: a section on commands that read or write
   several entities (one event per entity touched, a keyed read model
   per entity kind, `needs` as the set of reads the rule depends on).
5. **Typed dependencies** (`grist.Dep[T]` attached by `Setup` and
   `Dependencies`) instead of each app's context key, and a documented
   way for a double to record its effect at its site (the `actor` and
   `process` keys).

## Follow-up

What the trial's findings changed (docs/grist.md §0.8): `Unconditioned`
is documented as unprotected at every isolation level, and
`readThenWrite` is claimed at Serializable (`Some(Rejects("transfer"))`,
80 seeds, full mode); `grist.Main serve` records a settle on shutdown,
so `SERVE=1` judges the eventual vals; the skill's model rules and the
multi-entity pattern (guesses 1 to 4 above) are reference pages
(skills/grist/references/), with a double's `actor`/`process` record and
the declaration-shadows-consequence rule (finding 6); exploration
summaries name the untaken actions (the "other four" of 11 are the
model's helper actions `appends`, `refuses`, `readsThrough`,
`processes`); the "no model-level ITF" log says the verdict is
unaffected; Quint's reserved names are listed in AGENTS.md and the
skill. The generator, the harness helpers and typed dependencies are
ROADMAP's grist track.

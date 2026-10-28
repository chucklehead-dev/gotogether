---
name: grist
description: Build an event-sourced Go application on grist, gotogether's framework checked by its own model without annotations - events, read models, commands conditioned on their reads, idempotency by command id, queries, rules, processors (Emit, Skip, Effect at least or at most once), declared dependencies and calls, deny-by-default authorization, the HTTP API and grist.Main, the stores (memstore, simstore, sqlstore on PostgreSQL or SQLite), gristsim.System explorations, the model's skeleton and both bindings generated from the catalog (`<app> model`), `grist new`, and gristtest's harnesses. Use when starting, writing or changing a grist application, its model or binding, when a gristsim exploration fails, or when a store must keep grist's contract (grist/storetest).
---

# grist: an application checked by its model

grist records every block's step itself (each commit from the row it
wrote in its own transaction, each query, each effect); the app writes
its decisions and folds, and its model is one tenant's log, the
skeleton and both bindings generated from the App's catalog.
examples/counters is the walk-through: read its README's table first.

## Steps

1. Start green: `go run github.com/chucklehead-dev/gotogether/grist/cmd/grist
   new <name>` (`-replace <checkout>` inside this repository).
2. Add one block at a time in Go: events, read models, a command (reads
   and arguments in, events or a `grist.Rejection` out), a query, a rule,
   a processor; `Authorize` or `Internal` on each; `App.Verify()` names
   every problem.
3. Declare its shape: `Refuses`, `Appends`, `Emits` or `Delivers`,
   `Uses` for a dependency's calls.
4. Generate: `go run ./cmd/<name> model`; it names each new hole with a
   stub and warns when a rule's inputs changed. Fill the holes in
   `model/<name>_rules.qnt`, the invariants in `model/<name>.qnt`.
5. Witness it: `gristtest.Generated(t, App)`.
6. Explore: `gristsim.System(commits, App, cfg).WithRates(faulty).Explore(t,
   qtest.ExploreConfig{Seeds: 20, ShortSeeds: 3}).Expect(t,
   qtest.Conforms(), qtest.All(qtest.Settles()),
   gristtest.Catalog(App).Exercised())`; the clients' side with
   `.WithBinding(calls, qtest.Linearize())`.
7. Prove each check can fail: a mutant per claim in
   `testdata/mutants/<name>`, and each rule's generated `drop-<rule>`
   (`qtest.Mutants{Generated: gristtest.RuleMutants(App)}`).
8. Serve it for real (`grist.Main`, `serve -db sqlite:... -steps`) and
   validate the recorded steps with the same binding.

## References (when to read each)

- [references/loop.md](references/loop.md): the first hour and the loop
  in full: every block's API and options, rules, dependencies, the
  generator's layout, explorations' configuration, mutants.
- [references/triage.md](references/triage.md): what each grist failure
  means (`Rejects`, `Mismatch`, unknown outcomes, unsettled runs) and
  what to do; a store of your own (storetest); the real tier, the HTTP
  API, an app's binary, a simulated cluster (`gristsim.Cluster`),
  PostgreSQL.
- [references/model.md](references/model.md): the generated model, its
  holes and Quint's reserved names.
- [references/entities.md](references/entities.md): a command that
  touches more than one entity.

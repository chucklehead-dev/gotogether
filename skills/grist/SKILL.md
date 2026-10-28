---
name: grist
description: Build an event-sourced Go application on grist, gotogether's framework checked by its own model without annotations - events, read models, commands conditioned on their reads, idempotency by command id, queries, rules, processors (Emit, Skip, Effect at least or at most once), declared dependencies and calls, deny-by-default authorization, the HTTP API and grist.Main, the stores (memstore, simstore, sqlstore on PostgreSQL or SQLite), gristsim.System explorations, the model's skeleton and both bindings generated from the catalog (`<app> model`), `grist new`, and gristtest's harnesses. Use when starting, writing or changing a grist application, its model or binding, when a gristsim exploration fails, or when a store must keep grist's contract (grist/storetest).
---

# grist: an application checked by its model

grist records every block's step itself; the app writes its decisions,
folds and rules, its model is one tenant's log, and the skeleton and
both bindings are generated from the App's catalog. examples/counters is
the walk-through: read its README's table first.

## Choose your path

- A new app: `grist new <name>`, then the Steps.
- Every block's API, rules, dependencies, the generator's layout, explorations: [references/loop.md](references/loop.md).
- The real tier, the HTTP API, the binary, a simulated cluster (`gristsim.Cluster`), PostgreSQL, a store of your own: [references/triage.md](references/triage.md).
- A failure: the Results, then [references/triage.md](references/triage.md).
- The generated model, its holes, Quint's reserved names: [references/model.md](references/model.md).
- A command touching several entities: [references/entities.md](references/entities.md).
- An event whose shape changes (upcasters, grist.lock, a deployed version's log judged by today's model), a new version in a rolling update (readers first: `grist.WritesVersion`, `<app> catalog -diff deployed.json`, `gristtest.Upgrade`), a read model whose log is long (snapshots, their audit): [references/versions.md](references/versions.md).

## Steps

1. `go run github.com/chucklehead-dev/gotogether/grist/cmd/grist@latest new <name>` (outside any
   module; from a checkout, `-replace <checkout>`), then `go get -tool
   github.com/chucklehead-dev/gotogether/cmd/gotogether` in the app for `go tool gotogether`.
2. One block at a time in Go, `Authorize` or `Internal` on each, its
   shape declared (`Refuses`, `Appends`, `Emits`/`Delivers`, `Uses`).
3. `go run ./cmd/<name> model`; fill the named holes in
   `model/<name>_rules.qnt`, the invariants in `model/<name>.qnt`;
   `go tool gotogether simulate -binding <name>.binding.yaml`.
4. `gristtest.Generated(t, App)`; each scenario `gristtest.Scenario`,
   checked by `qtest.Possible`.
5. `gristsim.System(commits, App, cfg).WithRates(faulty).Explore(t,
   qtest.ExploreConfig{Seeds: 20, ShortSeeds: 3}).Expect(t,
   qtest.Conforms(), qtest.All(qtest.Settles()), gristtest.Catalog(App).Exercised())`.
6. A mutant per claim, and each rule's `drop-<rule>`:
   `qtest.Mutants{Generated: gristtest.RuleMutants(App)}.Run(t)`.
7. Serve it (`grist.Main`: `serve -db sqlite:... -steps`) and validate.

## Checklist

- [ ] Every command is conditioned on its reads (`Unconditioned` only with its reason).
- [ ] Every block is authorized or internal; `App.Verify()` is clean.
- [ ] The generated files are committed and `TestGenerated` passes.
- [ ] The generator printed no warning left unread (a fold's inputs changed).
- [ ] grist.lock is committed; a changed event is a new version with an upcaster, `gristtest.Upcasts` passes.
- [ ] The clients' side is judged too: `.WithBinding(calls, qtest.Linearize())`.

## Results

| Report says | Means | Next |
|---|---|---|
| `<app> model: ... does not define X, which the skeleton calls` | a new block's hole | paste the stub, then write the rule |
| `<app> model: warning: XEvents folds T now` | a fold's inputs changed | handle T in every fold over XEvents |
| `generated files differ from what the ... catalog generates` | a block changed without regenerating | `go run ./cmd/<app> model`, commit |
| `reject <command>` (`Rejects`) | a commit the model forbids | the decision's rule, its condition, a read not covered (`covers`), a key answered twice |
| `show a mismatch after <block>` | head, batch or causes differ | a raw append, or a fold disagreeing with its def |
| `grist.ErrOutcomeUnknown` | an append's outcome is unknown | retry with the same id |
| `event X version N changed shape since grist.lock was written` | a stored version's struct changed | [references/versions.md](references/versions.md): a new version, an upcaster |
| `X is version N, and this app reads X up to version M`; `catalog -diff` says `readers first` | a release writes what older nodes refuse | [references/versions.md](references/versions.md): `WritesVersion` until the rollout ends |
| `do not settle`, naming tasks | work left at the end | a node down (restart it), or a missing daemon |
| `model actions taken N of M (never: ...)` | what no run reached | widen `Send`, `Prologue`, `Gen` |
| `NO VERDICT`, `checks the actors */clients` on serve's steps | the calls binding judges clients' recordings | record the clients (`grist/client`), or validate the commits binding alone |
| `missing go.sum entry` running `gotogether` | the CLI is not a tool of the app | `go get -tool .../cmd/gotogether`, then `go tool gotogether` |

## Run it, do not infer it

Let the generator name the holes and warnings, and the exploration the
failing run (`reproduce:`, `navigate:`); do not hand-write a binding or
guess what the model's rules admit: `gotogether nav -binding
<app>.binding.yaml -json`.

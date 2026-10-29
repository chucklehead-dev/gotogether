# Examples

Each example is a Go module of its own (`replace => ../..`), runnable
with `go test ./...` in its directory and a `run-demo.sh`. Every
example app has a model in `model/`, a binding, at least one mutant (a
planted bug), tests showing which check catches which mutant, and a
README with the table of those tests ([checked](#what-every-example-app-has)).

The examples are grouped in tracks, each read in order. Each row says
what the example is and what it shows; the README of each has its table
of tests.

Tutorials, step by step on these examples: [docs/tutorials](../docs/tutorials/README.md) (proofs over the state graph, liveness, scenarios, a grist app on Kubernetes, production at volume).

Tracks: [First steps](#first-steps), [Services](#services), [Distributed systems](#distributed-systems), [Kubernetes](#kubernetes), [grist](#grist), [Migration](#migration), [Metastability](#metastability), [run-demo.sh](#run-demosh).

## First steps

Read in this order: hello is the smallest whole loop (a model, a simulated system, a mutant that needs a fault to show); bank runs every workflow on one small library (model-driven tests, Hegel, recorded steps, OTel spans); checkout is a real service's shape (isolation levels, an outbox, crashes).

| App | What it is | What it shows |
|---|---|---|
| [`hello`](hello) | the smallest simulated system: a counter behind a lossy network, a client that retries (73 lines) | one `qtest.System` explored by seeds; `NoDedupe` caught only when a fault makes the client retry |
| [`bank`](bank) | an in-memory bank (library) | every workflow on one small system: what model-driven testing cannot catch and Hegel + the model can |
| [`checkout`](checkout) | a checkout saga: orders, payments with idempotency keys and voids, shipping, cancellation, a transactional outbox | fault injection with `qenv`: crashable services, a database at three isolation levels, an at-least-once broker, a lossy network; which bug is a bug at which isolation level (lost update, write skew); a double charge; a lost event; Hegel shrinking a write skew; under load, too many payment consumers collapse on their own, a retry budget is bypassed by broker redelivery, deadline propagation recovers, and idempotency keys hold through the storm |

## Services

A service and its clients: HTTP, SQL, gRPC, logs, queues, clocks. Start with kvstore (an HTTP service under load, recorded two ways), then wallet (every check of a service that moves money), then payd (an unmodified net/http and database/sql service, simulated through adapters only), then by your stack: grpcledger, streams, jobqueue, lease, webjobs (a browser explorer's runs).

| App | What it is | What it shows |
|---|---|---|
| [`kvstore`](kvstore) | an HTTP key-value service with compare-and-swap (`kvserver`, `kvload`) | a concurrent service checked as a serial behaviour of its model, from its steps or its plain OTel spans (`kv.op` as a linearization key); a hand-built `qtest.Catalog` the load must exercise (each CAS and delete by outcome); five store bugs as overlays (`testdata/mutants`, `qtest.Mutants{Package: "kv"}`): a CAS race caught under load, a failed CAS advancing the revision (Hegel shrinks it to one step), stale reads no real-time order explains, two rotation bugs seen in the last log alone; errors that classify themselves; reads checked against the model; Hegel over HTTP |
| [`wallet`](wallet) | a wallet service: transfers on a transactional store applied once by request id, an outbox to a ledger, retrying clients, a credit line two accounts share | every check of a service that moves money: a lost response's retry found by `Exhaust` within one fault, a lost update and write skew by isolation level (qhistory names it G2 from the store's history), a post an outbox would have kept (`eventually`), crashed clients' pending calls resolved (`ResolvePending`), every fault family at once |
| [`payd`](payd) | an unmodified net/http + database/sql payments service (idempotency keys, a processor called between two transactions) and its retrying SDK, importing only the standard library, simulated through `qenv/qsql` and `qenv/qhttp` and run for real on SQLite | steps recorded only at the adapters (a RoundTripper, the database's commit feed, the processor double, a balance probe); mutants as `go test -overlay` files: a check-then-insert dedupe charges twice, a capture without the key captures twice, a pending key refused leaves captured money uncharged (`eventually`), a debit ahead of the compare-and-set takes money without a charge, the race's loser refused while its key is charged, a response's amount in cents |
| [`grpcledger`](grpcledger) | a gRPC ledger (idempotent transfers) with generated code, simulated through `qenv/simgrpc` (generated client and server unchanged; lost/duplicated requests, lost responses, refused connections, crashes) and served for real (otelgrpc spans converted to steps) | `NoDedupe` only with faults, `NoFundsCheck` from any load, `SplitTxn` (lost update) as a `Mismatch`; sweep of which faults expose the missing dedupe; Hegel shrink to one duplicated request |
| [`streams`](streams) | an outbox publisher and idempotent consumers on Kafka (`qenv/simlog/kafkalog`, kfake) and Kinesis (`qenv/simlog/kinesislog`, in-process fake), and the simulated partitioned log (`qenv/simlog`) with lost produces/commits, rewinds, evictions mid-batch and crashes; one model and binding for all three | `commit-first`, `no-dedupe`, `non-idempotent-producer`, `off-by-one` |
| [`jobqueue`](jobqueue) | an at-least-once job queue with leases, workers and restarts (`jobqueue`) | all three verdicts: an implementation bug, a design property the run violates (`exactlyOnce`, checked with `-invariants`), and conformance; restarts inferred from incarnations; when model-driven testing does not fit |
| [`lease`](lease) | a lock that grants leases, the store it guards, two holders, and clock faults (each process's clock skewed, jumping) | a bug no test with true clocks can see: without fencing a clock jump lets an old holder write after its successor, and comparing the lock's expiry with the holder's clock breaks on a skew alone; one jump is enough (`System.Exhaust`) |
| [`webjobs`](webjobs) | a standard-library web app (one HTML+JS page: submit a report, 202 accepted, a background job, poll until ready, cancel) explored by Bombadil, Antithesis's browser explorer, and driven by Playwright | a run gotogether did not drive: Bombadil's trace.jsonl (qdriver/bombadil) and a Playwright page's request log (adapters/playwright) turned into invocation records, each completed only by its own request's answer (a click is never a success, a poll's answer completes nothing), with refs to its screenshots and its own times, joined with the server's effects by wall clock; the page's end stated (eventual, `within`); a Go client runs the real server per push, recorded runs are checked offline, live runs nightly with a manifest replayed; mutants as overlays: ready shown before the commit, a double submit's two jobs, a cancel ignored or broken, a stale status, a commit after a cancel, a finished job run again in place |

## Distributed systems

Nodes that agree, replicate or gossip. twophase first (a Choreo model proved over its whole graph, then bound), then quorumkv (liveness, linearizability per key), raftkv (etcd's raft, unchanged), glomers (Maelstrom's challenges, per-node logs merged by Lamport clocks), gosim (whole programs simulated).

| App | What it is | What it shows |
|---|---|---|
| [`twophase`](twophase) | two-phase commit: a coordinator and participants with durable stores over a lossy network, checked against a [Choreo](https://github.com/informalsystems/choreo) model | a Choreo model with named transitions (`choreo::step_with`) proved over its whole state graph (`simulate -graph`, 99 states), bound, explored, shrunk and driving the code (connect); a coordinator that commits early, a participant that gives up after voting, a decision kept in memory |
| [`quorumkv`](quorumkv) | a replicated store: three crashable replicas, quorum reads and writes (ABD), hinted handoff, clients recording calls when invoked | liveness and linearizability under faults: a quorum that never finishes with a replica down (`SettleCore`, `Settles`), a call left unanswered (`Pending`), replicas that never converge (`eventually`), stale reads found by PCT schedules in a history linearized per key |
| [`raftkv`](raftkv) | etcd's raft library, unchanged, as a three-node store on qenv: write-ahead logs on Stores, leader-served clients, transfers across keys, a rolling upgrade of the log format | a cluster model of leadership (one leader a term, a leader `within` 300 ms of a heal), the clients' history linearized whole with pending calls placed, stale lease reads found by `Exhaust` over clock drifts, a vote not persisted, an upgrade that writes the new format too soon |
| [`glomers`](glomers) | Fly.io's Gossip Glomers: unique ids, broadcast, grow-only counter, kafka-style log, on a Maelstrom runtime and simulator | distributed systems: models of each challenge with their properties; Lamport clocks to merge per-node step logs; Hegel scheduling deliveries, losses and partitions, and shrinking a lost-update bug to three steps; the same nodes under real Maelstrom, their logs conforming to the models |
| [`gosim`](gosim) | the Glomers broadcast node over real TCP on three gosim machines, and the checkout saga's deployment on qenv's native runtime, both unchanged (`qgosim`) | whole-program simulation whose every choice (schedule, `select`, map order, faults) comes from a qenv `Chooser` in the test process; runs checked against the same models and bindings; exact replay; `System.Explore` over parallel gosim binaries; Hegel shrinking fire-and-forget gossip's lost value; a mutant's second symptom that gosim's schedules reach |

## Kubernetes

Controllers and providers. requeue first (a controller's requeue loop as a liveness lasso), then fleetrollout (multicluster-runtime, simulated and on kwok), then crossdb (a Crossplane provider, simulated and on kind).

| App | What it is | What it shows |
|---|---|---|
| [`requeue`](requeue) | a controller that applies a cloud resource's spec and requeues until it is ready; a cloud that starts provisioning over on a changed spec | liveness as cycles: a requeue that resends a changed spec is a retry storm weak fairness does not rule out, a lasso of the model (`simulate -graph`), the runs' `hot cycle`; the faithful model proved live |
| [`fleetrollout`](fleetrollout) | a multicluster-runtime controller rolling a Deployment across member clusters within a budget | the same controller simulated (`qenv/kubesim`: status lag, lost responses, crashes) and on three real kwok clusters (`kill -9` included), checked by one evidence-based model; `observedGeneration` and in-memory-state bugs caught in both; on kwok, pods can be timed by a production trace (`TRACE=` in kwok-demo.sh: kwok Stages from its pod lifetimes) |
| [`crossdb`](crossdb) | a Crossplane provider for a database-as-a-service (crossplane-runtime's managed reconciler), with connection details written back as Secrets | Crossplane's real reconcile logic simulated under lost responses, crashes, throttling and an eventually consistent cloud, then the same provider on kind with full Crossplane composing `AppDatabase`s; why the external name must be deterministic and the create idempotent; a leak with no crash; translating a library's real-clock timestamps at the seam; the provider's requeue policy against a capacity dip in the cloud (`qload`, closed loop) |

## grist

Applications on grist, whose model is generated from their catalog (the [grist user guide](../docs/grist-guide.md)). counters first (every feature, and the table of which test catches which mutant), then library (grown from `grist new`, an event versioned, snapshots), then wallet-grist (wallet's bank rebuilt on grist, with a newcomer's friction log), then payouts (two grist Apps, one calling the other's commands), then inventory-grist (three services, cancellation and a combined protocol).

| App | What it is | What it shows |
|---|---|---|
| [`counters`](counters) | grain's counter example on grist (create under a unique name, increment, show; a processor emitting the running sum as of each increment, a welcome mail at least once), in `gristsim` | an application checked by its model with no annotation: grain's unique-name race (`blind`, `Unconditioned`) as `Rejects` at ReadCommitted, its processor's raw append (`dispatchDirect`) as `Mismatch`, the faithful app through commit faults and crashes at two isolation levels and through duplicated and late requests (a key's first answer final), the at-least-once welcome's duplicate after a scripted crash admitted by the model and rejected by a test-only `welcomedOnce`, the at-most-once payout's loss admitted and rejected by a test-only exactly-once val, an effect checkpointed first (`checkpointFirst`) as `Rejects`, a payout of the wrong amount (`wrongAmount`) as `Rejects` of the bank's declared call; over HTTP (`grist.NewHandler`, `grist/client`) in simulation and on a real server over SQLite, its bank behind `grist.ServeCalls` losing answers, a payout retried after an unknown answer (`payTwice`) as `Rejects` from the steps file; sized from its own recorded steps (`counters sizing`: a Gateway's command retries multiplying the client's advised against, the chain and the simulation agreeing) |
| [`library`](library) | a lending library on grist, grown from `grist new` (members, a catalogue with copies, borrows refused past the copies, past three loans or to strangers, returns, a member's loans; receipts mailed at least once through a mailer double that deduplicates and records its sends at a site; book-added at version 2 with its upcaster, version 1's log kept and judged by today's model; read models from snapshots), in `gristsim` and on SQLite over HTTP, no binding written by hand | a borrow without its copy check (`Rejects`), an upcaster dropping a field (`CheckUpcasters`, `Mismatch` on the old log), the limit checked on a stale read (`Unconditioned`, `Rejects` at ReadCommitted), a mailer without dedupe after a scripted crash (`Rejects` of its site), a receipt naming no borrow (`Violates` of an eventual val); and a newcomer's friction log (TRIAL.md) |
| [`wallet-grist`](wallet-grist) | examples/wallet's bank on grist (transfers and deposits applied once by command id, balances keyed by account, a credit line, a ledger posting every move at least once and deduplicating by effect key), in `gristsim` and on SQLite over HTTP | wallet's claims where grist lets an application break them: invalid requests applied (`Rejects`), a transfer `Unconditioned` overdrawing an account and the credit line at ReadCommitted (`Rejects`), the post declared at most once (`Rejects`), a ledger without dedupe posting twice after a scripted crash (`Rejects`), a proxy acknowledging posts it never forwards (`Violates`, the generated obligation `ledgerPostEventuallyServed` alone), reads from the ledger's copy (`Mismatch`); and a newcomer's friction log of building it (TRIAL.md) |
| [`payouts`](payouts) | a payouts service and its bank, two grist Apps in one module: each requested payout paid at least once through the bank's `pay`, a peer's command (`grist.NewPeer`), every try under the effect's key as the bank's command id; both run in one `gristsim` run (`Config.Peers`), each on its own store, node and API | each side judged by its own generated model and the two related by key (`gristtest.Linked`): a client minting a new id per try pays twice (`freshKey`, generated: the bank's `Violates`, `Links` finds the unlinked commit, the payouts' model sees nothing), a payout requested twice (`Rejects` at the payouts, `Violates` at the bank, the sides still linked) |
| [`inventory-grist`](inventory-grist) | checkout → inventory → fulfillment, one SKU, two clients, cancellation versus authorization | three generated local models plus an independent protocol; stable-key retries, missing cancellation, shipment after cancellation, exhaustive bounded ordering checks; [adoption rehearsal](inventory-grist/TRIAL.md) |

## Migration

An existing system brought under a model. migrate-payments first (a legacy handler with tests, logs and spans but no model, in stages: requirements, the model, the first verdicts before any change), then edgepublish (a real exporter annotated, its three trace paths, then simulated by weaving).

| App | What it is | What it shows |
|---|---|---|
| [`migrate-payments`](migrate-payments) | a legacy payment handler (authorize with idempotency keys, partial capture, refunds, voids) with its tests, a production audit log and staging OTel spans, but no model; the gotogether-migration skill's walk-through | the migration in stages: a context map, requirements each with its provenance (`assumed` where one source), the model's `possible` scenarios (`gotogether simulate`, `qtest.Possible`), then the first verdicts before any change: the legacy tests recorded (`qtest.Record`) conform, the production log `Violates` the refund rule the tests miss (the legacy bug, kept as a mutant), a draft model's mistakes found by simulate, a recorded test, the log, `lint -src` and a convert rule file's unmatched spans; a rotated log's lost prefix; `go tool gotogether` |
| [`edgepublish`](edgepublish) | the spike's real case: an OTel exporter's publish protocol with S3 faults and crashes (Orchestrion) | `//quint:action` annotations; three trace paths with identical verdicts; MBT with gated effects; model → Go → telemetry → model; the publisher, unchanged, in a qenv simulation by weaving its locks and goroutines (`qenv/weave`), where exploration found a race the real exporter had already fixed |

## Metastability

Whether a system recovers after a load spike. retrystorm first (retry policies: a CTMC predicts, a simulation confirms, Hegel finds the smallest trigger), then checkout's load (`load.go`: consumers that collapse on their own) and crossdb's requeue policy against a capacity dip. [`sktrace`](sktrace) is the library that turns SimKube traces into load shapes for fleetrollout and retrystorm.

| App | What it is | What it shows |
|---|---|---|
| [`retrystorm`](retrystorm) | a client retry library: bounded retries, backoff with jitter, a retry budget | metastable failures in tiers: a CTMC predicts which policies recover from a spike, a simulation (the library on virtual time) confirms it, and Hegel shrinks the smallest trigger; backoff alone made things worse; alerting rules from the chain calibrated on the app's own load roles; the budget's accounting checked against a Quint model, which catches a bug load tests barely see |

## run-demo.sh

Every example app has a `run-demo.sh` at its root, run from anywhere:

```sh
examples/hello/run-demo.sh                 # the faithful system: every run conforms
MUTANT=noDedupe examples/hello/run-demo.sh # the mutant planted, and what catches it
```

The conventions, the same in every one:
- **bash**, `#!/usr/bin/env bash`, `set -euo pipefail` (or `set -uo
  pipefail` with each step's status checked), `cd "$(dirname "$0")"`
  first, so it runs from any directory.
- **The header comment says what it runs** and names every variable it
  reads, `MUTANT=` first with the mutants it knows.
- **`MUTANT=<name>`** plants one mutant, named as the code names it:
  its `testdata/mutants/<name>` directory (an overlay), or its Go
  constant in camelCase (`noLocks`, which a binary's `-mutation` flag may
  take kebab-cased). Without it, the faithful system runs.
- **The exit status says whether the demo showed what its README says**:
  0 when it did (the faithful runs conform and their claims hold; under
  `MUTANT=`, the mutant is caught: some run fails its check, or the test
  that claims the catch passes), 1 when it did not ("not as expected",
  naming what), 2 when it could not run (an unknown `MUTANT=`, which it
  names with the known ones; a build that fails; a missing tool). So CI
  runs every line plainly: `MUTANT=noDedupe examples/hello/run-demo.sh`
  fails the job only if the mutant was not caught.
- Other variables (`SEEDS=`, `ISOLATIONS=`, `CLOCKS=`, `MODEL=`,
  `SERVE=1`, `ROLLING=1`, `KIND=1`) choose what it runs, never what it
  expects.

Two kinds of demo keep the same contract. A **verdict demo** runs the
system (simulated, or a real server and load), then `gotogether
validate` on the step logs it wrote, and judges the verdicts against
`MUTANT=` (hello, lease, checkout, twophase, requeue, kvstore, jobqueue,
retrystorm, grpcledger, streams, edgepublish, migrate-payments). A
**claim demo** runs the example's own tests (`go test -run ...`), whose
`.Expect` claims already say what each mutant's runs must show (bank,
counters, library, wallet-grist, wallet, payd, webjobs, quorumkv,
raftkv, glomers, fleetrollout, crossdb, gosim).

Demos on real clusters and runtimes are separate scripts whose exit
status is the run's verdict (1 when a check fails, as a mutant's run
should): `fleetrollout/kwok-demo.sh` (kwok), `crossdb/kind/demo.sh`
(kind and Crossplane, also `KIND=1 crossdb/run-demo.sh`),
`glomers/maelstrom.sh` (Maelstrom). A real cluster's run may miss the
fault a mutant needs, so they report what happened and leave the
expectation to the caller (CI: `if MUTANT=... ./kwok-demo.sh; then exit 1; fi`).

## What every example app has

internal/exampleapps (`go test ./internal/exampleapps/` at the root)
checks every directory here that is a module, so a new example cannot
leave one out:
- a `README.md` with a table naming its tests (`` `TestX` ``), and a row
  in one of the tracks above;
- a `model/` with a `.qnt`;
- an executable `run-demo.sh` following the conventions above (the
  shebang, `set -u` and pipefail, the `cd`, `MUTANT=` in its header, an
  exit 2 for what it cannot run);
- a mutant: a README that names one, and a demo that plants it.

Exempt, each for its reason (the test lists them): `sktrace`, a library
other examples use, with no system to model; `gosim` has no `model/`
of its own (its runs are judged by glomers' broadcast model, checkout's
and edgepublish's).

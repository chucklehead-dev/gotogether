# Inventory reservation on three grist services

An adoption rehearsal for GR4: **checkout → inventory → fulfillment**.
Two clients buy one unit each of a single SKU with two units in stock.
Cancellation also reaches the shared inventory service. Each service has
its own durable log, generated model and HTTP API in `gristsim`.

The [business contract](CONTRACT.md) is independent of the local models.
Checkout accepts intents. Inventory decides whether cancellation or
fulfillment authorization wins; fulfillment starts only after durable
authorization. A cancellation accepted by inventory releases stock and
leaves a tombstone, even if the original reserve request arrives later.
Cancellation after authorization is refused `too-late`.

Two units make the retry bug observable while inventory's capacity rule
still holds: its reusable API allows additional allocations under a new
command id, while checkout buys exactly one unit. Stable effect keys
are therefore part of the checkout business contract.

## Run it

From this directory, with Go and Quint installed:

```sh
./run-demo.sh
MUTANT=freshKey ./run-demo.sh
MUTANT=missingRelease ./run-demo.sh
MUTANT=authorizeCancelled ./run-demo.sh
```

A mutant demo succeeds when the claimed check catches its bug. Unknown
mutant names exit 2. Run all tests with `go test -short -v ./...`.

| Test | Configuration / mutant | What it checks |
|---|---|---|
| `TestGenerated` | three App catalogs | all local bindings, skeletons and locks are current |
| `TestProtocolCorrelation` | edit one projected payload to name another existing order | rejects the first allocation whose payload differs from its originating intent |
| `TestModels` | models alone | placing then cancelling is possible; a third allocation is refused full |
| `TestFaithful` | two clients, two placed orders, cancel/confirm intents | local conformance, peer links, settles, and every causal protocol ordering under the explicit limit |
| `TestDecisionRaces` | cancellation first; authorization first | cancellation then authorization refused not-held, or one shipment then cancellation refused too-late |
| `TestLostReservationReply` | a scripted lost inventory response | the retry occurs, the stable key allocates one unit, all models conform |
| `TestCapacity` | three checkout intents as a boundary probe | two allocations succeed and a third is refused full |
| `TestCancellationOvertakesReserve` | one delayed reserve request | cancellation writes a tombstone before the reserve arrives; the late allocation is refused cancelled |
| `TestReservationCrash` | crash after reserve effect, before checkpoint | the repeated effect still allocates one unit |
| `TestMutants` | freshKey | one lost response causes two physical allocations; local models conform, Links detects the new key and the business model rejects inventoryReserve |
| `TestMutants` | missingRelease | a cancellation is checkpointed without its call; local models and links pass, but eventually allIntentsAnswered fails and a unit remains held |
| `TestInventoryMutants` | authorizeCancelled | a shipment starts after accepted cancellation; inventory rejects authorize and the protocol rejects inventoryAuthorize |

The faithful short run checks four seeded competing-client histories;
`TestDecisionRaces` fixes the workload order while retaining the seeded
simulation schedule. Customers pause in virtual time before deciding;
tests assert the actual durable outcomes rather than relying on the pause
as evidence. `TestFaithful` compiles the explicitly selected service and
protocol checkers once, wraps its `qtest.System` with
`gristtest.Composition.System`, and explores normally. `qtest.Conforms()` includes these
composition checks; no second assertion or simulator run is needed. The same
assessor runs when navigating a different simulation branch.

Each run's `Report.Composition` and its JSON/TUI receipt show all three local
models, both declared peer links, and the protocol search separately. A service
with no recorded work or an unused peer edge remains visible as not checked;
peer agreement alone cannot establish the business protocol. Every local model
judges the same native history. The protocol check requires exhaustive
completion for a pass; hitting its stated ordering limit is inconclusive.
This is a bounded experiment over recorded histories, not a proof for every execution.

## Save and replay a failure

```sh
TRIAL_EVIDENCE=/tmp/inventory-trial MUTANT=missingRelease ./run-demo.sh
go tool gotogether validate -binding protocol.binding.yaml /tmp/inventory-trial/missingRelease.jsonl
```

The validation command fails as expected: the recorded settle owes an
inventory cancellation answer. The JSONL retains `protocol.recorder` and
`protocol.seq` for the original commit identities. Repeat with either
other mutant to save its failing protocol history.

The local bindings are generated, never handwritten. The independent
[protocol](model/protocol.qnt) and [protocol binding](protocol.binding.yaml)
are handwritten. Regenerate locally with:

```sh
go run ./cmd/checkout model
(cd inventory && go run ./cmd/inventory model)
(cd fulfillment && go run ./cmd/fulfillment model)
```

The three `cmd/` binaries also use `grist.Main`; checkout attaches its peer
at `INVENTORY_URL`, inventory at `FULFILLMENT_URL`. The trial tests the
HTTP peer transports in simulation, not an external deployment.

Read [TRIAL.md](TRIAL.md) for the integration friction, diagnostic quality,
and the next recommendation. This is a repository-owned rehearsal, not
an independently maintained adopter's endorsement.

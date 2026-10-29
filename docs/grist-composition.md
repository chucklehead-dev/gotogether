# grist composition: the first GR4 experiment

Built 2026-10-29. This is a prerelease experiment in whether declared
calls compose. It extends GR3 slice 5's pair; it does not finish GR4's
system model or deployment platform.

An App still declares another App with `grist.NewPeer`, and its command
with `grist.NewPeerCall`. In a simulation, `gristsim.Config.Peers` lists
every reachable App, including a peer's own peers. The list is explicit:
a missing service is an error, rather than a silently supplied double.
Registration order determines setup order, independent of list order.

Each peer has its own simstore, crashable node, relay and HTTP API over
qhttp. A shared peer is instantiated once: callers reach the same
store, markers and processors. APIs are allocated before dependencies
are attached, so a peer can call a service allocated later. A crash
restarts that service with its dependencies attached again. The final
settle and relay error checks cover all services.

The root keeps its existing Config. Peers inherit Tenants, Isolation
and CommitFaults; each has one node and default doubles for non-peer
dependencies. Peers use simstore even if SQL selects qsql for the root.
No new configuration abstraction is introduced before this experiment
shows which per-service controls are needed.

## What is checked

`grist/gristsim/composition_test.go` builds orders -> bank -> ledger.
An accepted order triggers a payment at least once; each payment
triggers a ledger posting at least once. The two hops use different
effect keys, each qualified by its caller App. Neither service reaches
into another's store.

The test judges the same recorded histories with three generated
models and independently supplied business rules, one binding per App.
`gristtest.Linked` checks each edge by command, key, tenant and outcome.
A separate Go check compares the multisets of accepted orders and
ledger postings, including tenant, order and amount: after settling,
each order must contribute exactly one posting.

Three faithful scenarios script, at both hops:

- a lost HTTP answer, followed by a retry under the same key;
- a processor crash after its effect, before its checkpoint;
- a store commit applied whose answer was lost.

Fault coverage must show each scripted fault happened. The histories
must settle, all local models must conform, both links must agree and
postings must be conserved. A second topology adds orders -> ledger:
the shared ledger records one posting from each path, with no duplicate
service allocation.

The contrast is `freshKey`, the existing generated peer-transport
mutant. Under lost answers it sends retries as new commands. Every
local model still conforms: each sees requests allowed by its own
rules. The link checks and independent posting-conservation check fail.
Local conformance therefore does not imply system correctness.

The experiment also exposed a checker gap: Linked formerly matched a
key and outcome across different declared commands. A pay routed to
refund could pass. It now requires the called command, and examines
caller-owned ids on all the peer's commands, including undeclared ones.
Unknown and pending callers still require any peer commit to be of the
command sent. A key cannot commit twice across different commands.

## Bounds and next questions

The first increment accepts acyclic graphs. Cycles, including a peer
calling back to the root, fail with their path. App names are unique;
API and dependency names must not collide in the `rpc.<name>.*`
namespace. Peer node names cannot collide with the root's node names.

Still open: cyclic compositions, per-peer configuration and custom
doubles, SQL peers, clusters and rollouts, response-driven effects,
remote event feeds, and a combined causal protocol model. The Go
conservation check is an end-state property; it supplies no cross-App
causal ordering and proves no transient protocol invariant. A later
system model must declare that ordering explicitly, rather than merge
independent tenant clocks or rely on recorder arrival order.

The maintainer's priority is correctness that makes experiments
meaningful, then composition and an adoption trial. GR3 slice 6's spans
and further transports follow demonstrated needs. Breaking API changes
remain acceptable; release hardening is not an experimental gate.

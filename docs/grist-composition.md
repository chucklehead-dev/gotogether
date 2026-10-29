# grist composition: peer graphs and a system protocol

Built 2026-10-29. This is a prerelease experiment in whether declared
calls compose. It extends GR3 slice 5's pair; it does not finish GR4's
deployment platform. The next increment adds a combined Quint protocol
model through `gristtest.ProtocolSteps`; it leaves the runtime and wire
formats unchanged.

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
remote event feeds, and exploration of every concurrent interleaving.
The combined protocol model below supplements the end-state Go check;
it does not replace the local models or prove the whole distributed
system correct.

The maintainer's priority is correctness that makes experiments
meaningful, then composition and an adoption trial. GR3 slice 6's spans
and further transports follow demonstrated needs. Breaking API changes
remain acceptable; release hardening is not an experimental gate.

## The combined protocol model

`grist/gristsim/testdata/protocol/chain.qnt` is an independently written
specification of the whole orders -> bank -> ledger protocol. Each
accepted order may be paid once. A posting must depend on a committed
payment of that order and preserve its payload; it may be posted once.
`allPosted` is judged only at a recorded settle. A finite prefix owes
work by right and has no completion verdict.

`gristtest.ProtocolSteps(recorded, apps...)` takes the original native
records, including call invocations. It validates their reconstruction
before projecting committed commands into one actor per tenant,
`protocol/<tenant>`. Each App's native binding must still judge the
original history. The projection's actions are the App's Quint name
followed by the command's capitalized Quint name: `ordersPlace`,
`bankPay`, `ledgerPost` in this experiment. Its arguments, outcomes and
native observations remain intact.

The hard edges are:

- each store's committed sequence, including processor commits that
  are causal barriers even though they are not protocol actions;
- the commit containing a recorded peer call's trigger, before the
  peer's commit of that key and command in that tenant.

Both a completed and a pending call can establish the latter edge.
The exact recorded key is required: a plausible-looking command id
never manufactures its own cause. A peer commit with no matching call
retains empty parent fields for the model to reject. The source event
position identifies its containing batch, so a trigger in the middle
of a multi-event commit depends on that whole commit. A late request
for trigger 1 can commit after trigger 2's without creating a false
clock cycle.

Observations under the `protocol.` prefix carry service, position,
parentApp, parentID, parentPosition, trigger, recorder and seq. A parent
processor commit has no command id; a model of such a flow can use its
App and position instead. The original recorder/sequence identity also
appears in Source, and the observations retain it through JSONL export.
Gaps, losses, ambiguous causes, missing triggers, rewinds and causal
cycles fail before the output receives fresh dense sequence numbers.
No source record is mutated.

A heap produces one deterministic topological extension, breaking ties
by App name and committed sequence within each tenant. Neither wall
clocks nor input arrival order is a causal edge. This does **not**
quantify over all extensions: a model sensitive to unrelated concurrent
commands needs an explicit order search in a later increment. The
projection is an experimental native-record adapter, not a new format
or a general-purpose actor merge.

## Contrast cases and validation

The three faithful fault scenarios from the first experiment are now
checked against the combined model as well as the local bindings.
`freshKey` is rejected at `bankPay`: the transport's new id has no
matching recorded call and therefore no observed causal parent.

`TestProtocolMissingAndPremature` plants two implementation mutants:

- `missing` completes the bank effect without calling the ledger.
  Local models conform; `eventually allPosted` is violated at settle.
- `premature` posts directly from orders and suppresses the bank's
  later posting. A scripted bank delay witnesses a posting before its
  payment in the simulation's true time. All three local models
  conform and final totals agree, but the system model rejects
  `ledgerPost`, whose cause is an order instead of a payment. Time is
  only the mutant's witness; the model uses its recorded causal parent.

`TestProtocolDuplicatePosting` isolates the duplicate guard: a second
post of an already posted payment is rejected even with a valid-looking
causal parent. The faithful projection beside it conforms.

The projection's tests reverse input and App order, skew service clocks,
cover pending calls and multi-event batches, preserve late delivery,
reject incomplete or cyclic evidence, preserve identity through JSONL,
and compare its canonical extension against every permutation of a
small history. Removing causal edges must fail the faithful chain:
without them, alphabetical order puts bank before its order.

# Inventory adoption rehearsal — 2026-10-29

The question was whether the current GR4 APIs suffice for a small
three-service application with contention, cancellation and retries,
and whether their failures identify an actionable business defect.
This trial uses public APIs and checked-in generated files. It is a
repository-owned rehearsal; independent adoption remains a separate test.

## Result

The workflow fits an acyclic topology without SQL peers, cycles, timers,
clusters or rollouts. Each service's generated local model is necessary
but insufficient: fresh retry keys and omitted cancellation calls pass
local conformance. The independent protocol catches both. A bad inventory
authorization is rejected earlier by its own model as well.

The short faithful competing-client run checks 14, 33, 14 and 14 causal
orderings for its four fixed seeds. Cancellation-first checks five;
authorization-first checks twelve. The stable-key retry and crash traces
each have one ordering. The capacity boundary checks fourteen; a
cancellation overtaking its delayed reserve checks three. Every search completes under its stated limit. The standalone safety
harness samples finite inputs and verifies all traces through six steps;
completion remains a property of recorded settles. An edited projection
also proves that another existing order cannot replace the originating
intent's payload.
These counts describe these workloads and seeds, not a coverage claim
for every inventory execution. Tests assert both decisive race outcomes.

The domain decisions, folds and processors occupy about 220 Go lines (including
comments). Three handwritten rules files, three invariant modules and the
independent protocol/binding sit beside generated skeletons, local bindings
and vendored grist model files. The generated files are checked for drift;
the business rules and protocol remain application-owned.

## Friction and decisions

| Observation | What the trial did | Implication |
|---|---|---|
| Processor reads stop at their trigger; they cannot decide whether a subsequent cancellation won | Put authorization in a conditioned inventory command, invoked by a separate confirm intent | Ownership and the cancellation boundary must be decided before writing processors. Added an example and guide note. |
| An accepted checkout cancel is only an intent; inventory might already have authorized fulfillment | Distinguish accepted inventory cancellation from too-late; preserve refusal outcomes in native records | A feedback cycle is unnecessary for this contract. A customer-facing final status/receipt would require a further design; this trial does not claim one. |
| A business refusal returned as an effect error keeps retrying a final answer | Explicitly consume declared Rejected answers while returning transport errors | Added guidance; do not swallow unknown outcomes. |
| gristsim Prologue waits for command answers, not downstream work | Pace customer decisions in virtual time and assert the durable outcomes; fix operation selection with a small chooser wrapper | A documented phased-workload hook may be a useful next improvement. No speculative per-peer configuration was needed. |
| Every service needs generation in its own model directory | Three tiny binaries, three generated-file checks, one module; peer URLs attached at the binaries | Some orchestration boilerplate, but no need for a new deployment abstraction yet. |
| One call uses its processor's stable effect key | Separate reserve/cancel/confirm processors | Do not sequentially call different commands of the same peer under one effect key: markers are scoped to the App, not the command. |
| Generic Takes coverage and scenario expectations are different types | Wrap one-step scenarios when claiming Some over runs | The compiler catches the mistake, but expectation vocabulary adds learning cost. |
| A total ordering limit can be exhausted without a failure | Require Exhaustive in faithful trial tests and print Checked | Reporting is honest; users must choose a bounded workload. A checker is reused within a search, but many independent checks still add model-evaluation cost. |
| The repository simulates every example binding on its own | Add a finite-input step harness for the business model, including fresh accepted intent/authorization ids; sample safety and cap symbolic verification at six steps | The protocol model needs its own standalone harness as well as the trace binding. |
| Generated local models do not establish cross-service obligations | Handwrite one combined model and binding, and validate local histories too | Model ownership remains substantial. Generating the business contract from implementation would remove its independence. |

## Diagnostic quality

- **freshKey:** the native history witnesses two successful reserve commits
  for one checkout after a lost answer. Inventory's local capacity rule
  holds. Links names the unlinked command, and the protocol rejects the
  missing recorded checkout cause at inventoryReserve. The causal failure
  appears before the duplicate-allocation guard; do not describe the
  diagnostic as if it directly identified the second unit.
- **missingRelease:** the processor and local models finish normally,
  and links agree on every call that exists. At the recorded settle,
  allIntentsAnswered is false. This identifies an unanswered intent; the
  test additionally confirms a real unit remains held. The model does not
  yet print an order-specific outstanding-obligation explanation.
- **authorizeCancelled:** inventory's own sequence shows cancel before
  authorize, followed by a fulfillment start. The local failure says
  expected not-held but received ok; the protocol rejects the same durable
  decision. This is the clearest actionable diagnostic of the three.

TRIAL_EVIDENCE saves the projected failing history. Ordinary validation
reproduces the failure, retaining original recorder/sequence observations.
The report includes framework read sets and effect-key strings, which
are useful for diagnosis but verbose for a first-time app author.

## What to do next

Prefer an independently maintained adoption trial, using this workflow as
a reference. If improving the library first, investigate **phased workload
control** and **order-specific outstanding-obligation diagnostics**. These
are concrete friction from this trial. There is no evidence here that
SQL peers, cyclic graphs, cluster configuration, rollouts or span support
should come first. Keep the APIs flexible while those needs are tested.

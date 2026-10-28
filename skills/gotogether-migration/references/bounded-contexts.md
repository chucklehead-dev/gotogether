# Choosing and drawing a bounded context

From the gotogether-migration skill (its SKILL.md says when to read this).

A model is checked one context at a time. A context that is too wide
makes a model whose state is the whole system's and whose every run is
slow to judge; one that is too narrow leaves the interesting interleavings
outside it (a payment's refusal decided by another service).

## What makes a good first context

- **One place its state commits**: a database's tables one service owns,
  an event-sourced aggregate, a queue's consumer group. A step recorded
  there is recorded where it takes effect (gotogether-patterns §3).
- **A failure the team has seen**: a duplicate charge, a lost job, a
  stuck rollout. The first model's value is a check that would have
  caught it; its mutant is that incident.
- **An owner who can answer**: every disagreement between code and model
  becomes a question only the owners can decide.
- **Operations you can enumerate**: the handlers, commands, consumers and
  scheduled jobs that change the context's state, each a model action.

## Drawing it

Write the context's map at the top of its model file, as a comment, so
the specification carries it:

```quint
// Context: payments (owner: payments team)
// State: charges, refunds (postgres: payments.charges, payments.refunds)
// Operations: charge (POST /charges), refund (POST /refunds), settle (cron, nightly)
// Environment: processor (HTTP, at-least-once retries by us), ledger (Kafka topic ledger.v2)
// Outside: accounts (read only: balance checks), notifications (fire and forget)
module payments { ... }
```

- **Operations** become actions; what each observes, its parameters.
- **The environment** becomes doubles in a simulation (qenv's Store,
  Queue, Service) and, in the model, what their contracts allow: a
  processor that may apply a request and lose the answer, a topic that
  redelivers.
- **Outside** names what the model abstracts: a read-only dependency is
  a parameter or a nondeterministic choice, a fire-and-forget one is not
  modelled.

## Finding the operations in code

- `gotogether lint -binding b.yaml -src DIR` lists the steps a binding
  names that the code records nowhere, once a binding exists.
- `actionmap` (qanalysis/cmd/actionmap) prints the code each model
  action reaches from where its steps are recorded, and the functions no
  root reaches: run it on the context's packages to see what the model
  does not yet explain.
- Existing OTel spans name operations already: list the span names a
  service emits and map the ones that change state (`convert` rules).

## Splitting later

When a model's state grows past what replays judge cheaply, or two
teams own its halves, split it at a seam where one side's operations
are the other's environment: the other context's model is then a double
with a contract, checked on its own.

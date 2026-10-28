---
name: gotogether-patterns
description: Modeling patterns for Quint specs that real Go systems are checked against with gotogether - the requirement rather than one solution, the environment's real guarantees, linearization points, history properties with ghosts, eventual properties on a recorded settle, spec-level vs mechanism-level models with a lemma, outcomes and restarts, design limits, cheap long replays. Use when deciding what a model should allow, how to state a property, why a correct-looking model passed a buggy system (or rejected a correct one), or why validating long runs is slow.
---

# Modeling patterns for checked systems

A model that is too strict rejects correct code; one that is too loose,
or assumes more of the environment than it gives, passes buggy code.
Each pattern below names the mistake and the fix.

## Steps

1. Specify the requirement, not your solution: `gossip` may send any
   subset, an id is arbitrary and unique.
2. Model the environment you run on, its guarantees as weak as
   documented (at-least-once means duplicates; a read may be stale).
3. Record each step where it takes effect (inside the critical section,
   at the service, in program order per node).
4. Check a history property on arrival and remember a violation in a
   ghost variable (`val ok = not(repeated)`), never by re-scanning.
5. Condition an eventual property on a recorded settle: a binding's
   `eventually: [converged]`, judged in a settled run's last state.
6. When the spec-level state grows, check a bounded mechanism model and
   test the lemma it needs (a pure function's injectivity) with Hegel.
7. Model outcomes, refusals and restarts as values and actions.
8. Separate what the design promises from what it checks but does not
   promise (a design limit, the binding's `allows`).
9. Keep long replays cheap: opaque ids abstracted, collections as Go
   values, invariants linear in the state.

## References (when to read each)

- [references/patterns.md](references/patterns.md): each pattern in full
  with its examples (glomers, jobqueue, checkout, webjobs), settles,
  quorums, journeys that must finish, and the checklist for a new model.
- The gotogether-model skill: the conventions every checked model
  follows.

---
name: gotogether-patterns
description: Modeling patterns for Quint specs that real Go systems are checked against with gotogether - the requirement rather than one solution, the environment's real guarantees, linearization points, history properties with ghosts, eventual properties on a recorded settle, spec-level vs mechanism-level models with a lemma, outcomes and restarts, design limits, cheap long replays. Use when deciding what a model should allow, how to state a property, why a correct-looking model passed a buggy system (or rejected a correct one), or why validating long runs is slow.
---

# Modeling patterns for checked systems

A model too strict rejects correct code; one too loose, or assuming
more of the environment than it gives, passes buggy code. Each pattern
names the mistake and the fix.

## Choose your path

- Every pattern in full with its examples, settles, quorums, journeys that must finish: [references/patterns.md](references/patterns.md).
- The conventions every checked model follows: the gotogether-model skill.

## Steps: the patterns, in the order to check them

1. Specify the requirement, not your solution (`gossip` may send any subset).
2. Model the environment you run on, as weak as documented.
3. Record each step at its linearization point.
4. Check a history property on arrival; remember a violation in a ghost.
5. Condition an eventual property on a recorded settle (`eventually:`).
6. A bounded mechanism model, with the lemma it needs tested by Hegel.
7. Outcomes, refusals and restarts are part of the model.
8. Separate what the design promises from what it checks (`allows`).
9. Keep long replays cheap: ids abstracted, invariants linear, a log of
   independent entities partitioned (`partition:`).

## Checklist for a new model

- [ ] It allows every strategy the requirement allows. (1)
- [ ] The environment's guarantees are as weak as documented. (2)
- [ ] Every step is recorded at its linearization point. (3)
- [ ] Each history property is checked on arrival, with a ghost. (4)
- [ ] Each eventual property is conditioned on a settle, and recorded. (5)
- [ ] A journey that must finish ends in a ghost flag claimed by `Finishes()`. (5)
- [ ] Outcomes, refusals and restarts are modelled. (7)
- [ ] Promised and unpromised properties are separated. (8)
- [ ] There is a mutant per property, showing it can fail.

## Results

| Symptom | Pattern | Next |
|---|---|---|
| a correct implementation is rejected | 1: the model is one solution | widen the action's choices |
| a buggy system passed | 2: the environment is too kind; or 8: unpromised | weaken the double's guarantees; a mutant |
| `Rejects` on an ordering that happened | 3: recorded off the linearization point | move the record into the critical section |
| validation slows quadratically with run length | 4 or 9: an invariant re-scans history | a ghost checked on arrival |
| `eventual vals not judged` | 5: no settle recorded last | `Sim.Settle`, `qobs.Settled` |
| a val false in every run before quiet | 5: an eventual property written as an invariant | `eventually:` |
| state grows without bound | 6: spec-level state | a mechanism model and its lemma |
| `Rejects` on a refused call | 7: no step for a refusal | an outcome value, the binding's `outcomes` |

## Run it, do not infer it

Whether a model is too strict or too loose is found by running it: a
`possible` it cannot reach, a mutant it does not catch, `gotogether nav`
on the state where it rejects.

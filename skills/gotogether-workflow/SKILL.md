---
name: gotogether-workflow
description: End-to-end, model-first workflow for checking a Go system against a Quint model with gotogether - acceptance scenarios each with a possibility check, then safety, then liveness, all on the model alone, then the code recorded, bound, its checks proved by mutants (.Expect(t, claims...), qhegel.FindViolation), explored by Hegel and checked in the real environment - and how to triage each verdict and lay out CI. Use when starting verification of a new component or example, planning which tests to write, deciding whether a failure is the code's, the model's or the binding's, or setting up CI.
---

# Verifying a Go system against a Quint model, model first

Scenarios, then safety, then liveness, on the model alone; then code.
Each stage checks the one before, and a skipped stage leaves a class of
mistakes unseen: a model too strict for a scenario passes every
invariant and judges code that never shows it faithful.

## Choose your path

- Code exists and no model: the gotogether-migration skill first.
- Every stage's commands and code, triage, CI: [references/workflow.md](references/workflow.md).
- A verdict or claim: [references/verdicts.md](references/verdicts.md).
- Planting a mutant: [references/overlays.md](references/overlays.md).
- The model's conventions and patterns: gotogether-model, gotogether-patterns.

## Steps

1. **Scenarios**: each acceptance scenario a `possible` (`steps: [deposit,
   'withdraw(result = "insufficient")']`) or a `qtest.Scenario`
   checked by `qtest.Possible`; `gotogether simulate -binding m.binding.yaml`.
2. **Safety**: each property a `val`, in `holds` (no trace violates it)
   or `allows` (some trace must); `-graph` for every reachable state.
3. **Liveness**: eventual vals in the binding's `eventually:`, judged at
   a recorded settle; a model's intended end as `simulate.terminal`.
4. **Code**: record each step where it takes effect; `gotogether
   scaffold`, `lint -binding -src`, `scaffold -system`.
5. **Mutants**: one per property and per `expect`, each with its claim.
6. **Hegel**, then **the real environment** (`gotogether validate`).

## Checklists

Before code:
- [ ] Every scenario a stakeholder named is a `possible`, each reached.
- [ ] Each `holds` invariant was seen to fail once (a guard dropped).
- [ ] Each `allows` invariant is violated by some trace.
- [ ] Each eventual property is conditioned on a settle.

Before merging:
- [ ] The faithful system conforms, settles, and `Reaches(...).Faulted()` the faults it must survive.
- [ ] Every mutant is caught as its test states; the README's table lists them.
- [ ] `qtest.MutateSteps` has run on a recorded log.
- [ ] CI: `simulate` per binding, `go test -short`, `GOTOGETHER_STRICT=1`.

## Results

| Result | Means | Next |
|---|---|---|
| `possible X: FAIL`, the model forbids it | the model is too strict for a scenario | `gotogether nav -binding` to the guard |
| `not reached in N sampled traces`, `not found within the bound` | rare, or forbidden | `-graph`, or raise `-samples`, `simulate.searchStates` |
| a `holds` invariant violated | a design bug, or the property is wrong | the shortest counterexample: `-graph -out cex.qnt` |
| an `allows` invariant never violated | a vacuous check of the code | the model or the property |
| a deadlock | a state the step cannot leave | fix it, or declare `simulate.terminal` |
| `Rejects` | the code did what the model forbids | `why:`, then the binding (mapping, outcome, restart) |
| `Mismatch` | the code computed another state | the `expect` or the `obs.*` value |
| `Violates` | the design allows the bad run | fix the design, or the property |
| exit 2, no verdict | the binding or model does not fit the steps | the message names the step and template |
| a mutant survives | the check is too weak | strengthen it, or document the gap |
| conforms, the environment's checker fails | the model assumes more of the environment | gotogether-patterns §2 |

## Run it, do not infer it

What the model allows, and what a run did, are answered by running:
`gotogether simulate`, `gotogether nav -binding ... -json`, `gotogether
nav -run 'TestX#2:seed=17' -at fail -json`, and the report. State
expectations with `.Expect`; never loosen a check to get green.

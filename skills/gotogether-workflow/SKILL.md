---
name: gotogether-workflow
description: End-to-end workflow for checking a Go system against a Quint model with gotogether - model first (simulate its invariants and possibilities), implement with step recording, bind, prove each check can fail with mutants (.Expect(t, claims...), qhegel.FindViolation), let Hegel explore, then check runs from the real environment - and how to triage each verdict and lay out CI. Use when starting verification of a new component or example, planning which tests to write, deciding whether a failure is the code's, the model's or the binding's, or setting up CI for model-checked tests.
---

# Verifying a Go system against a Quint model

The order matters: each stage checks the previous one, and skipping one
leaves a class of mistakes undetected. The other skills hold each
stage's mechanics.

## Steps

1. Model and simulate before writing code: `quint typecheck`, then
   `gotogether simulate -binding m.binding.yaml` (`-graph` for every
   reachable state) with `simulate: {holds, allows, possible}`.
2. Implement, recording each step where it takes effect, its fields
   named after the model's parameters.
3. Bind and lint: `gotogether scaffold`, `gotogether lint -binding
   m.binding.yaml -src ./pkg`, `gotogether scaffold -system`.
4. Prove each check can fail: a mutant per property and per `expect`
   (`testdata/mutants/<name>`, `qtest.Mutants{}.Run(t)`), each with the
   claim that catches it; observation mutants with `qtest.MutateSteps`.
5. Let Hegel explore: `qhegel.Test` on the faithful code,
   `FindViolation` or `Shrink` on mutants, outside `-short`.
6. Check the real environment: step logs validated with `gotogether
   validate -binding` (`-require-finish` for a journey).
7. Triage a failure from its report (`expect:`, `obs:`, `why:`,
   `navigate:`); never loosen a check to get green.

## References (when to read each)

- [references/workflow.md](references/workflow.md): every stage with its
  commands and code, the triage table and CI's layout (strict mode,
  expected evidence, a finished run).
- [references/verdicts.md](references/verdicts.md): every verdict, its
  expectation and its claims over many runs.
- [references/overlays.md](references/overlays.md): planting a mutant
  as an overlay, and when `qweave -mutate` fits better.

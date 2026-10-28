---
name: gotogether-hegel
description: Combine Hegel property-based and state-machine testing (hegel.dev/go/hegel) with a Quint model through gotogether's qhegel - Hegel-driven runs judged and shrunk by the model (qhegel.Test, FindViolation, Shrink), or the model's traces replayed inside Hegel with drawn details (VaryTraces). Use when writing hegel.RunStateful machines for code that has a Quint spec, when a Hegel counterexample must be classified as an implementation or a design bug, or when model-based tests miss inputs the model forbids.
---

# Hegel and the model (`qhegel`)

Hegel generates and shrinks test cases; the model knows what the system
may do. Hegel drives and the model judges (finding what the model
forbids, which a model-driven test never asks for), or the model drives
and Hegel varies the details. Use both.

## Choose your path

- Either direction with code, the modes, expected failures, classifying: [references/guide.md](references/guide.md).
- The modes' costs and options, generators and budgets: [references/modes.md](references/modes.md).
- A machine for concurrent or distributed code (`hegelnet.Network`): [references/pitfalls.md](references/pitfalls.md) first.
- A simulated System's faults shrunk: `qhegel.Shrink` (the gotogether-faults skill).

## Steps

1. Record the code's model steps and bind them (gotogether-traces).
2. A `hegel.RunStateful` machine, one `Rule*` per operation, drawing
   invalid inputs too.
3. The faithful code: `qhegel.Test(t, qhegel.Config{Binding: ...,
   Mode: qhegel.Batched}, body)`.
4. A mutant: `qhegel.FindViolation(t, cfg, body,
   hegel.WithSeed(n)).Expect(t, qtest.Rejects("transfer"))`; a System:
   `qhegel.Shrink(t, sys).Expect(t, ..., qtest.ShrunkTo(1))`. Outside `-short`.
5. The model's traces, varied: `qhegel.VaryTraces(ht, qhegel.Traces(t,
   cfg), driver, qhegel.VaryOptions{Prefix: true})`.

## Checklist

- [ ] Each rule draws inputs the model forbids as well as those it allows.
- [ ] `qtest.Global()` in `Opts` when the body's goroutines record.
- [ ] An expected failure uses `FindViolation` or `Shrink`, never `hegel.Test`.
- [ ] Its shape is stated with `.Expect`, not read from the summary.
- [ ] A shrunk failure Hegel found is kept as a scripted regression.

## Results

| Report says | Means | Next |
|---|---|---|
| `qhegel: the run does not satisfy the model` with a step not a transition or a mismatch | the implementation did what the design forbids | fix the code |
| the same, conforming, a model invariant `FAIL` | the design allows the bad run | fix the design, then the code; the generated module is a regression |
| conforms and satisfies the model, a Hegel invariant fails | the Hegel property is stronger than the model | add the missing `val`, or fix the property |
| `qhegel: every test case satisfied the model (N checked with quint); want ...` | the mutant was not found | more cases, other draws, or the check is too weak |
| `qhegel: the test case recorded no model steps` | the body recorded nothing | the recorder, `qtest.Global()`, or `AllowEmpty` |
| `ShrunkTo(n)` fails | more faults than claimed survive shrinking | read the shrunk run's faults |

## Run it, do not infer it

Classify a counterexample from its validated report, not from reading
the machine: rerun the shrunk case (`GOTOGETHER_VERBOSE=1` shows Hegel's
replay) and validate it.

---
name: gotogether-hegel
description: Combine Hegel property-based and state-machine testing (hegel.dev/go/hegel) with a Quint model through gotogether's qhegel - Hegel-driven runs judged and shrunk by the model (qhegel.Test, FindViolation, Shrink), or the model's traces replayed inside Hegel with drawn details (VaryTraces). Use when writing hegel.RunStateful machines for code that has a Quint spec, when a Hegel counterexample must be classified as an implementation or a design bug, or when model-based tests miss inputs the model forbids.
---

# Hegel and the model (`qhegel`)

Hegel generates and shrinks test cases; the model knows what the system
may do. Hegel drives and the model judges (it finds what the model
forbids, which model-driven tests never ask for), or the model drives and
Hegel varies the details. Use both.

## Steps

1. Record the code's model steps (`qobs.Record`, `//quint:action`) and
   bind them (the gotogether-traces skill).
2. Write a `hegel.RunStateful` machine, one `Rule*` method per
   operation, drawing invalid inputs too.
3. Judge the faithful code: `qhegel.Test(t, qhegel.Config{Binding: ...,
   Mode: qhegel.Batched}, body)`.
4. State a mutant's failure: `qhegel.FindViolation(t, cfg, body,
   hegel.WithSeed(n)).Expect(t, qtest.Rejects("transfer"))`, or
   `qhegel.Shrink(t, sys).Expect(t, ..., qtest.ShrunkTo(1))` for a
   System. Skip both under `testing.Short()`.
5. Vary the model's own traces: `qhegel.VaryTraces(ht, qhegel.Traces(t,
   cfg), driver, qhegel.VaryOptions{Prefix: true})`.
6. Classify the shrunk run: does not conform (the code's bug), conforms
   but a model invariant fails (the design's), conforms and satisfies the
   model but a Hegel property fails (the property or a missing `val`).

## References (when to read each)

- [references/guide.md](references/guide.md): both directions with code,
  modes, expected failures, classifying a counterexample.
- [references/modes.md](references/modes.md): the modes' costs and
  options, generators and budgets.
- [references/pitfalls.md](references/pitfalls.md): before a machine for
  concurrent or distributed code (`hegelnet.Network`).

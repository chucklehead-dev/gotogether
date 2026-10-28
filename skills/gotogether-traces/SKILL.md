---
name: gotogether-traces
description: Instrument a Go application so its runs are checked against a Quint model, and check them - steps emitted with qobs.Record, qobs.Invoke, //quint:action (woven), slog records or existing OTel spans (convert rules), a binding YAML, gotogether lint and validate, qtest.Record and Validate in tests, linearizability (Linearize), request logs from external drivers (Bombadil, Playwright). Use when asked to check test, staging or production runs against a spec, to add model-step telemetry, to write or fix a binding, or to read a gotogether validate report.
---

# Trace validation: the running code reports, the model judges

The code emits one step per model action it performs; gotogether
reconstructs each actor's order, maps steps to Quint calls through a
binding, replays the run (the Go evaluator decides a passing one, quint
reports a failing one) and says, per step, whether it is a transition,
whether the observed state matches, and whether the invariants hold.

## Steps

1. Emit steps where they take effect, holding the lock that orders them:
   `qobs.Record(ctx, "deposit", err, "account", a, "obs.balance", bal)`,
   or `qobs.Invoke` then `Call.Done` for a call that may never hear back,
   or `//quint:action`, slog records, a `convert` rule file for spans.
2. In a program: a recorder on a sink (`qobs.FileSink(path,
   qobs.PerStep)`), ended by `rec.Close()`, whose error says whether the
   log is whole.
3. Scaffold the binding (`gotogether scaffold -spec ... -module ...`)
   and add only what is not a same-named field: `expect`, `outcomes`,
   `restart`, `constants`, `invariants`.
4. Lint: `gotogether lint -binding b.yaml -src ./pkg`.
5. Check in a test: `qtest.Record(t, "b.yaml", scenario).Expect(t,
   qtest.Conforms())`; a mutant's with `Rejects`, `Mismatch`,
   `Violates`. Outside: `gotogether validate -binding b.yaml run.jsonl`.
6. Read a rejection's `why:` first (the false condition, with values),
   then open the model there: `gotogether nav -binding b.yaml -run
   run.jsonl -at k -json`.
7. Prove the check reads what was recorded: `qtest.MutateSteps`.

## References (when to read each)

- [references/steps.md](references/steps.md): the four stages in full,
  each way to emit, the binding's fields, checking in tests and servers,
  the report's verdicts and issues table, the navigator.
- [references/emitting.md](references/emitting.md): step keys,
  outcomes, sinks, recording a server in a test.
- [references/binding.md](references/binding.md): templates, constants,
  interning, actors, snapshots.
- [references/histories.md](references/histories.md): linearizability
  and isolation histories (`Linearize`, `qhistory`).
- [references/partial-logs.md](references/partial-logs.md) and
  [references/production-logs.md](references/production-logs.md): lost
  prefixes and suffixes, rotated and sanitized production logs.
- [references/external-drivers.md](references/external-drivers.md):
  request logs from Bombadil and Playwright, `ShrinkActions`.
- [references/observation-mutants.md](references/observation-mutants.md):
  `MutateSteps`' operations and survivors.
- [references/patterns.md](references/patterns.md): concurrent services,
  restarts, Lamport clocks, before instrumenting one of those.

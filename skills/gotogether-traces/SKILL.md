---
name: gotogether-traces
description: Instrument a Go application so its runs are checked against a Quint model, and check them - steps emitted with qobs.Record, qobs.Invoke, //quint:action (woven), slog records or existing OTel spans (convert rules), a binding YAML, gotogether lint and validate, qtest.Record and Validate in tests, linearizability (Linearize), request logs from external drivers (Bombadil, Playwright). Use when asked to check test, staging or production runs against a spec, to add model-step telemetry, to write or fix a binding, or to read a gotogether validate report.
---

# Trace validation: the running code reports, the model judges

The code emits a step per model action; gotogether orders each actor's
steps, maps them to Quint calls by a binding, replays the run, and says
per step whether it is a transition, whether the observed state matches,
and whether the invariants hold.

## Choose your path

- The stages, each way to emit, the binding, tests and servers, the report's tables, the navigator: [references/steps.md](references/steps.md).
- Step keys, outcomes, sinks, a server in a test: [references/emitting.md](references/emitting.md); the binding's fields: [references/binding.md](references/binding.md).
- Linearizability and isolation histories: [references/histories.md](references/histories.md).
- Logs that lost a head or a tail, production logs: [references/partial-logs.md](references/partial-logs.md), [references/production-logs.md](references/production-logs.md).
- Bombadil, Playwright, `ShrinkActions`: [references/external-drivers.md](references/external-drivers.md).
- Does the check read what was recorded: [references/observation-mutants.md](references/observation-mutants.md).
- Concurrent services, restarts, Lamport clocks: [references/patterns.md](references/patterns.md).

## Steps

1. Emit each step where it takes effect: `qobs.Record(ctx, "deposit",
   err, "account", a, "obs.balance", bal)`; `qobs.Invoke`/`Call.Done` for
   a call that may never hear back; `//quint:action`; slog; `convert`.
2. A program: a recorder on `qobs.FileSink(path, qobs.PerStep)`, ended
   by `rec.Close()`.
3. `gotogether scaffold -spec ... -module ...`; add only `expect`,
   `outcomes`, `restart`, `constants`, `invariants`.
4. `gotogether lint -binding b.yaml -src ./pkg`. For agent/editor evidence add
   `-json`: inspect findings and requested-check statuses, including blocked
   checks. A clean static lint report does not establish trace conformance.
5. `qtest.Record(t, "b.yaml", scenario).Expect(t, qtest.Conforms())`, or
   `gotogether validate -binding b.yaml run.jsonl`.
6. `qtest.MutateSteps` once the binding is written.

## Checklist

- [ ] Each step is recorded holding the lock that orders it.
- [ ] Step fields are named after the model's parameters.
- [ ] A crash-tested program's sink is `PerStep` or `Synced`, never `Buffered`.
- [ ] Goroutines a test's scenario starts record through its recorder.
- [ ] A surprising verdict sends you to the binding first.

## Results

| Report says | Means | Next |
|---|---|---|
| `record <id> is not a transition` (QNT513) | the model cannot take the step | `why:` and `the model expects:` first: the code, or the binding's mapping, outcome or restart |
| `state does not match what was observed` (QNT508) | the observed state differs | `expect:` vs `obs:` |
| `invariant X: FAIL, violated after record <id>` | a design bug now seen in code | `the step changed` lines; fix the design, or the property |
| `issue: error gap` | steps lost or sampled | fix the pipeline: no verdict |
| `issue: error lost-prefix` | a log started after seq 1 | a snapshot step, or `resume:` |
| `issue: warn lost-suffix`, `conformance: N/A` | a lost tail | a log that kept them; `Synced` |
| `issue: error overlap` | an incarnation acted after its successor | fence it, or order by a domain clock |
| `issue: error cycle` | contradictory order keys | the instrumentation's order keys |
| `real time bounds none of its N steps` | no invocation times | `qobs.Invoke` |
| exit 2, no verdict | the binding or model does not fit | the message names step and template |

A record is named once, where it was read (`day.jsonl:12`, `span …`, else
`<recorder>#<seq>`), its wall time, process and trace on its `record:`
line; `-v` adds the replay's step and the seq. `validate -json`
(`gotogether.report/1`), `-report junit=F`, `-report sarif=F` are for
machines and CI.

Every verdict: [../gotogether-workflow/references/verdicts.md](../gotogether-workflow/references/verdicts.md).

## Run it, do not infer it

Open the model at the rejected step (`gotogether nav -binding b.yaml
-run run.jsonl -at k -json`, or the report's `navigate:` line) and read
its branches; do not infer what the model would have allowed.

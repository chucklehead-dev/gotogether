---
name: gotogether-traces
description: Instrument a Go application so its behaviour can be checked against a Quint model, and check it (qobs.Record, //quint:action with Orchestrion, OTel span events, native step logs, converting existing OTel spans with convert rules, binding YAML, gotogether lint/validate, qtest). Use when asked to check production or test runs against a spec, to add model-step telemetry, to write or fix a binding, or to interpret a gotogether validate report.
---

# Trace validation: the running code reports, the model judges

The code emits one **step** per model action it performs. gotogether
then works through the following stages:

1. **Reconstruct.** It orders the steps per actor, using dense sequence
   numbers, program-order threads and domain clocks. It detects lost steps
   and restarts.
2. **Map.** A **binding** maps each step to a Quint call.
3. **Replay.** It generates a Quint module and replays the run with
   `quint test`.
4. **Report.** For each step it says whether the step is a transition of
   the model, whether the observed state matches, and whether the
   invariants hold.

## 1. Emit steps (pick one or more)

| Way | Code | When |
|---|---|---|
| Explicit | `qobs.Record(ctx, "deposit", err, "account", a, "amount", n, "obs.balance", bal)` | anywhere; ~20 ns when no recorder is installed |
| At invocation | `c := qobs.Invoke(ctx, "transfer", "src", a, "dest", b, "amount", n)`, then `c.Done(err, "obs.balance", bal)` | a client's call whose caller may crash or never hear back: the trace says "invoked, never completed" (`qtest.Completes()`) |
| Annotation | `//quint:action action:deposit account:acct amount:n obs.balance:b.bal` on a function, built woven: `qweave go build` (this repository's weaver, an overlay), `go run github.com/DataDog/orchestrion go build`, or in gosim (a `gosim.tool.go` naming qobs) | function-shaped steps, no code change in plain builds |
| Structured logs | `slog.New(qobs.NewSlogHandler(next))`, then `slog.InfoContext(ctx, "deposited", "quint.action", "deposit", "account", a, ...)`: records with `quint.action` are steps, every record still reaches `next` | a service that already logs what it did |
| Existing OTel spans | a `convert` rule file mapping span names and attributes to actions | code you cannot change, or telemetry you already have |

**Record at the moment the step takes effect**, while holding the lock that
orders it, and before spawning work that depends on it. The step's final
`error` is its outcome; `obs.<name>` keys are observations of the state
after it.

Read [references/emitting.md](references/emitting.md) for the keys a step
takes (identity, `when:`, `order.<domain>`), outcomes an error can name,
and the sinks.

## 2. Write the binding

```sh
gotogether scaffold -spec model/bank.qnt -module bank -out bank.binding.yaml
```

```yaml
spec: model/bank.qnt
module: bank
constants: { ACCOUNTS: 'Set("alice", "bob")', MAX_AMOUNT: '{{ atLeast 5 (max "amount") }}' }
intern: { gen: process }        # opaque ids -> 1, 2, ... by first appearance
outcomes: { ok: Ok, error: Fail }
restart: crash                  # inserted where the process identity changes
actions:
  deposit:
    # account, amount: the step fields of those names (the default)
    outcomes: { error: skip }   # a refused call is no model step
    expect: 'balances.get({{.account}}) == {{.obs.balance}}'
invariants: [noOverdraft]
eventually: [noPending]          # must hold in the last state (a run that settled)
within:                          # bounded response: after a val, another within a time
  - { after: overdrawn, holds: alerted, bound: 5s }
hidden: [settleAny]              # nullary actions no step records: searched between steps
hiddenDepth: 4                   # at most this many between two recorded steps (default 4)
snapshot: restore                # an action of the whole state: a trace that starts with one starts from its state
```

`hidden` names actions the system takes without recording them (a
background gossip, a timer): the Go evaluator searches the states they
reach between recorded steps, and a trace conforms if some path takes
every step. A step no state within `hiddenDepth` of them takes, while
more hidden actions would reach new states, is no verdict ("raise
hiddenDepth"), never a rejection. Hidden actions do not combine with
Linearize or ResolvePending.

A parameter with no template is the step field of its name; give a
template only to map a field differently, and map every failed outcome
the templates ignore (`skip` or `call`). Then lint:
`gotogether lint -binding bank.binding.yaml -src ./pkg` (binding vs model,
annotations vs binding, literal `qobs.Record` actions vs model).

Read [references/binding.md](references/binding.md) when writing
templates (collections, outcomes, ITF), or setting up the analyzers
(quintvet, golangci-lint, actionmap).

## 3. Check

- In a test:
  `qtest.Record(t, "bank.binding.yaml", func(*qobs.Recorder) { scenario() }).Expect(t, qtest.Conforms())`.
  Use `qtest.Validate` for steps recorded elsewhere: every verdict is an
  `Expect`. Start with
  `qtest.RequireQuint(t)`, which skips the test without quint.
  The scenario's goroutine records into the run. Steps from goroutines
  it starts (an HTTP server's handlers, a cluster's nodes) fail the test
  unless you pass `qtest.Global()`. That option installs the run's
  recorder globally, so such tests must not run in parallel.
- **Expected verdicts** (mutants): `qtest.Record(...).Expect(t, e...)`,
  where each `e` says how the run must show up:
  - `qtest.Conforms()`;
  - `qtest.Rejects("ack")`: a step the model cannot take;
  - `qtest.Mismatch("read")`: a failed `expect`;
  - `qtest.Violates("prop")`: the run conforms and the invariant fails.

  On a miss, the failure message is the report.
- In code: `validate.NewChecker(binding, opts).Check(ctx, steps)`. Use
  `CheckBatch` for many runs in one quint process per chunk.
- From files: `gotogether validate -binding b.yaml FILE...`. FILE may be
  OTLP/JSON, a step log or ITF, detected by content. Add
  `-convert rules.yaml` for plain app spans. Exit status: 0 OK,
  1 violation, 2 tool error.
- Converted spans: `gotogether convert -rules bank.convert.yaml spans.jsonl > steps.jsonl`.

Before trusting a model's isolation level of a real database, check
the database: read [references/histories.md](references/histories.md)
for a list-append workload and `qhistory.Check`.

A log that lost its head (rotated, truncated, a recorder started
mid-run) or its tail (torn, killed): read
[references/partial-logs.md](references/partial-logs.md) for the
`snapshot` step it starts from and what a lost tail leaves unjudged.

Production logs are selected and sanitized before they are checked
(`gotogether steps -binding ... -pseudonymize ...`): read
[references/production-logs.md](references/production-logs.md).

## 4. Read the report

| Verdict | Meaning | Usual cause |
|---|---|---|
| `step i is not a transition` (disabled, QNT513) | the model cannot take the observed step from the state reached | implementation bug; or a wrong binding (argument mapping, missing restart, wrong outcome) |
| `state does not match what was observed` (QNT508) | the step was possible but `expect` fails; the report shows `expect:`, `obs:` and the model's state after the step | implementation computed a different state, or a wrong `expect`/observation |
| `invariant X: FAIL, violated after step i` | the run is a behaviour of the model, and the model's own property fails on it | a **design** bug the model already allows, now seen in real code |
| `issue: error gap` | sequence numbers are missing: steps were lost or sampled | fix the pipeline; the pass is unsound |
| `issue: error lost-prefix` | a log starts after seq 1: its first state is not `init` | record a snapshot step at each log's head and name its action (`snapshot:`) |
| `issue: error overlap` | an incarnation acted after its successor started | a split brain (fence the old incarnation), or wrong process ids or skewed clocks (order with a domain clock) |
| `issue: error cycle` | contradictory order keys | instrumentation order keys are wrong |

The generated module (`module:` in the report) is a plain Quint file:
`quint test` it, edit it, or keep it as a regression test. The `itf:` file
of a conforming run carries the model's full state and `mbt::nondetPicks`,
so `connect.Replay` (with `itf.ReadFile`) can drive the implementation with it again.

Soundness is relative to the binding: a wrong template can hide or invent
a bug. When a verdict surprises you, check the binding first.

## Patterns from the example apps

Concurrent services, restarts, properties the design does not promise,
distributed systems with Lamport clocks, and modelling the environment
you run on: read [references/patterns.md](references/patterns.md) before
instrumenting one of those.

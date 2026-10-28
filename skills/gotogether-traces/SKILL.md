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

A program records into a recorder on a sink (`sink, err :=
qobs.FileSink("steps.jsonl", qobs.PerStep)`, `rec :=
qobs.NewRecorder(sink)`, `qobs.SetGlobal(rec)` or
`qobshttp.Handler(h, rec)`) and ends with `if err := rec.Close(); err !=
nil { ... }`, which closes the sink and says whether the log is whole.

Read [references/emitting.md](references/emitting.md) for the keys a step
takes (identity, `when:`, `order.<domain>`), outcomes an error can name,
the sinks and who closes them.

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
    outcomes: { error: skip }   # a refused call is no model step (one the model judges: model the refusal)
    expect: 'balances.get({{.account}}) == {{.obs.balance}}'
invariants: [noOverdraft]
eventually: [noPending]          # must hold in the last state (a run that settled)
complete: true                   # validate fails a run that never settled or left calls pending (needs eventually)
within:                          # bounded response: after a val, another within a time
  - { after: overdrawn, holds: alerted, bound: 5s }
hidden: [settleAny]              # nullary actions no step records: searched between steps
hiddenDepth: 4                   # at most this many between two recorded steps (default 4)
snapshot: restore                # an action of the whole state: a trace that starts with one starts from its state
resume: resumeAny                # without a snapshot: the states a log that lost its head may start in
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
  it starts fail the test (strict: none is lost silently) unless they
  record through a context carrying the run's recorder
  (`qobs.WithRecorder(ctx, rec)`). An HTTP server's handlers run on
  net/http's goroutines: wrap the handler, and the run, in parallel with
  others, gets their steps:

  ```go
  qtest.Record(t, "kv.binding.yaml", func(rec *qobs.Recorder) {
      h := qobshttp.Handler(api, rec)
      srv := httptest.NewServer(h)
      defer h.Wait()    // hijacked connections' handlers too (srv.Close forgets them)
      defer srv.Close() // the requests in flight
      /* drive srv.URL */
  }).Expect(t, qtest.Conforms())
  ```

  `qtest.Global()` installs the run's recorder globally instead, for
  goroutines nothing can hand a context (a cluster's nodes, a server you
  cannot wrap); such tests must not run in parallel.
  Read [references/emitting.md](references/emitting.md), "Recording a
  server in a test", for handlers' goroutines, client steps, reruns and
  Hegel cases.
- **Expected verdicts** (mutants): `qtest.Record(...).Expect(t, e...)`,
  where each `e` says how the run must show up:
  - `qtest.Conforms()`;
  - `qtest.Rejects("ack")`: a step the model cannot take;
  - `qtest.Mismatch("read")`: a failed `expect`;
  - `qtest.Violates("prop")`: the run conforms and the invariant fails.
  - `qtest.Finishes()`: the run is a finished journey, not a conforming
    prefix: settled, every eventual val judged and true, every call
    completed.
  - `qtest.Undecided(qtest.Finishes(), "lost its tail")`: the run has no
    verdict for it (a lost tail), and says why.
  - `qtest.Absent(name, validate.Uncollected)`: a step it was due to show
    is missing, for that cause (`Dropped`, `Uncollected`, `Defect`, as
    the report's `absent:` section says; only a defect is a FAIL):
    references/partial-logs.md, "Absent steps".

  On a miss, the failure message is the report.
- In code: `validate.NewChecker(binding, opts).Check(ctx, steps)`. Use
  `CheckBatch` for many runs in one quint process per chunk.
- From files: `gotogether validate -binding b.yaml FILE...`. FILE may be
  OTLP/JSON, a step log, a driver's request log (package qdriver) or
  ITF, detected by content. Add
  `-convert rules.yaml` for plain app spans. Exit status: 0 OK,
  1 violation, 2 tool error. A run cut short passes (its eventual vals
  unjudged, an info issue says so): `-require-finish` (or the binding's
  `complete: true`) fails it as `incomplete (no settle)` or `incomplete
  (pending calls)`, as `qtest.Finishes()` judges it; a lost tail or an ITF
  input is `no verdict (...)`, also exit 1. It needs a binding with an
  eventual val, the journey's end. Other
  flags: `-linearize`, `-linearize-key k`, `-resolve-pending`,
  `-lost-prefix r`, `-lost-suffix r`, `-complete-through r=T` (a
  collector's watermark: r's steps through T are all in; `*=T`), `-invariants a,b`, `-itf`,
  `-history out.svg`, `-mutate-steps` (observation mutants after a
  pass; `-mutate-ops`, `-mutate-limit`).
- A portable failure: `-manifest run.json` writes the run's manifest
  (`gotogether.run/2`: gotogether's and quint's versions, the binding,
  spec, rules and inputs with their sha256, losses, constants,
  invariants, how it was judged, the verdict and failing step with its
  ref), and `-run-info driver.json` adds what gotogether did not see:
  `{"name", "driver": {"name", "version", "seed", "origin", "args"},
  "artifacts": [{"name", "path"}]}` (artifacts hashed). `gotogether
  validate -replay run.json` judges it again on the run's backend,
  refusing a changed input, binding, spec or one of the files it imports
  (the spec checked is the one the binding resolves now), naming it, and
  exiting 1 if the verdict is not the manifest's (reasons worded
  otherwise by a newer gotogether only noted). Keep the manifest beside
  its files: paths under its directory are relative; `Manifest.Bundle`
  copies them there. In code: `validate.NewManifest(validate.RunRecord{...})`,
  `Bundle`, `Write`, `ReadManifest`, `Verify`; in a test, `qtest.Driver`,
  `qtest.Artifacts` and `Run.Manifest()` (written under
  `GOTOGETHER_EVIDENCE` as `run<N>.json`, its files bundled in
  `run<N>/`).
- What a manifest holds, verbatim: the driver's arguments and origin,
  absolute paths (often with a user's name), the failing step's ref; its
  bundle (qtest's under `GOTOGETHER_EVIDENCE`) holds the whole,
  unsanitized step log, and CI jobs often upload that directory. To
  share a run, sanitize the log first (`gotogether steps -pseudonymize
  ... -binding b.yaml -out clean.jsonl`, `qtrace.Filter`) and validate
  the clean log with `-manifest`.
- Converted spans: `gotogether convert -rules bank.convert.yaml spans.jsonl > steps.jsonl`.

Once a run conforms, check that the binding reads what the recorder
wrote: `qtest.MutateSteps(t, binding, steps)` (or `gotogether validate
-mutate-steps`) judges the log with one mistake planted per mutant (a
step dropped, duplicated, swapped, a field corrupted, retimed, moved to
another actor) and lists the survivors, fields no check constrains. Read
[references/observation-mutants.md](references/observation-mutants.md)
for the ops, `Killed`/`Survived` claims, and how to read a survivor.

A run another tool drove (a Bombadil exploration, a Playwright suite, a
replayed session) is checked from its request log (`gotogether.requests/1`,
package qdriver: each request an invocation, only its own answer
completes it), which `gotogether validate` reads beside the server's
steps: read [references/external-drivers.md](references/external-drivers.md)
before producing one (adapters/playwright writes it from a page's routed
requests, `qdriver/bombadil` reads Bombadil's trace, any driver can
write it; the journey's own times, call ids and order kept; refs to its
screenshots and correlation ids; what a settle covers; its manifest, replayed by
`validate -replay` or `cli.Replay` (package validate/cli), and strict gating; shrinking its
failing journey with `qtest.ShrinkActions`, or `gotogether shrink`
outside go test).

Before trusting a model's isolation level of a real database, check
the database: read [references/histories.md](references/histories.md)
for a list-append workload and `qhistory.Check`.

A log that lost its head (rotated, truncated, a recorder started
mid-run) or its tail (torn, killed): read
[references/partial-logs.md](references/partial-logs.md) for the
`snapshot` step it starts from and what a lost tail leaves unjudged;
read it too when a report has an `absent:` section, or a log is still
being collected (a watermark: dropped, uncollected or a defect).

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
| `issue: error lost-prefix` | a log starts after seq 1: its first state is not `init` | record a snapshot step at each log's head and name its action (`snapshot:`), or name the states it may start in (`resume:`) |
| `issue: warn lost-suffix`, `conformance: N/A` | a recorder lost its tail and a later failure may depend on it: no verdict | check a log that has the steps; record so they survive (`qobs.Synced`) |
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
